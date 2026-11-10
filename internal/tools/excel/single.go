package excel

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

// ─────────────────────────────────────────────────────────────
// 单文件版（第一版不做批量）
//
// 用户先选一个文件，再说要干什么；结果写回**同一个目录**，
// 文件名是「原名-时间戳.xlsx」。三条理由：
//   · 原文件绝不动 —— 表格是用户的原始资产
//   · 同名 + 时间戳 —— 用户自己就能看出「这是刚才那次处理的产物」，
//     不需要记住我们起的名字（deskkit-out 那种目录他下次就找不到了）
//   · 不覆盖上一次的结果 —— 同一天试第二遍，两份都在，好对比
// ─────────────────────────────────────────────────────────────

// SingleInfo 一个文件的结构描述，给模型看的。
type SingleInfo struct {
	Path    string      `json:"path"`
	File    string      `json:"file"`
	Tables  []TableInfo `json:"tables"`
	Note    string      `json:"note,omitempty"`
	Output  string      `json:"outputHint"`
	Elapsed int64       `json:"elapsedMs"`
}

// LoadWorkbookFile 把单个工作簿装进内存 SQLite。
//
// 和 LoadCatalog 的区别：那个扫整个目录，这个只认一个文件 ——
// 第一版明确不做批量，目录里其它文件不该被碰到。
func LoadWorkbookFile(ctx context.Context, path string) (*sql.DB, SingleInfo, error) {
	info := SingleInfo{Path: path, File: filepath.Base(path)}

	stat, err := os.Stat(path)
	if err != nil {
		return nil, info, fmt.Errorf("File not found: %w", err)
	}
	if stat.IsDir() {
		return nil, info, fmt.Errorf("%s is a folder; choose a file instead", path)
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".xlsx", ".xlsm":
	default:
		return nil, info, fmt.Errorf("Only .xlsx / .xlsm are supported (save .xls as .xlsx first)")
	}

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, info, fmt.Errorf("Failed to open the in-memory database: %w", err)
	}
	db.SetMaxOpenConns(1)

	tables, err := loadWorkbook(ctx, db, path, filepath.Base(path))
	if err != nil {
		db.Close()
		return nil, info, err
	}
	if len(tables) == 0 {
		db.Close()
		info.Note = "This workbook has no readable data (the sheet is empty, or the first row is not a header)"
		return nil, info, fmt.Errorf("%s", info.Note)
	}
	info.Tables = tables
	info.Note = noteWarnings(info.Note, tables)
	info.Output = SuggestOutputPath(path, "")
	return db, info, nil
}

// SuggestOutputPath 生成输出路径：同目录、同名、带时间戳。
//
// suffix 为空时用时间戳；准备阶段要预览真实文件名时可以传死值。
func SuggestOutputPath(src, suffix string) string {
	if suffix == "" {
		suffix = time.Now().Format("20060102-150405")
	}
	dir := filepath.Dir(src)
	stem := strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	return filepath.Join(dir, fmt.Sprintf("%s-%s.xlsx", stem, suffix))
}

// WriteRowsAlong 把结果写到源文件旁边，返回实际路径与行数。
//
// ★ 落盘前必走 uniquePath：时间戳只到秒，用户连点两次「执行」或模型重试一次，
// 第二次就会覆盖第一次的结果（两次返回同一路径），结果既不可复现也不可追溯。
func WriteRowsAlong(src, suffix string, columns []string, rows [][]string) (string, error) {
	path, err := uniquePath(SuggestOutputPath(src, suffix))
	if err != nil {
		return "", err
	}

	f := excelize.NewFile()
	defer f.Close()
	sheet := f.GetSheetName(0)

	put := func(idx int, values []string) error {
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

	if err := put(1, columns); err != nil {
		return "", err
	}
	for i, r := range rows {
		if err := put(i+2, r); err != nil {
			return "", err
		}
	}

	if style, err := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"F2F2F2"}},
	}); err == nil && len(columns) > 0 {
		last, _ := excelize.CoordinatesToCellName(len(columns), 1)
		_ = f.SetCellStyle(sheet, "A1", last, style)
	}
	for i, c := range columns {
		width := len([]rune(c)) + 4
		for _, r := range rows {
			if i < len(r) {
				if n := len([]rune(r[i])) + 2; n > width {
					width = n
				}
			}
		}
		if width > 40 {
			width = 40
		}
		col, _ := excelize.ColumnNumberToName(i + 1)
		_ = f.SetColWidth(sheet, col, col, float64(width))
	}

	if err := f.SaveAs(path); err != nil {
		return "", fmt.Errorf("Failed to save: %w", err)
	}
	return path, nil
}

// QueryToFile 查询并把结果写到源文件旁边。
//
// 这是第一版的主力动作：用户说「按部门汇总」，模型写一条 SQL，
// 结果落在原目录同名带时间戳的新文件里。
func QueryToFile(ctx context.Context, db *sql.DB, src, query, suffix string) (string, int, error) {
	res, err := Query(ctx, db, query, 1_000_000)
	if err != nil {
		return "", 0, err
	}
	if len(res.Columns) == 0 {
		return "", 0, fmt.Errorf("The query returned no columns")
	}
	path, err := WriteRowsAlong(src, suffix, res.Columns, res.Rows)
	if err != nil {
		return "", 0, err
	}
	return path, res.Count, nil
}

