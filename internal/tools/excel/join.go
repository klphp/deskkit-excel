package excel

// 横向合并（vlookup / join）。
//
// 纵向合并（merge）解决「同样结构的表堆在一起」，
// 横向合并解决「按 key 把另一张表的列带过来」—— 这是 Excel 用户
// 最常做的 VLOOKUP，也是他们最常做错的地方（少匹配、匹配重复、带错列）。
// 所以这里必须把「匹配上多少、没匹配上多少、右表 key 重复多少」讲清楚。

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/klphp/deskkit-excel/internal/money"
)

// JoinOptions 横向合并选项。
type JoinOptions struct {
	LeftPath   string `json:"leftPath"`
	RightPath  string `json:"rightPath"`
	LeftSheet  string `json:"leftSheet,omitempty"`
	RightSheet string `json:"rightSheet,omitempty"`
	// On 两侧用来匹配的列（列名可以不同，用 leftKey / rightKey）。
	LeftKey  string `json:"leftKey"`
	RightKey string `json:"rightKey"`
	// Bring 从右表带哪些列；为空则除 key 之外全带。
	Bring []string `json:"bring,omitempty"`
	// How left / inner / right：保留哪边的行。
	How string `json:"how,omitempty"`
	// Prefix 右表列名前缀，默认「右表.」，避免重名。
	Prefix string `json:"prefix,omitempty"`
	// OnDuplicate 右表 key 重复时怎么取值。
	//
	// 以前一律「取第一条 + 在 JSON 里给个 warning」—— 模型不一定转述给用户，
	// 而 VLOOKUP 带金额时真实金额可能是 30 或 60，用户拿到 10 还以为是全的。
	// 所以现在必须显式选择：
	//
	//	""/"error"（默认） 直接报错，要求先把右表 key 弄唯一
	//	"first"           取第一条（老行为，金额可能是错的）
	//	"sum"             数值列求和（金额正确），非数值列取第一条并说明
	//	"all"             行展开（像 SQL 那样，一个 key 出多行）
	OnDuplicate string `json:"onDuplicate,omitempty"`
}

// JoinResult 横向合并结果与诊断。
type JoinResult struct {
	Table            *Table   `json:"-"`
	LeftRows         int      `json:"leftRows"`
	RightRows        int      `json:"rightRows"`
	Matched          int      `json:"matched"`   // 左表匹配上的行数
	Unmatched        int      `json:"unmatched"` // 左表没匹配上的行数
	RightDupKeys     int      `json:"rightDuplicateKeys"`
	DuplicateSamples []string `json:"duplicateSamples,omitempty"`
	OutputRows       int      `json:"outputRows"`
	// OnDuplicate 本次实际采用的重复 key 策略。
	OnDuplicate string `json:"onDuplicate,omitempty"`
	// Warning 需要用户知道但没到报错程度的说明。
	Warning string `json:"warning,omitempty"`
}

