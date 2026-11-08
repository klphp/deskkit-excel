package excel

// 数据的「体检报告」：列画像、数值统计、规则校验。
//
// 用户面对一张来路不明的表，最先想知道的是：每列到底是什么、有多少空的、
// 有没有重复、有没有「00123 被 Excel 吃掉前导零」这类问题。
// 这些用 SQL 也能查，但要写一堆语句；给模型一个工具，一句话就够。

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/klphp/excel-batch-tool/internal/money"
	"github.com/montanaflynn/stats"
	"github.com/shopspring/decimal"
)

// ColumnProfile 单列的画像。
type ColumnProfile struct {
	Name string `json:"name"`
	// Kind 推测类型：empty / number / date / text / mixed
	Kind     string  `json:"kind"`
	NonEmpty int     `json:"nonEmpty"`
	Empty    int     `json:"empty"`
	Unique   int     `json:"unique"`
	Sum      float64 `json:"sum,omitempty"`
	Mean     float64 `json:"mean,omitempty"`
	Median   float64 `json:"median,omitempty"`
	// StdDev / P25 / P75 / P95 用 montanaflynn/stats 算，不自己写
	StdDev  float64  `json:"stdDev,omitempty"`
	P25     float64  `json:"p25,omitempty"`
	P75     float64  `json:"p75,omitempty"`
	P95     float64  `json:"p95,omitempty"`
	Min     string   `json:"min,omitempty"`
	Max     string   `json:"max,omitempty"`
	Samples []string `json:"samples,omitempty"`
	// Warnings 值得提醒的事（前导零、货币符号、混合类型、全角数字等）。
	Warnings []string `json:"warnings,omitempty"`
}

// Profile 给整张表做画像。
func Profile(t *Table, sampleLimit int) []ColumnProfile {
	if sampleLimit <= 0 {
		sampleLimit = 5
	}
	out := make([]ColumnProfile, 0, len(t.Headers))
	for ci, h := range t.Headers {
		p := ColumnProfile{Name: h}
		seen := map[string]int{}
		var nums []float64
		var dates int
		var numberish, textish, empty int
		var hasLeadingZero, hasCurrency, hasFullWidthDigit bool
		var samples []string

		for _, r := range t.Rows {
			v := strings.TrimSpace(cellAt(r, ci))
			if v == "" {
				empty++
				continue
			}
			p.NonEmpty++
			seen[v]++
			if len(samples) < sampleLimit {
				if !containsStr(samples, v) {
					samples = append(samples, v)
				}
			}
			if n, ok := asNumber(v); ok {
				nums = append(nums, n)
				numberish++
			} else {
				textish++
			}
			if _, err := parseDate(v); err == nil {
				dates++
			}
			if len(v) > 1 && v[0] == '0' && v[1] >= '0' && v[1] <= '9' {
				hasLeadingZero = true
			}
			if strings.ContainsAny(v, "¥$€£￥") {
				hasCurrency = true
			}
			for _, r := range v {
				if r >= '０' && r <= '９' {
					hasFullWidthDigit = true
					break
				}
			}
		}

		p.Empty = empty
		p.Unique = len(seen)
		p.Samples = samples

		switch {
		case p.NonEmpty == 0:
			p.Kind = "empty"
		case dates == p.NonEmpty:
			p.Kind = "date"
			p.Min, p.Max = dateRange(t, ci)
		case numberish == p.NonEmpty:
			p.Kind = "number"
			p.Sum, p.Mean, p.Median, p.StdDev, p.P25, p.P75, p.P95 = numericStats(nums)
			p.Min = trimNum(minOf(nums))
			p.Max = trimNum(maxOf(nums))
		case textish > 0 && numberish > 0:
			p.Kind = "mixed"
		default:
			p.Kind = "text"
		}

		if hasLeadingZero {
			// 不管是哪一列都要提醒：工号、订单号、身份证这类东西一旦被当成数字，
			// 前面的 0 就永久没了，而且是「看起来正常」的那种错。
			p.Warnings = append(p.Warnings, "有前导零的值（如 00123），当数字处理会丢掉，导出时要保持文本")
		}
		if hasCurrency {
			p.Warnings = append(p.Warnings, "含货币符号，直接当数字算会失败")
		}
		if hasFullWidthDigit {
			p.Warnings = append(p.Warnings, "含全角数字/字符，需要先转半角")
		}
		if p.Kind == "mixed" {
			p.Warnings = append(p.Warnings, fmt.Sprintf("类型混杂：%d 个像数字、%d 个不像，排序和汇总会出错", numberish, textish))
		}
		if p.NonEmpty > 0 && p.Unique == 1 && p.NonEmpty > 3 {
			p.Warnings = append(p.Warnings, "整列只有一个值，可能是常量列或取错了列")
		}
		out = append(out, p)
	}
	return out
}

