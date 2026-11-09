package excel

// 分组汇总（透视表）。
//
// 为什么不用 SQL 就够了：给用户看的场景里，八成是「按部门汇总金额」
// 这种一句话需求。让模型拼 SQL 又多一步出错的可能，而且 SQL 结果用户
// 看不出是按哪几列算的。这里直接把聚合规则说清楚。

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/klphp/deskkit-excel/internal/money"
	"github.com/shopspring/decimal"
)

// Agg 一个聚合。
type Agg struct {
	// Column 要聚合的列（count 时可空）。
	Column string `json:"column,omitempty"`
	// Func: count / countDistinct / sum / avg / min / max / first / last / join
	Func string `json:"func"`
	// As 输出列名，留空自动生成「列名-函数」。
	As string `json:"as,omitempty"`
	// Sep 仅 join 用，默认「, 」。
	Sep string `json:"sep,omitempty"`
}

// GroupResult 分组结果。
type GroupResult struct {
	Table   *Table `json:"-"`
	Groups  int    `json:"groups"`
	RowsIn  int    `json:"rowsIn"`
	RowsOut int    `json:"rowsOut"`
}

// GroupBy 按列分组并聚合。
func GroupBy(t *Table, by []string, aggs []Agg) (*GroupResult, error) {
	if len(by) == 0 {
		return nil, fmt.Errorf("没给分组列")
	}
	if len(aggs) == 0 {
		aggs = []Agg{{Func: "count", As: "行数"}}
	}
	byIdx, err := t.Cols(by)
	if err != nil {
		return nil, err
	}
	aggIdx := make([]int, len(aggs))
	for i, a := range aggs {
		if strings.EqualFold(a.Func, "count") && a.Column == "" {
			aggIdx[i] = -1
			continue
		}
		ci, err := t.Col(a.Column)
		if err != nil {
			return nil, err
		}
		aggIdx[i] = ci
	}

	type bucket struct {
		keyVals []string
		rows    [][]string
	}
	order := []string{}
	buckets := map[string]*bucket{}
	for _, r := range t.Rows {
		parts := make([]string, len(byIdx))
		for k, ci := range byIdx {
			parts[k] = strings.TrimSpace(cellAt(r, ci))
		}
		key := strings.Join(parts, "\x00")
		b, ok := buckets[key]
		if !ok {
			b = &bucket{keyVals: parts}
			buckets[key] = b
			order = append(order, key)
		}
		b.rows = append(b.rows, r)
	}

	out := &Table{Path: t.Path, Sheet: t.Sheet}
	out.Headers = append(out.Headers, by...)
	for i, a := range aggs {
		out.Headers = append(out.Headers, aggName(aggs, i, a))
	}

	for _, key := range order {
		b := buckets[key]
		row := append([]string(nil), b.keyVals...)
		for i, a := range aggs {
			row = append(row, runAgg(b.rows, aggIdx[i], a))
		}
		out.Rows = append(out.Rows, row)
	}

	// 按数值聚合列降序排一下：用户看汇总表基本都是想看「谁最多」
	// 分组列多于一个时不排，保持稳定。
	if len(by) == 1 && len(out.Rows) > 1 {
		firstNum := len(by)
		sort.SliceStable(out.Rows, func(x, y int) bool {
			a, okA := asNumber(out.Rows[x][firstNum])
			b, okB := asNumber(out.Rows[y][firstNum])
			if okA && okB {
				return a > b
			}
			return false
		})
	}

	return &GroupResult{Table: out, Groups: len(order), RowsIn: len(t.Rows), RowsOut: len(out.Rows)}, nil
}

func aggName(aggs []Agg, i int, a Agg) string {
	if strings.TrimSpace(a.As) != "" {
		return a.As
	}
	fn := strings.ToLower(a.Func)
	if fn == "count" && a.Column == "" {
		return "行数"
	}
	label := map[string]string{
		"count": "计数", "countdistinct": "去重计数", "sum": "合计", "avg": "平均",
		"min": "最小", "max": "最大", "first": "首个", "last": "最后", "join": "合并",
	}[fn]
	if label == "" {
		label = a.Func
	}
	return a.Column + "-" + label
}

func runAgg(rows [][]string, ci int, a Agg) string {
	fn := strings.ToLower(strings.TrimSpace(a.Func))
	switch fn {
	case "count":
		if ci < 0 {
			return strconv.Itoa(len(rows))
		}
		n := 0
		for _, r := range rows {
			if strings.TrimSpace(cellAt(r, ci)) != "" {
				n++
			}
		}
		return strconv.Itoa(n)
	case "countdistinct":
		seen := map[string]struct{}{}
		for _, r := range rows {
			if v := strings.TrimSpace(cellAt(r, ci)); v != "" {
				seen[v] = struct{}{}
			}
		}
		return strconv.Itoa(len(seen))
	case "join":
		sep := a.Sep
		if sep == "" {
			sep = ", "
		}
		seen := map[string]struct{}{}
		var vals []string
		for _, r := range rows {
			if v := strings.TrimSpace(cellAt(r, ci)); v != "" {
				if _, ok := seen[v]; ok {
					continue
				}
				seen[v] = struct{}{}
				vals = append(vals, v)
			}
		}
		return strings.Join(vals, sep)
	case "first", "last":
		var got string
		for _, r := range rows {
			v := strings.TrimSpace(cellAt(r, ci))
			if v == "" {
				continue
			}
			got = v
			if fn == "first" {
				break
			}
		}
		return got
	}

	// 金额一律走 decimal：float64 累加几万行会差出分位，
	// 而「就差一分钱」在财务那里等于报表作废，最难查的是它看起来是对的。
	var vals []decimal.Decimal
	for _, r := range rows {
		if d, ok := money.Parse(cellAt(r, ci)); ok {
			vals = append(vals, d)
		}
	}
	if len(vals) == 0 {
		return ""
	}
	switch fn {
	case "sum":
		// 合计保留精确值，不强行截两位 —— 截位会掩盖数据本身的小数位，
		// 用户要对账时更希望看到真实合计。
		return money.Text(money.Sum(vals))
	case "avg":
		sum := money.Sum(vals)
		return money.Round(sum.Div(decimal.NewFromInt(int64(len(vals)))), 2).String()
	case "min":
		m := vals[0]
		for _, d := range vals[1:] {
			if d.LessThan(m) {
				m = d
			}
		}
		return money.Text(m)
	case "max":
		m := vals[0]
		for _, d := range vals[1:] {
			if d.GreaterThan(m) {
				m = d
			}
		}
		return money.Text(m)
	}
	return ""
}
