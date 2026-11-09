package fsx

// 工具表：文件能力对外暴露的「能力清单」。
//
// 这一层是纯的 —— 只依赖标准库和本包，不 import 桌面端的 ai / capability。
// 谁需要这份清单就自己适配：
//   · 桌面应用 → internal/tools/fsai 转成 ai.Registry 的 function tool；
//   · MCP server（cmd/mcp-fs）→ 直接转成 MCP 的 tools/list。
//
// 为什么值得这么做：MCP server 要独立成模块（github.com/klphp/deskmcp）。
// 如果工具定义写在 ai.Registry 里，它就永远搬不出这个仓 —— 而 ai 包带着
// 对话循环、会话存储，MCP 一个都用不上。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// Tool 一个可被 AI 调用的能力。
type Tool struct {
	Name        string
	Description string
	// Schema 参数 JSON Schema（object）。
	Schema map[string]any
	// Run 执行。返回值直接回给模型（字符串，通常是 JSON）。
	Run func(ctx context.Context, args json.RawMessage) (string, error)
}

// Catalog 本 Guard 边界下的全部工具。
//
// 顺序即模型选工具时的倾向顺序：先看（list/peek/read/search），再改。
func (g Guard) Catalog() []Tool {
	tools := []Tool{
		g.listTool(),
		g.peekTool(),
		g.readTool(),
		g.searchTool(),
		g.mkdirTool(),
		g.moveTool(),
		g.copyTool(),
	}
	return append(tools, g.deleteTool())
}

// ToolNames 工具名清单（测试和文档用）。
func (g Guard) ToolNames() []string {
	out := make([]string, 0, 8)
	for _, t := range g.Catalog() {
		out = append(out, t.Name)
	}
	return out
}

func schema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func (g Guard) listTool() Tool {
	return Tool{
		Name:        "fs_list",
		Description: "遍历目录，列出文件和子目录（可递归、可按通配符过滤）。比让模型自己写脚本快得多，几百个文件一次调用就够。",
		Schema: schema(map[string]any{
			"path":        map[string]any{"type": "string", "description": "目录绝对路径"},
			"recursive":   map[string]any{"type": "boolean", "description": "是否递归子目录"},
			"pattern":     map[string]any{"type": "string", "description": "文件名通配，如 *.xlsx；留空表示全部"},
			"maxEntries":  map[string]any{"type": "integer", "description": "最多返回多少条，默认 2000"},
			"concurrency": map[string]any{"type": "integer", "description": "递归时的并行度，默认核数×2（上限 32）；1 = 串行。一般不用传"},
		}, "path"),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in ListOptions
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			res, err := g.List(ctx, in)
			if err != nil {
				return "", err
			}
			// 条目多时只回传前 200 条 + 统计：把几千条 JSON 塞进上下文没有意义
			trimmed := res
			if len(trimmed.Entries) > 200 {
				trimmed.Entries = trimmed.Entries[:200]
			}
			raw, _ := json.Marshal(map[string]any{
				"root":      res.Root,
				"dirs":      res.Dirs,
				"files":     res.Files,
				"totalSize": res.TotalSize,
				"truncated": res.Truncated,
				"shown":     len(trimmed.Entries),
				"entries":   trimmed.Entries,
				"hint":      "条目较多时只回传前 200 条；需要精确结果请加 pattern 过滤或缩小目录。",
			})
			return string(raw), nil
		},
	}
}

func (g Guard) peekTool() Tool {
	return Tool{
		Name:        "fs_peek",
		Description: "读文件头部，告诉你编码、列分隔符、表头和前几行内容。处理任何未知文件之前先调它，别猜格式——国内导出的 CSV 常是 GBK，猜错就是满屏乱码。",
		Schema: schema(map[string]any{
			"path":     map[string]any{"type": "string", "description": "文件绝对路径"},
			"maxLines": map[string]any{"type": "integer", "description": "返回多少行样本，默认 10，上限 50"},
		}, "path"),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in PeekOptions
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			res, err := g.Peek(ctx, in)
			if err != nil {
				return "", err
			}
			raw, _ := json.Marshal(res)
			return string(raw), nil
		},
	}
}

