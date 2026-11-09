package fs

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
// 并行度统一按「核数×2，上限 32」——这是 IO 混合场景的经验值：核数以下吃不满
// 磁盘队列，核数×4 以上纯 IO 任务开始互相拖累（fs_search 的实测数据见 logs）。

import (
	"context"
	"runtime"
	"sync/atomic"

	"github.com/panjf2000/ants/v2"
	"golang.org/x/sync/errgroup"
)

const maxParallel = 32

// Parallelism 默认并行度：核数×2，夹在 [2, 32]。
func Parallelism() int {
	n := runtime.NumCPU() * 2
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
