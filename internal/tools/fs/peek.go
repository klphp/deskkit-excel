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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/klphp/excel-batch-tool/internal/xtext"
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
// detectAndDecode 交给 xtext：编码识别只有一份实现。
//
// 原来这里和表格模块各写一套，一旦一处认 GBK 另一处不认，就会出现
// 「看一眼正常、真读进来全是乱码」这种最难查的问题。
func detectAndDecode(b []byte) (text string, enc string, isText bool) {
	return xtext.Detect(b)
}

// guessDelimiter 同样交给 xtext。
func guessDelimiter(lines []string) string {
	return xtext.GuessDelimiter(lines)
}

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
