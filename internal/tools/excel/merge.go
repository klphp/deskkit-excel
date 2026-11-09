package excel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MergeOptions 合并参数。
type MergeOptions struct {
	// Dir 要处理的目录。
	Dir string `json:"dir"`

	// OutputDir 输出目录。留空则用 <Dir>/deskkit-out。
	// 单独开目录是刻意的：绝不往用户的原文件旁边写半成品。
	OutputDir string `json:"outputDir,omitempty"`

	// OutputName 输出文件名。留空用 merged-<时间>.xlsx。
	OutputName string `json:"outputName,omitempty"`

	// DedupKey 去重键（列名）。为空则不去重。
	DedupKey []string `json:"dedupKey,omitempty"`

	// DryRun 只统计不写文件。界面上「先给我看要做什么」走这条。
	DryRun bool `json:"dryRun"`
}

// SkipInfo 被跳过的文件及原因。
type SkipInfo struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// MergeResult 合并结果。DryRun 时 OutputPath 为空。
type MergeResult struct {
	OutputPath string     `json:"outputPath"`
	Files      int        `json:"files"`
	RowsIn     int        `json:"rowsIn"`
	RowsOut    int        `json:"rowsOut"`
	Duplicates int        `json:"duplicates"`
	Columns    []string   `json:"columns"`
	Skipped    []SkipInfo `json:"skipped,omitempty"`
	Warnings   []string   `json:"warnings,omitempty"`
	DryRun     bool       `json:"dryRun"`
	ElapsedMS  int64      `json:"elapsedMs"`
}

// Merge 把目录下的表格按**列名**纵向合并成一张表。
//
// 表头以第一个可读文件为准；其余文件按列名映射，缺的列留空并记一条 warning。
// 这是「格式不一的部门报表」最实用的行为：不要求列序一致，也不因为多一列就整份丢掉。
func Merge(ctx context.Context, opt MergeOptions) (MergeResult, error) {
	start := time.Now()
	var out MergeResult
	out.DryRun = opt.DryRun

	if strings.TrimSpace(opt.Dir) == "" {
		return out, fmt.Errorf("目录为空")
	}

	scan, err := Scan(ctx, opt.Dir)
	if err != nil {
		return out, err
	}
	if len(scan.Files) == 0 {
		return out, fmt.Errorf("目录里没有可处理的表格")
	}

	// ── 定表头：所有文件列名的并集，顺序按首次出现 ──
	//
	// 为什么不是「第一个文件的表头」：文件名排序是任意的，按第一个来
	// 可能选到一份缺列的报表，其它文件多出来的列会被静默丢掉 ——
	// 这类静默数据丢失最难被发现。
	//
	// 也不取「列最多那个文件的表头」：那样列序取决于哪个文件名字靠前，
	// 每次结果可能不一样。并集 + 首次出现顺序 = 不丢列且可预期。
	var columns []string
	seenCol := make(map[string]bool)
	for _, f := range scan.Files {
		if f.Err != "" {
			continue
		}
		// 读表时自动修正过的东西（重名列改名、补表头）必须转述给用户：
		// 否则合并结果里凭空多出「金额-2」这一列，没人知道它从哪来。
		for _, w := range f.Warnings {
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s: %s", f.Name, w))
		}
		for _, h := range f.Headers {
			if h == "" || seenCol[h] {
				continue
			}
			seenCol[h] = true
			columns = append(columns, h)
		}
	}
	var first string
	if len(columns) > 0 {
		first = fmt.Sprintf("%d 个文件的列名并集", len(scan.Files))
	}
	if len(columns) == 0 {
		return out, fmt.Errorf("所有文件都读不出表头")
	}
	out.Columns = columns
	if first != "" {
		out.Warnings = append(out.Warnings, fmt.Sprintf("表头取 %s（缺列留空，不丢列）", first))
	}

	dedup := len(opt.DedupKey) > 0
	seen := make(map[string]struct{})
	dupCount := 0
	var rows [][]string

	// ── 逐文件取数 ──
	for _, f := range scan.Files {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if f.Err != "" {
			out.Skipped = append(out.Skipped, SkipInfo{Name: f.Name, Reason: f.Err})
			continue
		}

		fileRows, missing, err := readAligned(ctx, f, columns)
		if err != nil {
			out.Skipped = append(out.Skipped, SkipInfo{Name: f.Name, Reason: err.Error()})
			continue
		}
		if len(missing) > 0 {
			out.Warnings = append(out.Warnings,
				fmt.Sprintf("%s 缺少列 %s（留空处理）", f.Name, strings.Join(missing, "、")))
		}

		out.Files++
		out.RowsIn += len(fileRows)

		keyIdx := make([]int, 0, len(opt.DedupKey))
		if dedup {
			colIdx := columnIndex(columns)
			valid := true
			for _, k := range opt.DedupKey {
				i, ok := colIdx[k]
				if !ok {
					valid = false
					out.Warnings = append(out.Warnings, fmt.Sprintf("去重键 %q 不在表头里，已忽略去重", k))
					break
				}
				keyIdx = append(keyIdx, i)
			}
			if !valid {
				keyIdx = nil
			}
		}

		for _, row := range fileRows {
			if len(keyIdx) > 0 {
				key := rowKey(row, keyIdx)
				if _, dup := seen[key]; dup {
					dupCount++
					continue
				}
				seen[key] = struct{}{}
			}
			rows = append(rows, row)
		}
	}

	out.RowsOut = len(rows)
	out.Duplicates = dupCount
	if opt.DryRun {
		out.ElapsedMS = time.Since(start).Milliseconds()
		return out, nil
	}

	// ── 写出 ──
	outDir := opt.OutputDir
	if outDir == "" {
		outDir = filepath.Join(opt.Dir, "deskkit-out")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return out, fmt.Errorf("创建输出目录失败: %w", err)
	}
	name := strings.TrimSpace(opt.OutputName)
	if name == "" {
		name = "merged-" + time.Now().Format("20060102-150405") + ".xlsx"
	}
	if !strings.HasSuffix(strings.ToLower(name), ".xlsx") {
		name += ".xlsx"
	}
	// outputName 是用户/模型给的，同样要过边界校验：相对名按输出目录解析，
	// ../ 一律拒绝，绝对路径放行；且不许落到任何一个源文件上。
	protect := make([]string, 0, len(scan.Files))
	for _, f := range scan.Files {
		if f.Path != "" {
			protect = append(protect, f.Path)
		}
	}
	path, err := resolveOutputPath(outDir, name, protect...)
	if err != nil {
		return out, err
	}

	// 统一走 WriteTable：数字写成数值（金额列自动千分位两位小数、负数红）、
	// 表头加粗冻结、并附一页「处理说明」。
	// 之前这里自己拼 SetSheetRow，所有单元格都是文本 —— 用户合并完连合计都看不到。
	//
	// ★ 必须用 WriteTable 的返回值：它内部会 uniquePath（同名加 -1），
	// 丢掉返回值就会出现「回显 merged.xlsx，实际写的是 merged-1.xlsx」——
	// 用户第二次合并时打开的是上一次的旧数据。
	written, err := WriteTable(path, &Table{
		Sheet:   "merged",
		Headers: columns,
		Rows:    rows,
		// ★ 带上 warnings：重名列改名（金额-2）、缺列留空这些必须写进「处理说明」页。
		// 只留在工具 JSON 里的话，用户拿到文件看不出「金额-2」是哪来的。
		Warnings: out.Warnings,
	}, WriteOptions{
		Sheet:            "merged",
		BoldHeader:       true,
		AutoWidth:        true,
		NumbersAsNumbers: true,
		Meta: []string{
			fmt.Sprintf("操作：纵向合并 %d 个文件", out.Files),
			fmt.Sprintf("行数：%d → %d（去重 %d 行）", out.RowsIn, out.RowsOut, out.Duplicates),
			"列：" + strings.Join(columns, " / "),
		},
	})
	if err != nil {
		return out, err
	}

	out.OutputPath = written
	out.ElapsedMS = time.Since(start).Milliseconds()
	_ = first
	return out, nil
}

