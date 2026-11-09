package fs

// 并行相关的正确性测试。
//
// 并行代码最怕两件事：结果和串行不一致（漏条目/重复条目）、取消之后还在跑。
// 这两个都必须有测试，否则「快」是不可信的。

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"sync/atomic"
	"testing"
	"time"
)

func TestParallelismBounds(t *testing.T) {
	p := Parallelism()
	if p < 2 || p > maxParallel {
		t.Fatalf("默认并行度应在 [2,%d] 内，实际 %d", maxParallel, p)
	}
	if got := resolveWorkers(0, 0); got != p {
		t.Errorf("0 应回落到默认并行度，实际 %d", got)
	}
	if got := resolveWorkers(1000, 0); got != maxParallel {
		t.Errorf("超大并发数应夹到 %d，实际 %d", maxParallel, got)
	}
	if got := resolveWorkers(8, 3); got != 3 {
		t.Errorf("并发数不该超过任务数，实际 %d", got)
	}
	if got := resolveWorkers(-1, 5); got != p && got > 5 {
		t.Errorf("负数应回落到默认值，实际 %d", got)
	}
}

func TestListParallelEqualsSerial(t *testing.T) {
	g, root := testGuard(t)
	// 造一棵有层级的树：7 个目录 × 12 个文件
	for d := 0; d < 7; d++ {
		for i := 0; i < 12; i++ {
			mustWrite(t, filepath.Join(root, fmt.Sprintf("d%d", d), fmt.Sprintf("sub%d", i%3), fmt.Sprintf("f%02d.txt", i)), "x")
		}
	}
	// 隐藏目录与 node_modules 在两种模式下都应被跳过
	mustWrite(t, filepath.Join(root, ".hidden", "a.txt"), "x")
	mustWrite(t, filepath.Join(root, "node_modules", "b.js"), "x")

	ser, err := g.List(context.Background(), ListOptions{Path: root, Recursive: true, Concurrency: 1})
	if err != nil {
		t.Fatalf("串行遍历失败: %v", err)
	}
	par, err := g.List(context.Background(), ListOptions{Path: root, Recursive: true, Concurrency: 12})
	if err != nil {
		t.Fatalf("并行遍历失败: %v", err)
	}

	if len(ser.Entries) != len(par.Entries) {
		t.Fatalf("条目数不一致: 串行 %d vs 并行 %d", len(ser.Entries), len(par.Entries))
	}
	if ser.Files != par.Files || ser.Dirs != par.Dirs || ser.TotalSize != par.TotalSize {
		t.Errorf("统计不一致: 串行 files=%d dirs=%d size=%d vs 并行 files=%d dirs=%d size=%d",
			ser.Files, ser.Dirs, ser.TotalSize, par.Files, par.Dirs, par.TotalSize)
	}
	// 并行版输出按路径排序，串行版是 DFS 顺序：比集合，不比顺序
	key := func(es []Entry) []string {
		out := make([]string, 0, len(es))
		for _, e := range es {
			k := e.Path
			if e.Dir {
				k += "/"
			}
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	a, b := key(ser.Entries), key(par.Entries)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("#%d 条目不一致: %q vs %q", i, a[i], b[i])
		}
	}
	// 并行版必须是有序的（否则同一份数据两次调用结果不同）
	for i := 1; i < len(par.Entries); i++ {
		if par.Entries[i-1].Path > par.Entries[i].Path {
			t.Fatalf("并行结果没排序: %q > %q", par.Entries[i-1].Path, par.Entries[i].Path)
		}
	}
	for _, e := range par.Entries {
		if e.Name == "a.txt" || e.Name == "b.js" {
			t.Errorf("隐藏目录/node_modules 应被跳过，出现了 %s", e.Path)
		}
	}
}

func TestListParallelPatternAndCap(t *testing.T) {
	g, root := testGuard(t)
	for i := 0; i < 60; i++ {
		mustWrite(t, filepath.Join(root, fmt.Sprintf("d%d", i%6), fmt.Sprintf("f%02d.xlsx", i)), "x")
		mustWrite(t, filepath.Join(root, fmt.Sprintf("d%d", i%6), fmt.Sprintf("f%02d.txt", i)), "x")
	}

	res, err := g.List(context.Background(), ListOptions{Path: root, Recursive: true, Pattern: "*.xlsx", Concurrency: 8})
	if err != nil {
		t.Fatalf("遍历失败: %v", err)
	}
	if res.Files != 60 {
		t.Errorf("只该有 60 个 xlsx，实际 %d", res.Files)
	}
	if res.Truncated {
		t.Error("没超上限不该标截断")
	}

	capped, err := g.List(context.Background(), ListOptions{Path: root, Recursive: true, Pattern: "*.xlsx", MaxEntries: 10, Concurrency: 8})
	if err != nil {
		t.Fatalf("遍历失败: %v", err)
	}
	if len(capped.Entries) != 10 || !capped.Truncated {
		t.Errorf("应截断到 10 条，实际 %d（truncated=%v）", len(capped.Entries), capped.Truncated)
	}
}

func TestParallelForCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var ran int64
	const tasks = 400

	done := make(chan error, 1)
	go func() {
		done <- parallelFor(ctx, 4, tasks, func(ctx context.Context, i int) error {
			atomic.AddInt64(&ran, 1)
			// 模拟长任务：等着被取消
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(30 * time.Millisecond):
				return nil
			}
		})
	}()

	time.Sleep(60 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("取消后应返回错误")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("取消后 parallelFor 没有及时退出")
	}
	// 400 个 30ms 任务串行要 12s；4 并发也至少 3s。取消得早，跑过的应该很少。
	if n := atomic.LoadInt64(&ran); n > tasks/2 {
		t.Errorf("取消后不该继续跑任务，实际跑了 %d/%d", n, tasks)
	}
}

func TestParallelForErrorCancelsOthers(t *testing.T) {
	ctx := context.Background()
	var ran int64
	start := time.Now()
	err := parallelFor(ctx, 3, 300, func(ctx context.Context, i int) error {
		atomic.AddInt64(&ran, 1)
		if i == 0 {
			return fmt.Errorf("第一个任务失败")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
			return nil
		}
	})
	if err == nil {
		t.Fatal("应返回错误")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("一个任务失败应立刻取消其余任务，实际耗时 %v", d)
	}
	if n := atomic.LoadInt64(&ran); n > 100 {
		t.Errorf("失败后不该跑几百个任务，实际 %d", n)
	}
}

func TestParallelForSerialFallback(t *testing.T) {
	var order []int
	err := parallelFor(context.Background(), 1, 5, func(ctx context.Context, i int) error {
		order = append(order, i)
		return nil
	})
	if err != nil {
		t.Fatalf("串行分支不该失败: %v", err)
	}
	for i := range order {
		if order[i] != i {
			t.Fatalf("并发度 1 时应严格按顺序执行，实际 %v", order)
		}
	}
	if err := parallelFor(context.Background(), 4, 0, func(ctx context.Context, i int) error {
		t.Fatal("0 个任务时不该调用")
		return nil
	}); err != nil {
		t.Fatalf("0 个任务应直接返回: %v", err)
	}
}
