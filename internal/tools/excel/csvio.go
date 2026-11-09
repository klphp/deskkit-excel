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

	"github.com/jfyne/csvd"
	"github.com/klphp/fsx/xtext"
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

// openWorkbook 统一入口：xlsx/xlsm 直接开，CSV 与 .xls 转成内存工作簿。
//
// 所有读取路径都必须走这里 —— 别处直接调 excelize.OpenFile 就等于又把
// CSV 和 .xls 排除在外了。
func openWorkbook(path string) (*excelize.File, error) {
	switch {
	case IsCSV(path):
		rows, err := readCSVRows(path)
		if err != nil {
			return nil, err
		}
		return buildWorkbook([]sheetData{{Name: csvSheetName(path), Rows: rows}})
	case IsXLS(path):
		sheets, err := readXLS(path)
		if err != nil {
			return nil, err
		}
		return buildWorkbook(sheets)
	default:
		return excelize.OpenFile(path, excelize.Options{})
	}
}

// sheetData 一张待写入内存工作簿的表。
type sheetData struct {
	Name string
	Rows [][]string
}

// buildWorkbook 把若干张二维表装成 excelize 工作簿。
//
// CSV 和 .xls 都靠它接到现有管线上 —— 下游（合并/清洗/查询/画像）完全不知道
// 原始格式是什么，这件事只在这里发生一次。
func buildWorkbook(sheets []sheetData) (*excelize.File, error) {
	f := excelize.NewFile()
	defaultSheet := f.GetSheetName(0)

	for i, sd := range sheets {
		name := sanitizeSheetName(sd.Name)
		if i == 0 {
			if name != defaultSheet {
				if err := f.SetSheetName(defaultSheet, name); err != nil {
					name = defaultSheet
				}
			}
		} else {
			name = uniqueSheetName(f, name)
			if _, err := f.NewSheet(name); err != nil {
				return nil, fmt.Errorf("建工作表失败: %w", err)
			}
		}
		for ri, row := range sd.Rows {
			for ci, cell := range row {
				if cell == "" {
					continue
				}
				cellName, err := excelize.CoordinatesToCellName(ci+1, ri+1)
				if err != nil {
					continue
				}
				if err := f.SetCellStr(name, cellName, cell); err != nil {
					return nil, fmt.Errorf("写入内存工作簿失败: %w", err)
				}
			}
		}
	}
	return f, nil
}

// sanitizeSheetName Excel 表名限制：31 字符，不能含 : \ / ? * [ ]。
func sanitizeSheetName(name string) string {
	name = strings.TrimSpace(name)
	r := strings.NewReplacer(":", "_", "\\", "_", "/", "_", "?", "_", "*", "_", "[", "_", "]", "_")
	name = r.Replace(name)
	if runes := []rune(name); len(runes) > 31 {
		name = string(runes[:31])
	}
	if name == "" {
		return "Sheet1"
	}
	return name
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
	rows, _, err := readCSVWithDelim(path)
	return rows, err
}

// readCSVWithDelim 同 readCSVRows，另外把识别到的分隔符带出来
// （fs_peek 要给模型看「这是分号分隔的」，不能只说「读到了」）。
func readCSVWithDelim(path string) ([][]string, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("读不了这个文件: %w", err)
	}
	if len(raw) == 0 {
		return nil, "", fmt.Errorf("文件是空的")
	}

	text, enc, isText := xtext.Detect(raw)
	if !isText {
		return nil, "", fmt.Errorf("这不是文本文件，没法按 CSV 读（识别为 %s）", enc)
	}

	// 分隔符交给 csvd：它按「切出来的列数分布」判断，比数逗号出现次数可靠 ——
	// 我们的老实现遇到「备注列里本身有逗号」就会判错。
	r := csvd.NewReader(strings.NewReader(text))
	r.FieldsPerRecord = -1 // 每行列数不齐是常态，交给调用方处理
	r.LazyQuotes = true    // 引号不规范的导出文件很常见

	rows, err := r.ReadAll()
	if err != nil {
		return nil, "", fmt.Errorf("解析 CSV 失败（编码 %s，分隔符 %q）: %w", enc, string(r.Comma), err)
	}
	return rows, string(r.Comma), nil
}

// writeCSVGBK 写 GBK 编码的 CSV（不带 BOM，GBK 本身不需要）。
//
// 为什么要有：老系统（尤其国内的 ERP、财务软件）导入时只认 GBK，
// 给它 UTF-8 就是满屏乱码，而用户在那边根本改不了设置。
func writeCSVGBK(path string, rows [][]string) error {
	var buf strings.Builder
	w := csv.NewWriter(&buf)
	if err := w.WriteAll(rows); err != nil {
		return fmt.Errorf("生成 CSV 失败: %w", err)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return fmt.Errorf("生成 CSV 失败: %w", err)
	}
	encoded, err := xtext.UTF8ToGB18030(buf.String())
	if err != nil {
		return fmt.Errorf("转成 GBK 失败: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("建输出目录失败: %w", err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		return fmt.Errorf("写文件失败: %w", err)
	}
	return nil
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
