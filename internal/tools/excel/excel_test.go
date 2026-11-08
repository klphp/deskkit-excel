package excel

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"
)

// mkxlsx 造一个真实 xlsx：headers 为表头，rows 为数据行。
func mkxlsx(t *testing.T, dir, name string, headers []string, rows [][]string) {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	sheet := f.GetSheetName(0)

	write := func(idx int, values []string) {
		cell, err := excelize.CoordinatesToCellName(1, idx)
		if err != nil {
			t.Fatalf("坐标失败: %v", err)
		}
		row := make([]any, len(values))
		for i, v := range values {
			row[i] = v
		}
		if err := f.SetSheetRow(sheet, cell, &row); err != nil {
			t.Fatalf("写行失败: %v", err)
		}
	}

	write(1, headers)
	for i, r := range rows {
		write(i+2, r)
	}
	if err := f.SaveAs(filepath.Join(dir, name)); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
}

func TestScanCountsAndHeaders(t *testing.T) {
	dir := t.TempDir()
	mkxlsx(t, dir, "a.xlsx", []string{"部门", "金额"}, [][]string{
		{"销售", "100"},
		{"技术", "200"},
		{"", ""}, // 空行不计
	})
	mkxlsx(t, dir, "b.xlsx", []string{"部门", "金额", "备注"}, [][]string{
		{"运营", "300", "x"},
	})
	// 假 .xls：现在 .xls 是支持的格式（银行流水大量是它），
	// 所以它会被当成表格文件去读，读不动就带上 Err —— 而不是悄悄跳过。
	os.WriteFile(filepath.Join(dir, "c.xls"), []byte("old"), 0o644)
	os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hi"), 0o644)   // 非表格
	os.WriteFile(filepath.Join(dir, "~$a.xlsx"), []byte("lock"), 0o644) // Excel 锁文件

	got, err := Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	var good, broken int
	for _, f := range got.Files {
		if f.Err != "" {
			broken++
		} else {
			good++
		}
	}
	if good != 2 {
		t.Fatalf("应当读出 2 个可用的表，实际 %d: %+v", good, got.Files)
	}
	if broken != 1 {
		t.Fatalf("假 .xls 应当被识别出来但报错，实际 broken=%d: %+v", broken, got.Files)
	}
	if got.TotalRows != 3 {
		t.Fatalf("数据行应为 3（空行不算），实际 %d", got.TotalRows)
	}
	if got.Ignored != 1 {
		t.Fatalf("非表格文件应为 1，实际 %d", got.Ignored)
	}
	if len(got.Unsupported) != 0 {
		t.Fatalf(".xls 现在支持了，不该再出现在 Unsupported 里: %+v", got.Unsupported)
	}
	if got.Files[0].Name != "a.xlsx" || len(got.Files[0].Headers) != 2 {
		t.Fatalf("文件顺序或表头不对: %+v", got.Files[0])
	}
}

func TestScanRejectsNonDir(t *testing.T) {
	if _, err := Scan(context.Background(), ""); err == nil {
		t.Fatal("空目录应报错")
	}
	f := filepath.Join(t.TempDir(), "x.xlsx")
	os.WriteFile(f, []byte("x"), 0o644)
	if _, err := Scan(context.Background(), f); err == nil {
		t.Fatal("传文件而不是目录应报错")
	}
}

// DryRun 不能产生任何文件 —— 这是「先给我看要做什么」的硬要求。
func TestMergeDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	mkxlsx(t, dir, "a.xlsx", []string{"部门", "金额"}, [][]string{{"销售", "100"}})
	mkxlsx(t, dir, "b.xlsx", []string{"部门", "金额"}, [][]string{{"技术", "200"}})

	got, err := Merge(context.Background(), MergeOptions{Dir: dir, DryRun: true})
	if err != nil {
		t.Fatalf("预览失败: %v", err)
	}
	if got.RowsOut != 2 || got.Files != 2 {
		t.Fatalf("预览统计不对: %+v", got)
	}
	if got.OutputPath != "" {
		t.Fatalf("预览不该有输出路径: %s", got.OutputPath)
	}
	if _, err := os.Stat(filepath.Join(dir, "deskkit-out")); !os.IsNotExist(err) {
		t.Fatal("预览不该创建输出目录")
	}
}

