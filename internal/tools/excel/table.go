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
	"time"

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
	// Warnings 读表过程中「自动修正过什么」的说明。
	//
	// 为什么要带出来：补表头、重名列改名、按文本写编号这些动作都改变用户看到的结果，
	// 静默修正和静默丢数据一样危险 —— 用户必须能知道「这一列的名字是我补的」。
	Warnings []string `json:"warnings,omitempty"`
}

// LoadTable 读某张表。sheet 为空时取第一个工作表。
func LoadTable(path, sheet string) (*Table, error) {
	f, err := openWorkbook(path)
	if err != nil {
		return nil, fmt.Errorf("Cannot open %s: %w", filepath.Base(path), err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, fmt.Errorf("%s has no sheets", filepath.Base(path))
	}
	if sheet == "" {
		sheet = sheets[0]
	} else if !contains(sheets, sheet) {
		return nil, fmt.Errorf("%s has no sheet named %q; existing: %s",
			filepath.Base(path), sheet, strings.Join(sheets, ", "))
	}

	rows, err := f.Rows(sheet)
	if err != nil {
		return nil, fmt.Errorf("Failed to read %q: %w", sheet, err)
	}
	defer rows.Close()

	t := &Table{Path: path, Sheet: sheet}
	for rows.Next() {
		cells, err := rows.Columns()
		if err != nil {
			return nil, fmt.Errorf("Failed to read row contents: %w", err)
		}
		if t.Headers == nil {
			var warns []string
			t.Headers, warns = normalizeHeadersWarn(cells)
			t.Warnings = append(t.Warnings, warns...)
			continue
		}
		// ★ 数据行比表头宽时按最大宽度补表头，绝不截断数据行。
		// 以前这里按表头宽度截断：表头漏写一格（或多一格合并单元格留下的空表头）
		// 那一列数据就整列消失，不报错、不 warning —— 财务表里极难发现。
		if len(t.Headers) > 0 && len(cells) > len(t.Headers) {
			t.extendHeaders(len(cells))
		}
		t.Rows = append(t.Rows, padRow(cells, len(t.Headers)))
	}
	if err := rows.Error(); err != nil {
		return nil, fmt.Errorf("Failed to iterate rows: %w", err)
	}
	if t.Headers == nil {
		return nil, fmt.Errorf("%s: sheet %q is empty", filepath.Base(path), sheet)
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
	return -1, fmt.Errorf("There is no column named %q; existing columns: %s", name, strings.Join(t.Headers, " / "))
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

// AppName / AppVersion 由启动流程注入，写进输出文件的「处理说明」，
// 出问题时用户能说清是哪个版本干的。
var (
	AppName    = "DeskKit Excel"
	AppVersion = ""
)

// SetAppInfo 由 pack 注册时调用。
func SetAppInfo(name, version string) {
	if name != "" {
		AppName = name
	}
	AppVersion = version
}

// 金额列与整数列的表头关键词。这几类在财务/仓储表里几乎一定需要千分位，
// 而「数量」硬套两位小数会被当成笑话，所以分开判断。
var (
	moneyHeaderWords = []string{"金额", "价", "额", "费", "余额", "收入", "支出", "税",
		"工资", "成本", "应收", "应付", "合计", "总计", "小计", "结存", "单价", "市值", "盈亏"}
	integerHeaderWords = []string{"数量", "件数", "人数", "笔数", "单数", "行数", "库存", "次"}
)

// numberLikeHeaderWords 这些列名里的值几乎一定是「编号 / 号码」，不是能参与计算的数值。
//
// 为什么要按列名再拦一道：光靠位数拦不住 11 位手机号、13 位订单号，
// 而 Excel 的数值只有 15 位有效数字 —— 写进去末位就被改写了。
// 与 moneyHeaderWords 冲突时（如「电话费」）以金额为准：那一列是要合计的。
var numberLikeHeaderWords = []string{
	"身份证", "证件", "卡号", "账号", "帐号", "工号", "单号",
	"电话", "手机", "邮编", "税号", "编码", "编号", "证号",
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
	// MoneyColumns 明确指定按金额格式（千分位 + 两位小数 + 负数红）输出的列。
	// 留空则按列名关键词自动判断。
	MoneyColumns []string
	// IntegerColumns 明确指定按整数千分位输出的列（数量/件数）。
	IntegerColumns []string
	// NoNumberFormat 关闭一切数字格式（要纯数据时用）。
	NoNumberFormat bool
	// Meta 处理说明里额外追加的条目，形如「源文件：xxx」。
	Meta []string
	// NoMeta 不生成「处理说明」工作表。默认**生成** —— 财务和审计要能追：
	// 这个文件是谁、什么时候、用什么版本、从哪些源文件算出来的。
	NoMeta bool
}

// WriteTable 把表写到 path。扩展名决定格式：.csv 写 CSV（带 BOM），其余写 xlsx。
//
// 绝不覆盖已存在的文件：同名时自动加后缀。用户的原始文件比我们的输出重要得多。
func WriteTable(path string, t *Table, opt WriteOptions) (string, error) {
	if opt.Sheet == "" {
		opt.Sheet = "Result"
	}
	path, err := uniquePath(path)
	if err != nil {
		return "", err
	}

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
	warns, err := putTableInto(f, sheet, t, opt)
	if err != nil {
		return "", err
	}
	// 默认带一页「处理说明」：财务要能追溯，出问题也要能说清是哪次处理。
	// 读表时自动修正过的东西（补表头 / 重名列改名 / 编号强制文本）一并写进去 ——
	// 静默修正和静默丢数据一样不可接受。
	if !opt.NoMeta {
		opt.Meta = append(append([]string(nil), opt.Meta...), warns...)
		opt.Meta = append(opt.Meta, t.Warnings...)
		if err := addMetaSheet(f, t, opt); err != nil {
			return "", err
		}
	}
	if err := f.SaveAs(path); err != nil {
		return "", fmt.Errorf("Failed to write the file: %w", err)
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
//
// 返回 warnings：本次写出过程中对「原本会写成数值」的列做了强制文本处理，
// 这些必须写进「处理说明」，否则用户不知道为什么会多出一堆文本单元格。
func putTableInto(f *excelize.File, sheet string, t *Table, opt WriteOptions) ([]string, error) {
	var warnings []string

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

	// ★ 按列名强制文本：身份证 / 卡号 / 工号 / 单号 这类列即使长得像数字
	// 也不能写成数值。与金额列冲突时（如「电话费」）以金额为准。
	var textCols map[int]bool
	if opt.NumbersAsNumbers {
		textCols = forceTextColumns(t.Headers)
		if len(textCols) > 0 {
			names := make([]string, 0, len(textCols))
			for i, h := range t.Headers {
				if textCols[i] {
					names = append(names, h)
				}
			}
			warnings = append(warnings, fmt.Sprintf(
				"Column %s was written as text (ids and codes lose their last digits as numbers; Excel keeps only 15 significant digits)",
				strings.Join(names, ", ")))
		}
	}

	put := func(idx int, values []string, style int, isData bool) error {
		first, err := excelize.CoordinatesToCellName(1, idx)
		if err != nil {
			return err
		}
		for j, v := range values {
			c, err := excelize.CoordinatesToCellName(j+1, idx)
			if err != nil {
				return err
			}
			// 表头永远按文本写；数据行才判断能不能当数字
			if isData && opt.NumbersAsNumbers && !textCols[j] {
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

	if err := put(1, t.Headers, headerStyle, false); err != nil {
		return nil, err
	}
	for i, r := range t.Rows {
		if err := put(i+2, r, 0, true); err != nil {
			return nil, err
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
	if opt.NumbersAsNumbers && !opt.NoNumberFormat {
		applyNumberFormats(f, sheet, t, opt, textCols)
	}
	return warnings, nil
}

// forceTextColumns 找出必须按文本写出的列（返回列号集合）。
func forceTextColumns(headers []string) map[int]bool {
	var out map[int]bool
	for i, h := range headers {
		if !matchAnyWord(h, numberLikeHeaderWords) {
			continue
		}
		if matchAnyWord(h, moneyHeaderWords) {
			continue // 「电话费」这类是金额，要参与合计
		}
		if out == nil {
			out = make(map[int]bool, 2)
		}
		out[i] = true
	}
	return out
}

func matchAnyWord(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// applyNumberFormats 给金额列/整数列套上会计看得懂的格式。
//
// 为什么这件事必须我们做：数字格式是 Excel 的「显示层」，AI 用脚本改数据时
// 复制不到这层信息，输出就变成一堆裸数字 —— 财务第一眼看到没千分位、没有
// 两位小数、负数跟正数一个颜色，就会觉得这工具「不专业」。
//
// 取样式：
//
//	金额   #,##0.00;[Red]-#,##0.00
//	整数   #,##0
func applyNumberFormats(f *excelize.File, sheet string, t *Table, opt WriteOptions, textCols map[int]bool) {
	moneyIdx := resolveCols(t, opt.MoneyColumns, moneyHeaderWords)
	intIdx := resolveCols(t, opt.IntegerColumns, integerHeaderWords)

	styles := map[string]int{}
	styleFor := func(format string) int {
		if st, ok := styles[format]; ok {
			return st
		}
		fmtCopy := format
		st, err := f.NewStyle(&excelize.Style{CustomNumFmt: &fmtCopy})
		if err != nil {
			return 0
		}
		styles[format] = st
		return st
	}

	apply := func(cols []int, format string) {
		st := styleFor(format)
		if st == 0 || len(t.Rows) == 0 {
			return
		}
		for _, ci := range cols {
			if textCols[ci] {
				continue // 按文本写出的列不套数字格式
			}
			first, err := excelize.CoordinatesToCellName(ci+1, 2)
			if err != nil {
				continue
			}
			last, err := excelize.CoordinatesToCellName(ci+1, len(t.Rows)+1)
			if err != nil {
				continue
			}
			// 整列一次性设，避免逐格调用（几万行时差几十倍）
			_ = f.SetCellStyle(sheet, first, last, st)
		}
	}

	if !containsAny(intIdx, moneyIdx) {
		apply(intIdx, "#,##0")
	}
	apply(moneyIdx, "#,##0.00;[Red]-#,##0.00")
}

func containsAny(a, b []int) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

// resolveCols 先用显式指定，没有再按表头关键词猜。
func resolveCols(t *Table, explicit []string, keywords []string) []int {
	if len(explicit) > 0 {
		if idx, err := t.Cols(explicit); err == nil {
			return idx
		}
		return nil
	}
	var out []int
	for i, h := range t.Headers {
		for _, kw := range keywords {
			if strings.Contains(h, kw) {
				out = append(out, i)
				break
			}
		}
	}
	return out
}

// metaSheetName 处理说明的表名。
const metaSheetName = "Processing notes"

// addMetaSheet 在输出文件里加一页「处理说明」。
//
// 内容刻意写得像一张表（字段 / 值），而不是一段散文：财务复核时是一行行看的，
// 审计要抄的是具体字段。
func addMetaSheet(f *excelize.File, t *Table, opt WriteOptions) error {
	lines := []string{
		"Generated at: " + time.Now().Format("2006-01-02 15:04:05"),
		"Generated by: " + AppName + versionSuffix(),
		"Note: this file is the processing result; the source file was not modified.",
	}
	if t.Path != "" {
		lines = append(lines, "Source file: "+t.Path)
	}
	if t.Sheet != "" {
		lines = append(lines, "Source sheet: "+t.Sheet)
	}
	lines = append(lines, fmt.Sprintf("Output size: %d rows x %d columns", len(t.Rows), len(t.Headers)))
	if opt.NumbersAsNumbers {
		lines = append(lines, "Numbers: written as numeric values, so SUM/AVERAGE and other formulas work directly.")
	} else {
		lines = append(lines, "Numbers: kept as text exactly as written; convert them to numbers before using formulas.")
	}
	lines = append(lines, "Note: formulas and formatting from the source file are not carried over; only the computed values are here.")
	lines = append(lines, opt.Meta...)

	sheet := metaSheetName
	if _, err := f.NewSheet(sheet); err != nil {
		return err
	}
	// 放进第一个位置之前先写内容
	for i, line := range lines {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		_ = f.SetCellStr(sheet, cell, line)
	}
	_ = f.SetColWidth(sheet, "A", "A", 90)
	return nil
}

func versionSuffix() string {
	if AppVersion == "" {
		return ""
	}
	return " v" + AppVersion
}

// asNumber 判断能不能当数值写。
//
// 不只认 "1234.5"：报表里的金额常写成 ￥1,234.50、(1,234.50) 表示负数、
// ８８００（全角）。这些如果原样当文本写回去，用户打开 Excel 连合计都看不到 ——
// 而那正是他合并完第一件要做的事。前导零、超长编号仍然保持文本（工号 00123 不能变 123）。
func asNumber(s string) (float64, bool) {
	v := strings.TrimSpace(s)
	if v == "" {
		return 0, false
	}

	// 全角转半角（报表导出里非常常见）
	v = toHalfWidth(v)
	v = strings.TrimSpace(v)

	// 会计写法的负数：(1,234.50)
	negative := false
	if strings.HasPrefix(v, "(") && strings.HasSuffix(v, ")") {
		negative = true
		v = strings.TrimSpace(v[1 : len(v)-1])
	}

	// 货币符号与千分位
	v = strings.NewReplacer(
		"￥", "", "¥", "", "$", "", "€", "", "£", "",
		",", "", "，", "", " ", "",
	).Replace(v)

	if v == "" || v == "-" || v == "+" || len(v) > 20 {
		return 0, false
	}
	// 前导零（00123）按文本保留
	if len(v) > 1 && v[0] == '0' && v[1] != '.' {
		return 0, false
	}
	// ★ 16 位以上纯整数一律按文本：身份证（18 位）、银行卡（19 位）、
	// 合同号、订单号。Excel 的数值只有 15 位有效数字，写成数值后末 3 位会被
	// 改写成 0（110101199003071234 → 110101199003071000），文件不报错、
	// 单元格看起来还是一串数字 —— 拿去报税 / 打款就是错的。
	if isLongInteger(v) {
		return 0, false
	}
	for _, r := range v {
		if !(r >= '0' && r <= '9') && r != '.' && r != '-' && r != '+' && r != 'e' && r != 'E' {
			return 0, false
		}
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, false
	}
	if negative {
		n = -n
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

// uniquePath 同名时加 -1、-2……，绝不覆盖已有文件。
//
// 候选名用尽（-1 到 -999 全被占）时**返回错误**，不退回原路径：
// 退回原路径等于把「绝不覆盖」的承诺作废——调用方接着写文件就会盖掉别人的东西。
// 这种情况本来就该让人先清理目录，而不是悄悄覆盖。
func uniquePath(path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		return path, nil
	}
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	for i := 1; i < 1000; i++ {
		p := fmt.Sprintf("%s-%d%s", stem, i, ext)
		if _, err := os.Stat(p); err != nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("Too many files with the same name: -1 to -999 are all taken for %s; clean up or rename something first",
		filepath.Base(path))
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
		// ★ 只补不截：截断等于整列数据静默丢失。调用方必须先按最大宽度补好表头
		// （LoadTable 的 extendHeaders），这里再兜一层不再截断。
		return cells
	}
	out := make([]string, n)
	copy(out, cells)
	return out
}

// extendHeaders 数据行比表头宽时按最大宽度补表头，并把已有行补空。
//
// 触发场景很常见：表头漏写一格、合并单元格导致后几列没表头、CSV 末尾多一个分隔符。
// 以前的做法是按表头宽度截断数据行 —— 那一列整列消失且不报错，财务表里极难发现。
func (t *Table) extendHeaders(width int) {
	if width <= len(t.Headers) {
		return
	}
	old := len(t.Headers)
	var added []string
	for i := old; i < width; i++ {
		name := fmt.Sprintf("Column %d", i+1)
		for n := 2; contains(t.Headers, name); n++ {
			name = fmt.Sprintf("Column %d-%d", i+1, n)
		}
		t.Headers = append(t.Headers, name)
		added = append(added, name)
	}
	t.Warnings = append(t.Warnings, fmt.Sprintf(
		"A data row has %d columns but the header only has %d; added header %s (no data was lost)",
		width, old, strings.Join(added, ", ")))
	for i := range t.Rows {
		if len(t.Rows[i]) < width {
			row := make([]string, width)
			copy(row, t.Rows[i])
			t.Rows[i] = row
		}
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
