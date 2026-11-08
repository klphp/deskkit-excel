// Package excel Excel 表格批处理。
//
// 只依赖 excelize，不碰 UI 也不碰 AI：这里的函数既能被界面按钮调用，
// 也能被 AI 的工具循环调用 —— 两条路走同一份实现，行为才不会分叉。
//
// 三条纪律：
//
//	① 绝不写原文件。所有输出落到独立的输出目录，用户自己决定要不要替换。
//	② 大目录要设上限，并且明确告诉用户被截断了（而不是悄悄只处理一部分）。
//	③ 表头按列名对齐，不按列序 —— 这是「格式不一的报表」最常见的实际情况。
package excel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xuri/excelize/v2"
)

const (
	// maxFiles 单次扫描上限。再大就不该用桌面应用同步处理了。
	maxFiles = 2000
	// maxScanCells 扫描时单个文件最多看多少行，避免一个畸形大文件拖死界面。
	maxPreviewRows = 5
)

// supported 支持的扩展名。
//
// .xls 是 Excel 97-2003（OLE2 结构），excelize 打不开，但银行、老 ERP、
// 税务系统导出的流水大量还是它 —— 而它往往正是用户最想处理的那个文件。
// 所以单独用 extrame/xls 读，读到内存工作簿再接同一条管线。
var supported = map[string]bool{
	".xlsx": true,
	".xlsm": true,
	".xls":  true,
	".csv":  true,
}

// SheetInfo 一个表格文件的扫描结果。
type SheetInfo struct {
	Path    string   `json:"path"`
	Name    string   `json:"name"`
	Sheet   string   `json:"sheet"`
	Headers []string `json:"headers"`
	Rows    int      `json:"rows"`
	Size    int64    `json:"size"`
	// Warnings 读这个文件时自动修正过什么（补表头 / 重名列改名）。
	Warnings []string `json:"warnings,omitempty"`
	// Err 非空表示这个文件读不了（加密、损坏、被占用），不影响其它文件。
	Err string `json:"error,omitempty"`
}

// ScanResult 一次扫描的汇总。
type ScanResult struct {
	Dir       string      `json:"dir"`
	Files     []SheetInfo `json:"files"`
	TotalRows int         `json:"totalRows"`
	TotalSize int64       `json:"totalSize"`
	// Ignored 非表格文件数量（图片、压缩包之类）。
	Ignored int `json:"ignored"`
	// Unsupported 是认识的扩展名但读不了（如 .xls）。
	Unsupported []string `json:"unsupported,omitempty"`
	// Truncated 表示文件数超过上限，只处理了前面一部分。
	Truncated bool `json:"truncated"`
}

// Scan 扫描目录下的表格文件，读出每个文件的表头与数据行数。
func Scan(ctx context.Context, dir string) (ScanResult, error) {
	out := ScanResult{Dir: dir}
	if strings.TrimSpace(dir) == "" {
		return out, fmt.Errorf("目录为空")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return out, fmt.Errorf("打不开目录: %w", err)
	}
	if !info.IsDir() {
		return out, fmt.Errorf("不是目录: %s", dir)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return out, fmt.Errorf("读取目录失败: %w", err)
	}

	for _, entry := range entries {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, "~$") {
			continue // Excel 打开文件时留下的锁文件
		}
		ext := strings.ToLower(filepath.Ext(name))
		if !supported[ext] {
			if ext == ".xls" {
				out.Unsupported = append(out.Unsupported, name)
			} else {
				out.Ignored++
			}
			continue
		}
		if len(out.Files) >= maxFiles {
			out.Truncated = true
			break
		}

		path := filepath.Join(dir, name)
		fi, statErr := entry.Info()
		var size int64
		if statErr == nil {
			size = fi.Size()
		}

		sheet, sheetName, headers, rows, warns, readErr := inspect(path)
		item := SheetInfo{Path: path, Name: name, Sheet: sheetName, Headers: headers, Rows: rows, Size: size, Warnings: warns}
		if readErr != nil {
			item.Err = readErr.Error()
		} else {
			out.TotalRows += rows
			out.TotalSize += size
		}
		_ = sheet
		out.Files = append(out.Files, item)
	}

	// 按文件名排序：目录顺序取决于文件系统，用户看到的列表要稳定
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Name < out.Files[j].Name })
	return out, nil
}

