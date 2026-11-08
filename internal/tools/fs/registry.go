package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/klphp/excel-batch-tool/internal/ai"
)

// Register 把文件操作注册成 AI 可调用的工具。
//
// 删除类工具刻意做成「默认进废纸篓」：模型误判时用户还能捞回来。
// 永久删除要显式传 permanent=true，且提示里写明不可恢复。
func Register(r *ai.Registry) error {
	return registerWithGuard(r, DefaultGuard())
}

// registerWithGuard 用指定的边界注册工具。
// 单独抽出来是为了测试能用临时目录当根，不去碰用户主目录。
func registerWithGuard(r *ai.Registry, g Guard) error {

	// ── 遍历 ──
	listDef, err := ai.FunctionTool("fs_list",
		"遍历目录，列出文件和子目录（可递归、可按通配符过滤）。比让模型自己写脚本快得多，几百个文件一次调用就够。",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":       map[string]any{"type": "string", "description": "目录绝对路径"},
				"recursive":  map[string]any{"type": "boolean", "description": "是否递归子目录"},
				"pattern":    map[string]any{"type": "string", "description": "文件名通配，如 *.xlsx；留空表示全部"},
				"maxEntries": map[string]any{"type": "integer", "description": "最多返回多少条，默认 2000"},
			},
			"required": []string{"path"},
		})
	if err != nil {
		return err
	}
	if err := r.Add(listDef, func(ctx context.Context, args json.RawMessage) (string, error) {
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
	}); err != nil {
		return err
	}

	// ── 看一眼这个文件是什么 ──
	peekDef, err := ai.FunctionTool("fs_peek",
		"读文件头部，告诉你编码、列分隔符、表头和前几行内容。处理任何未知文件之前先调它，别猜格式——国内导出的 CSV 常是 GBK，猜错就是满屏乱码。",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":     map[string]any{"type": "string", "description": "文件绝对路径"},
				"maxLines": map[string]any{"type": "integer", "description": "返回多少行样本，默认 10，上限 50"},
			},
			"required": []string{"path"},
		},
	)
	if err != nil {
		return err
	}
	if err := r.Add(peekDef, func(ctx context.Context, args json.RawMessage) (string, error) {
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
	}); err != nil {
		return err
	}

	// ── 创建目录 ──
	mkdirDef, err := ai.FunctionTool("fs_mkdir",
		"创建目录（含多级）。已存在时视为成功。",
		map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": map[string]any{"type": "string", "description": "目录绝对路径"}},
			"required":   []string{"path"},
		})
	if err != nil {
		return err
	}
	if err := r.Add(mkdirDef, func(ctx context.Context, args json.RawMessage) (string, error) {
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
	}); err != nil {
		return err
	}

	// ── 移动 / 重命名 ──
	moveDef, err := ai.FunctionTool("fs_move",
		"移动或重命名文件/目录。目标已存在时会拒绝，不会覆盖。",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"from": map[string]any{"type": "string", "description": "源路径"},
				"to":   map[string]any{"type": "string", "description": "目标路径"},
			},
			"required": []string{"from", "to"},
		})
	if err != nil {
		return err
	}
	if err := r.Add(moveDef, func(ctx context.Context, args json.RawMessage) (string, error) {
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
	}); err != nil {
		return err
	}

	// ── 复制 ──
	copyDef, err := ai.FunctionTool("fs_copy",
		"复制文件或整个目录。目标已存在时会拒绝。",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"from": map[string]any{"type": "string", "description": "源路径"},
				"to":   map[string]any{"type": "string", "description": "目标路径"},
			},
			"required": []string{"from", "to"},
		})
	if err != nil {
		return err
	}
	if err := r.Add(copyDef, func(ctx context.Context, args json.RawMessage) (string, error) {
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
	}); err != nil {
		return err
	}

	// ── 删除（默认进废纸篓）──
	deleteDef, err := ai.FunctionTool("fs_delete",
		"删除文件或目录。默认移到废纸篓（可恢复）；只有用户明确说「永久删除」时才传 permanent=true。",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"paths":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "要删除的路径列表"},
				"permanent": map[string]any{"type": "boolean", "description": "true = 永久删除，不可恢复。默认 false（进废纸篓）"},
			},
			"required": []string{"paths"},
		})
	if err != nil {
		return err
	}
	return r.Add(deleteDef, func(ctx context.Context, args json.RawMessage) (string, error) {
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
		if !ai.DefaultConfirms().Consume(in.ConfirmToken, "fs_delete", string(args)) {
			items, note := deleteConfirmInfo(g, in.Paths, in.Permanent)
			token := ai.DefaultConfirms().Issue("fs_delete", string(args), ai.ConfirmRequest{
				Title: "即将删除以下内容",
				Items: items,
				Note:  note,
			})
			payload, _ := json.Marshal(map[string]any{
				"token": token,
				"title": "即将删除以下内容",
				"items": items,
				"note":  note,
			})
			return ai.ConfirmPrefix + string(payload), nil
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
	})
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
