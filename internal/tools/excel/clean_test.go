package excel

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"
)

func readOutput(t *testing.T, path string) [][]string {
	t.Helper()
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("打不开输出: %v", err)
	}
	defer f.Close()
	rows, err := f.GetRows(f.GetSheetList()[0])
	if err != nil {
		t.Fatalf("读取输出失败: %v", err)
	}
	return rows
}

func TestCleanTrimsAndDropsBlankRows(t *testing.T) {
	dir := t.TempDir()
	mkxlsx(t, dir, "a.xlsx", []string{"部门", "姓名"}, [][]string{
		{"  销售  ", " 张三"},
		{"", ""}, // 空行
		{"技术", "李四   "},
	})

	res, err := Clean(context.Background(), CleanOptions{
		Dir: dir, TrimSpaces: true, DropBlankRows: true,
	})
	if err != nil {
		t.Fatalf("清洗失败: %v", err)
	}
	if res.RowsIn != 3 || res.RowsOut != 2 {
		t.Fatalf("应 3 行进 2 行出，实际 in=%d out=%d", res.RowsIn, res.RowsOut)
	}
	if res.BlankRows != 1 {
		t.Fatalf("应删除 1 个空行，实际 %d", res.BlankRows)
	}
	if res.Trimmed == 0 {
		t.Fatal("应当统计到被 trim 的单元格")
	}

	rows := readOutput(t, filepath.Join(res.OutputDir, "a-clean.xlsx"))
	if rows[1][0] != "销售" || rows[1][1] != "张三" {
		t.Fatalf("首尾空白没去掉: %v", rows[1])
	}
	if rows[2][1] != "李四" {
		t.Fatalf("尾部空白没去掉: %v", rows[2])
	}
}

func TestCleanDedupByKey(t *testing.T) {
	dir := t.TempDir()
	mkxlsx(t, dir, "a.xlsx", []string{"工号", "姓名"}, [][]string{
		{"E1", "张三"},
		{"E1", "张三"}, // 完全重复
		{"E2", "李四"},
	})

	res, err := Clean(context.Background(), CleanOptions{
		Dir: dir, DropDuplicates: true, DedupKey: []string{"工号"},
	})
	if err != nil {
		t.Fatalf("清洗失败: %v", err)
	}
	if res.RowsOut != 2 || res.Duplicate != 1 {
		t.Fatalf("按工号去重应剩 2 行，实际 out=%d dup=%d", res.RowsOut, res.Duplicate)
	}
}

// DedupKey 为空表示整行比较 —— 只有完全相同的行才算重复。
func TestCleanDedupWholeRow(t *testing.T) {
	dir := t.TempDir()
	mkxlsx(t, dir, "a.xlsx", []string{"工号", "姓名"}, [][]string{
		{"E1", "张三"},
		{"E1", "张三"},
		{"E1", "李四"}, // 工号相同但整行不同 → 保留
	})

	res, err := Clean(context.Background(), CleanOptions{Dir: dir, DropDuplicates: true})
	if err != nil {
		t.Fatalf("清洗失败: %v", err)
	}
	if res.RowsOut != 2 {
		t.Fatalf("整行去重应剩 2 行，实际 %d", res.RowsOut)
	}
}

func TestCleanNormalizesDates(t *testing.T) {
	dir := t.TempDir()
	mkxlsx(t, dir, "a.xlsx", []string{"日期", "备注"}, [][]string{
		{"2026/1/2", "斜杠"},
		{"2026.01.02", "点号"},
		{"2026年1月2日", "中文"},
		{"2026-01-02", "已标准"},
		{"不是日期", "文本"},
	})

	res, err := Clean(context.Background(), CleanOptions{
		Dir: dir, NormalizeDates: true, DateFormat: "YYYY-MM-DD",
	})
	if err != nil {
		t.Fatalf("清洗失败: %v", err)
	}
	if res.DateFixed != 3 {
		t.Fatalf("应修正 3 个日期，实际 %d", res.DateFixed)
	}
	rows := readOutput(t, filepath.Join(res.OutputDir, "a-clean.xlsx"))
	for i := 1; i <= 4; i++ {
		if rows[i][0] != "2026-01-02" {
			t.Fatalf("第 %d 行日期未归一: %q", i, rows[i][0])
		}
	}
	if rows[5][0] != "不是日期" {
		t.Fatalf("非日期文本不该被改动: %q", rows[5][0])
	}
}

