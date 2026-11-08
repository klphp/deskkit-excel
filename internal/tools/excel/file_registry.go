package excel

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/klphp/excel-batch-tool/internal/ai"
)

// registerFileTools 注册单文件工具（第一版主力）。
//
// 和目录版的区别不只是「少一个循环」：单文件版的输出必须落在源文件旁边、
// 同名带时间戳，而且绝不碰同目录下的其它文件。
func registerFileTools(r *ai.Registry) error {
	// ── 看结构 ──
	inspectDef, err := ai.FunctionTool("inspect_file",
		"打开一个 Excel 文件，返回里面每张工作表的表名、列名、行数，以及输出会写到哪里。做任何处理前先调用它。",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "文件的绝对路径（.xlsx / .xlsm）"},
			},
			"required": []string{"path"},
		})
	if err != nil {
		return err
	}
	if err := r.Add(inspectDef, func(ctx context.Context, args json.RawMessage) (string, error) {
		var in struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
		db, info, err := LoadWorkbookFile(ctx, in.Path)
		if err != nil {
			return "", err
		}
		defer db.Close()

		// 顺手给出每张表前几行，模型不用再问一次就知道数据长什么样
		type sample struct {
			Table   string     `json:"table"`
			Columns []string   `json:"columns"`
			Rows    int        `json:"rows"`
			Head    [][]string `json:"head"`
		}
		var samples []sample
		for _, t := range info.Tables {
			res, e := Query(ctx, db, fmt.Sprintf(`SELECT * FROM "%s" LIMIT 5`, t.Table), 5)
			if e != nil {
				continue
			}
			samples = append(samples, sample{Table: t.Table, Columns: t.Columns, Rows: t.Rows, Head: res.Rows})
		}

		raw, _ := json.Marshal(map[string]any{
			"file":   info.File,
			"tables": samples,
			"output": info.Output,
			"hint":   "列名按原表头保留，写 SQL 时用双引号包住；金额列已转成数字，可直接 SUM。结果写到 output 指定的路径（原文件不会改）。",
		})
		return string(raw), nil
	}); err != nil {
		return err
	}

	// ── 只读查询（预览）──
	queryParams := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":  map[string]any{"type": "string", "description": "文件绝对路径"},
			"sql":   map[string]any{"type": "string", "description": "只读 SQL（SELECT / WITH）"},
			"limit": map[string]any{"type": "integer", "description": "最多返回多少行，默认 100"},
		},
		"required": []string{"path", "sql"},
	}
	queryDef, err := ai.FunctionTool("query_file",
		"对文件执行只读 SQL 并返回结果，用来验证「我要的汇总长什么样」。不写任何文件。",
		queryParams)
	if err != nil {
		return err
	}
	if err := r.Add(queryDef, func(ctx context.Context, args json.RawMessage) (string, error) {
		var in struct {
			Path  string `json:"path"`
			SQL   string `json:"sql"`
			Limit int    `json:"limit"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
		db, _, err := LoadWorkbookFile(ctx, in.Path)
		if err != nil {
			return "", err
		}
		defer db.Close()
		res, err := Query(ctx, db, in.SQL, in.Limit)
		if err != nil {
			return "", err
		}
		raw, _ := json.Marshal(res)
		return string(raw), nil
	}); err != nil {
		return err
	}

	// ── 写结果（原文件旁边、同名带时间戳）──
	writeDef, err := ai.FunctionTool("write_file_result",
		"把 SQL 结果写成新文件：与原文件同目录、同名、带时间戳，原文件不动。用户看过 query_file 的结果并同意后调用。",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "源文件绝对路径"},
				"sql":  map[string]any{"type": "string", "description": "只读 SQL（SELECT / WITH）"},
				"suffix": map[string]any{
					"type":        "string",
					"description": "文件名中间那段（可选）。默认用当前时间戳；需要表达处理内容时可以写「部门汇总」之类。",
				},
			},
			"required": []string{"path", "sql"},
		})
	if err != nil {
		return err
	}
	if err := r.Add(writeDef, func(ctx context.Context, args json.RawMessage) (string, error) {
		var in struct {
			Path   string `json:"path"`
			SQL    string `json:"sql"`
			Suffix string `json:"suffix"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
		db, _, err := LoadWorkbookFile(ctx, in.Path)
		if err != nil {
			return "", err
		}
		defer db.Close()

		suffix := sanitizeSuffix(in.Suffix)
		path, rows, err := QueryToFile(ctx, db, in.Path, in.SQL, suffix)
		if err != nil {
			return "", err
		}
		raw, _ := json.Marshal(map[string]any{"output": path, "rows": rows})
		return string(raw), nil
	}); err != nil {
		return err
	}

	// ── 清洗（单文件）──
	cleanDef, err := ai.FunctionTool("clean_file",
		"清洗单个文件：去空行、去重、去首尾空白、统一日期、文本数字转数值。结果写到原文件旁边的同名带时间戳文件。",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":           map[string]any{"type": "string", "description": "源文件绝对路径"},
				"dropBlankRows":  map[string]any{"type": "boolean"},
				"dropDuplicates": map[string]any{"type": "boolean"},
				"dedupKey":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "按哪些列判重；留空按整行"},
				"trimSpaces":     map[string]any{"type": "boolean"},
				"normalizeDates": map[string]any{"type": "boolean"},
				"numbersAsText":  map[string]any{"type": "boolean"},
				"dryRun":         map[string]any{"type": "boolean", "description": "true 只统计不写文件"},
			},
			"required": []string{"path"},
		})
	if err != nil {
		return err
	}
	return r.Add(cleanDef, func(ctx context.Context, args json.RawMessage) (string, error) {
		var in struct {
			Path           string   `json:"path"`
			DropBlankRows  bool     `json:"dropBlankRows"`
			DropDuplicates bool     `json:"dropDuplicates"`
			DedupKey       []string `json:"dedupKey"`
			TrimSpaces     bool     `json:"trimSpaces"`
			NormalizeDates bool     `json:"normalizeDates"`
			NumbersAsText  bool     `json:"numbersAsText"`
			DryRun         bool     `json:"dryRun"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
		res, err := CleanFile(ctx, CleanFileOptions{
			Path:           in.Path,
			DropBlankRows:  in.DropBlankRows,
			DropDuplicates: in.DropDuplicates,
			DedupKey:       in.DedupKey,
			TrimSpaces:     in.TrimSpaces,
			NormalizeDates: in.NormalizeDates,
			NumbersAsText:  in.NumbersAsText,
			DryRun:         in.DryRun,
		})
		if err != nil {
			return "", err
		}
		raw, _ := json.Marshal(res)
		return string(raw), nil
	})
}
