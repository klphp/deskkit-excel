package money

import (
	"testing"

	"github.com/shopspring/decimal"
)

// 这个测试就是「为什么要用 decimal」的证据。
func TestFloatDriftIsGone(t *testing.T) {
	a, _ := Parse("0.1")
	b, _ := Parse("0.2")
	got := Sum([]decimal.Decimal{a, b})
	if got.String() != "0.3" {
		t.Fatalf("0.1+0.2 应该正好 0.3，实际 %s", got.String())
	}

	// 一万行 0.01 相加
	one, _ := Parse("0.01")
	sum := decimal.Zero
	for i := 0; i < 10000; i++ {
		sum = sum.Add(one)
	}
	if sum.String() != "100" {
		t.Fatalf("10000 × 0.01 应该正好 100，实际 %s", sum.String())
	}
}

func TestParseRealWorldMoney(t *testing.T) {
	cases := []struct{ in, want string }{
		{"1234.5", "1234.5"},
		{"1,234.50", "1234.5"},
		{"￥1,234.50", "1234.5"},
		{"$1,234.50", "1234.5"},
		{"(1,234.50)", "-1234.5"}, // 会计的负数写法
		{"１３８０", "1380"},          // 全角
		{"-1,234.50", "-1234.5"},
		{"1 234.50", "1234.5"}, // 空格千分位
		{"12.5%", "0.125"},     // 百分号
	}
	for _, c := range cases {
		d, ok := Parse(c.in)
		if !ok {
			t.Fatalf("%q 应该能解析", c.in)
		}
		if d.String() != c.want {
			t.Fatalf("%q 解析成 %s，应为 %s", c.in, d.String(), c.want)
		}
	}

	for _, bad := range []string{"", "—", "N/A", "-", "暂无"} {
		if _, ok := Parse(bad); ok {
			t.Fatalf("%q 不该被当成金额", bad)
		}
	}
}

func TestRoundHalfUp(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"0.125", "0.13"},
		{"0.135", "0.14"},
		{"2.675", "2.68"}, // float64 下这句会变成 2.67
		{"1.005", "1.01"}, // 同上
	}
	for _, c := range cases {
		d, _ := Parse(c.in)
		if got := RoundHalfUp(d, 2).String(); got != c.want {
			t.Fatalf("%s 四舍五入两位应为 %s，实际 %s", c.in, c.want, got)
		}
	}
}

// 尾差分摊：拆完必须一分不差。
func TestAllocateKeepsTotal(t *testing.T) {
	total, _ := Parse("100")
	ratios := []decimal.Decimal{
		decimal.NewFromInt(1), decimal.NewFromInt(1), decimal.NewFromInt(1),
	}
	parts := Allocate(total, ratios)
	if len(parts) != 3 {
		t.Fatalf("应拆 3 份，实际 %d", len(parts))
	}
	sum := Sum(parts)
	if !sum.Equal(total) {
		t.Fatalf("拆完合计 %s ≠ 原金额 %s（尾差丢了）", sum.String(), total.String())
	}
	// 33.34 + 33.33 + 33.33
	if parts[0].String() != "33.34" {
		t.Fatalf("尾差应补到第一份，实际 %s", parts[0].String())
	}
}

func TestSumStringsSkipsJunk(t *testing.T) {
	sum, skipped := SumStrings([]string{"1,200", "980", "暂无", "", "￥1,500"})
	if sum.String() != "3680" {
		t.Fatalf("合计应为 3680，实际 %s", sum.String())
	}
	// 空串不算「跳过的脏数据」，只有真的解析不了才算
	if skipped != 1 {
		t.Fatalf("应跳过 1 个脏值，实际 %d", skipped)
	}
}