// readAligned 读一个文件的数据行，按目标列顺序对齐。
//
// 返回缺失的列名，供上层汇总成 warning —— 不静默丢列，也不因缺列整份跳过。
func readAligned(ctx context.Context, info SheetInfo, columns []string) ([][]string, []string, error) {
	f, err := openWorkbook(info.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("打开失败: %w", err)
	}
	defer f.Close()

	sheet := info.Sheet
	if sheet == "" {
		sheets := f.GetSheetList()
		if len(sheets) == 0 {
			return nil, nil, fmt.Errorf("没有工作表")
		}
		sheet = sheets[0]
	}

	rows, err := f.Rows(sheet)
	if err != nil {
		return nil, nil, fmt.Errorf("读取行失败: %w", err)
	}
	defer rows.Close()

	var header []string
	srcIdx := map[string]int{}
	var missing []string
	seenHeader := false

	var out [][]string
	// 每 512 行看一次取消信号：以前只在文件之间检查，单个几百万行的文件
	// 一旦开始读就停不下来，用户点取消要等到整个文件读完。
	const cancelCheckEvery = 512
	for rows.Next() {
		if len(out)%cancelCheckEvery == 0 && ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		cells, err := rows.Columns()
		if err != nil {
			return nil, nil, fmt.Errorf("读取行内容失败: %w", err)
		}
		if !seenHeader {
			header = normalizeHeaders(cells)
			srcIdx = columnIndex(header)
			for _, col := range columns {
				if _, ok := srcIdx[col]; !ok {
					missing = append(missing, col)
				}
			}
			seenHeader = true
			continue
		}
		if isBlankRow(cells) {
			continue
		}

		aligned := make([]string, len(columns))
		for i, col := range columns {
			if src, ok := srcIdx[col]; ok && src < len(cells) {
				aligned[i] = strings.TrimSpace(cells[src])
			}
		}
		out = append(out, aligned)
	}
	if err := rows.Error(); err != nil {
		return nil, nil, fmt.Errorf("遍历行失败: %w", err)
	}
	return out, missing, nil
}

// rowKey 生成去重用的行键。
//
// idx 为空**切片**表示「整行比较」（清洗时的默认行为），
// idx 为 nil 表示「不去重」，两者语义不同，调用方用 nil 与 []int{} 区分。
//
// 分隔符用不可见字符：用逗号之类会让 "a,b" + "c" 与 "a" + "b,c" 撞键。
//
// 每个值都过一遍 normalizeKeyValue：不归一的话 1001 / 1001.0 / " 1001" / 001001
// 会被当成四条不同的单据，对账时「重复行查不出来」，合计直接翻倍。
func rowKey(row []string, idx []int) string {
	if len(idx) == 0 {
		parts := make([]string, len(row))
		for i, v := range row {
			parts[i] = normalizeKeyValue(v)
		}
		return strings.Join(parts, "\x1f")
	}
	parts := make([]string, 0, len(idx))
	for _, i := range idx {
		if i < len(row) {
			parts = append(parts, normalizeKeyValue(row[i]))
		}
	}
	return strings.Join(parts, "\x1f")
}
