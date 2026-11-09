package fs

// 读文本文件的一段。
//
// 和 fs_peek 的分工：
//   · fs_peek 只读头部，回答「这是什么文件」（编码、分隔符、表头）；
//   · fs_read 按行读一段，回答「里面写了什么」。
//
// 为什么必须按行走而不是「读整个文件」：模型往往只需要第 500~700 行看一段
// 配置或一段报错，把 300MB 日志整个塞进上下文既烧钱又没用。所以这里用
// offset/limit 定位，再加 maxBytes 兜底，保证单次调用最多只碰几 MB。
//
// 编码统一交给 xtext.Detect（和 fs_peek、表格模块同一份实现）：GBK / Big5 /
// UTF-16 / BOM 都能读，并且把**真实**编码回给调用方 —— 模型知道自己看到的是
// 转码后的 UTF-8，才能正确判断原文件该用什么编码写回。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/klphp/excel-batch-tool/internal/xtext"
)

const (
	// defaultReadLimit 一次默认返回 200 行。够看清一段代码/一段报错，
	// 又不至于把上下文塞满。
	defaultReadLimit = 200
	// maxReadLimit 上限 5 万行：再多就该用 fs_search 定位了。
	maxReadLimit = 50000
	// defaultReadMaxBytes 单次最多读 2MB，防止一个大文件直接吃掉内存。
	defaultReadMaxBytes int64 = 2 << 20
	// maxReadMaxBytes 调用方可以调大，但最多 64MB。
	maxReadMaxBytes int64 = 64 << 20
	// readLineCap 单行超长（压缩过的 JSON、minified JS）就截断，
	// 否则一行就能撑爆上下文。
	readLineCap = 4000
)

// ReadOptions 读文件的参数。
type ReadOptions struct {
	Path string `json:"path"`
	// Offset 起始行，从 1 开始。默认 1。
	Offset int `json:"offset,omitempty"`
	// Limit 读多少行，默认 200，上限 5 万。
	Limit int `json:"limit,omitempty"`
	// MaxBytes 单次最多读多少字节，默认 2MB，上限 64MB。
	MaxBytes int64 `json:"maxBytes,omitempty"`
	// Concurrency 仅在 ReadMany 里生效：并行读几个文件，默认核数×2。
	Concurrency int `json:"concurrency,omitempty"`
}

// ReadResult 读到的内容 + 定位信息。
type ReadResult struct {
	Path string `json:"path"`
	// Encoding 文件的真实编码（返回的文本已转成 UTF-8）。
	Encoding string `json:"encoding"`
	FileSize int64  `json:"fileSize"`
	// TotalLines 总行数。文件大于 MaxBytes 时是「已读部分」的行数，
	// 此时 TotalLinesExact=false。
	TotalLines      int  `json:"totalLines"`
	TotalLinesExact bool `json:"totalLinesExact"`
	// StartLine / EndLine 本次返回的行号区间（含两端，1 起）。
	StartLine int      `json:"startLine"`
	EndLine   int      `json:"endLine"`
	Lines     []string `json:"lines"`
	// Truncated 是否还有没返回的内容（行数不够读完，或文件超出 MaxBytes）。
	Truncated bool  `json:"truncated"`
	BytesRead int64 `json:"bytesRead"`
	// LongLinesCut 被按 4000 字截断的行数。
	LongLinesCut int    `json:"longLinesCut,omitempty"`
	Hint         string `json:"hint"`
}

// Read 读文本文件的一段。
func (g Guard) Read(ctx context.Context, opt ReadOptions) (ReadResult, error) {
	abs, err := g.Check(opt.Path)
	if err != nil {
		return ReadResult{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return ReadResult{}, fmt.Errorf("读不到这个文件: %w", err)
	}
	if info.IsDir() {
		return ReadResult{}, fmt.Errorf("%s 是个目录，fs_read 只用于文件；列目录请用 fs_list", abs)
	}

	offset := opt.Offset
	if offset <= 0 {
		offset = 1
	}
	limit := opt.Limit
	if limit <= 0 {
		limit = defaultReadLimit
	}
	if limit > maxReadLimit {
		limit = maxReadLimit
	}
	maxBytes := opt.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultReadMaxBytes
	}
	if maxBytes > maxReadMaxBytes {
		maxBytes = maxReadMaxBytes
	}

	out := ReadResult{Path: abs, FileSize: info.Size(), StartLine: offset}
	if info.Size() == 0 {
		out.Encoding = xtext.EncUTF8
		out.TotalLinesExact = true
		out.Hint = "这是一个空文件（0 字节）。"
		return out, nil
	}

	buf, enc, isText, err := readHead(ctx, abs, maxBytes)
	if err != nil {
		return ReadResult{}, err
	}
	if !isText {
		// 二进制不猜：乱码读出来模型会当成真内容用，比报错糟糕得多。
		return ReadResult{}, fmt.Errorf("这是二进制文件（识别为 %s），不能按文本读取；想知道它是什么请用 fs_peek", enc)
	}
	out.Encoding = enc
	out.BytesRead = int64(len(buf))
	out.TotalLinesExact = info.Size() <= int64(len(buf))

	lines := splitLines(string(buf))
	out.TotalLines = len(lines)

	if offset > len(lines) {
		switch {
		case !out.TotalLinesExact:
			return out, fmt.Errorf("offset=%d 超出已读范围（文件 %d 字节，只读了前 %d 字节，共 %d 行）；请调大 maxBytes 再试",
				offset, info.Size(), out.BytesRead, out.TotalLines)
		default:
			return out, fmt.Errorf("offset=%d 超出总行数 %d", offset, out.TotalLines)
		}
	}

	end := offset - 1 + limit
	if end > len(lines) {
		end = len(lines)
	}
	out.EndLine = end
	out.Lines = make([]string, 0, end-offset+1)
	for _, line := range lines[offset-1 : end] {
		if utf8.RuneCountInString(line) > readLineCap {
			line = truncateRunes(line, readLineCap) + "…（本行超长已截断）"
			out.LongLinesCut++
		}
		out.Lines = append(out.Lines, line)
	}
	// 还有内容没返回：要么行数没用完就到底了，要么文件本来就超出 MaxBytes。
	out.Truncated = end < out.TotalLines || !out.TotalLinesExact
	out.Hint = readHint(out, limit)
	return out, nil
}

