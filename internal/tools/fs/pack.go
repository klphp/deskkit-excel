package fs

import "github.com/klphp/excel-batch-tool/internal/capability"

// Pack 声明文件操作能力包。
//
// 纯 Go 实现（os / filepath），不依赖外部命令，所以不需要下载组件。
func Pack() capability.Pack {
	return capability.Pack{
		ID:          "files",
		Name:        "文件操作",
		Description: "遍历、创建、移动、复制、删除本地文件与目录。删除默认进废纸篓，可恢复。",
		Register:    Register,
	}
}
