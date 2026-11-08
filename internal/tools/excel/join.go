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

	// 右表建索引：一个 key 可能对应多行，这种必须报告，不能静默只取第一条
	type idxEntry struct {
		row   []string
		count int
	}
	index := map[string]*idxEntry{}
	rightOrder := []string{}
	for _, r := range right.Rows {
		k := normKey(cellAt(r, rk))
		if e, ok := index[k]; ok {
			e.count++
			continue
		}
		index[k] = &idxEntry{row: r, count: 1}
		rightOrder = append(rightOrder, k)
	}

	res := &JoinResult{LeftRows: len(left.Rows), RightRows: len(right.Rows)}
	for _, k := range rightOrder {
		if index[k].count > 1 {
			res.RightDupKeys++
			if len(res.DuplicateSamples) < 5 {
				res.DuplicateSamples = append(res.DuplicateSamples, k)
			}
		}
	}

	usedRight := map[string]bool{}
	for _, r := range left.Rows {
		k := normKey(cellAt(r, lk))
		e, ok := index[k]
		if ok {
			usedRight[k] = true
			res.Matched++
		} else {
			res.Unmatched++
		}
		if !ok && strings.ToLower(opt.How) == "inner" {
			continue
		}
		row := append([]string(nil), r...)
		for _, i := range bringIdx {
			v := ""
			if ok {
				v = cellAt(e.row, i)
			}
			row = append(row, v)
		}
		out.Rows = append(out.Rows, row)
	}

	// right join：把右表里有、左表没有的补在后面
	if strings.ToLower(opt.How) == "right" {
		for _, k := range rightOrder {
			if usedRight[k] {
				continue
			}
			e := index[k]
			row := make([]string, len(left.Headers))
			row[lk] = cellAt(e.row, rk)
			for _, i := range bringIdx {
				row = append(row, cellAt(e.row, i))
			}
			out.Rows = append(out.Rows, row)
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
