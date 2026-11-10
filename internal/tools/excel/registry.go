package excel

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/klphp/deskkit-excel/internal/ai"
)

// Register 把这些能力注册成 AI 可调用的工具。
//
// 为什么 preview 和 run 是两个工具而不是一个带 dryRun 参数的工具：
// 模型很容易「忘了」先预览就直接写文件。拆成两个之后，必须依次调用，
// 而这个顺序也正好是界面上按钮的顺序（先看变更，再执行）。
func Register(r *ai.Registry) error {
	scanDef, err := ai.FunctionTool("scan_excel_folder",
		"Scan a folder and list the Excel files in it, with each file's headers and row count. Call it before merging or cleaning so you know what the data looks like.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"dir": map[string]any{"type": "string", "description": "Absolute path of the folder"},
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
		"Preview a merge: only reports how many rows the result would have, which columns would be dropped and how many duplicates there are. Writes nothing. You must call it before merging, and tell the user the result.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"dir":      map[string]any{"type": "string", "description": "Absolute path of the folder"},
				"dedupKey": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Column names to dedupe by, optional"},
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
		"Run the merge and write a new file (the original files are never overwritten). Only call it after the user explicitly agrees, and only after preview_merge.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"dir":      map[string]any{"type": "string", "description": "Absolute path of the folder"},
				"dedupKey": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
			"required": []string{"dir"},
		})
	if err != nil {
		return err
	}
	if err := r.Add(runDef, func(ctx context.Context, args json.RawMessage) (string, error) {
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
	}); err != nil {
		return err
	}

	// ── 清洗 ──
	// 同样拆成 preview / run：清洗会删行，先看统计再动手。
	cleanParams := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"dir":            map[string]any{"type": "string", "description": "Absolute path of the folder"},
			"dropBlankRows":  map[string]any{"type": "boolean", "description": "Delete rows that are entirely empty"},
			"dropDuplicates": map[string]any{"type": "boolean", "description": "Delete duplicate rows"},
			"dedupKey":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Columns used to decide duplicates; empty means compare whole rows"},
			"trimSpaces":     map[string]any{"type": "boolean", "description": "Trim leading and trailing spaces in cells"},
			"normalizeDates": map[string]any{"type": "boolean", "description": "Normalize date formats"},
			"dateFormat":     map[string]any{"type": "string", "description": "YYYY-MM-DD or YYYY/MM/DD"},
			"numbersAsText":  map[string]any{"type": "boolean", "description": "Convert numbers stored as text into numeric values (leading zeros are left alone)"},
		},
		"required": []string{"dir"},
	}

	previewClean, err := ai.FunctionTool("preview_clean",
		"Preview a clean: only counts how many empty rows and duplicates would be deleted and how many dates and numbers would be fixed. Writes no file. You must call it before cleaning and report the result to the user.",
		cleanParams)
	if err != nil {
		return err
	}
	if err := r.Add(previewClean, func(ctx context.Context, args json.RawMessage) (string, error) {
		opt, err := cleanOptionsFrom(args)
		if err != nil {
			return "", err
		}
		opt.DryRun = true
		res, err := Clean(ctx, opt)
		if err != nil {
			return "", err
		}
		raw, _ := json.Marshal(map[string]any{
			"files": res.Files, "rowsIn": res.RowsIn, "rowsOut": res.RowsOut,
			"blankRows": res.BlankRows, "duplicateRows": res.Duplicate,
			"trimmedCells": res.Trimmed, "dateFixed": res.DateFixed,
			"numberFixed": res.NumFixed, "warnings": res.Warnings, "skipped": res.Skipped,
		})
		return string(raw), nil
	}); err != nil {
		return err
	}

	runClean, err := ai.FunctionTool("run_clean",
		"Run the clean and write new files (one -clean.xlsx per input, originals are not overwritten). Only call it after the user agrees, and only after preview_clean.",
		cleanParams)
	if err != nil {
		return err
	}
	if err := r.Add(runClean, func(ctx context.Context, args json.RawMessage) (string, error) {
		opt, err := cleanOptionsFrom(args)
		if err != nil {
			return "", err
		}
		res, err := Clean(ctx, opt)
		if err != nil {
			return "", err
		}
		raw, _ := json.Marshal(map[string]any{
			"outputDir": res.OutputDir, "files": res.Files, "rowsOut": res.RowsOut,
			"blankRows": res.BlankRows, "duplicateRows": res.Duplicate,
		})
		return string(raw), nil
	}); err != nil {
		return err
	}

	// ── SQL 层：让 AI 能组合出任意操作 ──
	//
	// 合并 / 清洗只能覆盖几种固定玩法，但用户想做的事是无穷的。把每张表
	// 装进内存 SQLite 之后，AI 只要写一条 SELECT 就能做筛选、分组、关联、
	// 计算列、排名…… 我们不需要为每一种玩法再写一个工具。
	if err := registerQueryTools(r); err != nil {
		return err
	}

	// 单文件为主的「精细化」工具：画像、校验、去重、比对、变形、分组汇总、
	// 横向合并、拆分、格式转换。用户真正天天做的操作都在这里，
	// 不必每次都绕到 SQL 上去。
	if err := registerTableTools(r); err != nil {
		return err
	}

	// 第一版界面走的是单文件工具（不做批量），但目录版工具也留着：
	// 模型在用户明确说「这个文件夹里所有表」时仍然能用。
	return registerFileTools(r)
}

