package excel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// 建一份能查的样例：两个部门、金额带货币符号、工号带前导零。
func queryFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mkxlsx(t, dir, "销售部-10月.xlsx", []string{"日期", "部门", "金额", "工号"}, [][]string{
		{"2026-01-02", "销售", "¥1,234.50", "00123"},
		{"2026-03-05", "销售", "980", "00124"},
		{"2026-03-06", "销售", "1000", "00125"},
	})
	mkxlsx(t, dir, "技术部-10月.xlsx", []string{"日期", "部门", "金额", "工号"}, [][]string{
		{"2026-03-05", "技术", "￥2,000", "E45"},
		{"", "", "", ""}, // 空行不该进库
	})
	return dir
}

func TestLoadCatalogBuildsQueryableTables(t *testing.T) {
	dir := queryFixture(t)
	ctx := context.Background()

	db, cat, err := LoadCatalog(ctx, dir)
	if err != nil {
		t.Fatalf("载入失败: %v", err)
	}
	defer db.Close()

	if len(cat.Tables) != 2 {
		t.Fatalf("应有 2 张表，实际 %d: %+v", len(cat.Tables), cat.Tables)
	}
	// 表名要能看出是哪个文件哪张表，AI 才写得对 SQL
	for _, ti := range cat.Tables {
		if !strings.Contains(ti.Table, ti.Sheet) {
			t.Errorf("表名 %q 里应该带工作表名 %q", ti.Table, ti.Sheet)
		}
		if ti.Rows == 0 {
			t.Errorf("%s 没有行", ti.Table)
		}
	}

	// 空行被跳过：销售部 3 行，技术部 1 行
	byFile := map[string]int{}
	for _, ti := range cat.Tables {
		byFile[ti.File] = ti.Rows
	}
	if byFile["销售部-10月.xlsx"] != 3 {
		t.Errorf("销售部应 3 行，实际 %d", byFile["销售部-10月.xlsx"])
	}
	if byFile["技术部-10月.xlsx"] != 1 {
		t.Errorf("技术部应 1 行（空行被丢弃），实际 %d", byFile["技术部-10月.xlsx"])
	}
}

// tableOf 从 catalog 里找出某个文件对应的表名。
// 不手写表名：名字是净化过的（"销售部-10月" 里的 "-" 会变成 "_"），
// 手写等于把净化规则复制进测试。
func tableOf(t *testing.T, cat Catalog, file string) string {
	t.Helper()
	for _, ti := range cat.Tables {
		if ti.File == file {
			return ti.Table
		}
	}
	t.Fatalf("catalog 里没有 %s: %+v", file, cat.Tables)
	return ""
}

// 金额列带货币符号和千分位，也必须能 SUM —— 否则 AI 写的汇总全是错的。
func TestQueryAggregatesCurrencyText(t *testing.T) {
	dir := queryFixture(t)
	ctx := context.Background()
	db, cat, err := LoadCatalog(ctx, dir)
	if err != nil {
		t.Fatalf("载入失败: %v", err)
	}
	defer db.Close()

	table := tableOf(t, cat, "销售部-10月.xlsx")
	res, err := Query(ctx, db, `SELECT 部门, SUM(金额) AS total, COUNT(*) AS n FROM "`+table+`" GROUP BY 部门`, 0)
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("应 1 组，实际 %d", len(res.Rows))
	}
	if res.Rows[0][0] != "销售" {
		t.Errorf("分组值不对: %v", res.Rows[0])
	}
	// 1234.50 + 980 + 1000 = 3214.5
	if res.Rows[0][1] != "3214.5" {
		t.Errorf("合计应为 3214.5，实际 %q", res.Rows[0][1])
	}
	if res.Rows[0][2] != "3" {
		t.Errorf("计数应为 3，实际 %q", res.Rows[0][2])
	}
}

// 前导零的工号必须保持文本，不能被当成数字 123。
func TestQueryKeepsLeadingZeroAsText(t *testing.T) {
	dir := queryFixture(t)
	ctx := context.Background()
	db, cat, err := LoadCatalog(ctx, dir)
	if err != nil {
		t.Fatalf("载入失败: %v", err)
	}
	defer db.Close()

	res, err := Query(ctx, db, `SELECT DISTINCT 工号 FROM "`+tableOf(t, cat, "销售部-10月.xlsx")+`" ORDER BY 工号`, 0)
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if res.Rows[0][0] != "00123" {
		t.Fatalf("工号前导零被吃掉: %q", res.Rows[0][0])
	}
}

