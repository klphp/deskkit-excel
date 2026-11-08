package excel

// CSV 支持。
//
// 为什么必须做：界面和文案一路写着「Excel / CSV」，但底层只用 excelize，
// 而 excelize 打不开 CSV（实测报 "zip: not a valid zip file"）。
// 国内用户导出的报表、银行流水基本都是 CSV，而且十有八九是 GBK ——
// 于是「说我支持，真用就报底层错误」成了最伤信任的那种问题。
//
// 做法：把 CSV 解码成一个内存里的单表工作簿，再走原有管线。
// 这样 merge/clean/query 一行都不用改，也不会出现「xlsx 一套逻辑、
// csv 另一套逻辑」的分叉。

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/klphp/excel-batch-tool/internal/xtext"
	"github.com/xuri/excelize/v2"
)

// csvSheetName CSV 转成工作簿之后表叫什么。
//
// 用文件名而不是「Sheet1」：用户看到表名和文件名对得上，才知道自己在看哪个文件。
const csvDefaultSheet = "Sheet1"

// IsCSV 是否是 CSV 文件。
func IsCSV(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".csv")
}

// openWorkbook 统一入口：xlsx/xlsm 直接开，CSV 转成内存工作簿。
//
// 所有读取路径都必须走这里 —— 别处直接调 excelize.OpenFile 就等于
// 又把 CSV 排除在外了。
func openWorkbook(path string) (*excelize.File, error) {
	if !IsCSV(path) {
		return excelize.OpenFile(path, excelize.Options{})
	}

	rows, err := readCSVRows(path)
	if err != nil {
		return nil, err
	}

	f := excelize.NewFile()
	sheet := csvSheetName(path)
	if sheet != csvDefaultSheet {
		if err := f.SetSheetName(csvDefaultSheet, sheet); err != nil {
			sheet = csvDefaultSheet
		}
	}
	for i, row := range rows {
		for j, cell := range row {
			cellName, err := excelize.CoordinatesToCellName(j+1, i+1)
			if err != nil {
				continue
			}
			if err := f.SetCellStr(sheet, cellName, cell); err != nil {
				return nil, fmt.Errorf("写入内存工作簿失败: %w", err)
			}
		}
	}
	if _, err := f.NewSheet(sheet); err != nil && !strings.Contains(err.Error(), "exist") {
		// 单表文件用默认表就够了，建表失败不影响读取
		_ = err
	}
	return f, nil
}

// csvSheetName 用文件名（去扩展名）当表名，Excel 表名有长度和字符限制。
func csvSheetName(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	// Excel 表名：最长 31 字符，不能含 : \ / ? * [ ]
	if len([]rune(base)) > 31 {
		base = string([]rune(base)[:31])
	}
	r := strings.NewReplacer(":", "_", "\\", "_", "/", "_", "?", "_", "*", "_", "[", "_", "]", "_")
	base = r.Replace(base)
	if base == "" {
		return csvDefaultSheet
	}
	return base
}

// readCSVRows 读 CSV，自动识别编码与分隔符。
func readCSVRows(path string) ([][]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读不了这个文件: %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("文件是空的")
	}

	text, enc, isText := xtext.Detect(raw)
	if !isText {
		return nil, fmt.Errorf("这不是文本文件，没法按 CSV 读（识别为 %s）", enc)
	}

	// 分隔符按样本行判断：逗号/分号/制表符/竖线都见过
	sample := strings.SplitN(text, "\n", 6)
	delim := ","
	if len(sample) > 1 {
		if d := xtext.GuessDelimiter(sample[:len(sample)-1]); d != "" {
			delim = d
		}
	}

	r := csv.NewReader(strings.NewReader(text))
	r.Comma = []rune(delim)[0]
	r.FieldsPerRecord = -1 // 每行列数不齐是常态，交给调用方处理
	r.LazyQuotes = true    // 引号不规范的导出文件很常见

	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("解析 CSV 失败（编码 %s，分隔符 %q）: %w", enc, delim, err)
	}
	return rows, nil
}

// WriteCSV 把二维表写进 CSV。
//
// 编码默认 UTF-8 带 BOM：Windows 上的 Excel 双击打开不带 BOM 的 UTF-8 CSV
// 会把中文显示成乱码，加 BOM 是最省事的解法（Excel 认，其它工具也认）。
func WriteCSV(path string, rows [][]string) error {
	if !strings.EqualFold(filepath.Ext(path), ".csv") {
		path += ".csv"
	}
	var buf strings.Builder
	buf.WriteString("\uFEFF")

	w := csv.NewWriter(&buf)
	if err := w.WriteAll(rows); err != nil {
		return fmt.Errorf("生成 CSV 失败: %w", err)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return fmt.Errorf("生成 CSV 失败: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("建输出目录失败: %w", err)
	}
	if err := os.WriteFile(path, []byte(buf.String()), 0o644); err != nil {
		return fmt.Errorf("写文件失败: %w", err)
	}
	return nil
}
