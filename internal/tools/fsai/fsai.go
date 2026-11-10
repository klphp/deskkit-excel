// Package fsai 把文件能力（github.com/klphp/deskkit-excel/internal/fsx）接进桌面应用的 ai.Registry。
//
// 为什么单独一个包：fsx 必须保持「零桌面依赖」，因为同一份能力还要被
// MCP server（github.com/klphp/deskmcp）复用。
// 而 ai 包带着对话循环、会话存储，MCP 侧一个都用不上。
// 所以「能力定义」留在 fsx，「怎么挂进桌面应用」放在这里。
package fsai

import (
	"context"
	"encoding/json"

	"github.com/klphp/deskkit-excel/internal/ai"
	"github.com/klphp/deskkit-excel/internal/capability"
	"github.com/klphp/deskkit-excel/internal/fsx"
)

// Register 把文件操作注册成 AI 可调用的工具。
//
// 删除类工具刻意做成「默认进废纸篓」：模型误判时用户还能捞回来。
// 永久删除要显式传 permanent=true，且必须带用户点过确认的令牌。
func Register(r *ai.Registry) error {
	return RegisterWithGuard(r, fsx.DefaultGuard())
}

// RegisterWithGuard 用指定的边界注册工具。
// 单独抽出来是为了测试能用临时目录当根，不去碰用户主目录。
func RegisterWithGuard(r *ai.Registry, g fsx.Guard) error {
	for _, tool := range g.Catalog() {
		def, err := ai.FunctionTool(tool.Name, tool.Description, tool.Schema)
		if err != nil {
			return err
		}
		run := tool.Run
		if err := r.Add(def, func(ctx context.Context, args json.RawMessage) (string, error) {
			return run(ctx, args)
		}); err != nil {
			return err
		}
	}
	return nil
}

// Pack 声明文件操作能力包。
//
// 纯 Go 实现（os / filepath），不依赖外部命令，所以不需要下载组件。
func Pack() capability.Pack {
	return capability.Pack{
		ID:          "files",
		Name:        "Files",
		Description: "Walk, create, move, copy and delete local files and folders. Deletes go to the trash by default and can be restored.",
		Register:    Register,
	}
}