func registerQueryTools(r *ai.Registry) error {
	listDef, err := ai.FunctionTool("list_tables",
		"Load every sheet in the folder into an in-memory database and return the queryable table names, column names and row counts. Call it before any SQL query to get the table names.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"dir": map[string]any{"type": "string", "description": "Absolute path of the folder"},
			},
			"required": []string{"dir"},
		})
	if err != nil {
		return err
	}
	if err := r.Add(listDef, func(ctx context.Context, args json.RawMessage) (string, error) {
		var in struct {
			Dir string `json:"dir"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
		db, cat, err := LoadCatalog(ctx, in.Dir)
		if err != nil {
			return "", err
		}
		defer db.Close()
		raw, _ := json.Marshal(map[string]any{
			"tables": cat.Tables,
			"note":   cat.Note,
			"hint":   "Column names keep the original headers (any language works; wrap them in double quotes in SQL). Money columns are already numeric, so SUM/AVG work directly.",
		})
		return string(raw), nil
	}); err != nil {
		return err
	}

	queryParams := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"dir":   map[string]any{"type": "string", "description": "Absolute path of the folder"},
			"sql":   map[string]any{"type": "string", "description": "Read-only SQL (SELECT / WITH). Wrap table names and non-ASCII column names in double quotes."},
			"limit": map[string]any{"type": "integer", "description": "Maximum rows to return, 200 by default"},
		},
		"required": []string{"dir", "sql"},
	}

	queryDef, err := ai.FunctionTool("query_sheets",
		"Run read-only SQL against the tables in the folder and return the result. Use it for questions like which department spent the most, or how many duplicate voucher numbers there are. It writes no files.",
		queryParams)
	if err != nil {
		return err
	}
	if err := r.Add(queryDef, func(ctx context.Context, args json.RawMessage) (string, error) {
		var in struct {
			Dir   string `json:"dir"`
			SQL   string `json:"sql"`
			Limit int    `json:"limit"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
		db, cat, err := LoadCatalog(ctx, in.Dir)
		if err != nil {
			return "", err
		}
		defer db.Close()
		res, err := Query(ctx, db, in.SQL, in.Limit)
		if err != nil {
			return "", err
		}
		// 装载时补过表头 / 改过重名列 → 必须在返回体里说一声
		res.Note = strings.Join(nonEmpty(res.Note, cat.Note), "；")
		raw, _ := json.Marshal(res)
		return string(raw), nil
	}); err != nil {
		return err
	}

	writeDef, err := ai.FunctionTool("write_query_result",
		"Write the SQL result to a new file (the originals are untouched). Only call it after the user has seen the query_sheets result and agreed. A name ending in .csv writes CSV, anything else writes xlsx.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"dir":       map[string]any{"type": "string", "description": "Absolute path of the input folder"},
				"sql":       map[string]any{"type": "string", "description": "Read-only SQL (SELECT / WITH)"},
				"outputDir": map[string]any{"type": "string", "description": "Output folder; empty means <dir>/deskkit-out"},
				"fileName":  map[string]any{"type": "string", "description": "Output file name, name only with no path (use outputDir to change folders), for example department-summary.xlsx"},
			},
			"required": []string{"dir", "sql"},
		})
	if err != nil {
		return err
	}
	return r.Add(writeDef, func(ctx context.Context, args json.RawMessage) (string, error) {
		var in struct {
			Dir       string `json:"dir"`
			SQL       string `json:"sql"`
			OutputDir string `json:"outputDir"`
			FileName  string `json:"fileName"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
		outDir := in.OutputDir
		if outDir == "" {
			outDir = fmt.Sprintf("%s/deskkit-out", in.Dir)
		}
		db, cat, err := LoadCatalog(ctx, in.Dir)
		if err != nil {
			return "", err
		}
		defer db.Close()
		// 把目录里的源文件都列为「不许覆盖」：模型完全可能照着用户的话
		// 说「就存回原目录、名字叫 销售.csv」，那会直接替换掉用户的原始表格。
		protect := make([]string, 0, len(cat.Tables))
		for _, t := range cat.Tables {
			protect = append(protect, filepath.Join(in.Dir, t.File))
		}
		path, rows, err := WriteQuery(ctx, db, in.SQL, outDir, in.FileName, protect...)
		if err != nil {
			return "", err
		}
		raw, _ := json.Marshal(map[string]any{"output": path, "rows": rows})
		return string(raw), nil
	})
}

// cleanOptionsFrom 从工具参数解析清洗选项。
func cleanOptionsFrom(args json.RawMessage) (CleanOptions, error) {
	var in struct {
		Dir            string   `json:"dir"`
		DropBlankRows  bool     `json:"dropBlankRows"`
		DropDuplicates bool     `json:"dropDuplicates"`
		DedupKey       []string `json:"dedupKey"`
		TrimSpaces     bool     `json:"trimSpaces"`
		NormalizeDates bool     `json:"normalizeDates"`
		DateFormat     string   `json:"dateFormat"`
		NumbersAsText  bool     `json:"numbersAsText"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return CleanOptions{}, err
	}
	return CleanOptions{
		Dir: in.Dir, DropBlankRows: in.DropBlankRows, DropDuplicates: in.DropDuplicates,
		DedupKey: in.DedupKey, TrimSpaces: in.TrimSpaces, NormalizeDates: in.NormalizeDates,
		DateFormat: in.DateFormat, NumbersAsText: in.NumbersAsText,
	}, nil
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
