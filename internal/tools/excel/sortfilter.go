package excel

// 排序与筛选。
//
// 排序要「数字按数字排、日期按日期排、文本按文本排」：直接字符串比较会把
// 10 排到 9 前面、把 2026/10/1 和 2026/2/1 排反 —— 这是用户最先察觉的错。
// 数字或日期列排不动的（"暂无" 之类），统一沉到末尾，而不是插在中间。

import (
	"fmt"
	"sort"
	"strings"
)

// SortKey 排序键。
type SortKey struct {
	Column string `json:"column"`
	Desc   bool   `json:"desc,omitempty"`
}

// Sort 多列排序。
func Sort(t *Table, keys []SortKey) (*Table, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("No sort keys given")
	}
	type keyInfo struct {
		col  int
		desc bool
		kind string
	}
	var infos []keyInfo
	for _, k := range keys {
		ci, err := t.Col(k.Column)
		if err != nil {
			return nil, err
		}
		infos = append(infos, keyInfo{col: ci, desc: k.Desc, kind: guessKind(t, ci)})
	}

	out := t.Clone()
	// 先把每行的排序键算好，避免比较时反复解析（几万行会明显卡）
	prepared := make([]struct {
		row  []string
		nums []float64
		tims []int64
		strs []string
		// oks 这一行的每个排序键是否真的解析成功了。
		// 没解析出来的（"暂无"、"N/A"、"-"）必须沉底，
		// 不能当 0 参与比较 —— 那会把「暂无」夹在 0 和 1 之间，用户读成 0。
		oks []bool
	}, len(out.Rows))
	for i, r := range out.Rows {
		p := struct {
			row  []string
			nums []float64
			tims []int64
			strs []string
			oks  []bool
		}{row: r}
		for _, k := range infos {
			v := strings.TrimSpace(cellAt(r, k.col))
			if n, ok := asNumber(v); ok {
				p.nums = append(p.nums, n)
			} else {
				p.nums = append(p.nums, 0)
			}
			if tm, err := parseDate(v); err == nil {
				p.tims = append(p.tims, tm.Unix())
			} else {
				p.tims = append(p.tims, 0)
			}
			p.strs = append(p.strs, v)
			// 按列类型判断这一格「排得动吗」
			okKey := true
			switch k.kind {
			case "number":
				_, okKey = asNumber(v)
			case "date":
				_, err := parseDate(v)
				okKey = err == nil
			}
			p.oks = append(p.oks, okKey)
		}
		prepared[i] = p
	}

	sort.SliceStable(prepared, func(a, b int) bool {
		for ki, k := range infos {
			av, bv := prepared[a], prepared[b]
			// 空值永远沉底，不管升序降序
			aEmpty := strings.TrimSpace(cellAt(av.row, k.col)) == ""
			bEmpty := strings.TrimSpace(cellAt(bv.row, k.col)) == ""
			if aEmpty != bEmpty {
				return bEmpty
			}
			if aEmpty && bEmpty {
				continue
			}
			// 排不动的值一律沉底，升序降序都一样：
			// 以前把它们补成 0 参与比较，「暂无」就夹在 0 和 1 之间，
			// 用户按金额排完序人工核对时会把「暂无」读成 0。
			if k.kind == "number" || k.kind == "date" {
				aOK, bOK := av.oks[ki], bv.oks[ki]
				if aOK != bOK {
					return aOK
				}
				if !aOK && !bOK {
					continue
				}
			}
			cmp := 0
			switch k.kind {
			case "number":
				switch {
				case av.nums[ki] < bv.nums[ki]:
					cmp = -1
				case av.nums[ki] > bv.nums[ki]:
					cmp = 1
				}
			case "date":
				switch {
				case av.tims[ki] < bv.tims[ki]:
					cmp = -1
				case av.tims[ki] > bv.tims[ki]:
					cmp = 1
				}
			default:
				cmp = strings.Compare(av.strs[ki], bv.strs[ki])
			}
			if cmp != 0 {
				if k.desc {
					return cmp > 0
				}
				return cmp < 0
			}
		}
		return false
	})

	for i := range prepared {
		out.Rows[i] = prepared[i].row
	}
	return out, nil
}

