package excel

// 落盘与判重的公共安全件。
//
// 都是「会毁数据」的那一类小事，集中在这里，改一处所有路径都受益：
//   · resolveOutputPath 所有 outputPath 入口共用的边界校验（不许 ../ 越界、不许覆盖源文件）
//   · safeOutputPath  输出目录 + 文件名 的拼接（文件名里不许带任何路径）
//   · sameFile        判断两个路径是不是同一个文件（硬链接/符号链接也算）
//   · isLongInteger   16 位以上整数（含带小数）必须按文本写（Excel 只有 15 位有效数字）
//   · normalizeKeyValue 判重键归一（1001 / 1001.0 / " 1001" / 001001 是同一个值）

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// isLongInteger 整数部分是否是 16 位以上的数字（允许小数、正负号）。
//
// 为什么卡 16 位：Excel 的数值只有 15 位有效数字，第 16 位开始就被四舍五入掉。
// 18 位身份证写成数值后是 110101199003071000，19 位卡号末 4 位变 0，
// 而文件不报错、单元格看起来还是一串数字。
//
// ★ 带小数的也要卡：1234567890123456.78 换算成 float64 后整数部分同样只剩
// 15 位有效数字，末位会被改掉。所以判据是「整数位的位数」，不是「是否纯整数」。
// 指数形式（1e20）一律按文本：再解析一步就丢精度。
func isLongInteger(v string) bool {
	s := strings.TrimSpace(v)
	s = strings.TrimLeft(s, "+-")
	if strings.ContainsAny(s, "eE") {
		return true
	}
	intPart := s
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart = s[:i]
	}
	digits := 0
	for _, r := range intPart {
		if r < '0' || r > '9' {
			return false
		}
		digits++
	}
	return digits >= 16
}

// resolveOutputPath 校验并规范化「用户 / 模型给出的输出路径」。
//
// 为什么必须集中一处：write_query_result 早就把文件名里的 ../ 挡住了，但
// convert / transform / group_summary / join / split / merge 都是直接拿
// outputPath 去写 —— 实测 outputPath=in/../OUTSIDE.csv 会把文件写到父目录，
// 用户以为东西存在项目目录里，实际散落在别处。
//
// 规则（刻意不做目录白名单）：
//   - 绝对路径照常放行：用户明确说「存到桌面」是合法用法，只做 Clean 归一；
//   - 相对路径按 base 解析，且任何位置都不许出现 .. 段 —— 相对路径不许跳出基准目录；
//   - 结果不得等于任何源文件（sameFile 级判断，硬链接/符号链接也算）。
//
// 不负责去重：调用方继续走 uniquePath / WriteTable（同名加 -1，绝不覆盖）。
func resolveOutputPath(base, outPath string, protect ...string) (string, error) {
	outPath = strings.TrimSpace(outPath)
	if outPath == "" {
		return "", fmt.Errorf("输出路径为空")
	}
	// 两端分隔符都看：模型偶尔会照 Windows 习惯给 a\..\b.xlsx
	for _, seg := range strings.FieldsFunc(outPath, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return "", fmt.Errorf("输出路径不能包含 .. 段：%q（请给绝对路径，或给基准目录内的相对路径）", outPath)
		}
	}

	var path string
	if filepath.IsAbs(outPath) {
		path = filepath.Clean(outPath)
	} else {
		baseAbs, err := filepath.Abs(base)
		if err != nil {
			return "", fmt.Errorf("输出基准目录不合法: %w", err)
		}
		path = filepath.Join(baseAbs, outPath)
	}
	for _, src := range protect {
		if sameFile(path, src) {
			return "", fmt.Errorf("拒绝写入源文件 %s：这会覆盖你的原始文件。请换一个输出路径", filepath.Base(path))
		}
	}
	return path, nil
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

// thousandsInt 形如「1,234」「12,345,678」的千分位整数部分。
//
// 只有整体长这样才允许去逗号；「1,2,3」这种含逗号文本不是数字，
// 去掉逗号会变成 123，判重时不同文本就被当成同一个值（假的重复）。
var thousandsInt = regexp.MustCompile(`^\d{1,3}(,\d{3})+$`)

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
	s = strings.NewReplacer("，", ",", " ", "", "　", "").Replace(s)
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
	// 小数部分不许有逗号；整数部分只有在逗号全部落在千分位时才去掉
	if strings.Contains(fracPart, ",") {
		return "", false
	}
	if strings.Contains(intPart, ",") {
		if !thousandsInt.MatchString(intPart) {
			return "", false
		}
		intPart = strings.ReplaceAll(intPart, ",", "")
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