// 校验规则。
type ValidateRules struct {
	// Required 必填列。
	Required []string `json:"required,omitempty"`
	// Unique 不允许重复的列（组合起来判重）。
	Unique []string `json:"unique,omitempty"`
	// Numeric 必须是数字的列。
	Numeric []string `json:"numeric,omitempty"`
	// Dates 必须是日期（可解析）的列。
	Dates []string `json:"dates,omitempty"`
	// NotNegative 不允许负数的列。
	NotNegative []string `json:"notNegative,omitempty"`
	// Pattern 正则校验：列名 → 正则。
	Pattern map[string]string `json:"pattern,omitempty"`
	// MaxIssues 最多返回多少条问题，默认 50。
	MaxIssues int `json:"maxIssues,omitempty"`
}

// Issue 一条问题。
type Issue struct {
	Row    int    `json:"row"` // 1 起，指数据行（不含表头）
	Column string `json:"column"`
	Value  string `json:"value"`
	Reason string `json:"reason"`
}

// ValidateResult 校验结果。
type ValidateResult struct {
	TotalIssues int     `json:"totalIssues"`
	Issues      []Issue `json:"issues"`
	Truncated   bool    `json:"truncated"`
	CheckedRows int     `json:"checkedRows"`
}

// Validate 按规则检查。
//
// 每条问题都带行号 + 原值：用户要能一条条去改，只说「有 37 处问题」没有意义。
func Validate(t *Table, r ValidateRules) (ValidateResult, error) {
	if r.MaxIssues <= 0 {
		r.MaxIssues = 50
	}
	res := ValidateResult{CheckedRows: len(t.Rows)}

	reqIdx, err := t.Cols(r.Required)
	if err != nil {
		return res, err
	}
	uniqIdx, err := t.Cols(r.Unique)
	if err != nil {
		return res, err
	}
	numIdx, err := t.Cols(r.Numeric)
	if err != nil {
		return res, err
	}
	dateIdx, err := t.Cols(r.Dates)
	if err != nil {
		return res, err
	}
	negIdx, err := t.Cols(r.NotNegative)
	if err != nil {
		return res, err
	}
	patIdx := map[int]*regexp.Regexp{}
	for col, expr := range r.Pattern {
		i, err := t.Col(col)
		if err != nil {
			return res, err
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return res, fmt.Errorf("列 %q 的正则写错了: %w", col, err)
		}
		patIdx[i] = re
	}

	add := func(row int, col int, val, reason string) {
		res.TotalIssues++
		if len(res.Issues) < r.MaxIssues {
			res.Issues = append(res.Issues, Issue{Row: row, Column: t.Headers[col], Value: val, Reason: reason})
		} else {
			res.Truncated = true
		}
	}

	seenKeys := map[string]int{} // 组合键 → 首次出现的行
	for ri, row := range t.Rows {
		rowNo := ri + 1
		for _, ci := range reqIdx {
			if strings.TrimSpace(cellAt(row, ci)) == "" {
				add(rowNo, ci, "", "必填但为空")
			}
		}
		for _, ci := range numIdx {
			v := strings.TrimSpace(cellAt(row, ci))
			if v == "" {
				continue
			}
			if _, ok := asNumber(v); !ok {
				add(rowNo, ci, v, "不是数字")
			}
		}
		for _, ci := range dateIdx {
			v := strings.TrimSpace(cellAt(row, ci))
			if v == "" {
				continue
			}
			if _, err := parseDate(v); err != nil {
				add(rowNo, ci, v, "不是能识别的日期")
			}
		}
		for _, ci := range negIdx {
			v := strings.TrimSpace(cellAt(row, ci))
			if n, ok := asNumber(v); ok && n < 0 {
				add(rowNo, ci, v, "不应为负数")
			}
		}
		for ci, re := range patIdx {
			v := strings.TrimSpace(cellAt(row, ci))
			if v != "" && !re.MatchString(v) {
				add(rowNo, ci, v, "不符合格式 "+re.String())
			}
		}
		if len(uniqIdx) > 0 {
			parts := make([]string, len(uniqIdx))
			for k, ci := range uniqIdx {
				parts[k] = strings.TrimSpace(cellAt(row, ci))
			}
			key := strings.Join(parts, "\x00")
			if first, ok := seenKeys[key]; ok {
				add(rowNo, uniqIdx[0], strings.Join(parts, " / "),
					fmt.Sprintf("与第 %d 行重复（按 %s）", first, strings.Join(r.Unique, "+")))
			} else {
				seenKeys[key] = rowNo
			}
		}
	}
	return res, nil
}

