package excel

// 格式转换：xlsx ↔ csv，以及「把某个 sheet 单独导出」。
//
// 为什么值得单独做一个工具：用户最常见的两个诉求是
//   · 「给我一个 csv，我要导进别的系统」
//   · 「把这个 sheet 单独存一个文件发出去」
// 用清洗或合并去凑都能做到，但模型要走很多步才敢下手。

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ConvertOptions 转换选项。
type ConvertOptions struct {
	Path       string `json:"path"`
	Sheet      string `json:"sheet,omitempty"`
	OutputPath string `json:"outputPath"`
	// Encoding 仅 CSV 输出有效：utf8（默认，带 BOM）或 gbk（老系统只认这个）。
	Encoding string `json:"encoding,omitempty"`
}

// Convert 把文件转成另一种格式（按 outputPath 的扩展名决定）。
func Convert(opt ConvertOptions) (string, int, error) {
	if strings.TrimSpace(opt.OutputPath) == "" {
		return "", 0, fmt.Errorf("要给输出文件路径")
	}
	// outputPath 可能是用户随手给的相对路径：相对源文件所在目录解析，
	// 且不许用 ../ 跳出该目录（绝对路径照常放行）。
	path, err := resolveOutputPath(filepath.Dir(opt.Path), opt.OutputPath, opt.Path)
	if err != nil {
		return "", 0, err
	}
	t, err := LoadTable(opt.Path, opt.Sheet)
	if err != nil {
		return "", 0, err
	}
	path, err = uniquePath(path)
	if err != nil {
		return "", 0, err
	}

	if IsCSV(path) {
		table := make([][]string, 0, len(t.Rows)+1)
		table = append(table, t.Headers)
		table = append(table, t.Rows...)
		if strings.EqualFold(strings.TrimSpace(opt.Encoding), "gbk") {
			if err := writeCSVGBK(path, table); err != nil {
				return "", 0, err
			}
		} else if err := WriteCSV(path, table); err != nil {
			return "", 0, err
		}
		return path, len(t.Rows), nil
	}

	written, err := WriteTable(path, t, WriteOptions{BoldHeader: true, AutoWidth: true, NumbersAsNumbers: true})
	if err != nil {
		return "", 0, err
	}
	return written, len(t.Rows), nil
}

// SuggestName 在源文件旁边给一个不重名的默认输出名。
func SuggestName(srcPath, suffix, ext string) string {
	base := strings.TrimSuffix(filepath.Base(srcPath), filepath.Ext(srcPath))
	return filepath.Join(filepath.Dir(srcPath), base+suffix+ext)
}