// inspect 打开一个文件，返回第一个工作表的表头与数据行数。
//
// 用 Rows 迭代器而不是 GetRows：后者一次把整表读进内存，
// 碰上几十万行的导出表会直接把内存吃掉；迭代器是流式的。
func inspect(path string) (*excelize.File, string, []string, int, []string, error) {
	f, err := openWorkbook(path)
	if err != nil {
		return nil, "", nil, 0, nil, fmt.Errorf("打开失败: %w", err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return f, "", nil, 0, nil, fmt.Errorf("没有工作表")
	}
	sheet := sheets[0]

	rows, err := f.Rows(sheet)
	if err != nil {
		return f, sheet, nil, 0, nil, fmt.Errorf("读取行失败: %w", err)
	}
	defer rows.Close()

	var headers, warns []string
	dataRows := 0
	for rows.Next() {
		row, err := rows.Columns()
		if err != nil {
			return f, sheet, headers, dataRows, warns, fmt.Errorf("读取行内容失败: %w", err)
		}
		if headers == nil {
			// 第一行当表头；全空的第一行说明表不是从 A1 开始的，也照样用它
			headers, warns = normalizeHeadersWarn(row)
			continue
		}
		if isBlankRow(row) {
			continue
		}
		dataRows++
	}
	return f, sheet, headers, dataRows, warns, nil
}

// normalizeHeaders 去掉尾部空列、去空白、给无名列起名、重名列加序号。
//
// 尾部空列必须去掉：Excel 里「曾经写过又删掉」的单元格会留下空表头，
// 不去掉会让后面按列名对齐时凭空多出无名的列。
//
// 但**中间**的空表头不能丢也不能空着 —— 那是「表头漏写一格」或合并单元格留下的，
// 裁掉就等于整列数据消失。
func normalizeHeaders(row []string) []string {
	h, _ := normalizeHeadersWarn(row)
	return h
}

// normalizeHeadersWarn 同 normalizeHeaders，并把「补名 / 改名」的说明交给调用方汇总成 warnings。
func normalizeHeadersWarn(row []string) ([]string, []string) {
	out := make([]string, len(row))
	for i, cell := range row {
		out[i] = strings.TrimSpace(cell)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}

	var warns []string
	for i, h := range out {
		if h == "" {
			out[i] = fmt.Sprintf("列%d", i+1)
			warns = append(warns, fmt.Sprintf("第 %d 列没有表头，已命名为「%s」", i+1, out[i]))
		}
	}

	// 重名列改名：两列都叫「金额」时，按列名对齐只认第一列，
	// 第二列整列数据会静默消失（合并 20 份报表少一块合计就是这么来的）。
	seen := make(map[string]bool, len(out))
	for i, h := range out {
		if !seen[h] {
			seen[h] = true
			continue
		}
		base := h
		for n := 2; ; n++ {
			cand := fmt.Sprintf("%s-%d", base, n)
			if seen[cand] {
				continue
			}
			out[i] = cand
			seen[cand] = true
			warns = append(warns, fmt.Sprintf("列名「%s」重复，第 %d 列已改名为「%s」", base, i+1, cand))
			break
		}
	}
	return out, warns
}

func isBlankRow(row []string) bool {
	for _, cell := range row {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}

// columnIndex 把表头映射成「列名 → 列号」。
func columnIndex(headers []string) map[string]int {
	idx := make(map[string]int, len(headers))
	for i, h := range headers {
		if h == "" {
			continue
		}
		if _, dup := idx[h]; dup {
			continue // 重名列表头只认第一个，避免互相覆盖
		}
		idx[h] = i
	}
	return idx
}
