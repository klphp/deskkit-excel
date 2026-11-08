package excel

// 拆分与多表输出。
//
// 两个方向都是日常刚需：
//   · 拆：一张总表按部门/月份拆成多个文件，发下去给各自核对
//   · 并：一个文件里几十个 sheet，合成一张总表或拆成多个文件

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SplitOutput 一个拆出来的文件。
type SplitOutput struct {
	Key  string `json:"key"`
	Path string `json:"path"`
	Rows int    `json:"rows"`
}

// SplitByColumn 按某列的值把表拆成多个文件（每个值一个文件）。
func SplitByColumn(t *Table, col, outDir, ext string, maxFiles int) ([]SplitOutput, error) {
	ci, err := t.Col(col)
	if err != nil {
		return nil, err
	}
	if outDir == "" {
		outDir = filepath.Join(filepath.Dir(t.Path), "deskkit-out")
	}
	if ext == "" {
		ext = ".xlsx"
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("建输出目录失败: %w", err)
	}

	order := []string{}
	groups := map[string][][]string{}
	for _, r := range t.Rows {
		k := strings.TrimSpace(cellAt(r, ci))
		if k == "" {
			k = "(空)"
		}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], r)
	}
	if maxFiles > 0 && len(order) > maxFiles {
		return nil, fmt.Errorf("这一列有 %d 个不同值，会被拆成 %d 个文件；超过上限 %d。"+
			"确认要拆的话请调大 maxFiles，或者先筛选出需要的值", len(order), len(order), maxFiles)
	}

	var out []SplitOutput
	for _, k := range order {
		name := sanitizeFileName(k)
		if name == "" {
			name = "sheet"
		}
		path := uniquePath(filepath.Join(outDir, name+ext))
		sub := &Table{Path: t.Path, Sheet: t.Sheet, Headers: append([]string(nil), t.Headers...), Rows: groups[k]}
		if _, err := WriteTable(path, sub, WriteOptions{BoldHeader: true, AutoWidth: true, NumbersAsNumbers: true}); err != nil {
			return nil, err
		}
		out = append(out, SplitOutput{Key: k, Path: path, Rows: len(groups[k])})
	}
	return out, nil
}

// SplitSheets 把一个文件里的每个工作表写成独立文件。
func SplitSheets(path, outDir string) ([]SplitOutput, error) {
	names, err := SheetNames(path)
	if err != nil {
		return nil, fmt.Errorf("打不开 %s: %w", filepath.Base(path), err)
	}
	if outDir == "" {
		outDir = filepath.Join(filepath.Dir(path), "deskkit-out")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("建输出目录失败: %w", err)
	}

	var out []SplitOutput
	for _, name := range names {
		t, err := LoadTable(path, name)
		if err != nil {
			return nil, err
		}
		p := uniquePath(filepath.Join(outDir, sanitizeFileName(name)+".xlsx"))
		if _, err := WriteTable(p, t, WriteOptions{Sheet: name, BoldHeader: true, AutoWidth: true, NumbersAsNumbers: true}); err != nil {
			return nil, err
		}
		out = append(out, SplitOutput{Key: name, Path: p, Rows: len(t.Rows)})
	}
	return out, nil
}

// ReadAllSheets 读文件里所有工作表（结构不同的表也读，用于「一张总表」）。
func ReadAllSheets(path string) ([]*Table, error) {
	names, err := SheetNames(path)
	if err != nil {
		return nil, err
	}
	var out []*Table
	for _, name := range names {
		t, err := LoadTable(path, name)
		if err != nil {
			// 空的 sheet 很常见（残留的 Sheet2），跳过而不是让整个操作失败
			continue
		}
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s 里没有可读的工作表", filepath.Base(path))
	}
	return out, nil
}

// StackSheets 把多个工作表纵向堆成一张（列名取并集）。
func StackSheets(tables []*Table) *Table {
	out := &Table{}
	for _, t := range tables {
		for _, h := range t.Headers {
			if !contains(out.Headers, h) {
				out.Headers = append(out.Headers, h)
			}
		}
	}
	for _, t := range tables {
		for _, r := range t.Rows {
			row := make([]string, len(out.Headers))
			for i, h := range out.Headers {
				if j, err := t.Col(h); err == nil {
					row[i] = cellAt(r, j)
				}
			}
			out.Rows = append(out.Rows, row)
		}
	}
	return out
}

// WriteMultiSheet 把多张表写进同一个文件的多个工作表（带来源列标注）。
func WriteMultiSheet(path string, tables []*Table, opt WriteOptions) (string, error) {
	path = uniquePath(path)
	f, err := newExcelFile()
	if err != nil {
		return "", err
	}
	defer f.Close()

	for i, t := range tables {
		name := sanitizeFileName(t.Sheet)
		if name == "" {
			name = fmt.Sprintf("表%d", i+1)
		}
		name = uniqueSheetName(f, name)
		if i == 0 {
			if err := f.SetSheetName(f.GetSheetName(0), name); err != nil {
				return "", err
			}
		} else {
			if _, err := f.NewSheet(name); err != nil {
				return "", err
			}
		}
		if _, err := putTableInto(f, name, t, opt); err != nil {
			return "", err
		}
	}
	if err := f.SaveAs(path); err != nil {
		return "", fmt.Errorf("写文件失败: %w", err)
	}
	return path, nil
}

// sanitizeFileName 去掉文件名里不能用的字符。
func sanitizeFileName(s string) string {
	s = strings.TrimSpace(s)
	bad := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|", "\n", "\r", "\t"}
	for _, b := range bad {
		s = strings.ReplaceAll(s, b, "_")
	}
	s = strings.Trim(s, " .")
	if len([]rune(s)) > 60 {
		s = string([]rune(s)[:60])
	}
	return s
}