// FindDuplicates 找重复行（按指定列；为空则整行比较）。返回重复行的样本。
func FindDuplicates(t *Table, cols []string, limit int) ([][]string, int, error) {
	if limit <= 0 {
		limit = 20
	}
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

	count := map[string]int{}
	first := map[string]int{}
	for ri, row := range t.Rows {
		parts := make([]string, len(idx))
		for k, ci := range idx {
			// 键归一：1001 / 1001.0 / " 1001" / 001001 是同一个值。
			// 不归一的话 find_duplicates 对「写法不同的同一笔」判不出重复。
			parts[k] = normalizeKeyValue(cellAt(row, ci))
		}
		key := strings.Join(parts, "\x00")
		count[key]++
		if _, ok := first[key]; !ok {
			first[key] = ri + 1
		}
	}

	type dup struct {
		row int
		n   int
	}
	var dups []dup
	for key, n := range count {
		if n > 1 {
			dups = append(dups, dup{row: first[key], n: n})
		}
	}
	sort.Slice(dups, func(i, j int) bool { return dups[i].row < dups[j].row })

	out := make([][]string, 0, limit)
	total := 0
	for _, d := range dups {
		total += d.n
		if len(out) < limit {
			out = append(out, t.Rows[d.row-1])
		}
	}
	return out, total, nil
}

// ── 小工具 ──

func cellAt(row []string, i int) string {
	if i < 0 || i >= len(row) {
		return ""
	}
	return row[i]
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// stats 数值列的统计量。分位数与标准差用 montanaflynn/stats —— 这些
// 公式看起来简单，但百分位取法（线性插值 vs 最近秩）各派不一，
// 自己写出来的数对不上 Excel 的 PERCENTILE 就会挨骂。
func numericStats(nums []float64) (sum, mean, median, stdDev, p25, p75, p95 float64) {
	if len(nums) == 0 {
		return
	}
	data := stats.Float64Data(nums)
	// 合计/平均走 decimal，分位数与标准差交给 stats（那两个是统计量，不是钱）
	decimals := make([]decimal.Decimal, len(nums))
	for i, f := range nums {
		decimals[i] = decimal.NewFromFloat(f)
	}
	if len(decimals) > 0 {
		dsum := money.Sum(decimals)
		sum, _ = dsum.Float64()
		dmean, _ := dsum.Div(decimal.NewFromInt(int64(len(decimals)))).Float64()
		mean = dmean
	}
	median, _ = data.Median()
	stdDev, _ = data.StandardDeviationSample()
	p25, _ = data.Percentile(25)
	p75, _ = data.Percentile(75)
	p95, _ = data.Percentile(95)
	return round2(sum), round2(mean), round2(median), round2(stdDev), round2(p25), round2(p75), round2(p95)
}

func round2(f float64) float64 {
	return math.Round(f*100) / 100
}

func minOf(nums []float64) float64 {
	if len(nums) == 0 {
		return 0
	}
	m := nums[0]
	for _, n := range nums[1:] {
		if n < m {
			m = n
		}
	}
	return m
}

func maxOf(nums []float64) float64 {
	if len(nums) == 0 {
		return 0
	}
	m := nums[0]
	for _, n := range nums[1:] {
		if n > m {
			m = n
		}
	}
	return m
}

func trimNum(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func dateRange(t *Table, ci int) (string, string) {
	var min, max time.Time
	for _, r := range t.Rows {
		v := strings.TrimSpace(cellAt(r, ci))
		if v == "" {
			continue
		}
		tm, err := parseDate(v)
		if err != nil {
			continue
		}
		if min.IsZero() || tm.Before(min) {
			min = tm
		}
		if max.IsZero() || tm.After(max) {
			max = tm
		}
	}
	if min.IsZero() {
		return "", ""
	}
	return min.Format("2006-01-02"), max.Format("2006-01-02")
}
