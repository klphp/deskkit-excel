package fsx

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

	"github.com/jfyne/csvd"
	"github.com/klphp/deskkit-excel/internal/fsx/xtext"
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
		return PeekResult{}, fmt.Errorf("Cannot read this file: %w", err)
	}
	if info.IsDir() {
		return PeekResult{}, fmt.Errorf("%s is a folder; peek only works on files. Use fs_list to list a folder", abs)
	}

	out := PeekResult{Path: abs, Size: info.Size()}
	if info.Size() == 0 {
		out.Kind = "empty"
		out.Hint = "This is an empty file (0 bytes)."
		return out, nil
	}

	f, err := os.Open(abs)
	if err != nil {
		return PeekResult{}, fmt.Errorf("Cannot open this file: %w", err)
	}
	defer f.Close()

	buf := make([]byte, peekReadBytes)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return PeekResult{}, fmt.Errorf("Read failed: %w", err)
	}
	buf = buf[:n]

	text, enc, isText := detectAndDecode(buf)
	out.Encoding = enc
	if !isText {
		out.Kind = "binary"
		out.Hint = "This is not a text file (binary); do not read it as a table."
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

// guessDelimiter 交给 csvd：它按「切出来的列数分布」判断。
// 返回空串表示看起来不是表格（只有一列，没有可切的）。
func guessDelimiter(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	r := csvd.NewReader(strings.NewReader(strings.Join(lines, "\n")))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	// 读第一行就够了：csvd 在构造时已经嗅探完
	if _, err := r.Read(); err != nil {
		return ""
	}
	delim := string(r.Comma)
	// 只有真的是多列才算带分隔符
	rec, err := r.Read()
	if err != nil || len(rec) < 2 {
		return ""
	}
	return delim
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
	parts := []string{fmt.Sprintf("%s encoding, %d bytes", r.Encoding, r.Size)}
	if r.Encoding == "gb18030" {
		parts = append(parts, "This is a Chinese national-standard encoding (GBK/GB18030); reading it as UTF-8 gives garbled text, so convert it first")
	}
	if r.Delimiter != "" {
		parts = append(parts, fmt.Sprintf("Looks like a table with separator %q and %d columns", r.Delimiter, r.Columns))
	} else if r.Kind == "text" {
		parts = append(parts, "No obvious column separator; it may be plain text or a single column")
	}
	if r.Truncated {
		parts = append(parts, "The sample was truncated")
	}
	return strings.Join(parts, "；") + "。"
}