func (g Guard) readTool() Tool {
	return Tool{
		Name:        "fs_read",
		Description: "读文本文件的一段（按行）。处理编码（UTF-8/GBK/Big5/UTF-16/BOM）并告诉你真实编码、总行数和行号区间。文件很大时用 offset/limit 定位，不要整文件读。读表格/导出的文本用它就行，不必为读文件另写脚本——编码判断是这里最容易出错的地方。",
		Schema: schema(map[string]any{
			"path":     map[string]any{"type": "string", "description": "文件绝对路径"},
			"offset":   map[string]any{"type": "integer", "description": "起始行，从 1 开始，默认 1"},
			"limit":    map[string]any{"type": "integer", "description": "读多少行，默认 200，上限 50000"},
			"maxBytes": map[string]any{"type": "integer", "description": "单次最多读多少字节，默认 2MB，上限 64MB"},
		}, "path"),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in ReadOptions
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			res, err := g.Read(ctx, in)
			if err != nil {
				return "", err
			}
			raw, _ := json.Marshal(res)
			return string(raw), nil
		},
	}
}

func (g Guard) searchTool() Tool {
	return Tool{
		Name:        "fs_search",
		Description: "按内容在目录里搜关键字（纯文本包含匹配，不是正则）。自动处理 GBK/Big5/UTF-16，跳过二进制和超大文件，返回文件路径 + 行号 + 该行内容。想知道「哪个文件里有这段报错」就用它，别自己写 grep——grep 在 GBK/UTF-16 编码的中文文件里搜不到东西，而且不报错。",
		Schema: schema(map[string]any{
			"path":          map[string]any{"type": "string", "description": "目录或单个文件的绝对路径"},
			"pattern":       map[string]any{"type": "string", "description": "要搜的关键字；regex=true 时按 Go 正则语法（RE2）解释"},
			"regex":         map[string]any{"type": "boolean", "description": "pattern 当正则用（默认 false=纯文本包含）。正则是加在解码后的文本上匹配的，所以 GBK 文件里的中文也能用正则搜"},
			"extensions":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "只看这些后缀，如 [\"txt\",\"csv\"]；留空表示全部"},
			"maxResults":    map[string]any{"type": "integer", "description": "最多返回多少条命中，默认 100，上限 5000"},
			"maxFileSize":   map[string]any{"type": "integer", "description": "超过多少字节的文件跳过，默认 5MB"},
			"concurrency":   map[string]any{"type": "integer", "description": "并行扫几个文件，默认可用算力的 60%（上限 8）"},
			"caseSensitive": map[string]any{"type": "boolean", "description": "是否区分大小写，默认 false"},
		}, "path", "pattern"),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in SearchOptions
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			res, err := g.Search(ctx, in)
			if err != nil {
				return "", err
			}
			raw, _ := json.Marshal(res)
			return string(raw), nil
		},
	}
}

func (g Guard) mkdirTool() Tool {
	return Tool{
		Name:        "fs_mkdir",
		Description: "创建目录（含多级）。已存在时视为成功。",
		Schema: schema(map[string]any{
			"path": map[string]any{"type": "string", "description": "目录绝对路径"},
		}, "path"),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			p, err := g.MakeDir(in.Path)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf(`{"created":%q}`, p), nil
		},
	}
}

func (g Guard) moveTool() Tool {
	return Tool{
		Name:        "fs_move",
		Description: "移动或重命名文件/目录。目标已存在时会拒绝，不会覆盖。",
		Schema: schema(map[string]any{
			"from": map[string]any{"type": "string", "description": "源路径"},
			"to":   map[string]any{"type": "string", "description": "目标路径"},
		}, "from", "to"),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				From string `json:"from"`
				To   string `json:"to"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			dst, err := g.Move(in.From, in.To)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf(`{"moved":%q}`, dst), nil
		},
	}
}

func (g Guard) copyTool() Tool {
	return Tool{
		Name:        "fs_copy",
		Description: "复制文件或整个目录。目标已存在时会拒绝。",
		Schema: schema(map[string]any{
			"from": map[string]any{"type": "string", "description": "源路径"},
			"to":   map[string]any{"type": "string", "description": "目标路径"},
		}, "from", "to"),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				From string `json:"from"`
				To   string `json:"to"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			dst, err := g.Copy(in.From, in.To)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf(`{"copied":%q}`, dst), nil
		},
	}
}