func TestCleanNumbersAsText(t *testing.T) {
	dir := t.TempDir()
	mkxlsx(t, dir, "a.xlsx", []string{"金额", "工号", "比例"}, [][]string{
		{"¥1,234.50", "00123", "15%"},
		{"￥2,000", "E45", "20%"},
	})

	res, err := Clean(context.Background(), CleanOptions{Dir: dir, NumbersAsText: true})
	if err != nil {
		t.Fatalf("清洗失败: %v", err)
	}
	rows := readOutput(t, filepath.Join(res.OutputDir, "a-clean.xlsx"))
	// 货币符号与千分位必须去掉，且金额要写成**数值**（回读带两位小数格式）。
	// 按数值比较：这条测试关心的是「能不能直接求和」，不是字符串长相。
	if !sameNumber(rows[1][0], 1234.5) {
		t.Fatalf("货币+千分位没处理成数值: %q", rows[1][0])
	}
	if !sameNumber(rows[2][0], 2000) {
		t.Fatalf("¥ 没处理成数值: %q", rows[2][0])
	}
	// 前导零必须保住：工号 00123 转成 123 就毁了
	if rows[1][1] != "00123" {
		t.Fatalf("前导零被破坏: %q", rows[1][1])
	}
	// 百分比保持原样
	if rows[1][2] != "15%" {
		t.Fatalf("百分比不该被改: %q", rows[1][2])
	}
}

func TestCleanDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	mkxlsx(t, dir, "a.xlsx", []string{"部门"}, [][]string{{" 销售 "}, {""}})

	res, err := Clean(context.Background(), CleanOptions{Dir: dir, TrimSpaces: true, DropBlankRows: true, DryRun: true})
	if err != nil {
		t.Fatalf("清洗失败: %v", err)
	}
	if res.RowsOut != 1 || res.BlankRows != 1 {
		t.Fatalf("预览统计不对: %+v", res)
	}
	if _, err := os.Stat(res.OutputDir); !os.IsNotExist(err) {
		t.Fatal("预览不该创建输出目录")
	}
}

// 清洗是逐文件的：2 个输入 → 2 个输出，原文件一字不改。
func TestCleanPerFileAndKeepsOriginals(t *testing.T) {
	dir := t.TempDir()
	mkxlsx(t, dir, "a.xlsx", []string{"部门"}, [][]string{{"销售"}})
	mkxlsx(t, dir, "b.xlsx", []string{"部门"}, [][]string{{"技术"}})
	beforeA, _ := os.ReadFile(filepath.Join(dir, "a.xlsx"))

	res, err := Clean(context.Background(), CleanOptions{Dir: dir, DropBlankRows: true})
	if err != nil {
		t.Fatalf("清洗失败: %v", err)
	}
	if res.Files != 2 {
		t.Fatalf("应处理 2 个文件，实际 %d", res.Files)
	}
	for _, name := range []string{"a-clean.xlsx", "b-clean.xlsx"} {
		if _, err := os.Stat(filepath.Join(res.OutputDir, name)); err != nil {
			t.Fatalf("缺少输出 %s: %v", name, err)
		}
	}
	afterA, _ := os.ReadFile(filepath.Join(dir, "a.xlsx"))
	if len(beforeA) != len(afterA) {
		t.Fatal("原文件被改动了")
	}
}

func TestNormalizeDateUnits(t *testing.T) {
	if got, ok := normalizeDate("2026/1/2", "2006-01-02"); !ok || got != "2026-01-02" {
		t.Fatalf("标准日期解析失败: %q %v", got, ok)
	}
	if _, ok := normalizeDate("13/45/2026", "2006-01-02"); ok {
		t.Fatal("非法日期不该解析成功")
	}
	// 「像日期但解不出来」要能被识别，用来提示用户
	if !looksLikeDate("2026-13-45") {
		t.Fatal("形似日期应被识别")
	}
	if looksLikeDate("合计") {
		t.Fatal("「合计」不该被当成日期")
	}
}

func TestNumericValueGuards(t *testing.T) {
	cases := map[string]bool{ // 输入 → 是否应当转换
		"1,234.50": true,
		"¥1,234":   true,
		"-99.5":    true,
		"00123":    true, // 正则允许，但上游 leadingZero 会拦住
		"15%":      false,
		"abc":      false,
		"1.2.3":    false,
	}
	for in, want := range cases {
		_, ok := numericValue(in)
		if ok != want {
			t.Errorf("%q 转换结果应为 %v，实际 %v", in, want, ok)
		}
	}
	if !leadingZero.MatchString("00123") {
		t.Fatal("前导零必须被识别，否则工号会被毁")
	}
	if leadingZero.MatchString("0.5") {
		t.Fatal("小数不应被当成前导零编号")
	}
}
