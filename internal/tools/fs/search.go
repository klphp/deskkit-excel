package fs

// 按内容搜关键字。
//
// 为什么不引入正则：模型写正则的失败率远高于写错路径 —— 一个括号没转义就
// 静默搜不到，它会当成「文件里没有」。纯文本包含匹配没有这个坑，语义也正好
// 覆盖「找哪个文件里有这段报错 / 这个配置项」这类真实需求。
//
// 编码是关键：中文项目里 GBK 的 .txt/.csv 非常多，按 UTF-8 硬比字节会全部
// 漏掉。所以每个文件都先过 xtext.Detect 转成 UTF-8 再比 —— 和 fs_read /
// fs_peek 是同一份编码判断，不会出现「peek 说有、search 说没有」。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/klphp/excel-batch-tool/internal/xtext"
)

const (
	// defaultSearchMaxResults 默认最多回 100 条命中：再多模型也看不完。
	defaultSearchMaxResults = 100
	// maxSearchResults 上限 5000 条，仍然可观但不会把内存吃干。
	maxSearchResults = 5000
	// defaultSearchMaxFileSize 超过 5MB 的文件跳过：日志/数据库/视频
	// 这类文件读了也只是白白浪费 IO。
	defaultSearchMaxFileSize int64 = 5 << 20
	// searchLineCap 命中行只回前 200 字，防止一整行 JSON 刷屏。
	searchLineCap = 200
	// maxSearchFiles 枚举上限：防止误传入 / 或家目录时把整台机器走一遍。
	maxSearchFiles = 200000
)

// errEnough 内部哨兵：命中数够了，主动停掉其余 worker。
// 不当错误上报给调用方，只是让 errgroup 提前 cancel。
var errEnough = errors.New("搜索结果已足够")

// SearchOptions 搜索参数。
type SearchOptions struct {
	// Path 目录或单个文件。
	Path string `json:"path"`
	// Pattern 纯文本关键字（不是正则）。
	Pattern string `json:"pattern"`
	// Extensions 可选后缀过滤，如 ["txt", "csv"] 或 [".go"]。
	Extensions []string `json:"extensions,omitempty"`
	// MaxResults 最多返回多少条，默认 100，上限 5000。
	MaxResults int `json:"maxResults,omitempty"`
	// MaxFileSize 超过这么多字节的文件直接跳过，默认 5MB。
	MaxFileSize int64 `json:"maxFileSize,omitempty"`
	// Concurrency 并行扫几个文件，默认核数×2（上限 32）。
	Concurrency int `json:"concurrency,omitempty"`
	// CaseSensitive 是否区分大小写，默认 false。
	CaseSensitive bool `json:"caseSensitive,omitempty"`
}

// SearchMatch 一条命中。
type SearchMatch struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// SearchStats 扫描统计 —— 让调用方能判断「是没搜到」还是「根本没扫」。
type SearchStats struct {
	FilesScanned    int   `json:"filesScanned"`
	FilesMatched    int   `json:"filesMatched"`
	FilesSkippedBig int   `json:"filesSkippedBig"`
	FilesSkippedBin int   `json:"filesSkippedBinary"`
	FilesSkippedBad int   `json:"filesSkippedUnreadable"`
	DirsSkipped     int   `json:"dirsSkipped"`
	ElapsedMS       int64 `json:"elapsedMs"`
	Concurrency     int   `json:"concurrency"`
}

// SearchResult 搜索结果。
type SearchResult struct {
	Root    string        `json:"root"`
	Pattern string        `json:"pattern"`
	Matches []SearchMatch `json:"matches"`
	// Truncated 命中数被 MaxResults 截断。
	Truncated bool        `json:"truncated"`
	Stats     SearchStats `json:"stats"`
	Hint      string      `json:"hint"`
}

// searchFile 一个待扫文件。
type searchFile struct {
	path string
	size int64
}

