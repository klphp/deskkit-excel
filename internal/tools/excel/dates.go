package excel

// 日期解析 —— 用 github.com/araddon/dateparse，不再自己列格式表。
//
// 自己列 layout 的结果是永远列不全：「2026年10月1日」「10/01/2026」
// 「Jan 2, 2006」「2026-10-01 8:30」用户都会发过来，而每漏一种就是一个
// 「我的日期识别不出来」的报障。dateparse 覆盖了几十种写法，是这块的事实标准。
//
// 我们只加两件事：
//   · 中文写法（dateparse 不认「年月日」）先归一
//   · 纯数字要先判：dateparse 会把 "123" 也当日期，那会让整列误判成日期列

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/araddon/dateparse"
)

// chineseDate 2026年10月1日 / 2026年1月2日 8:30
var chineseDateRe = regexp.MustCompile(`^(\d{4})\s*年\s*(\d{1,2})\s*月\s*(\d{1,2})\s*日?`)

// allDigits 纯数字串。
var allDigitsRe = regexp.MustCompile(`^-?\d+(\.\d+)?$`)

// parseDate 解析日期。
func parseDate(s string) (time.Time, error) {
	v := strings.TrimSpace(s)
	if v == "" {
		return time.Time{}, fmt.Errorf("空")
	}

	// ① 纯数字：只有 8 位（20261001）或 Excel 序列号才算日期。
	//    否则 "123"、"2026" 都会被 dateparse 认成日期，整列类型就错了。
	if allDigitsRe.MatchString(v) {
		digits := strings.TrimPrefix(v, "-")
		if len(digits) == 8 && !strings.Contains(digits, ".") {
			if t, err := time.ParseInLocation("20060102", digits, time.Local); err == nil {
				return t, nil
			}
		}
		if n, err := strconv.ParseFloat(v, 64); err == nil && n > 20000 && n < 80000 {
			// Excel 把日期存成从 1899-12-30 起的天数
			return time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(n)), nil
		}
		return time.Time{}, fmt.Errorf("不是日期: %s", s)
	}

	// ② 中文写法先归一成 2026-10-01
	if m := chineseDateRe.FindStringSubmatch(v); m != nil {
		v = fmt.Sprintf("%s-%s-%s", m[1], m[2], m[3])
	}

	// ③ 其余交给 dateparse
	t, err := dateparse.ParseAny(v)
	if err != nil {
		return time.Time{}, fmt.Errorf("不是日期: %s", s)
	}
	return t, nil
}

// datePart 取年/月/日/季度/星期。
func datePart(t time.Time, part string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(part)) {
	case "year", "年", "yyyy":
		return strconv.Itoa(t.Year()), nil
	case "month", "月", "mm":
		return fmt.Sprintf("%02d", int(t.Month())), nil
	case "day", "日", "dd":
		return fmt.Sprintf("%02d", t.Day()), nil
	case "quarter", "季度", "q":
		return fmt.Sprintf("Q%d", (int(t.Month())-1)/3+1), nil
	case "weekday", "星期", "周":
		return []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[int(t.Weekday())], nil
	case "week", "周数":
		_, w := t.ISOWeek()
		return strconv.Itoa(w), nil
	case "ym", "年月":
		return t.Format("2006-01"), nil
	}
	return "", fmt.Errorf("不认识的时间部分 %q（可用 year/month/day/quarter/weekday/week/ym）", part)
}
