package excel

// TASK-038 第 2 轮修复的回归测试。
//
// 对应复核报告（evidence/verify-report.md）的编号：
//   · R-001  query_file / query_sheets 装载路径数据比表头宽时整列静默丢失（P0）
//   · P1-9   输出路径越界只在 write_query_result 一处被挡（convert/transform/group/join/split/merge 全裸奔）
//   · N-1    判重键把所有逗号都去掉，"1,2,3" 被当成 123
//   · R-002  16 位以上且带小数的数值仍写成数值（精度丢失）
//   · R-003  merge 的「处理说明」页没有记录重名列改名

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klphp/deskkit-excel/internal/ai"
	"github.com/xuri/excelize/v2"
)

// ── R-001 query 装载路径：数据比表头宽，不许丢列 ──────────

func TestLoadCatalogKeepsExtraColumnsWithWarning(t *testing.T) {
	dir := t.TempDir()
	// 表头 2 列、数据 4 列：用户从系统导出的 CSV 经常这样（表头合并单元格 / 手写漏列）
	csv := "\uFEFF姓名,金额\n张三,100,餐费,备注A\n李四,200,差旅,备注B\n"
	if err := os.WriteFile(filepath.Join(dir, "支出.csv"), []byte(csv), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	db, cat, err := LoadCatalog(ctx, dir)
	if err != nil {
		t.Fatalf("载入失败: %v", err)
	}
	defer db.Close()

	if len(cat.Tables) != 1 {
		t.Fatalf("应装 1 张表，实际 %d", len(cat.Tables))
	}
	ti := cat.Tables[0]
	if len(ti.Columns) != 4 {
		t.Fatalf("SQL 表应建 4 列（源数据 4 列），实际 %q", ti.Columns)
	}
	if ti.Columns[2] != "Column_3" || ti.Columns[3] != "Column_4" {
		t.Errorf("the added columns must be named Column_3/Column_4, actual %q", ti.Columns)
	}
	if len(ti.Warnings) == 0 {
		t.Error("补表头必须给 warning")
	}

	// 真正查一次：第 3/4 列的数据必须在库里
	res, err := Query(ctx, db, `SELECT * FROM "`+ti.Table+`"`, 10)
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if len(res.Columns) != 4 {
		t.Fatalf("SELECT * 应返回 4 列，实际 %q", res.Columns)
	}
	if len(res.Rows) != 2 || res.Rows[0][2] != "餐费" || res.Rows[1][3] != "备注B" {
		t.Fatalf("第 3/4 列数据丢了: %q", res.Rows)
	}

	// warning 必须出现在 Catalog.Note 里（只留在 TableInfo 内部字段等于没说）
	if !strings.Contains(cat.Note, "Column 3") || !strings.Contains(cat.Note, "Column 4") {
		t.Errorf("Catalog.Note must mention the added Column 3/Column 4, actual: %q", cat.Note)
	}
}

func TestQuerySheetsToolReturnsLoadWarnings(t *testing.T) {
	dir := t.TempDir()
	csv := "\uFEFF姓名,金额\n张三,100,餐费,备注A\n"
	if err := os.WriteFile(filepath.Join(dir, "支出.csv"), []byte(csv), 0o644); err != nil {
		t.Fatal(err)
	}

	r := ai.NewRegistry()
	if err := Register(r); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// 先拿表名
	listArgs, _ := json.Marshal(map[string]any{"dir": dir})
	listed, err := r.Execute(ctx, "list_tables", string(listArgs))
	if err != nil {
		t.Fatalf("list_tables 失败: %v", err)
	}
	var catalog struct {
		Tables []TableInfo `json:"tables"`
		Note   string      `json:"note"`
	}
	if err := json.Unmarshal([]byte(listed), &catalog); err != nil {
		t.Fatalf("list_tables 返回不是 JSON: %q", listed)
	}
	if len(catalog.Tables) != 1 {
		t.Fatalf("应列出 1 张表，实际 %d", len(catalog.Tables))
	}
	if !strings.Contains(catalog.Note, "Column 3") {
		t.Errorf("the list_tables note must mention the added Column 3, actual: %q", catalog.Note)
	}

	// query_sheets 的返回体里也要能看到这条说明
	qArgs, _ := json.Marshal(map[string]any{
		"dir": dir, "sql": `SELECT * FROM "` + catalog.Tables[0].Table + `"`,
	})
	out, err := r.Execute(ctx, "query_sheets", string(qArgs))
	if err != nil {
		t.Fatalf("query_sheets 失败: %v", err)
	}
	var res QueryResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("query_sheets 返回不是 JSON: %q", out)
	}
	if len(res.Columns) != 4 {
		t.Fatalf("query_sheets 应返回 4 列，实际 %q", res.Columns)
	}
	if !strings.Contains(res.Note, "Column 3") {
		t.Errorf("the query_sheets note must mention the added Column 3, actual: %q", res.Note)
	}
}

