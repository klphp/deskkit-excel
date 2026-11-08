package excel

// 表的「变形」操作：选列、丢列、改名、重排、拆列、合列、转置、填充、
// 日期拆分、计算列、文本规范化。
//
// 这些操作单看都很小，但用户每天真在做：把「姓名+部门」拆成两列、
// 把合并单元格留下的空格子填上、从日期里取月份做汇总。
// 每一项都返回新表（Table），不碰原文件 —— 由调用方决定写到哪里。

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
)

// SelectColumns 只保留这些列（按给定顺序）。
func SelectColumns(t *Table, names []string) (*Table, error) {
	idx, err := t.Cols(names)
	if err != nil {
		return nil, err
	}
	out := &Table{Path: t.Path, Sheet: t.Sheet}
	for _, i := range idx {
		out.Headers = append(out.Headers, t.Headers[i])
	}
	for _, r := range t.Rows {
		row := make([]string, len(idx))
		for k, i := range idx {
			row[k] = cellAt(r, i)
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

// DropColumns 去掉这些列。
func DropColumns(t *Table, names []string) (*Table, error) {
	drop, err := t.Cols(names)
	if err != nil {
		return nil, err
	}
	dropSet := map[int]bool{}
	for _, i := range drop {
		dropSet[i] = true
	}
	out := &Table{Path: t.Path, Sheet: t.Sheet}
	for i, h := range t.Headers {
		if !dropSet[i] {
			out.Headers = append(out.Headers, h)
		}
	}
	for _, r := range t.Rows {
		var row []string
		for i := range t.Headers {
			if !dropSet[i] {
				row = append(row, cellAt(r, i))
			}
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

// RenameColumns 改名：旧名 → 新名。
func RenameColumns(t *Table, mapping map[string]string) (*Table, error) {
	out := t.Clone()
	for old, want := range mapping {
		i, err := t.Col(old)
		if err != nil {
			return nil, err
		}
		out.Headers[i] = strings.TrimSpace(want)
	}
	return out, nil
}

// ReorderColumns 按给定顺序重排；没提到的列排在后面保持原序。
func ReorderColumns(t *Table, order []string) (*Table, error) {
	idx, err := t.Cols(order)
	if err != nil {
		return nil, err
	}
	used := map[int]bool{}
	for _, i := range idx {
		used[i] = true
	}
	var final []int
	final = append(final, idx...)
	for i := range t.Headers {
		if !used[i] {
			final = append(final, i)
		}
	}
	out := &Table{Path: t.Path, Sheet: t.Sheet}
	for _, i := range final {
		out.Headers = append(out.Headers, t.Headers[i])
	}
	for _, r := range t.Rows {
		row := make([]string, len(final))
		for k, i := range final {
			row[k] = cellAt(r, i)
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

// SplitColumn 按分隔符把一列拆成多列。
//
// into 为空时自动命名「原名1/原名2…」。原列保留（用户常常要对照），
// 想丢掉就再调 DropColumns。
func SplitColumn(t *Table, col, sep string, into []string, limit int) (*Table, error) {
	ci, err := t.Col(col)
	if err != nil {
		return nil, err
	}
	if sep == "" {
		return nil, fmt.Errorf("分隔符不能为空")
	}
	out := t.Clone()
	if limit <= 0 {
		limit = 0 // 0 表示不限段数
	}
	// 先扫一遍算出最多几段
	maxParts := 1
	for _, r := range t.Rows {
		if n := len(strings.Split(cellAt(r, ci), sep)); n > maxParts {
			maxParts = n
		}
	}
	if limit > 0 && maxParts > limit {
		maxParts = limit
	}
	if len(into) == 0 {
		for i := 1; i <= maxParts; i++ {
			into = append(into, fmt.Sprintf("%s%d", col, i))
		}
	}
	if len(into) < maxParts {
		maxParts = len(into)
	}
	names := make([]string, 0, maxParts)
	for i := 0; i < maxParts; i++ {
		names = append(names, into[i])
	}
	out.Headers = append(out.Headers, names...)
	for ri, r := range t.Rows {
		parts := strings.Split(cellAt(r, ci), sep)
		for k := 0; k < maxParts; k++ {
			v := ""
			if k < len(parts) {
				v = strings.TrimSpace(parts[k])
			}
			out.Rows[ri] = append(out.Rows[ri], v)
		}
	}
	return out, nil
}

// ConcatColumns 把多列拼成一列。
func ConcatColumns(t *Table, cols []string, sep, newName string, skipEmpty bool) (*Table, error) {
	idx, err := t.Cols(cols)
	if err != nil {
		return nil, err
	}
	if newName == "" {
		newName = strings.Join(cols, "+")
	}
	out := t.Clone()
	out.Headers = append(out.Headers, newName)
	for ri, r := range t.Rows {
		var parts []string
		for _, i := range idx {
			v := strings.TrimSpace(cellAt(r, i))
			if skipEmpty && v == "" {
				continue
			}
			parts = append(parts, v)
		}
		out.Rows[ri] = append(out.Rows[ri], strings.Join(parts, sep))
	}
	return out, nil
}

// Transpose 行列转置。
func Transpose(t *Table) *Table {
	out := &Table{Path: t.Path, Sheet: t.Sheet}
	// 第一列当新表头
	first := make([]string, 0, len(t.Rows)+1)
	first = append(first, "")
	for _, r := range t.Rows {
		first = append(first, cellAt(r, 0))
	}
	out.Headers = first
	for ci := range t.Headers {
		row := make([]string, 0, len(t.Rows)+1)
		row = append(row, t.Headers[ci])
		for _, r := range t.Rows {
			row = append(row, cellAt(r, ci))
		}
		out.Rows = append(out.Rows, row)
	}
	return out
}

// FillDown 向下填充空单元格（合并单元格读出来之后下面几行是空的）。
func FillDown(t *Table, cols []string) (*Table, error) {
	var idx []int
	if len(cols) == 0 {
		for i := range t.Headers {
			idx = append(idx, i)
		}
	} else {
		var err error
		idx, err = t.Cols(cols)
		if err != nil {
			return nil, err
		}
	}
	out := t.Clone()
	for _, ci := range idx {
		last := ""
		for ri := range out.Rows {
			v := strings.TrimSpace(cellAt(out.Rows[ri], ci))
			if v == "" {
				if last != "" {
					out.Rows[ri][ci] = last
				}
				continue
			}
			last = v
		}
	}
	return out, nil
}

// DateParts 从日期列取出年/月/日/季度/星期等，加在表的末尾。
func DateParts(t *Table, col string, parts []string) (*Table, error) {
	ci, err := t.Col(col)
	if err != nil {
		return nil, err
	}
	if len(parts) == 0 {
		parts = []string{"year", "month"}
	}
	out := t.Clone()
	names := make([]string, len(parts))
	for k, p := range parts {
		names[k] = col + "-" + p
	}
	out.Headers = append(out.Headers, names...)

	var bad int
	for ri, r := range t.Rows {
		v := strings.TrimSpace(cellAt(r, ci))
		var tm time.Time
		if v != "" {
			tm, err = parseDate(v)
			if err != nil {
				bad++
			}
		}
		for _, p := range parts {
			val := ""
			if !tm.IsZero() {
				if got, e := datePart(tm, p); e == nil {
					val = got
				}
			}
			out.Rows[ri] = append(out.Rows[ri], val)
		}
	}
	_ = bad
	return out, nil
}

// NormalizeText 文本规范化：去空白、全角转半角、大小写。
type NormalizeTextOptions struct {
	Columns []string `json:"columns,omitempty"`
	// Trim 去首尾空白。
	Trim bool `json:"trim,omitempty"`
	// CollapseSpaces 连续空白压成一个空格。
	CollapseSpaces bool `json:"collapseSpaces,omitempty"`
	// ToHalfWidth 全角转半角（全角数字和逗号是 Excel 里最常见的坑）。
	ToHalfWidth bool `json:"toHalfWidth,omitempty"`
	// Lower 转小写；Upper 转大写（二选一）。
	Lower bool `json:"lower,omitempty"`
	Upper bool `json:"upper,omitempty"`
	// StripCurrency 去掉货币符号和千分位逗号，变成纯数字文本。
	StripCurrency bool `json:"stripCurrency,omitempty"`
}

// NormalizeText 按选项规范化文本列。
func NormalizeText(t *Table, opt NormalizeTextOptions) (*Table, error) {
	var idx []int
	if len(opt.Columns) == 0 {
		for i := range t.Headers {
			idx = append(idx, i)
		}
	} else {
		var err error
		idx, err = t.Cols(opt.Columns)
		if err != nil {
			return nil, err
		}
	}

	out := t.Clone()
	currency := regexp.MustCompile(`[¥$€£￥,\s]`)
	for _, ci := range idx {
		for ri := range out.Rows {
			v := cellAt(out.Rows[ri], ci)
			if v == "" {
				continue
			}
			if opt.ToHalfWidth {
				v = toHalfWidth(v)
			}
			if opt.Trim {
				v = strings.TrimSpace(v)
			}
			if opt.CollapseSpaces {
				v = strings.Join(strings.Fields(v), " ")
			}
			if opt.StripCurrency {
				v = currency.ReplaceAllString(v, "")
			}
			if opt.Lower {
				v = strings.ToLower(v)
			}
			if opt.Upper {
				v = strings.ToUpper(v)
			}
			out.Rows[ri][ci] = v
		}
	}
	return out, nil
}

// toHalfWidth 全角转半角（U+FF01..U+FF5E → ASCII，全角空格 → 空格）。
func toHalfWidth(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == 0x3000:
			b.WriteRune(' ')
		case r >= 0xFF01 && r <= 0xFF5E:
			b.WriteRune(r - 0xFEE0)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ComputedColumn 新增一列，值来自表达式（语法 [列名]*2、round([金额]/3, 2)）。
func ComputedColumn(t *Table, exprText, newName string) (*Table, error) {
	if strings.TrimSpace(exprText) == "" {
		return nil, fmt.Errorf("表达式不能为空")
	}
	if newName == "" {
		newName = exprText
	}
	prog, refs, err := compileCalc(exprText, t)
	if err != nil {
		return nil, err
	}
	out := t.Clone()
	out.Headers = append(out.Headers, newName)
	for ri := range out.Rows {
		v, err := prog.eval(t.Rows[ri], t, refs)
		if err != nil {
			return nil, fmt.Errorf("第 %d 行算不了: %w", ri+1, err)
		}
		out.Rows[ri] = append(out.Rows[ri], v)
	}
	return out, nil
}

// Dedup 按列（空则整行）去重，保留第一次出现。
func Dedup(t *Table, cols []string) (*Table, int, error) {
	var idx []int
	if len(cols) == 0 {
		for i := range t.Headers {
			idx = append(idx, i)
		}
	} else {
		var err error
		idx, err = t.Cols(cols)
		if err != nil {
			return nil, 0, err
		}
	}
	out := &Table{Path: t.Path, Sheet: t.Sheet, Headers: append([]string(nil), t.Headers...)}
	seen := map[string]struct{}{}
	removed := 0
	for _, r := range t.Rows {
		parts := make([]string, len(idx))
		for k, ci := range idx {
			parts[k] = strings.TrimSpace(cellAt(r, ci))
		}
		key := strings.Join(parts, "\x00")
		if _, ok := seen[key]; ok {
			removed++
			continue
		}
		seen[key] = struct{}{}
		out.Rows = append(out.Rows, append([]string(nil), r...))
	}
	return out, removed, nil
}

// DropEmptyRows 去掉整行为空的行。
func DropEmptyRows(t *Table) (*Table, int) {
	out := &Table{Path: t.Path, Sheet: t.Sheet, Headers: append([]string(nil), t.Headers...)}
	removed := 0
	for _, r := range t.Rows {
		empty := true
		for _, v := range r {
			if strings.TrimSpace(v) != "" {
				empty = false
				break
			}
		}
		if empty {
			removed++
			continue
		}
		out.Rows = append(out.Rows, append([]string(nil), r...))
	}
	return out, removed
}

// ── 计算列：交给 expr，不再自己写表达式解析 ──
//
// 自己写四则运算解析的代价是：括号优先级、除零、浮点尾巴、round 参数……
// 每一项都要单独测，而且迟早会漏。expr（expr-lang/expr）是这块的事实标准，
// 语法就是 Go 的子集，用户写 [单价]*[数量] 或 round([金额]/3, 2) 都认。
//
// 我们对模型和用户暴露的语法保持 [列名] —— 中文列名用别的写法都是坑。
// 内部把它翻译成 cols["列名"]，再喂给 expr 编译一次、逐行求值。

// calcProgram 编译好的计算列。
type calcProgram struct {
	prog  *vm.Program
	env   map[string]any
	cells map[string]float64
}

// colRefRe 匹配 [列名]。
var colRefRe = regexp.MustCompile(`\[([^\]]+)\]`)

// compileCalc 把 [列名] 表达式编译成可逐行求值的程序。
func compileCalc(src string, t *Table) (*calcProgram, map[string]int, error) {
	refs := map[string]int{}
	var firstErr error
	translated := colRefRe.ReplaceAllStringFunc(src, func(m string) string {
		name := strings.TrimSpace(m[1 : len(m)-1])
		ci, err := t.Col(name)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return "0"
		}
		refs[name] = ci
		return fmt.Sprintf("cols[%q]", name)
	})
	if firstErr != nil {
		return nil, nil, firstErr
	}
	if len(refs) == 0 {
		return nil, nil, fmt.Errorf("表达式里没有引用任何列，写成 [列名] 的形式")
	}

	cellMap := make(map[string]float64, len(refs))
	for name := range refs {
		cellMap[name] = 0
	}
	env := map[string]any{
		"cols": cellMap,
		"round": func(x float64, digits ...int) float64 {
			d := 2
			if len(digits) > 0 {
				d = digits[0]
			}
			pow := 1.0
			for i := 0; i < d; i++ {
				pow *= 10
			}
			return round(x*pow) / pow
		},
	}

	prog, err := expr.Compile(translated, expr.Env(env), expr.AsFloat64())
	if err != nil {
		return nil, nil, fmt.Errorf("表达式写错了（%s）: %w", translated, err)
	}
	return &calcProgram{prog: prog, env: env, cells: cellMap}, refs, nil
}

// eval 求一行。空单元格按 0（和 Excel 一致），不是数字的单元格直接报错 ——
// 静默按 0 算出来的汇总最坑人。
func (p *calcProgram) eval(row []string, t *Table, refs map[string]int) (string, error) {
	for name, ci := range refs {
		raw := strings.TrimSpace(cellAt(row, ci))
		if raw == "" {
			p.cells[name] = 0
			continue
		}
		n, ok := asNumber(raw)
		if !ok {
			return "", fmt.Errorf("列 %q 的值 %q 不是数字", name, raw)
		}
		p.cells[name] = n
	}
	out, err := expr.Run(p.prog, p.env)
	if err != nil {
		return "", err
	}
	f, ok := out.(float64)
	if !ok {
		return "", fmt.Errorf("算出来不是数字: %v", out)
	}
	return trimNum(round(f*1e6) / 1e6), nil
}

// round 四舍五入到整数（Go 的 math.Round 也是这个语义，这里避免依赖 math 的导入）。
func round(f float64) float64 {
	if f < 0 {
		return float64(int64(f - 0.5))
	}
	return float64(int64(f + 0.5))
}
