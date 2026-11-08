// Package money 金额与数值的精确计算。
//
// 用 github.com/shopspring/decimal，不用 float64。
// 理由只有一条：float64 算钱会出差一分钱的结果，而差一分钱的报表在财务
// 那里就是废纸 —— 最要命的是它看起来是对的，没人会发现。
//
//	0.1 + 0.2 = 0.30000000000000004   ← float64
//	0.1 + 0.2 = 0.3                   ← decimal
//
// 这里同时承接「脏金额」的解析：报表里的金额常常是 ￥1,234.50、(1,234.50)
// 表示负数、全角数字、带空格 —— 这些在财务导出的文件里天天见。
package money

import (
	"math/big"
	"strings"

	"github.com/shopspring/decimal"
)

// Zero 零。
var Zero = decimal.Zero

// Parse 解析一个金额。ok=false 表示这压根不是数字。
//
// 认这些写法（都是真实导出文件里见过的）：
//
//	1234.5  1,234.50  ￥1,234.50  $1,234.50  (1,234.50) 表示负数
//	１２３４（全角）    -1,234.50    1 234.50（空格千分位）
func Parse(s string) (decimal.Decimal, bool) {
	v := strings.TrimSpace(s)
	if v == "" {
		return Zero, false
	}

	// 全角转半角（财务系统导出的全角数字是经典坑）
	v = halfWidth(v)
	v = strings.TrimSpace(v)

	negative := false
	// 会计写法：(1,234.50) 表示负数
	if strings.HasPrefix(v, "(") && strings.HasSuffix(v, ")") {
		negative = true
		v = strings.TrimSpace(v[1 : len(v)-1])
	}

	// 去掉货币符号与千分位分隔符
	v = strings.NewReplacer(
		"￥", "", "¥", "", "$", "", "€", "", "£", "",
		",", "", "，", "", " ", "", "\u00a0", "",
	).Replace(v)

	// 百分号：报表里常出现，按「除以 100」处理
	percent := false
	if strings.HasSuffix(v, "%") {
		percent = true
		v = strings.TrimSuffix(v, "%")
	}

	if v == "" || v == "-" || v == "+" {
		return Zero, false
	}

	d, err := decimal.NewFromString(v)
	if err != nil {
		return Zero, false
	}
	if percent {
		d = d.Div(decimal.NewFromInt(100))
	}
	if negative {
		d = d.Neg()
	}
	return d, true
}

// Sum 求和。空输入返回 0。
func Sum(vals []decimal.Decimal) decimal.Decimal {
	// 用 decimal 自带累加，逐个 Add 在大数据量下会产生很多临时对象
	sum := decimal.Zero
	for _, v := range vals {
		sum = sum.Add(v)
	}
	return sum
}

// SumStrings 直接对字符串求和（不能解析的跳过，返回跳过的数量）。
func SumStrings(vals []string) (decimal.Decimal, int) {
	sum := decimal.Zero
	var skipped int
	for _, s := range vals {
		d, ok := Parse(s)
		if !ok {
			if strings.TrimSpace(s) != "" {
				skipped++
			}
			continue
		}
		sum = sum.Add(d)
	}
	return sum, skipped
}

// Round 四舍五入到 digits 位小数。
//
// shopspring/decimal 的 Round 就是「半数远离零」（0.125 → 0.13），
// 也就是会计口径的四舍五入 —— 不是银行家舍入（那种会把 0.125 变 0.12，
// 财务核账时会被当成 bug）。
func Round(d decimal.Decimal, digits int32) decimal.Decimal {
	return d.Round(digits)
}

// RoundHalfUp 与 Round 同义，保留这个名字是为了让调用处读起来更明确。
func RoundHalfUp(d decimal.Decimal, digits int32) decimal.Decimal {
	return d.Round(digits)
}

// Format 输出成字符串：保留指定小数位，不带千分位（便于再计算）。
func Format(d decimal.Decimal, digits int32) string {
	return d.StringFixed(digits)
}

// Text 输出成字符串但不补零（3380 而不是 3380.00），用于一般的汇总结果。
func Text(d decimal.Decimal) string {
	return d.String()
}

// Allocate 把一笔金额按比例拆成若干份，尾差按最大余数法分给前面几份，
// 保证拆完加起来**一分不差**。
//
// 这是财务最常踩的坑：100 块三个部门按 1:1:1 分，平均算出来每人 33.33，
// 合计 99.99，差的那一分钱必须有人担，不能凭空消失。
func Allocate(total decimal.Decimal, ratios []decimal.Decimal) []decimal.Decimal {
	n := len(ratios)
	if n == 0 {
		return nil
	}
	sumRatio := Sum(ratios)
	if sumRatio.IsZero() {
		// 比例全 0 就平均分
		ratios = make([]decimal.Decimal, n)
		for i := range ratios {
			ratios[i] = decimal.NewFromInt(1)
		}
		sumRatio = decimal.NewFromInt(int64(n))
	}

	// 先按比例各取 2 位小数
	out := make([]decimal.Decimal, n)
	assigned := decimal.Zero
	for i, r := range ratios {
		share := total.Mul(r).Div(sumRatio).Round(2)
		out[i] = share
		assigned = assigned.Add(share)
	}

	// 尾差补到金额最大的那一份上，避免「最后一格莫名其妙多一分」
	diff := total.Sub(assigned).Round(2)
	if !diff.IsZero() {
		idx := 0
		for i := 1; i < n; i++ {
			if out[i].Abs().GreaterThan(out[idx].Abs()) {
				idx = i
			}
		}
		out[idx] = out[idx].Add(diff)
	}
	return out
}

// Rat 转成 big.Rat，供需要精确分数运算的地方用。
func Rat(d decimal.Decimal) *big.Rat {
	r := new(big.Rat)
	r.SetString(d.String())
	return r
}

// IsInteger 是否是整数（判断「金额列还是数量列」时有用）。
func IsInteger(d decimal.Decimal) bool {
	return d.Equal(d.Truncate(0))
}

// halfWidth 全角转半角。
func halfWidth(s string) string {
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
