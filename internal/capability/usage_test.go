package capability

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/klphp/excel-batch-tool/internal/ai"
)

func demoPack(requires []string) Pack {
	return Pack{
		ID:          "demo",
		Name:        "演示能力",
		Description: "只是用来测用法生成的。",
		Requires:    requires,
		Register: func(r *ai.Registry) error {
			def, err := ai.FunctionTool("demo_tool", "演示工具：读一个文件。", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":  map[string]any{"type": "string"},
					"limit": map[string]any{"type": "integer"},
					"deep":  map[string]any{"type": "boolean"},
					"tags":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
				"required": []string{"path", "limit"},
			})
			if err != nil {
				return err
			}
			return r.Add(def, func(_ context.Context, _ json.RawMessage) (string, error) { return "", nil })
		},
	}
}

// 用法说明必须来自真实工具定义，不能是手写文档。
func TestBuildUsageFromToolDefinitions(t *testing.T) {
	doc, err := BuildUsage(demoPack(nil))
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	if doc.ID != "demo" || !doc.Bundled {
		t.Errorf("元信息不对: %+v", doc)
	}
	if len(doc.Tools) != 1 || doc.Tools[0].Name != "demo_tool" {
		t.Fatalf("工具清单不对: %+v", doc.Tools)
	}
	if !strings.Contains(doc.Markdown, "demo_tool") {
		t.Error("markdown 里没有工具名")
	}
	if !strings.Contains(doc.Markdown, `"path"`) {
		t.Error("markdown 里没有参数 schema")
	}
}

// 示例用必填字段拼，类型要对 —— 模型照抄示例的成功率高得多。
func TestExampleCallUsesRequiredWithTypes(t *testing.T) {
	doc, err := BuildUsage(demoPack(nil))
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	ex := doc.Tools[0].Example
	for _, want := range []string{`"path":"…"`, `"limit":0`} {
		if !strings.Contains(ex, want) {
			t.Errorf("示例里缺少 %s，实际 %s", want, ex)
		}
	}
	// 非必填字段不该出现在示例里，否则模型会以为要填
	for _, unwanted := range []string{`"deep"`, `"tags"`} {
		if strings.Contains(ex, unwanted) {
			t.Errorf("示例不该包含非必填字段 %s: %s", unwanted, ex)
		}
	}
}

// 需要外部命令的包要能看出「组件没装」。
func TestPackMissingAndBundled(t *testing.T) {
	p := demoPack([]string{"这个命令一定不存在-xyz"})
	if p.Bundled() {
		t.Error("有 Requires 时不该算内置")
	}
	if miss := p.Missing(); len(miss) != 1 {
		t.Errorf("应报告 1 个缺失依赖，实际 %v", miss)
	}

	doc, err := BuildUsage(p)
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	if doc.Bundled {
		t.Error("doc.Bundled 应为 false")
	}
	if !strings.Contains(doc.Markdown, "依赖外部组件") {
		t.Error("markdown 里应提示依赖")
	}

	if !demoPack(nil).Bundled() {
		t.Error("纯 Go 实现应算内置")
	}
}

func TestBuildAllUsageFilters(t *testing.T) {
	all := []Pack{demoPack(nil), {ID: "other", Name: "另一个", Register: func(r *ai.Registry) error { return nil }}}

	docs, err := BuildAllUsage(all, []string{"other"})
	if err != nil {
		t.Fatalf("失败: %v", err)
	}
	if len(docs) != 1 || docs[0].ID != "other" {
		t.Fatalf("过滤没生效: %+v", docs)
	}

	docs, _ = BuildAllUsage(all, nil)
	if len(docs) != 2 {
		t.Fatalf("不传 enabled 应返回全部，实际 %d", len(docs))
	}
}
