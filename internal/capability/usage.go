package capability

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/klphp/deskkit-excel/internal/ai"
)

// UsageDoc 一个能力包的用法说明。
//
// 它**由工具定义自动生成**，不是手写文档。
// 手写文档一定会落后于代码：加一个参数忘了改文档，模型就会照错的用法调，
// 于是「AI 不会用这个接口」。这里直接从 Registry 里读 name / description /
// parameters(JSON Schema)，工具改了说明就跟着改。
type UsageDoc struct {
	// ID 能力包标识。
	ID string `json:"id"`
	// Name 给人看的名字。
	Name string `json:"name"`
	// Description 一句话说明。
	Description string `json:"description"`
	// Bundled 功能组件是否随应用装好。
	Bundled bool `json:"bundled"`
	// Requires 依赖的外部命令。
	Requires []string `json:"requires,omitempty"`
	// Missing 本机缺失的外部命令。
	Missing []string `json:"missing,omitempty"`
	// Tools 工具清单。
	Tools []ToolDoc `json:"tools"`
	// Markdown 给模型看的提示词（也是给人看的文档）。
	Markdown string `json:"markdown"`
}

// ToolDoc 一个工具的用法。
type ToolDoc struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Parameters 原始 JSON Schema，前端可以拿去做表单。
	Parameters json.RawMessage `json:"parameters,omitempty"`
	// Example 由必填字段拼出来的调用示例。
	Example string `json:"example,omitempty"`
}

// BuildUsage 生成一个能力包的用法说明。
//
// 做法是把包注册进一个临时 Registry，再读回来 —— 因此不可能出现
// 「文档里有的工具实际没有」这种情况。注册失败也算一种信息，直接返回错误。
func BuildUsage(p Pack) (UsageDoc, error) {
	doc := UsageDoc{
		ID:          p.ID,
		Name:        p.Name,
		Description: p.Description,
		Bundled:     p.Bundled(),
		Requires:    p.Requires,
		Missing:     p.Missing(),
	}
	if p.Register == nil {
		return doc, fmt.Errorf("能力包 %s 没有注册函数", p.ID)
	}

	reg := ai.NewRegistry()
	if err := p.Register(reg); err != nil {
		return doc, fmt.Errorf("注册能力包 %s 失败: %w", p.ID, err)
	}

	defs := reg.Definitions()
	sort.Slice(defs, func(i, j int) bool { return defs[i].Function.Name < defs[j].Function.Name })

	for _, d := range defs {
		td := ToolDoc{
			Name:        d.Function.Name,
			Description: d.Function.Description,
			Parameters:  d.Function.Parameters,
			Example:     exampleCall(d.Function.Parameters),
		}
		doc.Tools = append(doc.Tools, td)
	}
	doc.Markdown = renderMarkdown(doc)
	return doc, nil
}

// BuildAllUsage 全部能力包的用法说明。
func BuildAllUsage(all []Pack, enabled []string) ([]UsageDoc, error) {
	want := map[string]bool{}
	for _, id := range enabled {
		want[id] = true
	}
	var out []UsageDoc
	for _, p := range all {
		if len(want) > 0 && !want[p.ID] {
			continue
		}
		doc, err := BuildUsage(p)
		if err != nil {
			// 一个包坏掉不该让整份说明拿不到
			doc = UsageDoc{ID: p.ID, Name: p.Name, Markdown: "（这个能力包暂时不可用：" + err.Error() + "）"}
		}
		out = append(out, doc)
	}
	return out, nil
}

// exampleCall 用必填字段拼一个调用示例。
//
// 只说「参数有 path」模型也会用，但给一条能照着抄的例子，
// 调用成功率明显不同。
func exampleCall(params json.RawMessage) string {
	if len(params) == 0 {
		return ""
	}
	var schema struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(params, &schema); err != nil {
		return ""
	}
	if len(schema.Required) == 0 {
		return ""
	}
	parts := make([]string, 0, len(schema.Required))
	for _, name := range schema.Required {
		typ := schema.Properties[name].Type
		switch typ {
		case "string":
			parts = append(parts, fmt.Sprintf(`%q:"…"`, name))
		case "number", "integer":
			parts = append(parts, fmt.Sprintf(`%q:0`, name))
		case "boolean":
			parts = append(parts, fmt.Sprintf(`%q:false`, name))
		case "array":
			parts = append(parts, fmt.Sprintf(`%q:["…"]`, name))
		default:
			parts = append(parts, fmt.Sprintf(`%q:{…}`, name))
		}
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func renderMarkdown(doc UsageDoc) string {
	var b strings.Builder

	fmt.Fprintf(&b, "## %s（%s）\n\n", doc.Name, doc.ID)
	if doc.Description != "" {
		fmt.Fprintf(&b, "%s\n\n", doc.Description)
	}
	if !doc.Bundled {
		fmt.Fprintf(&b, "> 依赖外部组件：%s", strings.Join(doc.Requires, "、"))
		if len(doc.Missing) > 0 {
			fmt.Fprintf(&b, "（本机缺少：%s）", strings.Join(doc.Missing, "、"))
		}
		b.WriteString("\n\n")
	}

	b.WriteString("可用工具：\n\n")
	for _, t := range doc.Tools {
		fmt.Fprintf(&b, "### `%s`\n\n%s\n\n", t.Name, t.Description)
		if len(t.Parameters) > 0 {
			var pretty any
			if err := json.Unmarshal(t.Parameters, &pretty); err == nil {
				raw, _ := json.MarshalIndent(pretty, "", "  ")
				b.WriteString("参数：\n\n```json\n")
				b.Write(append(raw, '\n'))
				b.WriteString("```\n\n")
			}
		}
		if t.Example != "" {
			fmt.Fprintf(&b, "调用示例：`%s`\n\n", t.Example)
		}
	}
	return b.String()
}
