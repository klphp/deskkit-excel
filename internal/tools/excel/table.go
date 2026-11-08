package excel

// Table 是新加功能共用的表格模型：一份表头 + 若干行，来源可能是 xlsx 也可能是 CSV。
//
// 为什么要有这一层：旧代码（merge/clean/query）直接在 excelize 的 File 上操作，
// 每加一个功能都要重写一遍「开文件 → 取第一个 sheet → 读表头 → 补空单元格」。
// 把这些收进 Table 之后，新功能只关心「列和行」，CSV 与 xlsx 天然一致。

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// Table 一张表。
type Table struct {
	// Path 来源文件路径。
	Path string `json:"path"`
	// Sheet 工作表名（CSV 用文件名）。
	Sheet string `json:"sheet"`
	// Headers 表头（已去重、去空）。
	Headers []string `json:"headers"`
	// Rows 数据行，每行长度与 Headers 一致。
	Rows [][]string `json:"rows"`
}

// LoadTable 读某张表。sheet 为空时取第一个工作表。
func LoadTable(path, sheet string) (*Table, error) {
	f, err := openWorkbook(path)
	if err != nil {
		return nil, fmt.Errorf("打不开 %s: %w", filepath.Base(path), err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, fmt.Errorf("%s 里没有工作表", filepath.Base(path))
	}
	if sheet == "" {
		sheet = sheets[0]
	} else if !contains(sheets, sheet) {
		return nil, fmt.Errorf("%s 里没有名为 %q 的表，现有: %s",
			filepath.Base(path), sheet, strings.Join(sheets, ", "))
	}

	rows, err := f.Rows(sheet)
	if err != nil {
		return nil, fmt.Errorf("读取 %q 失败: %w", sheet, err)
	}
	defer rows.Close()

	t := &Table{Path: path, Sheet: sheet}
	for rows.Next() {
		cells, err := rows.Columns()
		if err != nil {
			return nil, fmt.Errorf("读取行内容失败: %w", err)
		}
		if t.Headers == nil {
			t.Headers = normalizeHeaders(cells)
			continue
		}
		t.Rows = append(t.Rows, padRow(cells, len(t.Headers)))
	}
	if err := rows.Error(); err != nil {
		return nil, fmt.Errorf("遍历行失败: %w", err)
	}
	if t.Headers == nil {
		return nil, fmt.Errorf("%s 的 %q 是空表", filepath.Base(path), sheet)
	}
	return t, nil
}

// SheetNames 列出文件里所有工作表。
func SheetNames(path string) ([]string, error) {
	f, err := openWorkbook(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.GetSheetList(), nil
}

// Col 按列名找列号（0 起）。列名容错：忽略首尾空白与大小写；
// 找不到时若传入的是纯数字，则当作 1 起的列序号。
func (t *Table) Col(name string) (int, error) {
	want := strings.ToLower(strings.TrimSpace(name))
	for i, h := range t.Headers {
		if strings.ToLower(strings.TrimSpace(h)) == want {
			return i, nil
		}
	}
	// 允许用户按序号指定（第 3 列）
	if n, err := strconv.Atoi(want); err == nil && n >= 1 && n <= len(t.Headers) {
		return n - 1, nil
	}
	return -1, fmt.Errorf("没有名为 %q 的列，现有列: %s", name, strings.Join(t.Headers, " / "))
}

// Cols 批量找列号。
func (t *Table) Cols(names []string) ([]int, error) {
	out := make([]int, 0, len(names))
	for _, n := range names {
		i, err := t.Col(n)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, nil
}

// Cell 安全取值。
func (t *Table) Cell(row int, col int) string {
	if row < 0 || row >= len(t.Rows) || col < 0 || col >= len(t.Rows[row]) {
		return ""
	}
	return t.Rows[row][col]
}

// Clone 深拷贝（变换类功能不改动原表）。
func (t *Table) Clone() *Table {
	rows := make([][]string, len(t.Rows))
	for i, r := range t.Rows {
		rows[i] = append([]string(nil), r...)
	}
	return &Table{
		Path:    t.Path,
		Sheet:   t.Sheet,
		Headers: append([]string(nil), t.Headers...),
		Rows:    rows,
	}
}

// WriteOptions 写出选项。
type WriteOptions struct {
	// Sheet 工作表名，默认用「结果」。
	Sheet string
	// BoldHeader 表头加粗 + 底色 + 冻结首行。给人看的文件基本都要。
	BoldHeader bool
	// AutoWidth 按内容估算列宽。
	AutoWidth bool
	// NumbersAsNumbers 看起来是数字的单元格写成数值（可用 SUM），
	// 但保留前导零、超长编号、百分号等原样。
	NumbersAsNumbers bool
}

// WriteTable 把表写到 path。扩展名决定格式：.csv 写 CSV（带 BOM），其余写 xlsx。
//
// 绝不覆盖已存在的文件：同名时自动加后缀。用户的原始文件比我们的输出重要得多。
func WriteTable(path string, t *Table, opt WriteOptions) (string, error) {
	if opt.Sheet == "" {
		opt.Sheet = "结果"
	}
	path = uniquePath(path)

	if IsCSV(path) {
		table := make([][]string, 0, len(t.Rows)+1)
		table = append(table, t.Headers)
		table = append(table, t.Rows...)
		if err := WriteCSV(path, table); err != nil {
			return "", err
		}
		return path, nil
	}

	f, err := newExcelFile()
	if err != nil {
		return "", err
	}
	defer f.Close()
	sheet := opt.Sheet
	if err := f.SetSheetName(f.GetSheetName(0), sheet); err != nil {
		sheet = f.GetSheetName(0)
	}
	if err := putTableInto(f, sheet, t, opt); err != nil {
		return "", err
	}
	if err := f.SaveAs(path); err != nil {
		return "", fmt.Errorf("写文件失败: %w", err)
	}
	return path, nil
}

// newExcelFile 新建空工作簿。
func newExcelFile() (*excelize.File, error) {
	return excelize.NewFile(), nil
}

// uniqueSheetName 工作表重名时加序号（Excel 不允许同名）。
func uniqueSheetName(f *excelize.File, name string) string {
	existing := f.GetSheetList()
	if !contains(existing, name) {
		return name
	}
	for i := 2; i < 100; i++ {
		cand := fmt.Sprintf("%s-%d", name, i)
		if !contains(existing, cand) {
			return cand
		}
	}
	return name
}

// putTableInto 把表写进已打开工作簿的某个工作表。WriteTable 与 WriteMultiSheet 共用。
func putTableInto(f *excelize.File, sheet string, t *Table, opt WriteOptions) error {
	// 表头样式
	var headerStyle int
	if opt.BoldHeader {
		st, err := f.NewStyle(&excelize.Style{
			Font:      &excelize.Font{Bold: true},
			Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"F2F2F2"}},
			Alignment: &excelize.Alignment{Vertical: "center"},
		})
		if err == nil {
			headerStyle = st
		}
	}

	put := func(idx int, values []string, style int) error {
		first, err := excelize.CoordinatesToCellName(1, idx)
		if err != nil {
			return err
		}
		for j, v := range values {
			c, err := excelize.CoordinatesToCellName(j+1, idx)
			if err != nil {
				return err
			}
			if opt.NumbersAsNumbers {
				if num, ok := asNumber(v); ok {
					if err := f.SetCellValue(sheet, c, num); err != nil {
						return err
					}
					continue
				}
			}
			if err := f.SetCellStr(sheet, c, v); err != nil {
				return err
			}
		}
		if style != 0 && len(values) > 0 {
			last, err := excelize.CoordinatesToCellName(len(values), idx)
			if err == nil {
				if err := f.SetCellStyle(sheet, first, last, style); err != nil {
					return err
				}
			}
		}
		return nil
	}

	if err := put(1, t.Headers, headerStyle); err != nil {
		return err
	}
	for i, r := range t.Rows {
		if err := put(i+2, r, 0); err != nil {
			return err
		}
	}

	if opt.BoldHeader {
		// 冻结首行：几千行的表滚下去之后看不到列名是最烦的
		_ = f.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
		_ = f.AutoFilter(sheet, "A1:"+endColumn(len(t.Headers))+"1", nil)
	}
	if opt.AutoWidth {
		autoWidth(f, sheet, t.Headers, t.Rows)
	}
	return nil
}

// asNumber 判断能不能当数值写。前导零、超长数字、带千分位/货币符号的一律不动 ——
// 工号 00123 变成 123 是这类工具最常见的事故。
func asNumber(s string) (float64, bool) {
	v := strings.TrimSpace(s)
	if v == "" || len(v) > 15 {
		return 0, false
	}
	if len(v) > 1 && v[0] == '0' && v[1] != '.' {
		return 0, false
	}
	n, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", ""), 64)
	if err != nil {
		return 0, false
	}
	// 纯数字才转：+86-138 这种不属于「数字」
	for _, r := range v {
		if !(r >= '0' && r <= '9') && r != '.' && r != '-' && r != '+' && r != ',' && r != 'e' && r != 'E' {
			return 0, false
		}
	}
	return n, true
}

func autoWidth(f *excelize.File, sheet string, headers []string, rows [][]string) {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = displayWidth(h)
	}
	for _, r := range rows {
		for i := range headers {
			if i < len(r) {
				if w := displayWidth(r[i]); w > widths[i] {
					widths[i] = w
				}
			}
		}
	}
	for i, w := range widths {
		if w > 60 {
			w = 60 // 备注类长文本列不该把表撑到看不见
		}
		if w < 8 {
			w = 8
		}
		col := endColumn(i + 1)
		_ = f.SetColWidth(sheet, col, col, float64(w)+2)
	}
}

// displayWidth 估算显示宽度：中文按两个字符算。
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		if r > 0x2E80 {
			w += 2
		} else {
			w++
		}
	}
	return w
}

// uniquePath 同名时加 -1、-2，绝不覆盖已有文件。
func uniquePath(path string) string {
	if _, err := os.Stat(path); err != nil {
		return path
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 1; i < 1000; i++ {
		p := fmt.Sprintf("%s-%d%s", base, i, ext)
		if _, err := os.Stat(p); err != nil {
			return p
		}
	}
	return path
}

// endColumn 列号 → 列名（1 → A，27 → AA）。
func endColumn(n int) string {
	name, err := excelize.ColumnNumberToName(n)
	if err != nil || n < 1 {
		return "A"
	}
	return name
}

func padRow(cells []string, n int) []string {
	if len(cells) >= n {
		return cells[:n]
	}
	out := make([]string, n)
	copy(out, cells)
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
