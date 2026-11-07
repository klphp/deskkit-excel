package excel

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/xuri/excelize/v2"
)

// SampleInfo 示例数据集的描述。
type SampleInfo struct {
	Dir    string   `json:"dir"`
	Files  []string `json:"files"`
	Note   string   `json:"note"`
	Reused bool     `json:"reused"`
}

// EnsureSample 生成（或复用）一份示例数据。
//
// 为什么产品里要内置示例数据：用户第一次打开时手上没有任何表格，
// 界面上所有按钮都是灰的 —— 他不知道这东西到底能干什么。
// 一键生成三份「脏报表」，他立刻能点合并、点清洗，看到真实输出。
//
// 数据是刻意弄脏的：表头不一致、有多余空格、有空行、有重复行、
// 日期三种写法、金额带货币符号与千分位。这样合并与清洗都有可见效果。
func EnsureSample(baseDir string) (SampleInfo, error) {
	dir := filepath.Join(baseDir, "sample")
	info := SampleInfo{Dir: dir}

	// 已经生成过就直接复用，别每次点都覆盖（用户可能已经改过里面的数据）
	if entries, err := os.ReadDir(dir); err == nil {
		name := ""
		for _, e := range entries {
			if !e.IsDir() && filepath.Ext(e.Name()) == ".xlsx" {
				name = e.Name()
				break
			}
		}
		if name != "" {
			for _, e := range entries {
				if !e.IsDir() && filepath.Ext(e.Name()) == ".xlsx" {
					info.Files = append(info.Files, e.Name())
				}
			}
			info.Reused = true
			info.Note = "示例数据已存在，直接使用"
			return info, nil
		}
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return info, fmt.Errorf("创建示例目录失败: %w", err)
	}

	type sheet struct {
		name    string
		headers []string
		rows    [][]string
	}
	sheets := []sheet{
		{
			// 标准表：日期/部门/金额/工号，但带空格与货币符号
			name:    "销售部-10月.xlsx",
			headers: []string{"日期", "部门", "金额", "工号"},
			rows: [][]string{
				{"2026/1/2", "  销售  ", "¥1,234.50", "00123"},
				{"2026年3月5日", "销售", "980", "00123"},
				{"", "", "", ""}, // 空行：清洗会删掉
			},
		},
		{
			// 列序不同 + 多一列：合并按列名对齐，清洗按列处理
			name:    "技术部-10月.xlsx",
			headers: []string{"金额", "部门", "日期", "工号", "备注"},
			rows: [][]string{
				{"￥2,000", " 技术", "2026.03.05", "E45", "加班"},
				{"1500", "技术", "2026-03-06", "E46", ""},
			},
		},
		{
			// 缺列 + 重复行
			name:    "运营部-10月.xlsx",
			headers: []string{"部门", "日期", "工号"},
			rows: [][]string{
				{"运营", "2026/3/5", "O07"},
				{"运营", "2026/3/5", "O07"}, // 完全重复：清洗会删掉
				{"运营", "2026/3/7", "O08"},
			},
		},
	}

	for _, s := range sheets {
		if err := writeSample(filepath.Join(dir, s.name), s.headers, s.rows); err != nil {
			return info, err
		}
		info.Files = append(info.Files, s.name)
	}
	info.Note = "已生成 3 份示例报表（表头不一致、有空行与重复行、日期三种写法）"
	return info, nil
}

func writeSample(path string, headers []string, rows [][]string) error {
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

	if err := put(1, headers); err != nil {
		return err
	}
	for i, r := range rows {
		if err := put(i+2, r); err != nil {
			return err
		}
	}

	// 表头加粗，让示例看起来像真的报表
	if style, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}}); err == nil && len(headers) > 0 {
		last, _ := excelize.CoordinatesToCellName(len(headers), 1)
		_ = f.SetCellStyle(sheet, "A1", last, style)
	}
	return f.SaveAs(path)
}