// ── P1-9 输出路径边界校验（所有落盘入口共用） ──────────────

func TestResolveOutputPathRules(t *testing.T) {
	base := t.TempDir()

	// ① 含 .. 段一律拒绝（不论前缀看起来多正常）
	for _, p := range []string{
		"../OUTSIDE.csv",
		"in/../OUTSIDE.csv",
		`in\..\OUTSIDE.csv`,
		"a/b/../../x.csv",
		"..",
	} {
		if _, err := resolveOutputPath(base, p); err == nil {
			t.Errorf("resolveOutputPath(%q) 应被拒绝（会跳出基准目录）", p)
		}
	}

	// ② 空路径拒绝
	if _, err := resolveOutputPath(base, "  "); err == nil {
		t.Error("空输出路径应报错")
	}

	// ③ 绝对路径照常放行：用户明确说「存到桌面」是合法用法，不做目录白名单
	outside := filepath.Join(t.TempDir(), "桌面", "OUTSIDE.csv")
	got, err := resolveOutputPath(base, outside)
	if err != nil {
		t.Fatalf("绝对路径被误伤: %v", err)
	}
	if got != filepath.Clean(outside) {
		t.Errorf("绝对路径应原样放行，实际 %q", got)
	}

	// ④ 相对路径落在基准目录里 → 相对基准解析
	rel, err := resolveOutputPath(base, "deskkit-out/汇总.xlsx")
	if err != nil {
		t.Fatalf("正常的相对路径被误伤: %v", err)
	}
	if want := filepath.Join(base, "deskkit-out", "汇总.xlsx"); rel != want {
		t.Errorf("相对路径应按基准目录解析，期望 %q，实际 %q", want, rel)
	}

	// ⑤ 不许等于源文件
	src := filepath.Join(base, "源表.xlsx")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveOutputPath(base, src, src); err == nil {
		t.Error("输出路径等于源文件时必须拒绝")
	}
}

func TestConvertRejectsEscapeOutputPath(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in")
	if err := os.MkdirAll(in, 0o755); err != nil {
		t.Fatal(err)
	}
	mkxlsx(t, in, "a.xlsx", []string{"姓名", "金额"}, [][]string{{"张三", "100"}})

	// 复核报告里的真实复现用例：以前会真的写到父目录
	if _, _, err := Convert(ConvertOptions{
		Path:       filepath.Join(in, "a.xlsx"),
		OutputPath: "in/../OUTSIDE.csv",
	}); err == nil {
		t.Fatal("outputPath 带 ../ 竟然写成功了 —— 会落到源目录之外")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "OUTSIDE.csv")); statErr == nil {
		t.Fatal("文件真的写到源目录之外了")
	}
}

func TestConvertAllowsAbsoluteOutputOutsideSourceDir(t *testing.T) {
	srcDir := t.TempDir()
	outDir := t.TempDir()
	mkxlsx(t, srcDir, "a.xlsx", []string{"姓名", "金额"}, [][]string{{"张三", "100"}})

	out := filepath.Join(outDir, "汇总.csv")
	got, rows, err := Convert(ConvertOptions{Path: filepath.Join(srcDir, "a.xlsx"), OutputPath: out})
	if err != nil {
		t.Fatalf("绝对路径输出被误伤: %v", err)
	}
	if got != out {
		t.Errorf("应写到 %q，实际 %q", out, got)
	}
	if rows != 1 {
		t.Errorf("应写 1 行数据，实际 %d", rows)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("输出文件不存在: %v", err)
	}
}

func TestMergeRejectsEscapeOutputName(t *testing.T) {
	dir := t.TempDir()
	mkxlsx(t, dir, "A.xlsx", []string{"姓名", "金额"}, [][]string{{"张三", "100"}})

	if _, err := Merge(context.Background(), MergeOptions{
		Dir:        dir,
		OutputDir:  filepath.Join(dir, "in"),
		OutputName: "../OUTSIDE.xlsx",
	}); err == nil {
		t.Fatal("outputName 带 ../ 竟然写成功了 —— 会落到输出目录之外")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "OUTSIDE.xlsx")); statErr == nil {
		t.Fatal("文件真的写到输出目录之外了")
	}
}