// guessKind 列的类型：超过八成能解析就算数字/日期。混一点「暂无」不该让整列退化成文本排序。
func guessKind(t *Table, ci int) string {
	var num, date, total int
	for _, r := range t.Rows {
		v := strings.TrimSpace(cellAt(r, ci))
		if v == "" {
			continue
		}
		total++
		if _, ok := asNumber(v); ok {
			num++
		}
		if _, err := parseDate(v); err == nil {
			date++
		}
	}
	if total == 0 {
		return "text"
	}
	if float64(num)/float64(total) >= 0.8 {
		return "number"
	}
	if float64(date)/float64(total) >= 0.8 {
		return "date"
	}
	return "text"
}

// Condition 筛选条件。
type Condition struct {
	Column string `json:"column"`
	// Op: eq / ne / contains / notContains / gt / ge / lt / le / empty / notEmpty / in
	Op    string `json:"op"`
	Value string `json:"value,omitempty"`
	// Values 用于 op=in。
	Values []string `json:"values,omitempty"`
}

// Filter 按条件筛选。matchAll=true 表示全部满足（AND），否则任一满足（OR）。
func Filter(t *Table, conds []Condition, matchAll bool) (*Table, error) {
	if len(conds) == 0 {
		return t.Clone(), nil
	}
	type parsed struct {
		col int
		c   Condition
	}
	var ps []parsed
	for _, c := range conds {
		ci, err := t.Col(c.Column)
		if err != nil {
			return nil, err
		}
		ps = append(ps, parsed{col: ci, c: c})
	}

	out := &Table{Path: t.Path, Sheet: t.Sheet, Headers: append([]string(nil), t.Headers...)}
	for _, r := range t.Rows {
		matched := 0
		for _, p := range ps {
			if matchCond(cellAt(r, p.col), p.c) {
				matched++
			}
		}
		ok := matched == len(ps)
		if !matchAll {
			ok = matched > 0
		}
		if ok {
			out.Rows = append(out.Rows, append([]string(nil), r...))
		}
	}
	return out, nil
}

func matchCond(cell string, c Condition) bool {
	v := strings.TrimSpace(cell)
	// 数字比较时不区分千分位
	numA, okA := asNumber(v)
	numB, okB := asNumber(strings.TrimSpace(c.Value))

	switch strings.ToLower(strings.TrimSpace(c.Op)) {
	case "eq", "=", "等于":
		if okA && okB {
			return numA == numB
		}
		return strings.EqualFold(v, strings.TrimSpace(c.Value))
	case "ne", "!=", "不等于":
		if okA && okB {
			return numA != numB
		}
		return !strings.EqualFold(v, strings.TrimSpace(c.Value))
	case "contains", "包含":
		return strings.Contains(v, c.Value)
	case "notcontains", "不包含":
		return !strings.Contains(v, c.Value)
	case "gt", ">", "大于":
		return okA && okB && numA > numB
	case "ge", ">=", "大于等于":
		return okA && okB && numA >= numB
	case "lt", "<", "小于":
		return okA && okB && numA < numB
	case "le", "<=", "小于等于":
		return okA && okB && numA <= numB
	case "empty", "为空":
		return v == ""
	case "notempty", "不为空":
		return v != ""
	case "in", "任一":
		for _, want := range c.Values {
			if strings.EqualFold(v, strings.TrimSpace(want)) {
				return true
			}
		}
		return false
	}
	return false
}

// OffsetPaging 取一段（用于「只看前 20 行」或分批处理）。
func OffsetPaging(t *Table, offset, limit int) *Table {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(t.Rows) {
		return &Table{Path: t.Path, Sheet: t.Sheet, Headers: append([]string(nil), t.Headers...)}
	}
	end := len(t.Rows)
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	out := &Table{Path: t.Path, Sheet: t.Sheet, Headers: append([]string(nil), t.Headers...)}
	out.Rows = append(out.Rows, t.Rows[offset:end]...)
	return out
}
