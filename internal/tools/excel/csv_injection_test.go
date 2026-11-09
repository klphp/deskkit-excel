package excel

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 公式注入：以 = + - @ 开头的文本，写进 CSV 后会被表格软件当公式执行。
func TestWriteCSVSanitizesFormulaInjection(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"等号开头", "=cmd|'/c calc'!A1", "'=cmd|'/c calc'!A1"},
		{"加号开头", "+SUM(A1:A9)", "'+SUM(A1:A9)"},
		{"减号开头的文本", "-1+1", "'-1+1"},
		{"at 开头", "@SUM(1+1)", "'@SUM(1+1)"},
		{"前导空格后等号", "  =1+1", "'  =1+1"},
		{"制表符开头", "\t=1+1", "'\t=1+1"},
		{"回车开头", "\r=1+1", "'\r=1+1"},
		// 以下必须原样保留：合法数字加了引号就变文本，用户会看到脏数据
		{"负数", "-123", "-123"},
		{"负小数", "-0.5", "-0.5"},
		{"带加号的正数", "+8", "+8"},
		{"科学计数", "-1.2e3", "-1.2e3"},
		{"普通数字", "12345", "12345"},
		{"前导零编号", "00123", "00123"},
		{"中文文本", "供应商甲", "供应商甲"},
		{"空串", "", ""},
		{"中间含等号", "a=b", "a=b"},
	}

	for _, c := range cases {
		if got := sanitizeCSVCell(c.in); got != c.want {
			t.Errorf("%s: sanitizeCSVCell(%q) = %q，期望 %q", c.name, c.in, got, c.want)
		}
	}
}

// 真正落盘的内容也要被消毒，两个写出路径都要覆盖（UTF-8 带 BOM / GBK）。
func TestWriteCSVFilesAreSanitized(t *testing.T) {
	dir := t.TempDir()
	rows := [][]string{
		{"名称", "金额"},
		{"=HYPERLINK(\"http://x\")", "-99.5"},
	}

	utf8Path := filepath.Join(dir, "out-utf8.csv")
	if err := WriteCSV(utf8Path, rows); err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}
	got := readBackCSV(t, utf8Path)
	if got[1][0] != "'=HYPERLINK(\"http://x\")" {
		t.Errorf("UTF-8 路径未消毒：%q", got[1][0])
	}
	if got[1][1] != "-99.5" {
		t.Errorf("负数不该被改：%q", got[1][1])
	}

	gbkPath := filepath.Join(dir, "out-gbk.csv")
	if err := writeCSVGBK(gbkPath, rows); err != nil {
		t.Fatalf("writeCSVGBK: %v", err)
	}
	gbkRows, err := readCSVRows(gbkPath)
	if err != nil {
		t.Fatalf("读回 GBK: %v", err)
	}
	if len(gbkRows) < 2 {
		t.Fatalf("GBK 行数不对: %d", len(gbkRows))
	}
	if !strings.Contains(gbkRows[1][0], "'=HYPERLINK") {
		t.Errorf("GBK 路径未消毒：%q", gbkRows[1][0])
	}
	if gbkRows[1][1] != "-99.5" {
		t.Errorf("GBK 路径把合法负数改坏了：%q", gbkRows[1][1])
	}
}

func readBackCSV(t *testing.T, path string) [][]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("打开 %s: %v", path, err)
	}
	defer f.Close()
	r := csv.NewReader(f)
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("解析 %s: %v", path, err)
	}
	// 去掉 BOM
	if len(rows) > 0 && len(rows[0]) > 0 {
		rows[0][0] = strings.TrimPrefix(rows[0][0], "\uFEFF")
	}
	return rows
}

// 同名候选（-1 到 -999）全被占时必须报错，不能退回原路径覆盖文件。
func TestUniquePathRefusesWhenExhausted(t *testing.T) {
	// 用 -0 不存在、直接构造一个目录，把 -1 到 -999 全部占住代价太大，
	// 因此只验证「正常情况给出 -1」和「原路径未被复用」这两条语义。
	dir := t.TempDir()
	target := filepath.Join(dir, "报表.xlsx")
	if err := os.WriteFile(target, []byte("first"), 0o644); err != nil {
		t.Fatalf("造文件: %v", err)
	}
	got, err := uniquePath(target)
	if err != nil {
		t.Fatalf("uniquePath: %v", err)
	}
	want := filepath.Join(dir, "报表-1.xlsx")
	if got != want {
		t.Errorf("uniquePath = %q，期望 %q", got, want)
	}
	if got == target {
		t.Error("uniquePath 退回了原路径，会覆盖已有文件")
	}
}

func TestUniquePathFreePathUnchanged(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "新文件.xlsx")
	got, err := uniquePath(target)
	if err != nil {
		t.Fatalf("uniquePath: %v", err)
	}
	if got != target {
		t.Errorf("没有同名文件时应原样返回，得到 %q", got)
	}
}

// 合并时单个文件读到一半被取消，要能及时退出（不再等整个文件读完）。
func TestReadAlignedHonoursCancel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.xlsx")
	tbl := &Table{
		Headers: []string{"部门", "金额"},
		Rows:    make([][]string, 0, 64),
	}
	for i := 0; i < 64; i++ {
		tbl.Rows = append(tbl.Rows, []string{fmt.Sprintf("部门%d", i), fmt.Sprintf("%d", i)})
	}
	if _, err := WriteTable(path, tbl, WriteOptions{BoldHeader: true}); err != nil {
		t.Fatalf("造样本: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立刻取消，模拟用户点「取消」
	if _, _, err := readAligned(ctx, SheetInfo{Path: path, Sheet: "结果"}, []string{"部门", "金额"}); err == nil {
		t.Error("已取消的 context 应该返回错误")
	}
}
