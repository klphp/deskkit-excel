package excel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mergeFixture 造 n 个 xlsx，每个 rows 行、cols 列，用于压内存闸门。
func mergeFixture(t *testing.T, dir string, files, rows, cols int) {
	t.Helper()
	for f := 0; f < files; f++ {
		tbl := &Table{Headers: make([]string, cols)}
		for c := 0; c < cols; c++ {
			tbl.Headers[c] = fmt.Sprintf("列%d", c)
		}
		for r := 0; r < rows; r++ {
			row := make([]string, cols)
			for c := range row {
				row[c] = strings.Repeat("x", 32)
			}
			tbl.Rows = append(tbl.Rows, row)
		}
		path := filepath.Join(dir, fmt.Sprintf("f%02d.xlsx", f))
		if _, err := WriteTable(path, tbl, WriteOptions{BoldHeader: true}); err != nil {
			t.Fatalf("造样本 %s: %v", path, err)
		}
	}
}

// 超过硬线要拒绝，并给出「怎么办」的话，而不是让进程被系统杀掉。
func TestMergeRefusesOverHardLimit(t *testing.T) {
	oldSoft, oldHard := mergeSoftLimitBytes, mergeHardLimitBytes
	defer func() { mergeSoftLimitBytes, mergeHardLimitBytes = oldSoft, oldHard }()
	mergeSoftLimitBytes = 1 << 10 // 1KB
	mergeHardLimitBytes = 4 << 10 // 4KB

	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	mergeFixture(t, src, 2, 60, 6)

	_, err := Merge(context.Background(), MergeOptions{Dir: src, OutputDir: filepath.Join(dir, "out")})
	if err == nil {
		t.Fatal("超过硬线应返回错误")
	}
	msg := err.Error()
	for _, want := range []string{"too large", "batches", "MB"} {
		if !strings.Contains(msg, want) {
			t.Errorf("报错里缺少 %q：%s", want, msg)
		}
	}
}

// 只超软线时仍然能合并成功，但要带一条提醒。
func TestMergeWarnsOverSoftLimit(t *testing.T) {
	oldSoft, oldHard := mergeSoftLimitBytes, mergeHardLimitBytes
	defer func() { mergeSoftLimitBytes, mergeHardLimitBytes = oldSoft, oldHard }()
	mergeSoftLimitBytes = 1 << 10   // 1KB：会超过
	mergeHardLimitBytes = 256 << 20 // 硬线很高，不触发

	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	mergeFixture(t, src, 2, 40, 4)

	out, err := Merge(context.Background(), MergeOptions{Dir: src, OutputDir: filepath.Join(dir, "out")})
	if err != nil {
		t.Fatalf("软线不该失败: %v", err)
	}
	if out.RowsOut == 0 {
		t.Fatal("应该合并出行")
	}
	found := false
	for _, w := range out.Warnings {
		if strings.Contains(w, "memory") && strings.Contains(w, "batches") {
			found = true
		}
	}
	if !found {
		t.Errorf("应有一条内存提醒，实际 warnings=%v", out.Warnings)
	}
}

// 小数据（远低于软线）不该产生内存提醒，避免正常使用满屏噪音。
func TestMergeNoMemoryWarningForSmallJobs(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	mergeFixture(t, src, 2, 5, 3)

	out, err := Merge(context.Background(), MergeOptions{Dir: src, OutputDir: filepath.Join(dir, "out")})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	for _, w := range out.Warnings {
		if strings.Contains(w, "内存") {
			t.Errorf("小任务不该有内存提醒：%v", w)
		}
	}
}

func TestEstimateRowBytes(t *testing.T) {
	row := []string{"abc", "de"}
	// 2 个字符串头 × 24 + 内容 5 字节
	if got, want := estimateRowBytes(row), int64(2*24+5); got != want {
		t.Errorf("estimateRowBytes = %d，期望 %d", got, want)
	}
	if got := estimateRowBytes(nil); got != 0 {
		t.Errorf("空行应为 0，得到 %d", got)
	}
}