func TestQueryRejectsWrites(t *testing.T) {
	dir := queryFixture(t)
	ctx := context.Background()
	db, cat, err := LoadCatalog(ctx, dir)
	if err != nil {
		t.Fatalf("载入失败: %v", err)
	}
	defer db.Close()

	tbl := tableOf(t, cat, "销售部-10月.xlsx")
	for _, bad := range []string{
		`DELETE FROM "` + tbl + `"`,
		`DROP TABLE "` + tbl + `"`,
		`UPDATE "` + tbl + `" SET 部门='x'`,
		`INSERT INTO "` + tbl + `" VALUES (1,2,3,4)`,
	} {
		if _, err := Query(ctx, db, bad, 0); err == nil {
			t.Errorf("不该放过写操作: %s", bad)
		}
	}
}

// 模型可能忘了写 LIMIT，一层十万行的表会把上下文撑爆。
func TestQueryCapsRows(t *testing.T) {
	dir := t.TempDir()
	rows := make([][]string, 250)
	for i := range rows {
		rows[i] = []string{"x", "y", "1", "z"}
	}
	mkxlsx(t, dir, "big.xlsx", []string{"a", "b", "c", "d"}, rows)

	ctx := context.Background()
	db, _, err := LoadCatalog(ctx, dir)
	if err != nil {
		t.Fatalf("载入失败: %v", err)
	}
	defer db.Close()

	res, err := Query(ctx, db, `SELECT * FROM "big_Sheet1"`, 50)
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if res.Count != 50 {
		t.Fatalf("应被限制到 50 行，实际 %d", res.Count)
	}
}

func TestWriteQueryProducesXlsx(t *testing.T) {
	dir := queryFixture(t)
	ctx := context.Background()
	db, cat, err := LoadCatalog(ctx, dir)
	if err != nil {
		t.Fatalf("载入失败: %v", err)
	}
	defer db.Close()

	outDir := filepath.Join(t.TempDir(), "out")
	path, n, err := WriteQuery(ctx, db,
		`SELECT 部门, SUM(金额) AS 合计 FROM "`+tableOf(t, cat, "销售部-10月.xlsx")+`" GROUP BY 部门`, outDir, "汇总.xlsx")
	if err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
	if n != 1 {
		t.Fatalf("应写 1 行数据，实际 %d", n)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("文件不存在: %v", err)
	}

	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("打不开输出: %v", err)
	}
	defer f.Close()
	rows, _ := f.GetRows(f.GetSheetList()[0])
	if len(rows) != 2 {
		t.Fatalf("应有表头+1行，实际 %d 行", len(rows))
	}
	if rows[0][0] != "部门" || rows[0][1] != "合计" {
		t.Fatalf("表头不对: %v", rows[0])
	}
	if rows[1][1] != "3214.5" {
		t.Fatalf("汇总值不对: %v", rows[1])
	}
}

func TestTableNameSanitizes(t *testing.T) {
	got := tableName("销售部 10月(终版).xlsx", "Sheet 1")
	if strings.ContainsAny(got, " ()") {
		t.Fatalf("表名里不该有需要转义的字符: %q", got)
	}
	if !strings.Contains(got, "销售部") {
		t.Fatalf("应保留中文，AI 才看得懂: %q", got)
	}
	if strings.HasPrefix(got, "0") {
		t.Fatalf("表名不能以数字开头: %q", got)
	}
}

func TestColumnNameHandlesBlankAndDup(t *testing.T) {
	used := map[string]int{}
	if got := columnName("", 0, used); got != "c1" {
		t.Errorf("空表头应补 c1，实际 %q", got)
	}
	if got := columnName("金额", 1, used); got != "金额" {
		t.Errorf("正常表头不该改，实际 %q", got)
	}
	if got := columnName("金额", 2, used); got != "金额_2" {
		t.Errorf("重名应加后缀，实际 %q", got)
	}
}
