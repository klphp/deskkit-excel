package excel

// CSV 相关测试。
//
// 这些用例的存在理由：CSV 支持是补上去的（原来界面说支持、实际 excelize
// 根本打不开）。这里的每一条都对应一个真实会遇到的导出文件形态。

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/klphp/excel-batch-tool/internal/xtext"
)

func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadCSVUTF8(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "a.csv", []byte("姓名,部门,金额\n张一,技术,100\n李二,运营,200\n"))

	rows, err := readCSVRows(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0][0] != "姓名" || rows[2][2] != "200" {
		t.Fatalf("解析不对: %#v", rows)
	}
}

func TestReadCSVGBK(t *testing.T) {
	dir := t.TempDir()
	// 国内导出的报表十有八九是这种：GBK 编码、逗号分隔
	gbk, err := xtext.UTF8ToGB18030("姓名,部门,金额\n张一,技术,100\n")
	if err != nil {
		t.Fatal(err)
	}
	p := writeFile(t, dir, "gbk.csv", gbk)

	rows, err := readCSVRows(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("行数不对: %d", len(rows))
	}
	if rows[0][0] != "姓名" || rows[1][1] != "技术" {
		t.Fatalf("GBK 没解对，还是乱码: %#v", rows)
	}
}

func TestReadCSVUTF8BOM(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "bom.csv", append([]byte{0xEF, 0xBB, 0xBF}, []byte("姓名,部门\n张一,技术\n")...))

	rows, err := readCSVRows(p)
	if err != nil {
		t.Fatal(err)
	}
	// BOM 如果没去掉，第一列表头会变成 "\ufeff姓名"，后面按列名匹配全部失败
	if rows[0][0] != "姓名" {
		t.Fatalf("BOM 没去掉: %q", rows[0][0])
	}
}

func TestReadCSVDelimiters(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"分号", "姓名;部门;金额\n张一;技术;100\n"},
		{"制表符", "姓名\t部门\t金额\n张一\t技术\t100\n"},
		{"竖线", "姓名|部门|金额\n张一|技术|100\n"},
		// 备注列里本身有逗号、而真正的分隔符是分号 —— 老实现（数逗号出现次数）会判错
		{"分号里含逗号", "姓名;备注;金额\n张一;第一行,含逗号;100\n李二;简单;200\n"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		p := writeFile(t, dir, "x.csv", []byte(c.body))
		rows, err := readCSVRows(p)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if len(rows[0]) != 3 {
			t.Fatalf("%s 没切对列: %#v", c.name, rows[0])
		}
	}
}

func TestReadCSVQuotedNewline(t *testing.T) {
	dir := t.TempDir()
	// 备注列里有换行和逗号 —— 用逗号切会把一行拆成两行，必须靠引号
	p := writeFile(t, dir, "q.csv", []byte("姓名,备注\n张一,\"第一行,含逗号\n第二行\"\n"))

	rows, err := readCSVRows(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("含换行的引号字段被拆开了，行数 %d: %#v", len(rows), rows)
	}
	if rows[1][1] != "第一行,含逗号\n第二行" {
		t.Fatalf("引号字段内容不对: %q", rows[1][1])
	}
}

// CSV 要能走完整条管线：扫描 → 合并 → 清洗，而不是只有单独读能用。
func TestScanFolderIncludesCSV(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.csv", []byte("姓名,部门,金额\n张一,技术,100\n"))
	writeFile(t, dir, "b.csv", []byte("姓名,部门,金额\n李二,运营,200\n"))

	res, err := Scan(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	var csvCount int
	for _, s := range res.Files {
		if s.Err != "" {
			t.Fatalf("CSV 被打上错误: %s -> %s", s.Name, s.Err)
		}
		if IsCSV(s.Path) {
			csvCount++
			if len(s.Headers) != 3 {
				t.Fatalf("CSV 表头没读出来: %#v", s.Headers)
			}
		}
	}
	if csvCount != 2 {
		t.Fatalf("应该扫到 2 个 CSV，实际 %d", csvCount)
	}
}

func TestMergeCSVFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "技术部.csv", []byte("姓名,部门,金额\n张一,技术,100\n"))
	writeFile(t, dir, "运营部.csv", []byte("部门,姓名\n运营,李二\n"))

	out := filepath.Join(dir, "out")
	res, err := Merge(context.Background(), MergeOptions{
		Dir:        dir,
		OutputDir:  out,
		OutputName: "merged.xlsx",
		DedupKey:   []string{"姓名"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.RowsOut != 2 {
		t.Fatalf("合并后应 2 行，实际 %d", res.RowsOut)
	}
	// 列取并集：缺的列补空
	if len(res.Columns) != 3 {
		t.Fatalf("列并集不对: %#v", res.Columns)
	}
}

func TestWriteCSVAddsBOM(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.csv")
	if err := WriteCSV(out, [][]string{{"姓名", "金额"}, {"张一", "100"}}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// 不带 BOM 的 UTF-8 CSV，Windows 上 Excel 双击打开中文是乱码
	if len(raw) < 3 || raw[0] != 0xEF || raw[1] != 0xBB || raw[2] != 0xBF {
		t.Fatalf("缺少 UTF-8 BOM，Excel 打开会乱码")
	}
	// 写完再读回来，应该完全一致
	rows, err := readCSVRows(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[1][0] != "张一" {
		t.Fatalf("回读不一致: %#v", rows)
	}
}

func TestWriteCSVAppendsExtension(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "noext")
	if err := WriteCSV(out, [][]string{{"a"}, {"1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out + ".csv"); err != nil {
		t.Fatalf("没自动补 .csv 扩展名: %v", err)
	}
}
