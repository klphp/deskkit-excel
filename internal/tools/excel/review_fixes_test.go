package excel

// TASK-037 审查发现的问题的回归测试。
//
// 每一条都对应审查报告里的一个编号（P0-1 … P1-10）。这些测试的存在理由是
// 「出过事的分支必须有回归保护」—— 审查报告指出：覆盖率数字不难看，
// 但风险最高的四个分支恰好没测。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klphp/deskkit-excel/internal/ai"
	"github.com/xuri/excelize/v2"
)

// ── P0-1 长编号必须按文本写 ──────────────────────────────

func TestAsNumberRejectsLongIntegers(t *testing.T) {
	cases := []struct {
		in   string
		want bool // true = 可以当数值写
	}{
		{"110101199003071234", false},  // 18 位身份证
		{"6222021234567890123", false}, // 19 位银行卡
		{"1234567890123456", false},    // 刚好 16 位
		{"123456789012345", true},      // 15 位：Excel 还能精确表示
		{"1234.5", true},               // 普通金额
		{"1,234.50", true},             // 千分位
		{"(1,234.50)", true},           // 会计负数
		{"00123", false},               // 前导零：工号不能变 123
		{"1234567890123456.78", false}, // 整数位 16 位：带小数也只剩 15 位有效数字
		{"123456789012345.78", true},   // 整数位 15 位：仍可当数值
		{"１２３４", true},                 // 全角
		{"-", false},                   //
		{"", false},                    //
	}
	for _, c := range cases {
		if _, ok := asNumber(c.in); ok != c.want {
			t.Errorf("asNumber(%q) = %v，期望 %v", c.in, ok, c.want)
		}
	}
}

func TestWriteTableKeepsLongIdsExact(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ids.xlsx")
	headers := []string{"工号", "身份证", "卡号", "备注", "金额"}
	rows := [][]string{
		{"00123", "110101199003071234", "6222021234567890123", "123456789012345678", "1234.5"},
	}
	if _, err := WriteTable(path, &Table{Headers: headers, Rows: rows},
		WriteOptions{BoldHeader: true, NumbersAsNumbers: true}); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}

	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("打不开输出: %v", err)
	}
	defer f.Close()
	sh := f.GetSheetName(0)

	read := func(col int) string {
		c, _ := excelize.CoordinatesToCellName(col, 2)
		v, _ := f.GetCellValue(sh, c)
		return v
	}

	// 关键断言：身份证 / 卡号 / 18 位纯数字必须一字不差
	if got := read(2); got != "110101199003071234" {
		t.Errorf("身份证被改写成了 %q（末位丢了）", got)
	}
	if got := read(3); got != "6222021234567890123" {
		t.Errorf("卡号被改写成了 %q", got)
	}
	if got := read(4); got != "123456789012345678" {
		t.Errorf("18 位纯数字被改写成了 %q", got)
	}
	if got := read(1); got != "00123" {
		t.Errorf("工号前导零丢了: %q", got)
	}
	// 金额仍然要写成数值（带千分位两位小数），否则用户连合计都看不到
	if got := read(5); !strings.Contains(got, "1,234.50") {
		t.Errorf("金额没有按数值格式写出: %q", got)
	}

	// 处理说明里必须告诉用户「这几列被强制成文本了」
	meta, err := f.GetRows(metaSheetName)
	if err != nil || len(meta) == 0 {
		t.Fatalf("没有处理说明页: %v", err)
	}
	joined := ""
	for _, r := range meta {
		joined += strings.Join(r, " ")
	}
	for _, want := range []string{"身份证", "卡号", "written as text"} {
		if !strings.Contains(joined, want) {
			t.Errorf("处理说明里没提到 %q:\n%s", want, joined)
		}
	}
}

func TestForceTextColumnsByHeader(t *testing.T) {
	headers := []string{"姓名", "身份证号", "银行卡号", "工号", "手机", "邮编", "电话费", "金额"}
	got := forceTextColumns(headers)
	for i, want := range []bool{false, true, true, true, true, true, false, false} {
		if got[i] != want {
			t.Errorf("列 %q forceText=%v，期望 %v", headers[i], got[i], want)
		}
	}
}

// ── P0-2 write_query_result 不许覆盖源文件 ──────────────

func TestWriteQueryNeverOverwritesSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "销售.csv")
	original := "\uFEFF单号,金额\n1001,10\n"
	if err := os.WriteFile(src, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, _, err := LoadCatalog(ctx, dir)
	if err != nil {
		t.Fatalf("载入失败: %v", err)
	}
	defer db.Close()

	// ① 目标就是源文件 → 直接报错
	if _, _, err := WriteQuery(ctx, db, `SELECT 1 AS x`, dir, "销售.csv", src); err == nil {
		t.Fatal("写回源文件竟然成功了 —— 会直接覆盖用户的原始表格")
	}
	after, _ := os.ReadFile(src)
	if string(after) != original {
		t.Fatalf("源文件被改了:\nbefore=%q\nafter=%q", original, after)
	}

	// ② 不指明 protect 时也不能覆盖：uniquePath 要生效
	p, _, err := WriteQuery(ctx, db, `SELECT 1 AS x`, dir, "销售.csv")
	if err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
	if p == src {
		t.Fatal("返回的就是源文件路径")
	}
	if filepath.Base(p) != "销售-1.csv" {
		t.Errorf("同名时应加 -1，实际 %s", filepath.Base(p))
	}
	after, _ = os.ReadFile(src)
	if string(after) != original {
		t.Fatalf("源文件被改了:\nbefore=%q\nafter=%q", original, after)
	}
}

func TestWriteQueryRejectsPathInFileName(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "in"), 0o755); err != nil {
		t.Fatal(err)
	}
	mkxlsx(t, filepath.Join(dir, "in"), "a.xlsx", []string{"单号"}, [][]string{{"1"}})
	ctx := context.Background()
	db, _, err := LoadCatalog(ctx, filepath.Join(dir, "in"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, _, err := WriteQuery(ctx, db, `SELECT 1 AS x`, filepath.Join(dir, "in"), "../OUTSIDE.csv"); err == nil {
		t.Fatal("文件名带 ../ 竟然写成功了 —— 能落到输出目录之外")
	}
	if _, err := os.Stat(filepath.Join(dir, "OUTSIDE.csv")); err == nil {
		t.Fatal("文件真的写到输出目录之外了")
	}
}

// ── P0-3 空表头 / 数据比表头宽 ──────────────────────────

func TestLoadTableNamesBlankHeaderAndKeepsData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unnamed.xlsx")
	f := excelize.NewFile()
	sh := f.GetSheetName(0)
	_ = f.SetCellStr(sh, "A1", "姓名")
	_ = f.SetCellStr(sh, "B1", "")
	_ = f.SetCellStr(sh, "C1", "金额")
	_ = f.SetCellStr(sh, "A2", "张三")
	_ = f.SetCellStr(sh, "B2", "备注X")
	_ = f.SetCellStr(sh, "C2", "100")
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	f.Close()

	tb, err := LoadTable(path, "")
	if err != nil {
		t.Fatalf("读表失败: %v", err)
	}
	if len(tb.Headers) != 3 || tb.Headers[1] != "Column 2" {
		t.Fatalf("a blank header must be named 'Column 2', actual headers=%q", tb.Headers)
	}
	if len(tb.Rows) != 1 || tb.Rows[0][1] != "备注X" {
		t.Fatalf("中间列的数据丢了: %q", tb.Rows)
	}
	if len(tb.Warnings) == 0 {
		t.Error("补表头必须给 warning")
	}
}

func TestLoadTablePadsHeadersWhenDataWider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wide.xlsx")
	f := excelize.NewFile()
	sh := f.GetSheetName(0)
	_ = f.SetCellStr(sh, "A1", "姓名")
	_ = f.SetCellStr(sh, "B1", "金额")
	_ = f.SetCellStr(sh, "A2", "张三")
	_ = f.SetCellStr(sh, "B2", "100")
	_ = f.SetCellStr(sh, "C2", "备注X")
	_ = f.SetCellStr(sh, "D2", "部门Y")
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	f.Close()

	tb, err := LoadTable(path, "")
	if err != nil {
		t.Fatalf("读表失败: %v", err)
	}
	if len(tb.Headers) != 4 {
		t.Fatalf("数据 4 列，表头应补到 4 列，实际 %q", tb.Headers)
	}
	if len(tb.Rows[0]) != 4 || tb.Rows[0][2] != "备注X" || tb.Rows[0][3] != "部门Y" {
		t.Fatalf("超宽的数据列丢了: %q", tb.Rows)
	}
	if len(tb.Warnings) == 0 {
		t.Error("补表头必须给 warning")
	}
}