// CleanFileOptions 单文件清洗参数。
type CleanFileOptions struct {
	Path           string
	DropBlankRows  bool
	DropDuplicates bool
	DedupKey       []string
	TrimSpaces     bool
	NormalizeDates bool
	NumbersAsText  bool
	DryRun         bool
}

// CleanFileResult 单文件清洗结果。
type CleanFileResult struct {
	File      string   `json:"file"`
	RowsIn    int      `json:"rowsIn"`
	RowsOut   int      `json:"rowsOut"`
	BlankRows int      `json:"blankRows"`
	Duplicate int      `json:"duplicates"`
	Trimmed   int      `json:"trimmedCells"`
	DateFixed int      `json:"dateFixed"`
	NumFixed  int      `json:"numberFixed"`
	Output    string   `json:"output,omitempty"`
	DryRun    bool     `json:"dryRun"`
	Warnings  []string `json:"warnings,omitempty"`
}

// CleanFile 清洗单个文件，结果写到原文件旁边。
//
// 只处理第一张工作表：单文件场景下用户指的就是「这张表」，
// 把工作簿里所有 sheet 都洗一遍会超出他的预期。
func CleanFile(ctx context.Context, opt CleanFileOptions) (CleanFileResult, error) {
	res := CleanFileResult{File: filepath.Base(opt.Path), DryRun: opt.DryRun}

	f, err := openWorkbook(opt.Path)
	if err != nil {
		return res, fmt.Errorf("Cannot open the file: %w", err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return res, fmt.Errorf("This file has no sheets")
	}
	sheet := sheets[0]
	if len(sheets) > 1 {
		res.Warnings = append(res.Warnings,
			fmt.Sprintf("The file has %d sheets; only the first one (%s) was processed", len(sheets), sheet))
	}

	rows, err := f.GetRows(sheet)
	if err != nil || len(rows) == 0 {
		return res, fmt.Errorf("No data could be read")
	}

	headers := rows[0]
	body := rows[1:]
	res.RowsIn = len(body)

	// ★ 表头规范化 + 按最大宽度补齐，绝不按表头宽度截断数据行。
	// 以前 cells := make([]string, len(headers)) 直接拷贝：表头漏写一格
	// （或 CSV 末尾多一个分隔符）那一列数据就整列消失，还不报错。
	var headerWarns []string
	headers, headerWarns = normalizeHeadersWarn(headers)
	res.Warnings = append(res.Warnings, headerWarns...)
	grow := &Table{Headers: headers}
	width := len(headers)
	for _, r := range body {
		if len(r) > width {
			width = len(r)
		}
	}
	grow.extendHeaders(width)
	res.Warnings = append(res.Warnings, grow.Warnings...)
	headers = grow.Headers

	// 去重键的列序号
	var keyIdx []int
	if opt.DropDuplicates {
		if len(opt.DedupKey) == 0 {
			keyIdx = []int{} // 空切片 = 整行比较
		} else {
			for _, k := range opt.DedupKey {
				found := -1
				for i, h := range headers {
					if strings.TrimSpace(h) == strings.TrimSpace(k) {
						found = i
						break
					}
				}
				if found < 0 {
					res.Warnings = append(res.Warnings, fmt.Sprintf("There is no column named %s; this dedupe key is ignored", k))
					continue
				}
				keyIdx = append(keyIdx, found)
			}
			if len(keyIdx) == 0 {
				res.Warnings = append(res.Warnings, "None of the dedupe keys exist; dedupe falls back to whole rows")
				keyIdx = []int{}
			}
		}
	}

	var out [][]string
	seen := map[string]bool{}
	for _, row := range body {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		cells := make([]string, len(headers))
		copy(cells, row)
		blank := true
		for i := range cells {
			if opt.TrimSpaces {
				trimmed := strings.TrimSpace(cells[i])
				if trimmed != cells[i] {
					res.Trimmed++
					cells[i] = trimmed
				}
			}
			if strings.TrimSpace(cells[i]) != "" {
				blank = false
			}
		}
		if blank && opt.DropBlankRows {
			res.BlankRows++
			continue
		}
		if opt.NormalizeDates {
			for i := range cells {
				if got, ok := normalizeDate(cells[i], "2006-01-02"); ok && got != cells[i] {
					cells[i] = got
					res.DateFixed++
				}
			}
		}
		if opt.NumbersAsText {
			for i := range cells {
				if cells[i] == "" || leadingZero.MatchString(cells[i]) {
					continue
				}
				if got, ok := numericValue(cells[i]); ok && got != cells[i] {
					cells[i] = got
					res.NumFixed++
				}
			}
		}
		if opt.DropDuplicates {
			key := rowKey(cells, keyIdx)
			if seen[key] {
				res.Duplicate++
				continue
			}
			seen[key] = true
		}
		out = append(out, cells)
	}
	res.RowsOut = len(out)

	if opt.DryRun {
		return res, nil
	}

	path, err := WriteRowsAlong(opt.Path, "", headers, out)
	if err != nil {
		return res, err
	}
	res.Output = path
	return res, nil
}

// sanitizeSuffix 清掉文件名里不能用的字符。
//
// 模型可能写出「部门/汇总」这种带斜杠的后缀，直接拼进路径就成了另一层目录。
func sanitizeSuffix(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	repl := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "*", "", "?", "", "\"", "", "<", "", ">", "", "|", "-")
	s = repl.Replace(s)
	if r := []rune(s); len(r) > 24 {
		s = string(r[:24])
	}
	return s
}
