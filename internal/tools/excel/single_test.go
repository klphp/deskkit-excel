package excel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// 输出必须落在**原文件同目录**、同名、带时间戳 —— 这是第一版的核心约定。
func TestSuggestOutputPathKeepsFolderAndStem(t *testing.T) {
	got := SuggestOutputPath("/tmp/报表/销售部-10月.xlsx", "20260101-120000")
	want := "/tmp/报表/销售部-10月-20260101-120000.xlsx"
	if got != want {
		t.Fatalf("输出路径不符:\n  得到 %s\n  期望 %s", got, want)
	}
	// 不带后缀时自动加时间戳，且与源文件不同名
	auto := SuggestOutputPath("/tmp/报表/销售部-10月.xlsx", "")
	if auto == "/tmp/报表/销售部-10月.xlsx" {
		t.Fatal("不能和源文件同名，否则会覆盖原表")
	}
	if !strings.HasSuffix(auto, ".xlsx") {
		t.Fatalf("扩展名丢了: %s", auto)
	}
}

func TestQueryToFileWritesBesideSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "明细.xlsx")
	mkxlsx(t, dir, "明细.xlsx", []string{"部门", "金额"}, [][]string{
		{"销售", "¥1,000"}, {"销售", "500"}, {"技术", "300"},
	})
	before, _ := os.ReadFile(src)

	ctx := context.Background()
	db, info, err := LoadWorkbookFile(ctx, src)
	if err != nil {
		t.Fatalf("载入失败: %v", err)
	}
	defer db.Close()
	if info.File != "明细.xlsx" {
		t.Errorf("文件名不对: %s", info.File)
	}

	out, rows, err := QueryToFile(ctx, db, src,
		`SELECT 部门, SUM(金额) AS 合计 FROM "`+info.Tables[0].Table+`" GROUP BY 部门 ORDER BY 合计 DESC`, "")
	if err != nil {
		t.Fatalf("写出失败: %v", err)
	}
	if rows != 2 {
		t.Fatalf("应写 2 行，实际 %d", rows)
	}
	if filepath.Dir(out) != dir {
		t.Errorf("输出必须在源目录，实际 %s", filepath.Dir(out))
	}
	if !strings.HasPrefix(filepath.Base(out), "明细-") {
		t.Errorf("输出文件名应以原名开头: %s", filepath.Base(out))
	}

	// 原文件一个字节都不能动
	after, _ := os.ReadFile(src)
	if len(before) != len(after) {
		t.Fatal("原文件被改动了")
	}

	f, err := excelize.OpenFile(out)
	if err != nil {
		t.Fatalf("打不开输出: %v", err)
	}
	defer f.Close()
	got, _ := f.GetRows(f.GetSheetList()[0])
	if got[1][0] != "销售" || got[1][1] != "1500" {
		t.Fatalf("汇总结果不对: %v", got)
	}
}

func TestCleanFileNormalizesAndKeepsLeadingZero(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "脏表.xlsx")
	mkxlsx(t, dir, "脏表.xlsx", []string{"日期", "部门", "工号"}, [][]string{
		{"2026/1/2", "  销售  ", "00123"},
		{"", "", ""},
		{"2026/1/2", "  销售  ", "00123"}, // 与第一行去重后相同
		{"2026年3月5日", "技术", "E45"},
	})
	before, _ := os.ReadFile(src)

	res, err := CleanFile(context.Background(), CleanFileOptions{
		Path:           src,
		DropBlankRows:  true,
		TrimSpaces:     true,
		DropDuplicates: true,
		DedupKey:       []string{"日期", "工号"},
		NormalizeDates: true,
	})
	if err != nil {
		t.Fatalf("清洗失败: %v", err)
	}
	if res.RowsIn != 4 || res.RowsOut != 2 {
		t.Fatalf("应 4 行 → 2 行，实际 %d → %d", res.RowsIn, res.RowsOut)
	}
	if res.BlankRows != 1 || res.Duplicate != 1 {
		t.Fatalf("统计不对: 空行 %d 重复 %d", res.BlankRows, res.Duplicate)
	}
	if res.DateFixed == 0 {
		t.Error("应修正日期")
	}
	if res.Output == "" {
		t.Fatal("没有输出路径")
	}
	if filepath.Dir(res.Output) != dir {
		t.Errorf("输出应在源目录: %s", res.Output)
	}

	after, _ := os.ReadFile(src)
	if len(before) != len(after) {
		t.Fatal("原文件被改动了")
	}

	f, err := excelize.OpenFile(res.Output)
	if err != nil {
		t.Fatalf("打不开输出: %v", err)
	}
	defer f.Close()
	rows, _ := f.GetRows(f.GetSheetList()[0])
	if rows[1][0] != "2026-01-02" {
		t.Errorf("日期没归一: %q", rows[1][0])
	}
	if rows[1][1] != "销售" {
		t.Errorf("首尾空白没去掉: %q", rows[1][1])
	}
	if rows[1][2] != "00123" {
		t.Errorf("工号前导零被吃掉: %q", rows[1][2])
	}
}

func TestCleanFileDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.xlsx")
	mkxlsx(t, dir, "a.xlsx", []string{"部门"}, [][]string{{" 销售 "}, {""}})

	before, _ := os.ReadDir(dir)
	res, err := CleanFile(context.Background(), CleanFileOptions{
		Path: src, DropBlankRows: true, TrimSpaces: true, DryRun: true,
	})
	if err != nil {
		t.Fatalf("预览失败: %v", err)
	}
	if res.RowsOut != 1 || res.Output != "" {
		t.Fatalf("预览不该写文件: %+v", res)
	}
	after, _ := os.ReadDir(dir)
	if len(before) != len(after) {
		t.Fatal("预览动了目录内容")
	}
}

func TestLoadWorkbookFileRejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	if _, _, err := LoadWorkbookFile(ctx, filepath.Join(dir, "没有这个.xlsx")); err == nil {
		t.Error("不存在的文件应报错")
	}
	if _, _, err := LoadWorkbookFile(ctx, dir); err == nil {
		t.Error("传目录应报错")
	}
	txt := filepath.Join(dir, "a.txt")
	os.WriteFile(txt, []byte("x"), 0o644)
	if _, _, err := LoadWorkbookFile(ctx, txt); err == nil {
		t.Error("非 Excel 文件应报错")
	}
}

func TestSanitizeSuffix(t *testing.T) {
	cases := map[string]string{
		"部门/汇总":    "部门-汇总",
		"a:b*c?d":  "a-bcd",
		"":         "",
		"  部门汇总  ": "部门汇总",
	}
	for in, want := range cases {
		if got := sanitizeSuffix(in); got != want {
			t.Errorf("%q → %q，期望 %q", in, got, want)
		}
	}
	// 超长后缀要截断，否则文件名会超过系统限制
	long := strings.Repeat("很长", 40)
	if n := len([]rune(sanitizeSuffix(long))); n > 24 {
		t.Errorf("超长后缀没截断: %d 字", n)
	}
}
