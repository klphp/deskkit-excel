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
		Description: "Walk a folder and list files and subfolders (optionally recursive, optionally filtered by wildcard). Far faster than having the model write a script; one call handles hundreds of files.",
		Schema: schema(map[string]any{
			"path":        map[string]any{"type": "string", "description": "Absolute path of the folder"},
			"recursive":   map[string]any{"type": "boolean", "description": "Whether to recurse into subfolders"},
			"pattern":     map[string]any{"type": "string", "description": "File name wildcard, such as *.xlsx; empty means all files"},
			"maxEntries":  map[string]any{"type": "integer", "description": "Maximum entries to return, 2000 by default"},
			"concurrency": map[string]any{"type": "integer", "description": "Concurrency when recursing, default is 2x the core count (max 32); 1 means serial. Usually left out"},
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
				"hint":      "With many entries only the first 200 are returned; for an exact result add a pattern filter or narrow the folder.",
			})
			return string(raw), nil
		},
	}
}

func (g Guard) peekTool() Tool {
	return Tool{
		Name:        "fs_peek",
		Description: "Read the start of a file and report the encoding, column separator, headers and first few rows. Call it before handling any unknown file instead of guessing the format: exported CSVs are often GBK, and guessing wrong produces garbled text.",
		Schema: schema(map[string]any{
			"path":     map[string]any{"type": "string", "description": "Absolute path of the file"},
			"maxLines": map[string]any{"type": "integer", "description": "How many sample rows to return, 10 by default, 50 at most"},
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
		Description: "Read a slice of a text file, line by line. It detects the real encoding (UTF-8/GBK/Big5/UTF-16/BOM) and reports it along with the total line count and the line range returned. For large files use offset/limit instead of reading everything. Use it to read tables and exported text; there is no need to write a script just to read a file, since encoding detection is the easiest thing to get wrong.",
		Schema: schema(map[string]any{
			"path":     map[string]any{"type": "string", "description": "Absolute path of the file"},
			"offset":   map[string]any{"type": "integer", "description": "Starting line, 1-based, 1 by default"},
			"limit":    map[string]any{"type": "integer", "description": "How many lines to read, 200 by default, 50000 at most"},
			"maxBytes": map[string]any{"type": "integer", "description": "Maximum bytes to read in one call, 2MB by default, 64MB at most"},
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
		Description: "Search a folder for a keyword by content (plain substring match, not regex). It handles GBK/Big5/UTF-16 automatically, skips binary and oversized files, and returns file path + line number + line text. Use it to find which file contains some error text instead of writing your own grep: grep silently finds nothing in GBK/UTF-16 files.",
		Schema: schema(map[string]any{
			"path":          map[string]any{"type": "string", "description": "Absolute path of a folder or a single file"},
			"pattern":       map[string]any{"type": "string", "description": "Keyword to search for; with regex=true it is interpreted as a Go regular expression (RE2)"},
			"regex":         map[string]any{"type": "boolean", "description": "Treat pattern as a regular expression (default false = plain substring). The regex runs on the decoded text, so non-ASCII text in GBK files can be matched too"},
			"extensions":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Only look at these extensions, such as [\"txt\",\"csv\"]; empty means all"},
			"maxResults":    map[string]any{"type": "integer", "description": "Maximum hits to return, 100 by default, 5000 at most"},
			"maxFileSize":   map[string]any{"type": "integer", "description": "Skip files larger than this many bytes, 5MB by default"},
			"concurrency":   map[string]any{"type": "integer", "description": "How many files to scan in parallel, default is 60% of available capacity (max 8)"},
			"caseSensitive": map[string]any{"type": "boolean", "description": "Whether the search is case sensitive, false by default"},
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
		Description: "Create a folder, including parent folders. An existing folder counts as success.",
		Schema: schema(map[string]any{
			"path": map[string]any{"type": "string", "description": "Absolute path of the folder"},
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
		Description: "Move or rename a file or folder. It refuses when the target already exists; nothing is overwritten.",
		Schema: schema(map[string]any{
			"from": map[string]any{"type": "string", "description": "Source path"},
			"to":   map[string]any{"type": "string", "description": "Target path"},
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
		Description: "Copy a file or a whole folder. It refuses when the target already exists.",
		Schema: schema(map[string]any{
			"from": map[string]any{"type": "string", "description": "Source path"},
			"to":   map[string]any{"type": "string", "description": "Target path"},
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
		Description: "Delete files or folders. By default they go to the trash (recoverable); pass permanent=true only when the user explicitly asks to delete permanently.",
		Schema: schema(map[string]any{
			"paths":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "List of paths to delete"},
			"permanent":    map[string]any{"type": "boolean", "description": "true = delete permanently, cannot be undone. Default false (moves to the trash)"},
			"confirmToken": map[string]any{"type": "string", "description": "Token returned after the user confirms. Do not pass it on the first call; pass it on the retry as the returned hint says"},
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
				return "", fmt.Errorf("paths is empty: give the files or folders to delete")
			}

			// ★ 硬门：没有用户点过的令牌，一步都不执行。
			if !DefaultConfirms().Consume(in.ConfirmToken, "fs_delete", string(args)) {
				title := "These items will be deleted"
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
			items = append(items, fmt.Sprintf("%s (cannot access: %v)", raw, err))
			continue
		}
		info, err := os.Stat(abs)
		switch {
		case err != nil:
			items = append(items, fmt.Sprintf("%s (does not exist or cannot be read)", abs))
		case info.IsDir():
			items = append(items, abs+" (a folder, including everything inside)")
		default:
			items = append(items, fmt.Sprintf("%s (%d bytes)", abs, info.Size()))
		}
	}
	note := "To avoid accidents, back the data up first."
	if permanent {
		note = "This is a permanent delete and cannot be undone. To avoid accidents, back the data up first."
	}
	return items, note
}

func trashHint(permanent bool) string {
	if permanent {
		return "Permanently deleted; this cannot be undone. Tell the user exactly that."
	}
	return "Moved to the trash; the user can restore it from there. Tell the user where it went."
}
