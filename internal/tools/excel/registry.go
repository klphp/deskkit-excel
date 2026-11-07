package excel

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/klphp/excel-batch-tool/internal/ai"
)

// Register 把这些能力注册成 AI 可调用的工具。
//
// 为什么 preview 和 run 是两个工具而不是一个带 dryRun 参数的工具：
// 模型很容易「忘了」先预览就直接写文件。拆成两个之后，必须依次调用，
// 而这个顺序也正好是界面上按钮的顺序（先看变更，再执行）。
func Register(r *ai.Registry) error {
	scanDef, err := ai.FunctionTool("scan_excel_folder",
		"扫描一个目录，列出里面的 Excel 文件、每个文件的表头与行数。在合并或清洗之前先調用它了解数据。",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"dir": map[string]any{"type": "string", "description": "目录的绝对路径"},
			},
			"required": []string{"dir"},
		})
	if err != nil {
		return err
	}
	if err := r.Add(scanDef, func(ctx context.Context, args json.RawMessage) (string, error) {
		var in struct {
			Dir string `json:"dir"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
		res, err := Scan(ctx, in.Dir)
		if err != nil {
			return "", err
		}
		// 只回传摘要 + 前若干个文件，避免把几千行 JSON 塞进上下文
		type brief struct {
			Name    string `json:"name"`
			Rows    int    `json:"rows"`
			Headers string `json:"headers"`
			Err     string `json:"error,omitempty"`
		}
		items := make([]brief, 0, min(len(res.Files), 20))
		for _, f := range res.Files {
			if len(items) >= 20 {
				break
			}
			items = append(items, brief{
				Name: f.Name, Rows: f.Rows, Headers: joinHeaders(f.Headers), Err: f.Err,
			})
		}
		out := map[string]any{
			"files":       len(res.Files),
			"totalRows":   res.TotalRows,
			"ignored":     res.Ignored,
			"unsupported": res.Unsupported,
			"sample":      items,
		}
		raw, _ := json.Marshal(out)
		return string(raw), nil
	}); err != nil {
		return err
	}

	previewDef, err := ai.FunctionTool("preview_merge",
		"预览合并结果：只统计会得到多少行、会丢掉哪些列、有多少重复，不写任何文件。合并前必须先调用它，并把结论告诉用户。",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"dir":      map[string]any{"type": "string", "description": "目录的绝对路径"},
				"dedupKey": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "去重用的列名，可选"},
			},
			"required": []string{"dir"},
		})
	if err != nil {
		return err
	}
	if err := r.Add(previewDef, func(ctx context.Context, args json.RawMessage) (string, error) {
		var in struct {
			Dir      string   `json:"dir"`
			DedupKey []string `json:"dedupKey"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
		res, err := Merge(ctx, MergeOptions{Dir: in.Dir, DedupKey: in.DedupKey, DryRun: true})
		if err != nil {
			return "", err
		}
		raw, _ := json.Marshal(map[string]any{
			"rowsIn":     res.RowsIn,
			"rowsOut":    res.RowsOut,
			"duplicates": res.Duplicates,
			"files":      res.Files,
			"columns":    res.Columns,
			"skipped":    res.Skipped,
			"warnings":   res.Warnings,
		})
		return string(raw), nil
	}); err != nil {
		return err
	}

	runDef, err := ai.FunctionTool("run_merge",
		"执行合并并写出一个新文件（不会覆盖原文件）。只在用户明确同意后调用，且必须先调用过 preview_merge。",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"dir":      map[string]any{"type": "string", "description": "目录的绝对路径"},
				"dedupKey": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
			"required": []string{"dir"},
		})
	if err != nil {
		return err
	}
	return r.Add(runDef, func(ctx context.Context, args json.RawMessage) (string, error) {
		var in struct {
			Dir      string   `json:"dir"`
			DedupKey []string `json:"dedupKey"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
		res, err := Merge(ctx, MergeOptions{Dir: in.Dir, DedupKey: in.DedupKey})
		if err != nil {
			return "", err
		}
		raw, _ := json.Marshal(map[string]any{
			"output":  res.OutputPath,
			"rowsOut": res.RowsOut,
			"files":   res.Files,
			"skipped": res.Skipped,
		})
		return string(raw), nil
	})
}

func joinHeaders(headers []string) string {
	out := ""
	for i, h := range headers {
		if i > 0 {
			out += " | "
		}
		out += h
	}
	return out
}

var _ = fmt.Sprintf
