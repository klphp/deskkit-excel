package excel

// .xls（Excel 97-2003）读取。
//
// 为什么必须有：银行流水、老 ERP、税务系统导出的一大批还是 .xls。
// excelize 打不开它（那是 OLE2 复合文档，不是 zip），所以以前只能跳过并告知。
// 用户把流水丢进来得到「不支持」，第一印象就没了 —— 而这个文件恰好是他
// 最想处理的那个。AI 也没法帮忙：它看不见二进制结构。
//
// 用 github.com/extrame/xls 读，读完转成内存里的 xlsx 走同一条管线，
// 这样清洗/合并/查询一行都不用改。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/extrame/xls"
)

// IsXLS 是否是老的 .xls 格式（注意和 .xlsx 区分开）。
func IsXLS(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".xls")
}

// readXLS 读 .xls 的所有工作表。
func readXLS(path string) ([]sheetData, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("读不了这个文件: %w", err)
	}
	wb, err := xls.Open(path, "utf-8")
	if err != nil {
		return nil, fmt.Errorf("这个 .xls 读不了（可能是加密文件或格式损坏）: %w", err)
	}

	var out []sheetData
	for i := 0; i < wb.NumSheets(); i++ {
		sh := wb.GetSheet(i)
		if sh == nil {
			continue
		}
		name := strings.TrimSpace(sh.Name)
		if name == "" {
			name = fmt.Sprintf("Sheet%d", i+1)
		}

		rows := make([][]string, 0, int(sh.MaxRow)+1)
		for r := 0; r <= int(sh.MaxRow); r++ {
			row := sh.Row(r)
			if row == nil {
				rows = append(rows, nil)
				continue
			}
			last := row.LastCol()
			if last < 0 {
				rows = append(rows, nil)
				continue
			}
			cells := make([]string, last+1)
			for c := row.FirstCol(); c <= last && c >= 0; c++ {
				cells[c] = strings.TrimSpace(row.Col(c))
			}
			rows = append(rows, cells)
		}

		// 去掉尾部整行为空的行（.xls 的 MaxRow 常常远大于实际内容）
		for len(rows) > 0 && blankRow(rows[len(rows)-1]) {
			rows = rows[:len(rows)-1]
		}
		if len(rows) == 0 {
			continue
		}
		out = append(out, sheetData{Name: name, Rows: rows})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s 里没有可读的工作表", filepath.Base(path))
	}
	return out, nil
}

func blankRow(row []string) bool {
	for _, v := range row {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}
