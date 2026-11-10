package excel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/klphp/deskkit-excel/internal/ai"
)

// registerFileTools 注册单文件工具（第一版主力）。
//
// 和目录版的区别不只是「少一个循环」：单文件版的输出必须落在源文件旁边、
// 同名带时间戳，而且绝不碰同目录下的其它文件。
func registerFileTools(r *ai.Registry) error {
	// ── 看结构 ──
	inspectDef, err := ai.FunctionTool("inspect_file",
		"Open one Excel file and return the name, columns and row count of every sheet, plus where the output would be written. Call it before any processing.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "Absolute path of the file (.xlsx / .xlsm)"},
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
			"note":   info.Note,
			"output": info.Output,
			"hint":   "Column names keep the original headers; wrap them in double quotes in SQL. Money columns are already numeric, so SUM works. Results are written to the path given by output (the original file is never changed).",
		})
		return string(raw), nil
	}); err != nil {
		return err
	}

	// ── 只读查询（预览）──
	queryParams := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":  map[string]any{"type": "string", "description": "Absolute path of the file"},
			"sql":   map[string]any{"type": "string", "description": "Read-only SQL (SELECT / WITH)"},
			"limit": map[string]any{"type": "integer", "description": "Maximum rows to return, 100 by default"},
		},
		"required": []string{"path", "sql"},
	}
	queryDef, err := ai.FunctionTool("query_file",
		"Run read-only SQL against the file and return the result, to check what the summary the user wants looks like. It writes no files.",
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
		db, info, err := LoadWorkbookFile(ctx, in.Path)
		if err != nil {
			return "", err
		}
		defer db.Close()
		res, err := Query(ctx, db, in.SQL, in.Limit)
		if err != nil {
			return "", err
		}
		// 装载时补过表头 / 改过重名列 → 必须在返回体里说一声
		res.Note = strings.Join(nonEmpty(res.Note, info.Note), "；")
		raw, _ := json.Marshal(res)
		return string(raw), nil
	}); err != nil {
		return err
	}

	// ── 写结果（原文件旁边、同名带时间戳）──
	writeDef, err := ai.FunctionTool("write_file_result",
		"Write the SQL result to a new file in the same folder as the original, with the same name plus a timestamp; the original is untouched. Call it after the user has seen the query_file result and agreed.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "Absolute path of the source file"},
				"sql":  map[string]any{"type": "string", "description": "Read-only SQL (SELECT / WITH)"},
				"suffix": map[string]any{
					"type":        "string",
					"description": "The middle part of the file name (optional). Defaults to the current timestamp; use something like department-summary to describe what was done.",
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
		"Clean one file: drop empty rows, dedupe, trim spaces, unify dates, convert text numbers to numeric. The result goes next to the original in a file with the same name plus a timestamp.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":           map[string]any{"type": "string", "description": "Absolute path of the source file"},
				"dropBlankRows":  map[string]any{"type": "boolean"},
				"dropDuplicates": map[string]any{"type": "boolean"},
				"dedupKey":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Columns to dedupe on; empty means whole rows"},
				"trimSpaces":     map[string]any{"type": "boolean"},
				"normalizeDates": map[string]any{"type": "boolean"},
				"numbersAsText":  map[string]any{"type": "boolean"},
				"dryRun":         map[string]any{"type": "boolean", "description": "true counts only and writes no file"},
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