func TestCleanFileKeepsExtraColumns(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "c.csv")
	// 表头 2 列，数据 3 列
	if err := os.WriteFile(src, []byte("姓名,金额\n张三,100,备注X\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := CleanFile(context.Background(), CleanFileOptions{
		Path: src, DropBlankRows: true, TrimSpaces: true,
	})
	if err != nil {
		t.Fatalf("清洗失败: %v", err)
	}
	if res.Output == "" {
		t.Fatal("没有输出文件")
	}
	tb, err := LoadTable(res.Output, "")
	if err != nil {
		t.Fatalf("读输出失败: %v", err)
	}
	if len(tb.Headers) != 3 {
		t.Fatalf("表头应补到 3 列，实际 %q", tb.Headers)
	}
	if len(tb.Rows[0]) != 3 || tb.Rows[0][2] != "备注X" {
		t.Fatalf("第三列被截断了: %q", tb.Rows)
	}
}

// ── P0-4 merge 重名列 ────────────────────────────────────

func TestMergeKeepsDuplicateColumns(t *testing.T) {
	dir := t.TempDir()
	mkxlsx(t, dir, "A.xlsx",
		[]string{"姓名", "金额", "金额"},
		[][]string{{"张三", "100", "999"}})

	res, err := Merge(context.Background(), MergeOptions{Dir: dir, OutputName: "m.xlsx"})
	if err != nil {
		t.Fatalf("合并失败: %v", err)
	}
	if len(res.Columns) != 3 {
		t.Fatalf("两个「金额」列都该保留，实际 %q", res.Columns)
	}
	if res.Columns[2] != "金额-2" {
		t.Errorf("第二个「金额」应改名为 金额-2，实际 %q", res.Columns[2])
	}
	if len(res.Warnings) == 0 {
		t.Error("重名列改名必须给 warning")
	}

	tb, err := LoadTable(res.OutputPath, "merged")
	if err != nil {
		t.Fatalf("读输出失败: %v", err)
	}
	row := tb.Rows[0]
	if !strings.Contains(row[1], "100") || !strings.Contains(row[2], "999") {
		t.Fatalf("重名列的数据丢了: %q", row)
	}
}

// ── P1-1 merge 回显真实输出路径 ──────────────────────────

func TestMergeReportsActualOutputPath(t *testing.T) {
	dir := t.TempDir()
	mkxlsx(t, dir, "A.xlsx", []string{"姓名", "金额"}, [][]string{{"张三", "100"}})
	ctx := context.Background()

	r1, err := Merge(ctx, MergeOptions{Dir: dir, OutputName: "merged.xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := Merge(ctx, MergeOptions{Dir: dir, OutputName: "merged.xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	if r1.OutputPath == r2.OutputPath {
		t.Fatalf("两次合并回显同一路径 %s（第二次实际写到 -1，用户会打开旧文件）", r1.OutputPath)
	}
	if _, err := os.Stat(r2.OutputPath); err != nil {
		t.Fatalf("回显的路径不存在: %s (%v)", r2.OutputPath, err)
	}
	if filepath.Base(r2.OutputPath) != "merged-1.xlsx" {
		t.Errorf("第二次应写到 merged-1.xlsx，实际 %s", filepath.Base(r2.OutputPath))
	}
}

// ── P1-2 单文件输出不覆盖 ────────────────────────────────

func TestWriteRowsAlongNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "s.xlsx")
	mkxlsx(t, dir, "s.xlsx", []string{"a"}, [][]string{{"1"}})

	p1, err := WriteRowsAlong(src, "suffix", []string{"a"}, [][]string{{"1"}})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := WriteRowsAlong(src, "suffix", []string{"a"}, [][]string{{"2"}})
	if err != nil {
		t.Fatal(err)
	}
	if p1 == p2 {
		t.Fatalf("两次写到同一路径 %s（第一次结果被覆盖了）", p1)
	}
}

// ── P1-3 join 一对多必须显式选择 ────────────────────────

