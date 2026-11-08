package fs

// 文件「看一眼」—— 让模型先了解一个文件是什么，再决定怎么处理。
//
// 为什么需要这个工具：
//   · 用户丢过来一个 .csv，我们不知道是 UTF-8 还是 GBK（国内导出的报表
//     十有八九是 GBK）。猜错就是满屏乱码，而模型看不出来它错了。
//   · 分隔符可能是逗号、分号、制表符，也可能是竖线。
//   · 模型需要「先看一眼再决定」，而不是先假设格式、失败、再换一种假设。
//
// 只读前几十行，不加载整个文件：几百 MB 的日志也能秒回。

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// PeekOptions 看一眼的参数。
type PeekOptions struct {
	Path string `json:"path"`
	// MaxLines 最多返回多少行样本，默认 10，上限 50。
	MaxLines int `json:"maxLines,omitempty"`
}

// PeekResult 文件面貌。
type PeekResult struct {
	Path string `json:"path"`
	// Size 字节数。
	Size int64 `json:"size"`
	// Kind 粗分类：text / binary / empty。
	Kind string `json:"kind"`
	// Encoding 识别出的编码：utf-8 / utf-8-bom / utf-16le / utf-16be / gb18030。
	Encoding string `json:"encoding"`
	// Delimiter 推测的列分隔符，非文本或无法判断时为空。
	Delimiter string `json:"delimiter,omitempty"`
	// Columns 首行按分隔符切出来的列数。
	Columns int `json:"columns,omitempty"`
	// Header 首行各列名（最多 30 个）。
	Header []string `json:"header,omitempty"`
	// Lines 样本行（已转成 UTF-8）。
	Lines []string `json:"lines"`
	// Truncated 是否因为行太长或太多而截断。
	Truncated bool `json:"truncated"`
	// Hint 给模型的一句话结论。
	Hint string `json:"hint"`
}

const (
	// peekReadBytes 只读文件头部这么多字节。判断编码和分隔符足够，
	// 而且保证几百 MB 的文件也是秒回。
	peekReadBytes = 64 * 1024
	// peekMaxLineLen 单行超过这么长就截断，避免一整行 JSON 撑爆上下文。
	peekMaxLineLen = 500
)

// Peek 读文件头部，返回编码、分隔符、样本行。
func (g Guard) Peek(ctx context.Context, opt PeekOptions) (PeekResult, error) {
	abs, err := g.Check(opt.Path)
	if err != nil {
		return PeekResult{}, err
	}

	info, err := os.Stat(abs)
	if err != nil {
		return PeekResult{}, fmt.Errorf("读不到这个文件: %w", err)
	}
	if info.IsDir() {
		return PeekResult{}, fmt.Errorf("%s 是个目录，peek 只用于文件；列目录请用 fs_list", abs)
	}

	out := PeekResult{Path: abs, Size: info.Size()}
	if info.Size() == 0 {
		out.Kind = "empty"
		out.Hint = "这是一个空文件（0 字节）。"
		return out, nil
	}

	f, err := os.Open(abs)
	if err != nil {
		return PeekResult{}, fmt.Errorf("打不开这个文件: %w", err)
	}
	defer f.Close()

	buf := make([]byte, peekReadBytes)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return PeekResult{}, fmt.Errorf("读取失败: %w", err)
	}
	buf = buf[:n]

	text, enc, isText := detectAndDecode(buf)
	out.Encoding = enc
	if !isText {
		out.Kind = "binary"
		out.Hint = "这不是文本文件（二进制），不要按表格读取。"
		return out, nil
	}
	out.Kind = "text"

	maxLines := opt.MaxLines
	if maxLines <= 0 {
		maxLines = 10
	}
	if maxLines > 50 {
		maxLines = 50
	}

	raw := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, line := range raw {
		if i >= maxLines {
			out.Truncated = true
			break
		}
		if len(line) > peekMaxLineLen {
			line = line[:peekMaxLineLen]
			out.Truncated = true
		}
		out.Lines = append(out.Lines, line)
	}

	// 分隔符与表头：只在样本里至少两行、且每行都能切出多列时才算数
	if delim := guessDelimiter(raw); delim != "" {
		first := raw[0]
		cols := strings.Split(first, delim)
		// 至少 2 列，且第二行切出来的列数与之接近，才认定为表格
		if len(cols) >= 2 && looksTabular(raw, delim, len(cols)) {
			out.Delimiter = delim
			out.Columns = len(cols)
			for i, c := range cols {
				if i >= 30 {
					break
				}
				out.Header = append(out.Header, strings.TrimSpace(c))
			}
		}
	}

	out.Hint = peekHint(out)
	_ = filepath.Ext(abs) // 扩展名只用于提示，不参与判断
	return out, nil
}

