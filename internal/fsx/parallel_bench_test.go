package fsx

// 并发方案的横向对比：ants 协程池 + 原子抢任务 vs errgroup.WithContext+SetLimit。
//
// 为什么要测：ants 是「引入第三方并发库」的选择，但池本身也有成本（Submit
// 走 channel、任务入队出队）。如果 errgroup 自带的 SetLimit 更快，那引入 ants
// 就只是为了多一个依赖。这里的基准跑在真实目录上，不跑就是拍脑袋。
//
// 跑法：go test ./internal/tools/fs -bench Parallel -benchtime 3x -run '^$'
// 目录用环境变量 DESKKIT_BENCH_DIR 指定，默认本仓根（够多文件又不碰用户目录）。

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/sync/errgroup"
)

func benchDir(tb testing.TB) string {
	tb.Helper()
	if d := os.Getenv("DESKKIT_BENCH_DIR"); d != "" {
		return d
	}
	wd, err := os.Getwd()
	if err != nil {
		tb.Fatal(err)
	}
	// internal/tools/fs → 仓根
	return filepath.Join(wd, "..", "..", "..")
}

// parallelForErrgroup 只用 errgroup 的有界并发（对照实现）。
func parallelForErrgroup(ctx context.Context, workers, n int, fn func(context.Context, int) error) error {
	if n <= 0 {
		return nil
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(workers)
	for i := 0; i < n; i++ {
		i := i
		g.Go(func() error { return fn(gctx, i) })
	}
	return g.Wait()
}

func benchSearch(b *testing.B, run func(context.Context, int, int, func(context.Context, int) error) error) {
	dir := benchDir(b)
	g := Guard{Roots: []string{dir}}
	files, _ := g.collectFiles(context.Background(), dir, nil, defaultSearchMaxFileSize, &SearchStats{})
	if len(files) == 0 {
		b.Skip("基准目录里没有可扫的文件")
	}
	workers := Parallelism()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var hit int64
		err := run(context.Background(), workers, len(files), func(ctx context.Context, idx int) error {
			matches, state := scanFile(ctx, files[idx].path, "zzz_not_found", false, nil, 100)
			if state == scanOK {
				atomic.AddInt64(&hit, int64(len(matches)))
			}
			return nil
		})
		if err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(len(files)), "files")
	b.ReportMetric(float64(workers), "workers")
}

func BenchmarkParallelAntsPool(b *testing.B) {
	benchSearch(b, func(ctx context.Context, workers, n int, fn func(context.Context, int) error) error {
		return parallelFor(ctx, workers, n, fn)
	})
}

func BenchmarkParallelErrgroupLimit(b *testing.B) {
	benchSearch(b, func(ctx context.Context, workers, n int, fn func(context.Context, int) error) error {
		return parallelForErrgroup(ctx, workers, n, fn)
	})
}

// BenchmarkParallelSerial 串行基线：并行省了多少一目了然。
func BenchmarkParallelSerial(b *testing.B) {
	benchSearch(b, func(ctx context.Context, workers, n int, fn func(context.Context, int) error) error {
		return parallelFor(ctx, 1, n, fn)
	})
}

// 确认两种实现的结果一致：同样一批文件，扫出来的命中数必须相同。
func TestParallelImplementationsAgree(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 200; i++ {
		content := "普通内容\n"
		if i%7 == 0 {
			content = "NEEDLE\n"
		}
		writeTestFile(t, filepath.Join(dir, "f.txt"+itoa(i)), content)
	}
	files, _ := Guard{}.collectFiles(context.Background(), dir, nil, defaultSearchMaxFileSize, &SearchStats{})
	if len(files) != 200 {
		t.Fatalf("应枚举 200 个文件，实际 %d", len(files))
	}

	run := func(run func(context.Context, int, int, func(context.Context, int) error) error) int {
		var mu sync.Mutex
		var total int
		err := run(context.Background(), 8, len(files), func(ctx context.Context, idx int) error {
			matches, _ := scanFile(ctx, files[idx].path, "needle", false, nil, 100)
			mu.Lock()
			total += len(matches)
			mu.Unlock()
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return total
	}
	antsTotal := run(parallelFor)
	egTotal := run(parallelForErrgroup)
	if antsTotal != egTotal {
		t.Fatalf("两种并行实现结果不一致: ants=%d errgroup=%d", antsTotal, egTotal)
	}
	if antsTotal != 29 {
		t.Fatalf("命中数应为 29，实际 %d", antsTotal)
	}
	if runtime.NumCPU() < 2 {
		t.Skip("单核机器上并行没有意义")
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