func TestJoinOnDuplicatePolicies(t *testing.T) {
	dir := t.TempDir()
	mkxlsx(t, dir, "left.xlsx", []string{"单号", "客户"}, [][]string{
		{"1001", "甲"}, {"1002", "乙"},
	})
	mkxlsx(t, dir, "right.xlsx", []string{"单号", "金额"}, [][]string{
		{"1001", "10"}, {"1001", "20"}, {"1001", "30"},
	})
	left := filepath.Join(dir, "left.xlsx")
	right := filepath.Join(dir, "right.xlsx")

	// ① 默认：右表 key 重复 → 报错，不许静默取第一条
	if _, err := Join(JoinOptions{LeftPath: left, RightPath: right,
		LeftKey: "单号", RightKey: "单号"}); err == nil {
		t.Fatal("右表 key 重复时默认应当报错")
	}

	// ② first：老行为，取第一条
	res, err := Join(JoinOptions{LeftPath: left, RightPath: right,
		LeftKey: "单号", RightKey: "单号", Bring: []string{"金额"}, OnDuplicate: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if res.RightDupKeys != 1 || res.OnDuplicate != "first" {
		t.Fatalf("诊断信息不对: dup=%d policy=%q", res.RightDupKeys, res.OnDuplicate)
	}
	if res.Warning == "" {
		t.Error("first 策略必须留下 warning")
	}

	// ③ sum：金额必须求和（10+20+30=60），这才是财务要的
	res, err = Join(JoinOptions{LeftPath: left, RightPath: right,
		LeftKey: "单号", RightKey: "单号", Bring: []string{"金额"}, OnDuplicate: "sum"})
	if err != nil {
		t.Fatal(err)
	}
	if got := cellAt(res.Table.Rows[0], 2); got != "60" {
		t.Fatalf("sum 策略应得 60，实际 %q", got)
	}

	// ④ all：行展开
	res, err = Join(JoinOptions{LeftPath: left, RightPath: right,
		LeftKey: "单号", RightKey: "单号", Bring: []string{"金额"}, OnDuplicate: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if res.OutputRows != 4 { // 1001 出 3 行 + 1002 出 1 行
		t.Fatalf("all 策略应展开成 4 行，实际 %d", res.OutputRows)
	}

	// ⑤ 非法策略要报错，不能默默当成 first
	if _, err := Join(JoinOptions{LeftPath: left, RightPath: right,
		LeftKey: "单号", RightKey: "单号", OnDuplicate: "whatever"}); err == nil {
		t.Fatal("非法 onDuplicate 应当报错")
	}
}

// ── P1-4 判重键归一 ─────────────────────────────────────

func TestNormalizeKeyValueCanonicalizes(t *testing.T) {
	same := [][]string{
		{"1001", "1001.0", " 1001", "001001", "1,001"},
		{"-0", "0", "0.0"},
		{"2.50", "2.5", "002.500"},
	}
	for _, group := range same {
		want := normalizeKeyValue(group[0])
		for _, v := range group[1:] {
			if got := normalizeKeyValue(v); got != want {
				t.Errorf("%q 归一后 %q，与 %q 的 %q 不等", v, got, group[0], want)
			}
		}
	}
	// 不同值不能被归一成同一个
	if normalizeKeyValue("1001") == normalizeKeyValue("1002") {
		t.Error("不同单号被归一到一起了")
	}
	// 文本原样（只去空白与全角）
	if got := normalizeKeyValue(" 张三 "); got != "张三" {
		t.Errorf("文本归一结果 %q", got)
	}
	// 负数与正数不能混
	if normalizeKeyValue("-5") == normalizeKeyValue("5") {
		t.Error("-5 与 5 被当成同一个值")
	}
}

func TestMergeDedupNormalizesNumericKeys(t *testing.T) {
	dir := t.TempDir()
	mkxlsx(t, dir, "A.xlsx", []string{"单号", "金额"}, [][]string{
		{"1001", "10"},
		{"1001.0", "10"},
		{" 1001", "10"},
		{"001001", "10"},
	})
	res, err := Merge(context.Background(), MergeOptions{Dir: dir, DedupKey: []string{"单号"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.RowsOut != 1 {
		t.Fatalf("四种写法是同一笔单据，应只剩 1 行，实际 %d 行（重复 %d）", res.RowsOut, res.Duplicates)
	}
	if res.Duplicates != 3 {
		t.Errorf("应报告 3 行重复，实际 %d", res.Duplicates)
	}
}

func TestFindDuplicatesNormalizes(t *testing.T) {
	tb := &Table{
		Headers: []string{"金额"},
		Rows:    [][]string{{"-0"}, {"0"}, {"1.0"}, {"1"}},
	}
	_, total, err := FindDuplicates(tb, []string{"金额"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 {
		t.Fatalf("-0/0 与 1.0/1 各算一组重复，共 4 行，实际 %d", total)
	}
}

// ── P1-5 目录里的 CSV 也要能查 ──────────────────────────

func TestLoadCatalogIncludesCSVAndXLS(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "流水.csv"),
		[]byte("\uFEFF单号,金额\n1001,10\n1002,20\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, cat, err := LoadCatalog(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if len(cat.Tables) != 1 {
		t.Fatalf("只有 CSV 的目录应载入 1 张表，实际 %d（note=%q）", len(cat.Tables), cat.Note)
	}
	res, err := Query(ctx, db, fmt.Sprintf(`SELECT SUM(金额) AS 合计 FROM "%s"`, cat.Tables[0].Table), 10)
	if err != nil {
		t.Fatalf("查 CSV 里的表失败: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0] != "30" {
		t.Fatalf("CSV 合计应为 30，实际 %v", res.Rows)
	}

	// 混放：CSV 与 xlsx 都要在
	mkxlsx(t, dir, "b.xlsx", []string{"单号", "金额"}, [][]string{{"2001", "5"}})
	db2, cat2, err := LoadCatalog(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if len(cat2.Tables) != 2 {
		t.Fatalf("CSV + xlsx 混放应载入 2 张表，实际 %d（%q）", len(cat2.Tables), cat2.Note)
	}
}

// ── P1-7 排序时排不动的值沉底 ───────────────────────────

func TestSortKeepsUnparseableAtEnd(t *testing.T) {
	// 数字占 7/8 > 80%，列会被判成数值列（混一两个「暂无」不该让整列退化成文本排序）
	tb := &Table{
		Headers: []string{"金额"},
		Rows: [][]string{
			{"10"}, {"暂无"}, {"0"}, {"9"}, {"1.5"}, {"2"}, {"7"}, {"100"},
		},
	}
	up, err := Sort(tb, []SortKey{{Column: "金额"}})
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, r := range up.Rows {
		got = append(got, r[0])
	}
	// 前 7 个必须是排好序的数值，最后一个是排不动的「暂无」
	if strings.Join(got[:7], ",") != "0,1.5,2,7,9,10,100" {
		t.Fatalf("数值部分排序不对: %v", got)
	}
	if got[7] != "暂无" {
		t.Fatalf("排不动的值必须沉底，实际顺序 %v", got)
	}

	down, err := Sort(tb, []SortKey{{Column: "金额", Desc: true}})
	if err != nil {
		t.Fatal(err)
	}
	gotDown := []string{}
	for _, r := range down.Rows {
		gotDown = append(gotDown, r[0])
	}
	if strings.Join(gotDown[:7], ",") != "100,10,9,7,2,1.5,0" {
		t.Fatalf("降序数值部分不对: %v", gotDown)
	}
	if gotDown[7] != "暂无" {
		t.Fatalf("降序时排不动的值也必须沉底，实际 %v", gotDown)
	}
}

// ── P1-10 query_file / query_sheets 行数硬上限 ──────────

func TestQueryCapsAtMaxRows(t *testing.T) {
	dir := t.TempDir()
	rows := make([][]string, MaxQueryRows+120)
	for i := range rows {
		rows[i] = []string{"x", "1"}
	}
	mkxlsx(t, dir, "big.xlsx", []string{"a", "b"}, rows)

	ctx := context.Background()
	db, _, err := LoadCatalog(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	res, err := Query(ctx, db, `SELECT * FROM "big_Sheet1"`, 10_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if res.Count != MaxQueryRows {
		t.Fatalf("应被硬限制到 %d 行，实际 %d", MaxQueryRows, res.Count)
	}
	if !res.Truncated {
		t.Error("截断时必须标记 Truncated")
	}
	if !strings.Contains(res.Note, "write_query_result") {
		t.Errorf("截断提示应引导用 write_query_result 落盘，实际 %q", res.Note)
	}

	// 写文件不受上限约束：落盘要全量
	path, n, err := WriteQuery(ctx, db, `SELECT * FROM "big_Sheet1"`, filepath.Join(dir, "out"), "全部.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	if n != len(rows) {
		t.Fatalf("落盘应写全部 %d 行，实际 %d（%s）", len(rows), n, path)
	}
}

// ── P0-2 工具层：write_query_result 拒绝覆盖源文件 ──────

func TestToolWriteQueryResultRefusesSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "销售.csv")
	original := "\uFEFF单号,金额\n1001,10\n"
	if err := os.WriteFile(src, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	r := ai.NewRegistry()
	if err := Register(r); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	args, _ := json.Marshal(map[string]any{
		"dir": dir, "sql": `SELECT 1 AS x`, "outputDir": dir, "fileName": "销售.csv",
	})
	if _, err := r.Execute(ctx, "write_query_result", string(args)); err == nil {
		t.Fatal("工具层允许把结果写回源文件")
	}
	after, _ := os.ReadFile(src)
	if string(after) != original {
		t.Fatalf("源文件被改了: %q", after)
	}

	// 换个名字就该成功，并且落在输出目录里
	args, _ = json.Marshal(map[string]any{
		"dir": dir, "sql": `SELECT 1 AS x`, "fileName": "结论.xlsx",
	})
	out, err := r.Execute(ctx, "write_query_result", string(args))
	if err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
	var payload struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("返回不是 JSON: %q", out)
	}
	if !strings.Contains(payload.Output, "deskkit-out") {
		t.Errorf("默认输出目录应是 <dir>/deskkit-out，实际 %s", payload.Output)
	}
}
