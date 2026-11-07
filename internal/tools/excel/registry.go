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
			"dir":            map[string]any{"type": "string", "description": "目录的绝对路径"},
			"dropBlankRows":  map[string]any{"type": "boolean", "description": "删除整行为空的行"},
			"dropDuplicates": map[string]any{"type": "boolean", "description": "删除重复行"},
			"dedupKey":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "按哪些列判断重复；留空按整行"},
			"trimSpaces":     map[string]any{"type": "boolean", "description": "去掉单元格首尾空白"},
			"normalizeDates": map[string]any{"type": "boolean", "description": "统一日期格式"},
			"dateFormat":     map[string]any{"type": "string", "description": "YYYY-MM-DD 或 YYYY/MM/DD"},
			"numbersAsText":  map[string]any{"type": "boolean", "description": "把被存成文本的数字转成数值（跳前导零）"},
		},
		"required": []string{"dir"},
	}

	previewClean, err := ai.FunctionTool("preview_clean",
		"预览清洗：只统计会删掉多少空行、多少重复行、修正多少日期与数字，不写文件。清洗前必须先调用并汇报给用户。",
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
		"执行清洗并写出新文件（每个输入生成一个 -clean.xlsx，不覆盖原文件）。只在用户同意后调用，且必须先调用过 preview_clean。",
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

	return nil
}

func registerQueryTools(r *ai.Registry) error {
	listDef, err := ai.FunctionTool("list_tables",
		"把目录里的每个工作表载入内存数据库，返回可查询的表名、列名与行数。做任何 SQL 查询之前先调用它拿到表名。",
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
			"hint":   "列名按原表头保留（中文可用，写 SQL 时用双引号包住）。金额列已转成数字，可直接 SUM/AVG。",
		})
		return string(raw), nil
	}); err != nil {
		return err
	}

	queryParams := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"dir":   map[string]any{"type": "string", "description": "目录的绝对路径"},
			"sql":   map[string]any{"type": "string", "description": "只读 SQL（SELECT / WITH）。表名和中文列名用双引号包住。"},
			"limit": map[string]any{"type": "integer", "description": "最多返回多少行，默认 200"},
		},
		"required": []string{"dir", "sql"},
	}

	queryDef, err := ai.FunctionTool("query_sheets",
		"对目录里的表格执行只读 SQL 并返回结果。用来回答「哪个部门花得最多」「有几条重复的凭证号」这类问题。不会写任何文件。",
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
		db, _, err := LoadCatalog(ctx, in.Dir)
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

	writeDef, err := ai.FunctionTool("write_query_result",
		"把 SQL 结果写成新的 xlsx 文件（原文件不动）。只在用户看过 query_sheets 的结果并同意后调用。",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"dir":       map[string]any{"type": "string", "description": "输入目录的绝对路径"},
				"sql":       map[string]any{"type": "string", "description": "只读 SQL（SELECT / WITH）"},
				"outputDir": map[string]any{"type": "string", "description": "输出目录；留空用 <dir>/deskkit-out"},
				"fileName":  map[string]any{"type": "string", "description": "输出文件名，建议描述内容，如「部门汇总.xlsx」"},
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
		db, _, err := LoadCatalog(ctx, in.Dir)
		if err != nil {
			return "", err
		}
		defer db.Close()
		path, rows, err := WriteQuery(ctx, db, in.SQL, outDir, in.FileName)
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
