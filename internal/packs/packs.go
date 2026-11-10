// Package packs 能力包汇总。
//
// 这一层刻意做得很薄：只维护「有哪些能力包」这张清单，
// 具体的工具定义、实现、测试全在各工具包自己的目录里。
//
//	internal/capability      类型定义（叶子包，不依赖业务）
//	internal/tools/excel     表格处理 + excel.Pack()
//	github.com/klphp/deskkit-excel/internal/fsx     文件能力核心（独立模块，纯 Go，零桌面依赖）
//	internal/tools/fsai      把文件能力挂进 ai.Registry + fsai.Pack()
//	internal/tools/<新工具>   照抄上面两个，加 Pack() 即可
//
// 加一个第三方工具（视频、PDF、OCR…）要改的只有下面 All() 一行。
package packs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/klphp/deskkit-excel/internal/ai"
	"github.com/klphp/deskkit-excel/internal/capability"
	"github.com/klphp/deskkit-excel/internal/tools/excel"
	"github.com/klphp/deskkit-excel/internal/tools/fsai"
)

// All 全部能力包。顺序即界面顺序，也是模型选工具时的倾向顺序。
func All() []capability.Pack {
	return []capability.Pack{
		excel.Pack(),
		fsai.Pack(),
		// 待做：
		//   video.Pack()  依赖 ffmpeg
		//   pdf.Pack()    依赖 pandoc / pdfcpu
	}
}

// registerAskTool 注册「问用户」这个元工具。
//
// 为什么需要它：模型遇到需要用户决定的事（删哪些文件、按哪列去重、
// 要不要覆盖），纯文本问一句「确定吗」体验很差 —— 用户得自己打字回答，
// 而且容易答得含糊。有了这个工具，问题会变成界面上的按钮，点一下就答完。
//
// 实现上是「问完就停」：工具返回一个带标记的结果，循环层看到标记就不再
// 继续调用模型，把回合交给用户。否则模型会在没有答案的情况下自己编一个。
func registerAskTool(r *ai.Registry) error {
	def, err := ai.FunctionTool("ask_user",
		"Ask the user a question with clickable options. Use it whenever the user has to decide, instead of writing a yes/no question in the reply: "+
			"confirm before destructive actions such as delete/overwrite/move, when key details are missing (which column to dedupe by, where to write the output), "+
			"or when the user must pick one of several approaches. The round ends after the call and waits for the click.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{
					"type":        "string",
					"description": "The question itself, in one sentence. Be specific; do not ask uninformative questions such as continue?",
				},
				"options": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "2 to 4 options, in recommended order. Whatever the user clicks is the answer",
				},
				"detail": map[string]any{
					"type":        "string",
					"description": "Optional extra detail, such as the list of files that will be affected or a risk note.",
				},
			},
			"required": []string{"question", "options"},
		},
	)
	if err != nil {
		return err
	}

	type askArgs struct {
		Question string   `json:"question"`
		Options  []string `json:"options"`
		Detail   string   `json:"detail,omitempty"`
	}

	return r.Add(def, func(ctx context.Context, args json.RawMessage) (string, error) {
		var in askArgs
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
		if strings.TrimSpace(in.Question) == "" {
			return "", fmt.Errorf("question must not be empty")
		}
		// 选项数量收口：0 个就是普通文本提问（没有可点的东西），
		// 超过 4 个界面会挤成一团，而且用户选择反而变慢。
		opts := in.Options
		if len(opts) > 4 {
			opts = opts[:4]
		}
		payload, _ := json.Marshal(map[string]any{
			"question": in.Question,
			"options":  opts,
			"detail":   in.Detail,
		})
		return ai.AwaitPrefix + string(payload), nil
	})
}

// registerRememberTool 注册「记住这个会话的工作方式」。
//
// 为什么需要：用户会说「下载的存 D:\待整理\，处理结果存 D:\报表\」。
// 这句话只对当前会话有效（下个会话可能是别的任务），所以要写到会话上，
// 而不是全局设置里。写进去之后，后面每轮的系统提示都会带上它。
func registerRememberTool(r *ai.Registry) error {
	def, err := ai.FunctionTool("remember_instructions",
		"Record the user's working preferences for this session (for example which folder results go to, and the naming rules)."+
			"Only record what the user actually said, never make things up. Recording again in the same session replaces the previous text.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{
					"type":        "string",
					"description": "The full text to remember, phrased in the user's own words as one or a few sentences, e.g. save results to D:\\reports\\ with the date in the file name",
				},
			},
			"required": []string{"text"},
		},
	)
	if err != nil {
		return err
	}
	return r.Add(def, func(ctx context.Context, args json.RawMessage) (string, error) {
		var in struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
		if strings.TrimSpace(in.Text) == "" {
			return "", fmt.Errorf("text must not be empty")
		}
		if err := ai.SaveSessionInstructions(ctx, in.Text); err != nil {
			return "", err
		}
		raw, _ := json.Marshal(map[string]any{"ok": true, "saved": in.Text})
		return string(raw), nil
	})
}

// registerMetaTools 注册「查用法」这个元工具。
//
// 为什么让 AI 自己去查，而不是把所有用法塞进系统提示：
// 用法说明加起来几千字，每轮对话都带上是纯浪费；用户提到哪类功能，
// 模型查一下对应那一段就够了。这也是 /api/v1/capabilities/:id/usage
// 同一个数据源 —— 人和模型看到的是同一份东西。
func registerMetaTools(r *ai.Registry, packs []capability.Pack) error {
	def, err := ai.FunctionTool("describe_capability",
		"Read the full usage of a capability pack: every tool name, parameter JSON Schema and example call. Call it first when unsure how a tool works, or when the user asks what you can do.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Capability pack id (for example excel / files). Leave empty to return all of them",
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
			return fmt.Sprintf("No capability pack %q. Available: %s", in.ID, strings.Join(names, ", ")), nil
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
	if err := registerAskTool(r); err != nil {
		return loaded, err
	}
	if err := registerRememberTool(r); err != nil {
		return loaded, err
	}
	// 危险操作：必须用户点过确认才允许执行。
	// 现在只有删除；将来覆盖式移动、批量改名也走同一条门。
	r.MarkDestructive("fs_delete")
	return loaded, nil
}