func readHint(r ReadResult, limit int) string {
	parts := []string{fmt.Sprintf("%s 编码，第 %d–%d 行，共 %d 行", r.Encoding, r.StartLine, r.EndLine, r.TotalLines)}
	if r.Encoding == xtext.EncGB18030 {
		parts = append(parts, "原文件是国标编码（GBK/GB18030），返回内容已转成 UTF-8，写回时不要当成 UTF-8 存")
	}
	if !r.TotalLinesExact {
		parts = append(parts, fmt.Sprintf("文件 %d 字节，只读了前 %d 字节，后面的行没数；要看更后面请调大 maxBytes", r.FileSize, r.BytesRead))
	}
	if r.EndLine < r.TotalLines {
		parts = append(parts, fmt.Sprintf("还有 %d 行没读，接着读用 offset=%d", r.TotalLines-r.EndLine, r.EndLine+1))
	}
	if r.LongLinesCut > 0 {
		parts = append(parts, fmt.Sprintf("有 %d 行超长已截断", r.LongLinesCut))
	}
	if r.Truncated && r.EndLine >= r.TotalLines && r.TotalLinesExact {
		parts = append(parts, "已到文件末尾")
	}
	return strings.Join(parts, "；") + "。"
}

// readHead 读文件前 maxBytes 字节并判断编码。
func readHead(ctx context.Context, path string, maxBytes int64) ([]byte, string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", false, fmt.Errorf("打不开这个文件: %w", err)
	}
	defer f.Close()
	if err := ctx.Err(); err != nil {
		return nil, "", false, err
	}
	buf := make([]byte, maxBytes)
	n, rerr := io.ReadFull(f, buf)
	if rerr != nil && !errors.Is(rerr, io.EOF) && !errors.Is(rerr, io.ErrUnexpectedEOF) {
		return nil, "", false, fmt.Errorf("读取失败: %w", rerr)
	}
	buf = buf[:n]
	text, enc, isText := xtext.Detect(buf)
	if !isText {
		return nil, enc, false, nil
	}
	return []byte(text), enc, true, nil
}

// splitLines 把文本切成行。兼容 \r\n（Windows）与 \r（老 Mac），
// 末尾的换行不算「多一行空行」。
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// truncateRunes 按「字」截断，避免把中文/emoji 截成半个字节变成乱码。
func truncateRunes(s string, n int) string {
	i := 0
	for pos := range s {
		if i == n {
			return s[:pos]
		}
		i++
	}
	return s
}

// ReadFile 单个文件的读取结果（供 ReadMany 用）。
type ReadFile struct {
	Path   string      `json:"path"`
	Result *ReadResult `json:"result,omitempty"`
	Error  string      `json:"error,omitempty"`
}

// ReadMany 并行读多个文件的一段。
//
// 场景：模型说「把这 40 个配置文件的第一屏都给我看看」。一个文件一个请求
// 要 40 个来回，这里一次调用并行读返回。
// 单个文件失败不影响其它文件：错误记在条目里，整体仍然成功 ——
// 一个没权限的文件不该让另外 39 个白读。
func (g Guard) ReadMany(ctx context.Context, paths []string, opt ReadOptions) ([]ReadFile, error) {
	if len(paths) == 0 {
		return nil, errors.New("paths 为空：请给出要读的文件")
	}
	out := make([]ReadFile, len(paths))
	workers := resolveWorkers(opt.Concurrency, len(paths))
	err := parallelFor(ctx, workers, len(paths), func(ctx context.Context, i int) error {
		o := opt
		o.Path = paths[i]
		res, err := g.Read(ctx, o)
		if err != nil {
			out[i] = ReadFile{Path: paths[i], Error: err.Error()}
			return nil // 单文件失败不算整体失败
		}
		out[i] = ReadFile{Path: paths[i], Result: &res}
		return nil
	})
	if err != nil {
		return out, err
	}
	return out, nil
}