// Search 按内容搜关键字。
func (g Guard) Search(ctx context.Context, opt SearchOptions) (SearchResult, error) {
	start := time.Now()

	root, err := g.Check(opt.Path)
	if err != nil {
		return SearchResult{}, err
	}
	if strings.TrimSpace(opt.Pattern) == "" {
		return SearchResult{}, errors.New("pattern 为空：请给出要搜的关键字")
	}
	info, err := os.Stat(root)
	if err != nil {
		return SearchResult{}, fmt.Errorf("打不开: %w", err)
	}

	maxResults := opt.MaxResults
	if maxResults <= 0 {
		maxResults = defaultSearchMaxResults
	}
	if maxResults > maxSearchResults {
		maxResults = maxSearchResults
	}
	maxFileSize := opt.MaxFileSize
	if maxFileSize <= 0 {
		maxFileSize = defaultSearchMaxFileSize
	}

	out := SearchResult{Root: root, Pattern: opt.Pattern}

	// ── ① 列出要扫的文件 ──
	var files []searchFile
	if info.IsDir() {
		files, out.Stats.DirsSkipped = g.collectFiles(ctx, root, opt.Extensions, maxFileSize, &out.Stats)
	} else {
		if matchExt(root, opt.Extensions) {
			files = []searchFile{{path: root, size: info.Size()}}
		}
	}

	// ── ② 并行扫 ──
	workers := resolveWorkers(opt.Concurrency, len(files))
	out.Stats.Concurrency = workers

	pattern := opt.Pattern
	if !opt.CaseSensitive {
		pattern = strings.ToLower(pattern)
	}

	perFile := make([][]SearchMatch, len(files))
	var scanned, skippedBig, skippedBin, skippedBad int64
	var found int64

	scanErr := parallelFor(ctx, workers, len(files), func(ctx context.Context, i int) error {
		f := files[i]
		if f.size > maxFileSize {
			atomic.AddInt64(&skippedBig, 1)
			return nil
		}
		if atomic.LoadInt64(&found) >= int64(maxResults) {
			return errEnough
		}
		matches, state := scanFile(ctx, f.path, pattern, opt.CaseSensitive, maxResults)
		switch state {
		case scanBinary:
			atomic.AddInt64(&skippedBin, 1)
			return nil
		case scanFailed:
			atomic.AddInt64(&skippedBad, 1)
			return nil
		}
		atomic.AddInt64(&scanned, 1)
		if len(matches) > 0 {
			// 每个任务只写自己的槽位，不用锁；合并顺序即文件枚举顺序，
			// 保证同样的输入 always 得到同样的输出。
			perFile[i] = matches
			atomic.AddInt64(&found, int64(len(matches)))
		}
		return nil
	})
	if scanErr != nil && !errors.Is(scanErr, errEnough) && !errors.Is(scanErr, context.Canceled) {
		return out, scanErr
	}
	// 调用方主动取消/超时：如实报错，别让上层以为「搜完了没有命中」。
	if err := ctx.Err(); err != nil {
		return out, err
	}

	for _, ms := range perFile {
		out.Matches = append(out.Matches, ms...)
	}
	// 命中跨文件按路径排序，行号天然递增 —— 模型读起来比随机顺序省心。
	sort.SliceStable(out.Matches, func(i, j int) bool {
		if out.Matches[i].Path != out.Matches[j].Path {
			return out.Matches[i].Path < out.Matches[j].Path
		}
		return out.Matches[i].Line < out.Matches[j].Line
	})
	if len(out.Matches) > maxResults {
		out.Matches = out.Matches[:maxResults]
		out.Truncated = true
	}
	if len(out.Matches) >= maxResults {
		out.Truncated = true
	}
	// 命中文件数按去重后的路径算（「命中 3 个文件 40 处」比「40 处」有用）。
	seen := map[string]bool{}
	for _, m := range out.Matches {
		seen[m.Path] = true
	}
	out.Stats.FilesScanned = int(scanned)
	out.Stats.FilesMatched = len(seen)
	// collectFiles 已经记了枚举阶段因过大跳过的文件数，这里叠加扫描阶段的
	// 兜底计数（两处都可能命中），不能被覆盖成 0。
	out.Stats.FilesSkippedBig += int(skippedBig)
	out.Stats.FilesSkippedBin = int(skippedBin)
	out.Stats.FilesSkippedBad = int(skippedBad)
	out.Stats.ElapsedMS = time.Since(start).Milliseconds()
	out.Hint = searchHint(out, len(files))
	return out, nil
}

// scanState 单文件扫描的结局。
type scanState int

const (
	scanOK scanState = iota
	scanBinary
	scanFailed
)