// deleteTool 删除 —— 默认进废纸篓，且必须带确认令牌。
func (g Guard) deleteTool() Tool {
	return Tool{
		Name:        "fs_delete",
		Description: "删除文件或目录。默认移到废纸篓（可恢复）；只有用户明确说「永久删除」时才传 permanent=true。",
		Schema: schema(map[string]any{
			"paths":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "要删除的路径列表"},
			"permanent":    map[string]any{"type": "boolean", "description": "true = 永久删除，不可恢复。默认 false（进废纸篓）"},
			"confirmToken": map[string]any{"type": "string", "description": "用户确认后拿到的令牌。第一次调用不要传，按返回的提示再带上重调"},
		}, "paths"),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Paths        []string `json:"paths"`
				Permanent    bool     `json:"permanent"`
				ConfirmToken string   `json:"confirmToken"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			// 空列表不生成确认清单：给用户看一张「即将删除以下内容」的空清单，
			// 他点确认之后什么也没删，反而是个说不清的体验。
			if len(in.Paths) == 0 {
				return "", fmt.Errorf("paths 为空：请给出要删除的文件或目录")
			}

			// ★ 硬门：没有用户点过的令牌，一步都不执行。
			if !DefaultConfirms().Consume(in.ConfirmToken, "fs_delete", string(args)) {
				title := "即将删除以下内容"
				items, note := deleteConfirmInfo(g, in.Paths, in.Permanent)
				token := DefaultConfirms().Issue("fs_delete", string(args), ConfirmRequest{
					Title: title,
					Items: items,
					Note:  note,
				})
				payload, _ := json.Marshal(map[string]any{
					"token": token,
					"title": title,
					"items": items,
					"note":  note,
				})
				return ConfirmPrefix + string(payload), nil
			}

			done, failed, err := g.Delete(in.Paths, in.Permanent)
			if err != nil {
				return "", err
			}
			raw, _ := json.Marshal(map[string]any{
				"deleted":   done,
				"failed":    failed,
				"permanent": in.Permanent,
				"hint":      trashHint(in.Permanent),
			})
			return string(raw), nil
		},
	}
}

// deleteConfirmInfo 生成给用户看的清单：一行一个完整路径。
//
// 只给「3 个文件」是不够的 —— 用户是靠路径判断「是不是这几个」的。
// 不存在的路径也列出来并标注，避免用户以为漏掉了。
func deleteConfirmInfo(g Guard, paths []string, permanent bool) ([]string, string) {
	items := make([]string, 0, len(paths))
	for _, raw := range paths {
		abs, err := g.Check(raw)
		if err != nil {
			items = append(items, fmt.Sprintf("%s（无法访问：%v）", raw, err))
			continue
		}
		info, err := os.Stat(abs)
		switch {
		case err != nil:
			items = append(items, fmt.Sprintf("%s（不存在或读不到）", abs))
		case info.IsDir():
			items = append(items, abs+"（目录，含其中全部内容）")
		default:
			items = append(items, fmt.Sprintf("%s（%d 字节）", abs, info.Size()))
		}
	}
	note := "为避免误操作，建议先备份。"
	if permanent {
		note = "这是永久删除，无法恢复。为避免误操作，建议先备份。"
	}
	return items, note
}

func trashHint(permanent bool) string {
	if permanent {
		return "已永久删除，无法恢复。请如实告知用户。"
	}
	return "已移到废纸篓，用户可以从废纸篓恢复。请告诉用户去了哪里。"
}
