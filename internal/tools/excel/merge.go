package excel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
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

		fileRows, missing, err := readAligned(f, columns)
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
	path := filepath.Join(outDir, name)

	f := excelize.NewFile()
	defer f.Close()
	sheet := "merged"
	defaultSheet := f.GetSheetName(0)
	if err := f.SetSheetName(defaultSheet, sheet); err != nil {
		return out, fmt.Errorf("设置工作表名失败: %w", err)
	}

	writeRow := func(idx int, values []string) error {
		cell, err := excelize.CoordinatesToCellName(1, idx)
		if err != nil {
			return err
		}
		row := make([]any, len(values))
		for i, v := range values {
			row[i] = v
		}
		return f.SetSheetRow(sheet, cell, &row)
	}

	if err := writeRow(1, columns); err != nil {
		return out, fmt.Errorf("写表头失败: %w", err)
	}
	for i, row := range rows {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if err := writeRow(i+2, row); err != nil {
			return out, fmt.Errorf("写第 %d 行失败: %w", i+2, err)
		}
	}

	// 表头加粗 + 冻结首行：合并表动辄几千行，没有冻结很难用
	style, styleErr := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	if styleErr == nil {
		lastCell, _ := excelize.CoordinatesToCellName(len(columns), 1)
		_ = f.SetCellStyle(sheet, "A1", lastCell, style)
	}
	_ = f.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})

	if err := f.SaveAs(path); err != nil {
		return out, fmt.Errorf("保存失败: %w", err)
	}

	out.OutputPath = path
	out.ElapsedMS = time.Since(start).Milliseconds()
	_ = first
	return out, nil
}

// readAligned 读一个文件的数据行，按目标列顺序对齐。
//
// 返回缺失的列名，供上层汇总成 warning —— 不静默丢列，也不因缺列整份跳过。
func readAligned(info SheetInfo, columns []string) ([][]string, []string, error) {
	f, err := excelize.OpenFile(info.Path, excelize.Options{})
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
	for rows.Next() {
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
func rowKey(row []string, idx []int) string {
	if len(idx) == 0 {
		return strings.Join(row, "\x1f")
	}
	parts := make([]string, 0, len(idx))
	for _, i := range idx {
		if i < len(row) {
			parts = append(parts, row[i])
		}
	}
	return strings.Join(parts, "\x1f")
}
