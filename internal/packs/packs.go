// Package packs 能力包汇总。
//
// 这一层刻意做得很薄：只维护「有哪些能力包」这张清单，
// 具体的工具定义、实现、测试全在各工具包自己的目录里。
//
//	internal/capability      类型定义（叶子包，不依赖业务）
//	internal/tools/excel     表格处理 + excel.Pack()
//	internal/tools/fs        文件操作 + fs.Pack()
//	internal/tools/<新工具>   照抄上面两个，加 Pack() 即可
//
// 加一个第三方工具（视频、PDF、OCR…）要改的只有下面 All() 一行。
package packs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/klphp/excel-batch-tool/internal/ai"
	"github.com/klphp/excel-batch-tool/internal/capability"
	"github.com/klphp/excel-batch-tool/internal/tools/excel"
	"github.com/klphp/excel-batch-tool/internal/tools/fs"
)

// All 全部能力包。顺序即界面顺序，也是模型选工具时的倾向顺序。
func All() []capability.Pack {
	return []capability.Pack{
		excel.Pack(),
		fs.Pack(),
		// 待做：
		//   video.Pack()  依赖 ffmpeg
		//   pdf.Pack()    依赖 pandoc / pdfcpu
	}
}

// registerMetaTools 注册「查用法」这个元工具。
//
// 为什么让 AI 自己去查，而不是把所有用法塞进系统提示：
// 用法说明加起来几千字，每轮对话都带上是纯浪费；用户提到哪类功能，
// 模型查一下对应那一段就够了。这也是 /api/v1/capabilities/:id/usage
// 同一个数据源 —— 人和模型看到的是同一份东西。
func registerMetaTools(r *ai.Registry, packs []capability.Pack) error {
	def, err := ai.FunctionTool("describe_capability",
		"查看某个能力包的完整用法：所有工具名、参数 JSON Schema、调用示例。不确定某个工具怎么用、或用户问「你能做什么」时先调用它。",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "能力包 id（如 excel / files）。留空则返回全部",
				},
			},
		})
	if err != nil {
		return err
	}
	return r.Add(def, func(ctx context.Context, args json.RawMessage) (string, error) {
		var in struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(args, &in)

		docs, err := capability.BuildAllUsage(packs, nil)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		found := false
		for _, d := range docs {
			if in.ID != "" && d.ID != in.ID {
				continue
			}
			found = true
			b.WriteString(d.Markdown)
			b.WriteString("\n")
		}
		if !found {
			names := make([]string, 0, len(docs))
			for _, d := range docs {
				names = append(names, d.ID)
			}
			return fmt.Sprintf("没有能力包 %q。可用：%s", in.ID, strings.Join(names, "、")), nil
		}
		return b.String(), nil
	})
}

// enabledIDs 本次运行启用的能力包（由 config/app.yaml 的 tools.packs 决定）。
//
// 放在包级变量里，是为了让「界面上的功能选择」和「实际注册的工具」
// 永远一致 —— 两边都读这一份，不会出现「界面上有、实际调不到」。
var enabledIDs []string

// SetEnabled 设置启用清单。启动时调一次。
func SetEnabled(ids []string) {
	enabledIDs = append([]string(nil), ids...)
}

// Enabled 当前启用的能力包（按 All() 的顺序）。
func Enabled() []capability.Pack {
	return filter(All(), enabledIDs)
}

func filter(all []capability.Pack, want []string) []capability.Pack {
	if len(want) == 0 {
		return all
	}
	keep := map[string]bool{}
	for _, id := range want {
		keep[id] = true
	}
	out := make([]capability.Pack, 0, len(all))
	for _, p := range all {
		if keep[p.ID] {
			out = append(out, p)
		}
	}
	return out
}

// Register 把选中的能力包挂进工具表。enabled 为空表示全部。
//
// 配置里写错一个包名只会让它不生效，不会让应用起不来 ——
// 桌面应用起不来比少一个功能糟糕得多。
func Register(r *ai.Registry, enabled []string) (loaded []capability.Pack, err error) {
	SetEnabled(enabled)
	for _, p := range Enabled() {
		if p.Register == nil {
			continue
		}
		if e := p.Register(r); e != nil {
			return loaded, e
		}
		loaded = append(loaded, p)
	}

	// 元工具最后挂：它要能查到本次实际装载了哪些包
	if err := registerMetaTools(r, loaded); err != nil {
		return loaded, err
	}
	return loaded, nil
}