// 列顺序不同、缺列、多列都要正确处理 —— 这是真实部门报表的常态。
// 语义：表头取所有文件列名的并集（按首次出现），缺列留空，一列都不丢。
func TestMergeAlignsByColumnName(t *testing.T) {
	dir := t.TempDir()
	mkxlsx(t, dir, "a.xlsx", []string{"部门", "金额"}, [][]string{
		{"销售", "100"},
		{"技术", "200"},
	})
	// 列序反过来，并且多一列
	mkxlsx(t, dir, "b.xlsx", []string{"金额", "部门", "备注"}, [][]string{
		{"300", "运营", "特殊单"},
	})
	// 缺「金额」列
	mkxlsx(t, dir, "c.xlsx", []string{"部门"}, [][]string{
		{"财务"},
	})

	got, err := Merge(context.Background(), MergeOptions{Dir: dir, DryRun: false})
	if err != nil {
		t.Fatalf("合并失败: %v", err)
	}
	if got.RowsOut != 4 {
		t.Fatalf("应有 4 行，实际 %d", got.RowsOut)
	}
	if len(got.Columns) != 3 {
		t.Fatalf("列应为并集（部门/金额/备注 三列），实际 %v", got.Columns)
	}
	if len(got.Warnings) == 0 {
		t.Fatal("缺列应当产生 warning，而不是静默留空")
	}

	f, err := excelize.OpenFile(got.OutputPath)
	if err != nil {
		t.Fatalf("打不开输出文件: %v", err)
	}
	defer f.Close()
	rows, err := f.GetRows(f.GetSheetList()[0])
	if err != nil {
		t.Fatalf("读取输出失败: %v", err)
	}
	if len(rows) != 5 { // 表头 + 4 行
		t.Fatalf("输出应有 5 行，实际 %d: %v", len(rows), rows)
	}
	if rows[0][0] != "部门" || rows[0][1] != "金额" || rows[0][2] != "备注" {
		t.Fatalf("表头应为首次出现的顺序，实际 %v", rows[0])
	}
	// b.xlsx 的「金额」在前，必须按列名搬到第 2 列；备注也不能丢
	var aligned, kept bool
	for _, r := range rows[1:] {
		// 金额现在是数值单元格，回读会带上千分位/两位小数格式（300 → "300.00"），
		// 所以按数值比，不按字符串比。
		if len(r) >= 3 && r[0] == "运营" && sameNumber(r[1], 300) && r[2] == "特殊单" {
			aligned = true
		}
		// excelize 读回时会把行尾空单元格裁掉，所以「留空」表现为列数不足
		if r[0] == "财务" && (len(r) < 2 || r[1] == "") {
			kept = true
		}
	}
	if !aligned {
		t.Fatalf("列名对齐失败：没找到 运营/300/特殊单，实际 %v", rows)
	}
	if !kept {
		t.Fatalf("缺列的行应留空而不是被丢弃，实际 %v", rows)
	}
}

func TestMergeDedup(t *testing.T) {
	dir := t.TempDir()
	mkxlsx(t, dir, "a.xlsx", []string{"订单号", "金额"}, [][]string{
		{"A1", "100"},
		{"A2", "200"},
	})
	mkxlsx(t, dir, "b.xlsx", []string{"订单号", "金额"}, [][]string{
		{"A2", "200"}, // 重复
		{"A3", "300"},
	})

	got, err := Merge(context.Background(), MergeOptions{
		Dir: dir, DedupKey: []string{"订单号"}, DryRun: true,
	})
	if err != nil {
		t.Fatalf("合并失败: %v", err)
	}
	if got.RowsIn != 4 {
		t.Fatalf("输入应为 4 行，实际 %d", got.RowsIn)
	}
	if got.RowsOut != 3 || got.Duplicates != 1 {
		t.Fatalf("去重结果不对: out=%d dup=%d", got.RowsOut, got.Duplicates)
	}
}

// 去重键写错时不能默默按空键去重（那会把所有行合并成 1 行）。
func TestMergeUnknownDedupKeyIsIgnored(t *testing.T) {
	dir := t.TempDir()
	mkxlsx(t, dir, "a.xlsx", []string{"部门"}, [][]string{{"销售"}, {"技术"}})

	got, err := Merge(context.Background(), MergeOptions{
		Dir: dir, DedupKey: []string{"不存在的列"}, DryRun: true,
	})
	if err != nil {
		t.Fatalf("合并失败: %v", err)
	}
	if got.RowsOut != 2 {
		t.Fatalf("未知去重键应当忽略去重，保留 2 行，实际 %d", got.RowsOut)
	}
	if len(got.Warnings) == 0 {
		t.Fatal("应当提示去重键无效")
	}
}

func TestMergeEmptyDir(t *testing.T) {
	if _, err := Merge(context.Background(), MergeOptions{Dir: t.TempDir()}); err == nil {
		t.Fatal("空目录应报错")
	}
}

func TestMergeNeverOverwritesOriginal(t *testing.T) {
	dir := t.TempDir()
	mkxlsx(t, dir, "a.xlsx", []string{"部门"}, [][]string{{"销售"}})
	before, err := os.ReadFile(filepath.Join(dir, "a.xlsx"))
	if err != nil {
		t.Fatal(err)
	}

	got, err := Merge(context.Background(), MergeOptions{Dir: dir})
	if err != nil {
		t.Fatalf("合并失败: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(dir, "a.xlsx"))
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatal("原文件被改动了")
	}
	if filepath.Dir(got.OutputPath) == dir {
		t.Fatalf("输出必须落在独立目录，实际 %s", got.OutputPath)
	}
}

// 任何一列都不能因为「别的文件没有」而丢失。
func TestMergeKeepsEveryColumn(t *testing.T) {
	dir := t.TempDir()
	mkxlsx(t, dir, "a-窄.xlsx", []string{"部门"}, [][]string{{"销售"}})
	mkxlsx(t, dir, "b-宽.xlsx", []string{"部门", "金额", "备注"}, [][]string{{"技术", "200", "x"}})

	got, err := Merge(context.Background(), MergeOptions{Dir: dir, DryRun: true})
	if err != nil {
		t.Fatalf("合并失败: %v", err)
	}
	if len(got.Columns) != 3 {
		t.Fatalf("列并集应为 3 列，实际 %v", got.Columns)
	}
	if got.Columns[0] != "部门" || got.Columns[1] != "金额" || got.Columns[2] != "备注" {
		t.Fatalf("列顺序应按首次出现：%v", got.Columns)
	}
}

// sameNumber 按数值比较单元格文本（"300" 与 "300.00" 视为相同）。
func sameNumber(cell string, want float64) bool {
	got, ok := asNumber(cell)
	return ok && got == want
}