func TestSplitStackRejectsEscapeOutputPath(t *testing.T) {
	dir := t.TempDir()
	mkxlsx(t, dir, "a.xlsx", []string{"姓名", "金额"}, [][]string{{"张三", "100"}})

	r := ai.NewRegistry()
	if err := Register(r); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{
		"path": filepath.Join(dir, "a.xlsx"), "mode": "stack",
		"outputPath": "../OUTSIDE.xlsx",
	})
	if _, err := r.Execute(context.Background(), "split_table", string(args)); err == nil {
		t.Fatal("split_table stack 模式的 outputPath 带 ../ 竟然写成功了")
	}
}

// ── N-1 判重键只去掉真正的千分位逗号 ────────────────────

func TestNormalizeKeyValueOnlyStripsThousandSeparators(t *testing.T) {
	// 含逗号的文本不能被当成数字：「1,2,3」去掉逗号就是 123
	if got := normalizeKeyValue("1,2,3"); got != "1,2,3" {
		t.Errorf(`normalizeKeyValue("1,2,3") = %q，应与原文一致（不能被归一成 123）`, got)
	}
	if normalizeKeyValue("1,2,3") == normalizeKeyValue("123") {
		t.Error(`"1,2,3" 与 "123" 被判成同一个值 —— 会产生假的重复`)
	}
	// 千分位写法仍要归一：1,234 == 1234
	if normalizeKeyValue("1,234") != normalizeKeyValue("1234") {
		t.Errorf(`normalizeKeyValue("1,234") = %q，应与 1234 相同`, normalizeKeyValue("1,234"))
	}
	if got := normalizeKeyValue("12,345,678.50"); got != "12345678.5" {
		t.Errorf(`normalizeKeyValue("12,345,678.50") = %q，期望 12345678.5`, got)
	}
	if got := normalizeKeyValue("-1,234.50"); got != "-1234.5" {
		t.Errorf(`normalizeKeyValue("-1,234.50") = %q，期望 -1234.5`, got)
	}
	if got := normalizeKeyValue("1，234"); got != "1234" { // 全角逗号同样是千分位
		t.Errorf(`normalizeKeyValue("1，234") = %q，期望 1234`, got)
	}
	// 逗号位置不对就不是数字
	if got := normalizeKeyValue("12,34"); got != "12,34" {
		t.Errorf(`normalizeKeyValue("12,34") = %q，应原样返回`, got)
	}
}

// ── R-002 整数位 ≥16 位，带小数也必须按文本 ──────────────

func TestAsNumberRejectsLongIntegerWithFraction(t *testing.T) {
	if _, ok := asNumber("1234567890123456.78"); ok {
		t.Error("16 位整数 + 小数仍被当数值写 —— float64 只有 15 位有效数字")
	}
	if _, ok := asNumber("￥1,234,567,890,123,456.78"); ok {
		t.Error("带千分位的 16 位整数 + 小数仍被当数值写")
	}
	if _, ok := asNumber("123456789012345.78"); !ok {
		t.Error("15 位整数 + 小数应仍可当数值（Excel 能精确表示整数位）")
	}
	if _, ok := asNumber("1234.5678"); !ok {
		t.Error("普通金额被误伤了")
	}
	// 列级判断同步生效：整列按文本存
	if numericColumn([]string{"1234567890123456.78"}) {
		t.Error("numericColumn 仍把 16 位整数 + 小数判为数值列")
	}
}

// ── R-003 merge 的「处理说明」页要记录重名列改名 ──────────

func TestMergeMetaRecordsRenameWarning(t *testing.T) {
	dir := t.TempDir()
	mkxlsx(t, dir, "A.xlsx",
		[]string{"姓名", "金额", "金额"},
		[][]string{{"张三", "100", "999"}})

	res, err := Merge(context.Background(), MergeOptions{Dir: dir, OutputName: "m.xlsx"})
	if err != nil {
		t.Fatalf("合并失败: %v", err)
	}

	f, err := excelize.OpenFile(res.OutputPath)
	if err != nil {
		t.Fatalf("打不开输出: %v", err)
	}
	defer f.Close()
	lines, err := f.GetRows(metaSheetName)
	if err != nil {
		t.Fatalf("读「%s」页失败: %v", metaSheetName, err)
	}
	var sb strings.Builder
	for _, row := range lines {
		sb.WriteString(strings.Join(row, " "))
		sb.WriteString("\n")
	}
	if !strings.Contains(sb.String(), "金额-2") {
		t.Errorf("「处理说明」页应记录重名列改名为 金额-2，实际:\n%s", sb.String())
	}
}
