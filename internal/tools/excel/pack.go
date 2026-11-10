package excel

import "github.com/klphp/deskkit-excel/internal/capability"

// Pack 声明表格处理能力包。
//
// 自我描述放在工具包内部：加一个新工具包时，改动只发生在那个目录里，
// 汇总方不需要知道它的细节。
func Pack() capability.Pack {
	return capability.Pack{
		ID:          "excel",
		Name:        "Spreadsheets",
		Description: "Merge, clean, filter, group and summarize Excel / CSV files, or query them with SQL. Everything is read and written locally; nothing is uploaded.",
		Register:    Register,
	}
}
