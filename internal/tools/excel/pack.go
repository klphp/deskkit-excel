package excel

import "github.com/klphp/excel-batch-tool/internal/capability"

// Pack 声明表格处理能力包。
//
// 自我描述放在工具包内部：加一个新工具包时，改动只发生在那个目录里，
// 汇总方不需要知道它的细节。
func Pack() capability.Pack {
	return capability.Pack{
		ID:          "excel",
		Name:        "表格处理",
		Description: "Excel / CSV 的合并、清洗、筛选、分组汇总、按 SQL 任意查询，本地读写，不上传。",
		Register:    Register,
	}
}
