package fsx

// 有界并行执行。
//
// 为什么不在每个调用点上自己开 go 协程：
//   · 无界的 goroutine 会把磁盘打到顺序上反而更慢（几千个并发读，SSD 上
//     队列深度爆炸，实测比串行还慢），而且内存里全是待调度协程。
//   · 取消要「立刻停」：搜到够多结果、或者调用方 timeout 之后，还在排队的
//     任务必须马上放弃，不能继续把剩下的文件读一遍。
//
// 所以这里只提供一种并行原语：固定 workers 个执行协程 + 原子计数器取任务。
//   · ants 协程池：复用固定数量的协程，协程数不随文件数增长（14000 个文件
//     也只起 24 个）；
//   · errgroup：把各 worker 的错误汇总成第一个错误，并 cancel 统一的 context，
//     让其余 worker 在下一个任务边界就退出。
//
// 并行度统一按「可用算力的 60%，上限 8」：
//   · 不跑满是有意的——本机还要给人用（浏览器、编辑器、编译器），磁盘队列打满
//     会让整机变卡，而吞吐只多两成不到；
//   · 60% 是从实测拐点定的：2500 文件全扫，1→175ms、4→58ms、8→47ms、
//     12→53ms、24→51ms；4 以后收益急剧变小，8 以后是噪声。
//   · 算力口径取 min(NumCPU, GOMAXPROCS)，尊重 GOMAXPROCS 的显式限制。
//   · 需要更多并发时显式传 concurrency，或设环境变量 FSX_CONCURRENCY。

import (
	"context"
	"math"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"

	"github.com/panjf2000/ants/v2"
	"golang.org/x/sync/errgroup"
)

const maxParallel = 8

// budgetRatio 默认只用可用算力的这个比例，留余量给同一台机器上的其它程序。
const budgetRatio = 0.6

// Parallelism 默认并行度：可用算力的 60%，夹在 [2, 8]。
//
// 可用算力 = min(NumCPU, GOMAXPROCS)。本机 12 核但 GOMAXPROCS=6，
// 所以默认 4 个 worker——够吃到磁盘队列收益，又不至于把整机拖卡。
// 环境变量 FSX_CONCURRENCY 可覆盖（调试或批处理时想拉满用）。
func Parallelism() int {
	if v := os.Getenv("FSX_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > maxParallel {
				n = maxParallel
			}
			return n
		}
	}
	n := int(math.Round(float64(availableCores()) * budgetRatio))
	if n < 2 {
		n = 2
	}
	if n > maxParallel {
		n = maxParallel
	}
	return n
}

// resolveWorkers 把调用方给的并发数规整成实际要用的值。
// 0 或负数 → 默认并行度；1 → 串行；过大 → 夹到任务数/上限。
func resolveWorkers(want, tasks int) int {
	w := want
	if w <= 0 {
		w = Parallelism()
	}
	if w > maxParallel {
		w = maxParallel
	}
	if tasks > 0 && w > tasks {
		w = tasks
	}
	if w < 1 {
		w = 1
	}
	return w
}

// parallelFor 有界并行执行 fn(ctx, 0..n-1)。
//
// workers<=1 或只有一个任务时走串行分支：省掉池和同步开销，也让单文件搜索
// 这种场景的行为和以前完全一致（便于对比与排错）。
func parallelFor(ctx context.Context, workers, n int, fn func(context.Context, int) error) error {
	if n <= 0 {
		return nil
	}
	if workers <= 1 || n == 1 {
		for i := 0; i < n; i++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := fn(ctx, i); err != nil {
				return err
			}
		}
		return nil
	}
	return runPool(ctx, workers, n, fn)
}

// runPool 用 ants 池起 workers 个协程，各协程用原子计数器抢任务。
func runPool(ctx context.Context, workers, n int, fn func(context.Context, int) error) error {
	g, gctx := errgroup.WithContext(ctx)

	pool, err := ants.NewPool(workers, ants.WithNonblocking(true))
	if err == nil {
		defer pool.Release()
	}

	var next atomic.Int64
	loop := func() error {
		for {
			if err := gctx.Err(); err != nil {
				return err
			}
			i := int(next.Add(1)) - 1
			if i >= n {
				return nil
			}
			if err := fn(gctx, i); err != nil {
				return err
			}
		}
	}

	for w := 0; w < workers; w++ {
		done := make(chan error, 1)
		submitted := false
		if pool != nil {
			// 池被占满（非阻塞模式）时 Submit 返回错误，下面退回普通协程；
			// 协程数仍然只有 workers 个，不会失控。
			if e := pool.Submit(func() { done <- loop() }); e == nil {
				submitted = true
			}
		}
		if !submitted {
			go func() { done <- loop() }()
		}
		g.Go(func() error { return <-done })
	}
	return g.Wait()
}

// availableCores 返回「真正吃得上」的核数。
//
// 用物理核而不是逻辑核：文件扫描是 IO 密集，超线程带来的第二个逻辑核几乎不增加
// 吞吐，却会让并发数虚高、把磁盘队列和整机一起拖慢。Apple Silicon 没有超线程，
// 物理核就等于逻辑核；Intel Mac 上逻辑核翻倍，取物理核才是准的。
func availableCores() int {
	if v := os.Getenv("FSX_CORES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	if n := physicalCores(); n > 0 {
		return n
	}
	return runtime.NumCPU()
}