// Join 把右表按 key 接到左表右边。
func Join(opt JoinOptions) (*JoinResult, error) {
	if strings.TrimSpace(opt.LeftKey) == "" || strings.TrimSpace(opt.RightKey) == "" {
		return nil, fmt.Errorf("两边都要给匹配列")
	}
	left, err := LoadTable(opt.LeftPath, opt.LeftSheet)
	if err != nil {
		return nil, err
	}
	right, err := LoadTable(opt.RightPath, opt.RightSheet)
	if err != nil {
		return nil, err
	}

	lk, err := left.Col(opt.LeftKey)
	if err != nil {
		return nil, fmt.Errorf("左表: %w", err)
	}
	rk, err := right.Col(opt.RightKey)
	if err != nil {
		return nil, fmt.Errorf("右表: %w", err)
	}

	bringIdx := []int{}
	if len(opt.Bring) == 0 {
		for i := range right.Headers {
			if i == rk {
				continue
			}
			bringIdx = append(bringIdx, i)
		}
	} else {
		bringIdx, err = right.Cols(opt.Bring)
		if err != nil {
			return nil, fmt.Errorf("右表: %w", err)
		}
	}

	prefix := opt.Prefix
	if prefix == "" {
		prefix = "右表."
	}

	out := &Table{Path: opt.LeftPath, Sheet: left.Sheet}
	out.Headers = append(out.Headers, left.Headers...)
	for _, i := range bringIdx {
		name := prefix + right.Headers[i]
		// 前缀之后还可能重名（两张表都叫「金额」且都带前缀），加序号
		base := name
		for n := 2; contains(out.Headers, name); n++ {
			name = base + "-" + strconv.Itoa(n)
		}
		out.Headers = append(out.Headers, name)
	}

	// 右表建索引：一个 key 可能对应多行，这种必须让调用方显式选择怎么取，
	// 不能静默只取第一条（VLOOKUP 带金额时那可能是错的数）。
	index := map[string][][]string{}
	rightOrder := []string{}
	for _, r := range right.Rows {
		k := normKey(cellAt(r, rk))
		if _, ok := index[k]; !ok {
			rightOrder = append(rightOrder, k)
		}
		index[k] = append(index[k], r)
	}

	res := &JoinResult{LeftRows: len(left.Rows), RightRows: len(right.Rows)}
	for _, k := range rightOrder {
		if len(index[k]) > 1 {
			res.RightDupKeys++
			if len(res.DuplicateSamples) < 5 {
				res.DuplicateSamples = append(res.DuplicateSamples, k)
			}
		}
	}

	policy := strings.ToLower(strings.TrimSpace(opt.OnDuplicate))
	if policy == "" {
		policy = "error"
	}
	switch policy {
	case "error", "first", "sum", "all":
	default:
		return nil, fmt.Errorf("onDuplicate 只能是 error / first / sum / all，收到 %q", opt.OnDuplicate)
	}
	res.OnDuplicate = policy
	if res.RightDupKeys > 0 && policy == "error" {
		return nil, fmt.Errorf("右表有 %d 个 key 对应多行（示例: %s），直接取第一条会让金额出错。"+
			"请先把右表 key 弄唯一，或在调用时明确 onDuplicate：first（取第一条）/ sum（数值列求和）/ all（行展开）",
			res.RightDupKeys, strings.Join(res.DuplicateSamples, "、"))
	}

	usedRight := map[string]bool{}

	// mergedRight 在 policy=sum 时把同一 key 的多行合成一行。
	mergedRight := map[string][]string{}
	var summedCols, firstCols []string
	if policy == "sum" && res.RightDupKeys > 0 {
		for _, k := range rightOrder {
			rows := index[k]
			if len(rows) == 1 {
				mergedRight[k] = rows[0]
				continue
			}
			merged := make([]string, len(right.Headers))
			copy(merged, rows[0])
			for _, i := range bringIdx {
				vals := make([]string, 0, len(rows))
				for _, r := range rows {
					vals = append(vals, cellAt(r, i))
				}
				if d, skipped := money.SumStrings(vals); skipped == 0 {
					merged = setCell(merged, i, money.Text(d))
					summedCols = appendUnique(summedCols, right.Headers[i])
				} else {
					firstCols = appendUnique(firstCols, right.Headers[i])
				}
			}
			mergedRight[k] = merged
		}
		if len(firstCols) > 0 {
			res.Warning = fmt.Sprintf(
				"右表 %d 个 key 重复；列 %s 不是纯数值，已取第一条（这几列的值可能不完整）",
				res.RightDupKeys, strings.Join(firstCols, "、"))
		} else {
			res.Warning = fmt.Sprintf("右表 %d 个 key 重复；列 %s 已按 key 求和",
				res.RightDupKeys, strings.Join(summedCols, "、"))
		}
	}
	if res.RightDupKeys > 0 && policy == "first" {
		res.Warning = fmt.Sprintf("右表有 %d 个 key 对应多行（示例: %s），按 onDuplicate=first 只取了第一条",
			res.RightDupKeys, strings.Join(res.DuplicateSamples, "、"))
	}
	if res.RightDupKeys > 0 && policy == "all" {
		res.Warning = fmt.Sprintf("右表有 %d 个 key 对应多行，按 onDuplicate=all 已展开成多行（输出行数会变多）",
			res.RightDupKeys)
	}

	// pickRows 按策略决定一个 key 该取哪几行（返回 0 行表示没匹配上）。
	pickRows := func(k string) [][]string {
		rows, ok := index[k]
		if !ok {
			return nil
		}
		switch policy {
		case "all":
			return rows
		case "sum":
			if merged, ok := mergedRight[k]; ok {
				return [][]string{merged}
			}
			return rows[:1]
		default:
			return rows[:1]
		}
	}

	for _, r := range left.Rows {
		k := normKey(cellAt(r, lk))
		picked := pickRows(k)
		ok := len(picked) > 0
		if ok {
			usedRight[k] = true
			res.Matched++
		} else {
			res.Unmatched++
		}
		if !ok && strings.ToLower(opt.How) == "inner" {
			continue
		}
		if !ok {
			row := append([]string(nil), r...)
			for range bringIdx {
				row = append(row, "")
			}
			out.Rows = append(out.Rows, row)
			continue
		}
		for _, src := range picked {
			row := append([]string(nil), r...)
			for _, i := range bringIdx {
				row = append(row, cellAt(src, i))
			}
			out.Rows = append(out.Rows, row)
		}
	}

	// right join：把右表里有、左表没有的补在后面
	if strings.ToLower(opt.How) == "right" {
		for _, k := range rightOrder {
			if usedRight[k] {
				continue
			}
			for _, src := range pickRows(k) {
				row := make([]string, len(left.Headers))
				row[lk] = cellAt(src, rk)
				for _, i := range bringIdx {
					row = append(row, cellAt(src, i))
				}
				out.Rows = append(out.Rows, row)
			}
		}
	}

	res.Table = out
	res.OutputRows = len(out.Rows)
	return res, nil
}

// normKey 匹配用的规范化：去空白、忽略大小写。
// 不做全角/前导零处理 —— 那是清洗工具的职责，静默改写 key 会带来更难查的问题。
func normKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// setCell 越界安全地改一格。
func setCell(row []string, i int, v string) []string {
	if i < 0 || i >= len(row) {
		return row
	}
	row[i] = v
	return row
}

// appendUnique 去重追加。
func appendUnique(list []string, s string) []string {
	if contains(list, s) {
		return list
	}
	return append(list, s)
}
