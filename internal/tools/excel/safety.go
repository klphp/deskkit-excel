package excel

// 落盘与判重的公共安全件。
//
// 都是「会毁数据」的那一类小事，集中在这里，改一处所有路径都受益：
//   · safeOutputPath  输出路径不许被文件名里的 ../ 带出输出目录
//   · sameFile        判断两个路径是不是同一个文件（硬链接/符号链接也算）
//   · isLongInteger   16 位以上纯整数必须按文本写（Excel 只有 15 位有效数字）
//   · normalizeKeyValue 判重键归一（1001 / 1001.0 / " 1001" / 001001 是同一个值）

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// isLongInteger 是否是 16 位以上的纯整数（不含小数点、指数、正负号）。
//
// 为什么卡 16 位：Excel 的数值只有 15 位有效数字，第 16 位开始就被四舍五入掉。
// 18 位身份证写成数值后是 110101199003071000，19 位卡号末 4 位变 0，
// 而文件不报错、单元格看起来还是一串数字。
func isLongInteger(v string) bool {
	digits := 0
	for _, r := range v {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '.' || r == 'e' || r == 'E' || r == '+' || r == '-':
			return false
		default:
			return false
		}
	}
	return digits >= 16
}

// safeOutputPath 把「输出目录 + 文件名」拼成路径，并拒绝任何带目录的文件名。
//
// 模型给的文件名一旦带 ../，就能在任意可写位置生成文件（实测 in/../OUTSIDE.csv
// 会真的写到父目录）。输出只允许落在给定目录里。
func safeOutputPath(dir, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("输出文件名为空")
	}
	if filepath.IsAbs(name) || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return "", fmt.Errorf("输出文件名不能带路径: %q（目录请用 outputDir 传，文件名只给名字）", name)
	}
	dirAbs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("输出目录不合法: %w", err)
	}
	return filepath.Join(dirAbs, name), nil
}

// sameFile 两个路径是否指向同一个文件。
//
// 先比字符串：目标还不存在时 Stat 会失败，但「路径等于源文件」本身就该拒绝。
func sameFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(fa, fb)
}

// normalizeKeyValue 把「同一个值的不同写法」归一到同一个键。
//
// 为什么必须归一：判重 / 去重以前按原始字符串比较，于是 1001、1001.0、" 1001"、
// 001001 被当成四条不同的单据 —— 对账时「重复行查不出来」，合计直接翻倍。
//
// 规则：全角转半角、去首尾空白；能解析成十进制数就用规范形
// （去千分位、去前导零、去尾随零）；其它原样返回。
func normalizeKeyValue(s string) string {
	v := strings.TrimSpace(toHalfWidth(s))
	if v == "" {
		return ""
	}
	if n, ok := canonDecimal(v); ok {
		return n
	}
	return v
}

// canonDecimal 把一个数字文本规范成「等价写法唯一」的形式。
//
// 用字符串处理而不是 decimal：目的只是让相等值相等，
// 不需要做运算，也就不该引入精度与量级的边界。
func canonDecimal(v string) (string, bool) {
	s := strings.TrimSpace(v)
	neg := false
	switch {
	case strings.HasPrefix(s, "-"):
		neg = true
		s = s[1:]
	case strings.HasPrefix(s, "+"):
		s = s[1:]
	}
	s = strings.NewReplacer(",", "", "，", "", " ", "", "　", "").Replace(s)
	if s == "" {
		return "", false
	}

	intPart, fracPart := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, fracPart = s[:i], s[i+1:]
		if strings.Contains(fracPart, ".") {
			return "", false
		}
	}
	if intPart == "" && fracPart == "" {
		return "", false
	}
	for _, r := range intPart {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	for _, r := range fracPart {
		if r < '0' || r > '9' {
			return "", false
		}
	}

	intPart = strings.TrimLeft(intPart, "0")
	if intPart == "" {
		intPart = "0"
	}
	fracPart = strings.TrimRight(fracPart, "0")
	out := intPart
	if fracPart != "" {
		out += "." + fracPart
	}
	if neg && out != "0" {
		out = "-" + out
	}
	return out, true
}