// scanFile 扫一个文件，返回到 maxResults 条命中为止。
//
// 一次性整读：调用方已按 MaxFileSize（默认 5MB）把大文件挡在门外，
// 5MB 以内整读再逐行比流式读更简单也更准 —— 流式解码会在 64KB 边界上
// 把多字节字符切开，中文关键字恰好跨边界时就会漏。宁可多占几 MB，
// 也不要「搜不到」这种最难解释的错。
func scanFile(ctx context.Context, path, pattern string, caseSensitive bool, maxResults int) ([]SearchMatch, scanState) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, scanFailed
	}
	if len(data) == 0 {
		return nil, scanOK
	}
	text, _, isText := xtext.Detect(data)
	if !isText {
		return nil, scanBinary
	}

	var matches []SearchMatch
	for i, line := range splitLines(text) {
		if ctx.Err() != nil {
			break
		}
		if matchesLine(line, pattern, caseSensitive) {
			matches = append(matches, SearchMatch{Path: path, Line: i + 1, Text: clipLine(line, searchLineCap)})
			if len(matches) >= maxResults {
				break
			}
		}
	}
	return matches, scanOK
}

// matchesLine 单行是否命中。
//
// 大小写不敏感时两边都 lower：中文匹配不受影响，英文「Error/error/ERROR」
// 都能命中，这正是用户想要的行为。
func matchesLine(line, pattern string, caseSensitive bool) bool {
	if caseSensitive {
		return strings.Contains(line, pattern)
	}
	return strings.Contains(strings.ToLower(line), pattern)
}

// clipLine 截断到 n 个字（按 rune，避免切出半个汉字）。
func clipLine(s string, n int) string {
	s = strings.TrimRight(s, " \t")
	if len(s) <= n {
		return s
	}
	cut := truncateRunes(s, n)
	return strings.TrimRight(cut, " \t") + "…"
}

// collectFiles 枚举待扫文件。返回文件列表与被跳过的目录数。
//
// 目录遍历本身是串行的：瓶颈在下面读文件内容的并行扫描，
// 串行枚举可以省掉协调开销，也让「同一个目录 always 扫同样的文件」更好保证。
func (g Guard) collectFiles(ctx context.Context, root string, exts []string, maxFileSize int64, stats *SearchStats) ([]searchFile, int) {
	var files []searchFile
	dirsSkipped := 0
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// 单个条目没权限就跳过，不中断整次搜索
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if path == root {
			return nil
		}
		if d.IsDir() {
			if shouldSkipDir(d.Name()) {
				dirsSkipped++
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // 软链/设备文件不跟，避免搜到界外或卡死
		}
		if !matchExt(path, exts) {
			return nil
		}
		fi, e := d.Info()
		if e != nil {
			return nil
		}
		if fi.Size() > maxFileSize {
			stats.FilesSkippedBig++
			return nil
		}
		if len(files) >= maxSearchFiles {
			return filepath.SkipAll
		}
		files = append(files, searchFile{path: path, size: fi.Size()})
		return nil
	})
	return files, dirsSkipped
}

// matchExt 后缀过滤。写法容错："txt" 与 ".txt" 都认，大小写不敏感。
func matchExt(path string, exts []string) bool {
	if len(exts) == 0 {
		return true
	}
	ext := strings.ToLower(filepath.Ext(path))
	for _, want := range exts {
		w := strings.ToLower(strings.TrimSpace(want))
		if w == "" {
			continue
		}
		if !strings.HasPrefix(w, ".") {
			w = "." + w
		}
		if ext == w {
			return true
		}
	}
	return false
}

func searchHint(r SearchResult, total int) string {
	st := r.Stats
	parts := []string{fmt.Sprintf("扫了 %d 个文件（共 %d 个候选），命中 %d 处 / %d 个文件，耗时 %dms",
		st.FilesScanned, total, len(r.Matches), st.FilesMatched, st.ElapsedMS)}
	if r.Truncated {
		parts = append(parts, fmt.Sprintf("命中数已达上限，请缩小目录或加 extensions 过滤"))
	}
	if st.FilesSkippedBig > 0 {
		parts = append(parts, fmt.Sprintf("跳过 %d 个超大文件（>%d 字节，可用 maxFileSize 放宽）", st.FilesSkippedBig, defaultSearchMaxFileSize))
	}
	if st.FilesSkippedBin > 0 {
		parts = append(parts, fmt.Sprintf("跳过 %d 个二进制文件", st.FilesSkippedBin))
	}
	if len(r.Matches) == 0 && st.FilesScanned == 0 {
		parts = append(parts, "一个文件都没扫（可能是后缀/大小过滤把候选全排除了）")
	}
	return strings.Join(parts, "；") + "。"
}