// detectAndDecode 判断编码并转成 UTF-8。isText=false 表示是二进制。
func detectAndDecode(b []byte) (text string, enc string, isText bool) {
	// ① BOM 最可靠
	if len(b) >= 3 && bytes.Equal(b[:3], []byte{0xEF, 0xBB, 0xBF}) {
		return string(b[3:]), "utf-8-bom", true
	}
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		return decodeUTF16(b[2:], binary.LittleEndian), "utf-16le", true
	}
	if len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF {
		return decodeUTF16(b[2:], binary.BigEndian), "utf-16be", true
	}

	// ② 出现 NUL 基本就是二进制（UTF-16 无 BOM 也会命中，但那极少见）
	if bytes.IndexByte(b, 0) >= 0 {
		return "", "binary", false
	}

	// ③ 合法 UTF-8 就当 UTF-8
	if utf8.Valid(b) {
		return string(b), "utf-8", true
	}

	// ④ 剩下的按 GB18030 解（GBK 是它的子集）。
	//    国内导出的报表、银行流水绝大多数是这种。
	if decoded, err := gbkToUTF8(b); err == nil {
		return decoded, "gb18030", true
	}

	return "", "unknown", false
}

func decodeUTF16(b []byte, order binary.ByteOrder) string {
	n := len(b) / 2
	u := make([]uint16, n)
	for i := 0; i < n; i++ {
		u[i] = order.Uint16(b[i*2:])
	}
	return strings.TrimPrefix(string(utf16Decode(u)), "\uFEFF")
}

// utf16Decode 把 UTF-16 码元转成字符串（含代理对处理）。
func utf16Decode(u []uint16) string {
	var sb strings.Builder
	for i := 0; i < len(u); i++ {
		c := u[i]
		if c >= 0xD800 && c <= 0xDBFF && i+1 < len(u) {
			low := u[i+1]
			if low >= 0xDC00 && low <= 0xDFFF {
				sb.WriteRune(rune(0x10000 + (int32(c)-0xD800)<<10 + (int32(low) - 0xDC00)))
				i++
				continue
			}
		}
		sb.WriteRune(rune(c))
	}
	return sb.String()
}

func gbkToUTF8(b []byte) (string, error) {
	out, _, err := transform.Bytes(simplifiedchinese.GB18030.NewDecoder(), b)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// guessDelimiter 在前几行里数候选分隔符，取「每行出现次数一致且最多」的那个。
//
// 一致性很重要：Markdown 的竖线、正文里的逗号都可能出现一次，
// 但表格的每一行出现次数是稳定的。
func guessDelimiter(lines []string) string {
	cands := []string{",", ";", "\t", "|"}
	// 只数前 5 行非空行
	var sample []string
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		sample = append(sample, l)
		if len(sample) == 5 {
			break
		}
	}
	if len(sample) < 1 {
		return ""
	}

	best, bestCount, bestStable := "", 0, false
	for _, d := range cands {
		counts := make([]int, 0, len(sample))
		for _, l := range sample {
			counts = append(counts, strings.Count(l, d))
		}
		minC, maxC := counts[0], counts[0]
		for _, c := range counts[1:] {
			if c < minC {
				minC = c
			}
			if c > maxC {
				maxC = c
			}
		}
		stable := minC >= 1 && minC == maxC
		// 稳定优先；都不稳定时退回「出现次数最多且至少 1」
		if (stable && !bestStable) || (stable == bestStable && maxC > bestCount) {
			best, bestCount, bestStable = d, maxC, stable
		}
	}
	if bestCount == 0 {
		return ""
	}
	return best
}

// looksTabular 第二行切出来的列数要和首行接近，才认为这是表格而不是散文。
func looksTabular(lines []string, delim string, want int) bool {
	if len(lines) < 2 {
		return false
	}
	second := strings.Split(lines[1], delim)
	diff := len(second) - want
	if diff < 0 {
		diff = -diff
	}
	return diff <= 1
}

func peekHint(r PeekResult) string {
	parts := []string{fmt.Sprintf("%s 编码，%d 字节", r.Encoding, r.Size)}
	if r.Encoding == "gb18030" {
		parts = append(parts, "这是国标编码（GBK/GB18030），按 UTF-8 直接读会乱码，需要先转码")
	}
	if r.Delimiter != "" {
		parts = append(parts, fmt.Sprintf("看起来是分隔符为 %q 的表格，%d 列", r.Delimiter, r.Columns))
	} else if r.Kind == "text" {
		parts = append(parts, "没有明显的列分隔符，可能是纯文本或单列数据")
	}
	if r.Truncated {
		parts = append(parts, "样本已截断")
	}
	return strings.Join(parts, "；") + "。"
}
