package excel

// 新一批表格工具：画像、校验、去重、比对、变形、分组汇总、横向合并、
// 拆分、格式转换。
//
// 注册原则（和旧工具一致）：
//   · 每个工具的描述先说「什么时候该用它」，再说参数 —— 模型靠这句话选工具
//   · 任何写文件的操作都不覆盖已有文件，输出路径自动避开重名
//   · 返回值只带计数和少量样本，不把几万行塞进模型上下文

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/klphp/deskkit-excel/internal/ai"
)

// registerTableTools 注册新一批工具。
func registerTableTools(r *ai.Registry) error {
	regs := []func(*ai.Registry) error{
		registerProfileTool,
		registerValidateTool,
		registerDuplicatesTool,
		registerCompareTool,
		registerTransformTool,
		registerGroupTool,
		registerJoinTool,
		registerSplitTool,
		registerConvertTool,
	}
	for _, fn := range regs {
		if err := fn(r); err != nil {
			return err
		}
	}
	return nil
}

// ── 画像 ──

func registerProfileTool(r *ai.Registry) error {
	def, err := ai.FunctionTool("profile_table",
		"Health check a single table: the type of each column, how many empty values and how many distinct values it has, "+
			"sum/average/median of numeric columns, and problems worth flagging (leading zeros, currency symbols, full-width digits, mixed types)."+
			"Call it first when the user asks how the data looks or whether it can be calculated directly.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":  map[string]any{"type": "string", "description": "Absolute path of the file (xlsx or csv)"},
				"sheet": map[string]any{"type": "string", "description": "Sheet name; empty means the first sheet"},
			},
			"required": []string{"path"},
		})
	if err != nil {
		return err
	}
	return r.Add(def, func(ctx context.Context, args json.RawMessage) (string, error) {
		var in struct {
			Path  string `json:"path"`
			Sheet string `json:"sheet"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
		t, err := LoadTable(in.Path, in.Sheet)
		if err != nil {
			return "", err
		}
		profiles := Profile(t, 3)
		return jsonOut(map[string]any{
			"path":    t.Path,
			"sheet":   t.Sheet,
			"rows":    len(t.Rows),
			"columns": len(t.Headers),
			"profile": profiles,
		})
	})
}

// ── 校验 ──

func registerValidateTool(r *ai.Registry) error {
	def, err := ai.FunctionTool("validate_table",
		"Check a table against rules: required but empty, should be unique but duplicated, not a number, not a date, negative values, regex mismatch."+
			"Every issue carries the row number and the original value, so the user can fix them one by one. Use it when the user asks to check the data for problems.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":        map[string]any{"type": "string", "description": "Absolute path of the file"},
				"sheet":       map[string]any{"type": "string", "description": "Sheet name; empty means the first sheet"},
				"required":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Columns that must be filled in"},
				"unique":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Columns that must not repeat (several columns are judged together)"},
				"numeric":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Columns that must be numbers"},
				"dates":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Columns that must be dates"},
				"notNegative": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Columns that must not be negative"},
				"pattern": map[string]any{
					"type": "object", "description": "Regex checks: column name to regular expression",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"maxIssues": map[string]any{"type": "integer", "description": "Maximum issues to return, 50 by default"},
			},
			"required": []string{"path"},
		})
	if err != nil {
		return err
	}
	return r.Add(def, func(ctx context.Context, args json.RawMessage) (string, error) {
		var in struct {
			Path        string            `json:"path"`
			Sheet       string            `json:"sheet"`
			Required    []string          `json:"required"`
			Unique      []string          `json:"unique"`
			Numeric     []string          `json:"numeric"`
			Dates       []string          `json:"dates"`
			NotNegative []string          `json:"notNegative"`
			Pattern     map[string]string `json:"pattern"`
			MaxIssues   int               `json:"maxIssues"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
		t, err := LoadTable(in.Path, in.Sheet)
		if err != nil {
			return "", err
		}
		res, err := Validate(t, ValidateRules{
			Required: in.Required, Unique: in.Unique, Numeric: in.Numeric,
			Dates: in.Dates, NotNegative: in.NotNegative, Pattern: in.Pattern,
			MaxIssues: in.MaxIssues,
		})
		if err != nil {
			return "", err
		}
		return jsonOut(res)
	})
}

// ── 重复行 ──

func registerDuplicatesTool(r *ai.Registry) error {
	def, err := ai.FunctionTool("find_duplicates",
		"Find duplicate rows. You can dedupe on specific columns (an employee id, for example); empty compares whole rows."+
			"Returns how many rows are duplicated plus the first few samples. Use it when the user asks whether anything repeats; do not guess with SQL.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "Absolute path of the file"},
				"sheet":   map[string]any{"type": "string"},
				"columns": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Columns to dedupe on; empty compares whole rows"},
				"limit":   map[string]any{"type": "integer", "description": "How many samples to return, 20 by default"},
			},
			"required": []string{"path"},
		})
	if err != nil {
		return err
	}
	return r.Add(def, func(ctx context.Context, args json.RawMessage) (string, error) {
		var in struct {
			Path    string   `json:"path"`
			Sheet   string   `json:"sheet"`
			Columns []string `json:"columns"`
			Limit   int      `json:"limit"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
		t, err := LoadTable(in.Path, in.Sheet)
		if err != nil {
			return "", err
		}
		rows, total, err := FindDuplicates(t, in.Columns, in.Limit)
		if err != nil {
			return "", err
		}
		return jsonOut(map[string]any{
			"headers":       t.Headers,
			"totalRows":     len(t.Rows),
			"duplicateRows": total,
			"samples":       rows,
			"sampleNote":    "Each sample is the first row of a duplicate group, which shows whether it really is a duplicate",
		})
	})
}

// ── 两表比对 ──

func registerCompareTool(r *ai.Registry) error {
	def, err := ai.FunctionTool("compare_tables",
		"Compare two tables on a key column and report additions, deletions and changes (which row, which column, from what to what)."+
			"Use it when the user asks what changed since last month or how these two files differ."+
			"Given an outputDir, each of the three differences is written to its own file.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"oldPath":        map[string]any{"type": "string", "description": "Path of the old table"},
				"newPath":        map[string]any{"type": "string", "description": "Path of the new table"},
				"oldSheet":       map[string]any{"type": "string"},
				"newSheet":       map[string]any{"type": "string"},
				"keys":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Column used to line up rows (same name on both sides), such as an employee id"},
				"compareColumns": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Columns to compare; empty compares every column the two tables share"},
				"outputDir":      map[string]any{"type": "string", "description": "Export the differences to this folder; empty only returns statistics and samples"},
				"limit":          map[string]any{"type": "integer", "description": "How many samples to return, 20 by default"},
			},
			"required": []string{"oldPath", "newPath", "keys"},
		})
	if err != nil {
		return err
	}
	return r.Add(def, func(ctx context.Context, args json.RawMessage) (string, error) {
		var in struct {
			OldPath        string   `json:"oldPath"`
			NewPath        string   `json:"newPath"`
			OldSheet       string   `json:"oldSheet"`
			NewSheet       string   `json:"newSheet"`
			Keys           []string `json:"keys"`
			CompareColumns []string `json:"compareColumns"`
			OutputDir      string   `json:"outputDir"`
			Limit          int      `json:"limit"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
		oldT, err := LoadTable(in.OldPath, in.OldSheet)
		if err != nil {
			return "", err
		}
		newT, err := LoadTable(in.NewPath, in.NewSheet)
		if err != nil {
			return "", err
		}
		res, err := Compare(oldT, newT, in.Keys, in.CompareColumns, in.Limit)
		if err != nil {
			return "", err
		}
		out := map[string]any{
			"added": res.Added, "removed": res.Removed, "changed": res.Changed, "same": res.Same,
			"addedSamples": res.AddedSamples, "removedSamples": res.RemovedSamples,
			"changedSamples": res.ChangedSamples,
		}
		if in.OutputDir != "" {
			var files []string
			write := func(t *Table, name string) error {
				if t == nil || len(t.Rows) == 0 {
					return nil
				}
				p, err := WriteTable(filepath.Join(in.OutputDir, name), t,
					WriteOptions{BoldHeader: true, AutoWidth: true, NumbersAsNumbers: true})
				if err != nil {
					return err
				}
				files = append(files, p)
				return nil
			}
			if err := write(res.AddedTable, "added.xlsx"); err != nil {
				return "", err
			}
			if err := write(res.RemovedTable, "removed.xlsx"); err != nil {
				return "", err
			}
			if err := write(res.ChangedTable, "changed.xlsx"); err != nil {
				return "", err
			}
			out["files"] = files
		}
		return jsonOut(out)
	})
}

// ── 变形 ──

// transformStep 一步变形。所有字段都可选，按 action 取用。
type transformStep struct {
	Action         string            `json:"action"`
	Columns        []string          `json:"columns,omitempty"`
	Column         string            `json:"column,omitempty"`
	Mapping        map[string]string `json:"mapping,omitempty"`
	Sep            string            `json:"sep,omitempty"`
	Into           []string          `json:"into,omitempty"`
	As             string            `json:"as,omitempty"`
	Expr           string            `json:"expr,omitempty"`
	Parts          []string          `json:"parts,omitempty"`
	Keys           []SortKey         `json:"keys,omitempty"`
	Conditions     []Condition       `json:"conditions,omitempty"`
	MatchAll       bool              `json:"matchAll,omitempty"`
	Trim           bool              `json:"trim,omitempty"`
	CollapseSpaces bool              `json:"collapseSpaces,omitempty"`
	ToHalfWidth    bool              `json:"toHalfWidth,omitempty"`
	Lower          bool              `json:"lower,omitempty"`
	Upper          bool              `json:"upper,omitempty"`
	StripCurrency  bool              `json:"stripCurrency,omitempty"`
	SkipEmpty      bool              `json:"skipEmpty,omitempty"`
}

func registerTransformTool(r *ai.Registry) error {
	actionNames := "select (keep columns) / drop (remove columns) / rename (with mapping) / reorder (reorder columns) / " +
		"split (one column into several by a separator, with column+sep+into) / concat (several columns into one, with columns+sep+as) / " +
		"fillDown (fill empty cells downward, with columns) / transpose (swap rows and columns) / " +
		"normalize (text cleanup, with trim/toHalfWidth/collapseSpaces/stripCurrency/lower/upper) / " +
		"dateParts (year/month/day out of a date, with column+parts) / computed (new calculated column, with expr+as, for example [unit price]*[qty] or round([amount]/3,2)) / " +
		"sort (with keys) / filter (with conditions+matchAll) / dedup (by columns) / dropEmptyRows (delete empty rows)"

	def, err := ai.FunctionTool("transform_table",
		"Reshape one table: keep columns, drop columns, rename, reorder, split a column, join columns, fill down, transpose, normalize text, "+
			"split dates, calculated columns, sort, filter, dedupe and delete empty rows. The steps run in order."+
			"Without an outputPath it only previews (returns the first rows plus row and column counts); a file is written only when it is given."+
			"List of actions:"+actionNames,
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":       map[string]any{"type": "string", "description": "Absolute path of the source file (never modified)"},
				"sheet":      map[string]any{"type": "string"},
				"outputPath": map[string]any{"type": "string", "description": "Output file path; .csv writes CSV, otherwise xlsx. Empty means preview only, no file"},
				"sheetName":  map[string]any{"type": "string", "description": "Sheet name in the output workbook, Result by default"},
				"steps": map[string]any{
					"type": "array", "description": "Reshape steps, run in order",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"action":  map[string]any{"type": "string"},
							"columns": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
							"column":  map[string]any{"type": "string"},
							"mapping": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
							"sep":     map[string]any{"type": "string"},
							"into":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
							"as":      map[string]any{"type": "string"},
							"expr":    map[string]any{"type": "string"},
							"parts":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
							"keys": map[string]any{"type": "array", "items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"column": map[string]any{"type": "string"},
									"desc":   map[string]any{"type": "boolean"},
								},
							}},
							"conditions": map[string]any{"type": "array", "items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"column": map[string]any{"type": "string"},
									"op":     map[string]any{"type": "string", "description": "eq/ne/contains/notContains/gt/ge/lt/le/empty/notEmpty/in"},
									"value":  map[string]any{"type": "string"},
									"values": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
								},
							}},
							"matchAll":       map[string]any{"type": "boolean"},
							"trim":           map[string]any{"type": "boolean"},
							"collapseSpaces": map[string]any{"type": "boolean"},
							"toHalfWidth":    map[string]any{"type": "boolean"},
							"lower":          map[string]any{"type": "boolean"},
							"upper":          map[string]any{"type": "boolean"},
							"stripCurrency":  map[string]any{"type": "boolean"},
							"skipEmpty":      map[string]any{"type": "boolean"},
						},
						"required": []string{"action"},
					},
				},
			},
			"required": []string{"path", "steps"},
		})
	if err != nil {
		return err
	}

	return r.Add(def, func(ctx context.Context, args json.RawMessage) (string, error) {
		var in struct {
			Path       string          `json:"path"`
			Sheet      string          `json:"sheet"`
			OutputPath string          `json:"outputPath"`
			SheetName  string          `json:"sheetName"`
			Steps      []transformStep `json:"steps"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
		t, err := LoadTable(in.Path, in.Sheet)
		if err != nil {
			return "", err
		}
		rowsIn, colsIn := len(t.Rows), len(t.Headers)

		log := make([]map[string]any, 0, len(in.Steps))
		for i, st := range in.Steps {
			beforeRows, beforeCols := len(t.Rows), len(t.Headers)
			if err := applyStep(&t, st); err != nil {
				return "", fmt.Errorf("Step %d (%s) failed: %w", i+1, st.Action, err)
			}
			log = append(log, map[string]any{
				"step": i + 1, "action": st.Action,
				"rows":    fmt.Sprintf("%d→%d", beforeRows, len(t.Rows)),
				"columns": fmt.Sprintf("%d→%d", beforeCols, len(t.Headers)),
			})
		}

		out := map[string]any{
			"rowsIn": rowsIn, "rowsOut": len(t.Rows),
			"columnsIn": colsIn, "columnsOut": len(t.Headers),
			"headers": t.Headers, "steps": log,
		}
		if in.OutputPath == "" {
			out["preview"] = OffsetPaging(t, 0, 10).Rows
			out["note"] = "Preview only, no file was written. Call it again with outputPath once the user confirms."
			return jsonOut(out)
		}
		path, err := resolveOutputPath(filepath.Dir(in.Path), in.OutputPath, in.Path)
		if err != nil {
			return "", err
		}
		written, err := WriteTable(path, t, WriteOptions{
			Sheet: in.SheetName, BoldHeader: true, AutoWidth: true, NumbersAsNumbers: true,
		})
		if err != nil {
			return "", err
		}
		out["output"] = written
		return jsonOut(out)
	})
}

// applyStep 执行一个步骤。
func applyStep(t **Table, st transformStep) error {
	cur := *t
	var err error
	switch strings.ToLower(strings.TrimSpace(st.Action)) {
	case "select":
		*t, err = SelectColumns(cur, st.Columns)
	case "drop":
		*t, err = DropColumns(cur, st.Columns)
	case "rename":
		*t, err = RenameColumns(cur, st.Mapping)
	case "reorder":
		*t, err = ReorderColumns(cur, st.Columns)
	case "split":
		*t, err = SplitColumn(cur, st.Column, st.Sep, st.Into, 0)
	case "concat":
		*t, err = ConcatColumns(cur, st.Columns, st.Sep, st.As, st.SkipEmpty)
	case "filldown":
		*t, err = FillDown(cur, st.Columns)
	case "transpose":
		*t = Transpose(cur)
	case "normalize":
		*t, err = NormalizeText(cur, NormalizeTextOptions{
			Columns: st.Columns, Trim: st.Trim, CollapseSpaces: st.CollapseSpaces,
			ToHalfWidth: st.ToHalfWidth, Lower: st.Lower, Upper: st.Upper, StripCurrency: st.StripCurrency,
		})
	case "dateparts":
		*t, err = DateParts(cur, st.Column, st.Parts)
	case "computed":
		*t, err = ComputedColumn(cur, st.Expr, st.As)
	case "sort":
		*t, err = Sort(cur, st.Keys)
	case "filter":
		*t, err = Filter(cur, st.Conditions, st.MatchAll)
	case "dedup":
		var removed int
		*t, removed, err = Dedup(cur, st.Columns)
		_ = removed
	case "dropemptyrows":
		*t, _ = DropEmptyRows(cur)
	default:
		return fmt.Errorf("Unknown action %q", st.Action)
	}
	return err
}

// ── 分组汇总 ──

func registerGroupTool(r *ai.Registry) error {
	def, err := ai.FunctionTool("group_summary",
		"Group by columns and aggregate without writing SQL: sum, average, max, min, count, distinct count and merged distinct text."+
			"Use it when the user asks to total amounts by department, to count orders per month, or who has the most orders; it is less error-prone than writing SQL."+
			"Given an outputPath it writes a file.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string"},
				"sheet":   map[string]any{"type": "string"},
				"groupBy": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Grouping columns, can be several"},
				"aggregations": map[string]any{
					"type": "array", "description": "Aggregation rules",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"column": map[string]any{"type": "string", "description": "Column to aggregate (may be empty for count)"},
							"func":   map[string]any{"type": "string", "description": "sum/avg/min/max/count/countDistinct/first/last/join"},
							"as":     map[string]any{"type": "string", "description": "Output column name"},
							"sep":    map[string]any{"type": "string", "description": "Separator used by join"},
						},
						"required": []string{"func"},
					},
				},
				"outputPath": map[string]any{"type": "string", "description": "Output path; empty only returns the result (at most 50 rows)"},
			},
			"required": []string{"path", "groupBy"},
		})
	if err != nil {
		return err
	}
	return r.Add(def, func(ctx context.Context, args json.RawMessage) (string, error) {
		var in struct {
			Path       string   `json:"path"`
			Sheet      string   `json:"sheet"`
			GroupBy    []string `json:"groupBy"`
			Aggs       []Agg    `json:"aggregations"`
			OutputPath string   `json:"outputPath"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
		t, err := LoadTable(in.Path, in.Sheet)
		if err != nil {
			return "", err
		}
		res, err := GroupBy(t, in.GroupBy, in.Aggs)
		if err != nil {
			return "", err
		}
		out := map[string]any{
			"groups": res.Groups, "rowsIn": res.RowsIn, "rowsOut": res.RowsOut,
			"headers": res.Table.Headers,
			"rows":    OffsetPaging(res.Table, 0, 50).Rows,
		}
		if in.OutputPath != "" {
			path, err := resolveOutputPath(filepath.Dir(in.Path), in.OutputPath, in.Path)
			if err != nil {
				return "", err
			}
			p, err := WriteTable(path, res.Table, WriteOptions{BoldHeader: true, AutoWidth: true, NumbersAsNumbers: true})
			if err != nil {
				return "", err
			}
			out["output"] = p
		}
		return jsonOut(out)
	})
}

// ── 横向合并 ──

func registerJoinTool(r *ai.Registry) error {
	def, err := ai.FunctionTool("join_tables",
		"Join two tables horizontally: bring the columns of the other table onto the right of this one, matched on a key column (this is VLOOKUP)."+
			"It reports how many rows matched, how many did not, and whether the right-hand keys repeat."+
			"When the right-hand keys repeat it errors out by default (it will not silently take the first one, which would undercount money); "+
			"if you really need a value, pass onDuplicate explicitly."+
			"Given an outputPath it writes a file; otherwise it only returns statistics and a preview.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"leftPath":    map[string]any{"type": "string", "description": "Main table (its rows are kept)"},
				"rightPath":   map[string]any{"type": "string", "description": "Table whose columns are brought in"},
				"leftSheet":   map[string]any{"type": "string"},
				"rightSheet":  map[string]any{"type": "string"},
				"leftKey":     map[string]any{"type": "string", "description": "Matching column in the main table"},
				"rightKey":    map[string]any{"type": "string", "description": "Matching column in the other table (may have a different name from leftKey)"},
				"bring":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Which columns to bring in; empty means everything except the key"},
				"how":         map[string]any{"type": "string", "description": "left (default, keep every main-table row) / inner (matched rows only) / right (also keep unmatched rows)"},
				"prefix":      map[string]any{"type": "string", "description": "Prefix for the brought-in column names, right. by default"},
				"onDuplicate": map[string]any{"type": "string", "description": "What to do when right-hand keys repeat: error (default, report it and require unique keys first) / first (take the first row) / sum (add up numeric columns) / all (expand to rows). With money involved never use first; use sum or all"},
				"outputPath":  map[string]any{"type": "string", "description": "Output path; empty previews only"},
			},
			"required": []string{"leftPath", "rightPath", "leftKey", "rightKey"},
		})
	if err != nil {
		return err
	}
	return r.Add(def, func(ctx context.Context, args json.RawMessage) (string, error) {
		var in struct {
			JoinOptions
			OutputPath string `json:"outputPath"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
		res, err := Join(in.JoinOptions)
		if err != nil {
			return "", err
		}
		out := map[string]any{
			"leftRows": res.LeftRows, "rightRows": res.RightRows,
			"matched": res.Matched, "unmatched": res.Unmatched,
			"rightDuplicateKeys": res.RightDupKeys, "duplicateSamples": res.DuplicateSamples,
			"outputRows": res.OutputRows, "headers": res.Table.Headers,
			"onDuplicate": res.OnDuplicate,
			"preview":     OffsetPaging(res.Table, 0, 10).Rows,
		}
		if res.Warning != "" {
			out["warning"] = res.Warning
		}
		if in.OutputPath != "" {
			// 相对路径按主表所在目录解析；绝对路径放行；不许覆盖左/右表
			path, err := resolveOutputPath(filepath.Dir(in.LeftPath), in.OutputPath, in.LeftPath, in.RightPath)
			if err != nil {
				return "", err
			}
			p, err := WriteTable(path, res.Table, WriteOptions{BoldHeader: true, AutoWidth: true, NumbersAsNumbers: true})
			if err != nil {
				return "", err
			}
			out["output"] = p
		}
		return jsonOut(out)
	})
}

// ── 拆分 ──

func registerSplitTool(r *ai.Registry) error {
	def, err := ai.FunctionTool("split_table",
		"Split or combine sheets: one file per distinct value of a column (one file per department), "+
			"one file per sheet inside a workbook, or several sheets stacked vertically into one table."+
			"Use it when the user asks to split by department or to combine these sheets into one.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":       map[string]any{"type": "string", "description": "Absolute path of the file"},
				"sheet":      map[string]any{"type": "string"},
				"mode":       map[string]any{"type": "string", "description": "byColumn (split by column value) / bySheet (one file per sheet) / stack (stack several sheets into one)"},
				"column":     map[string]any{"type": "string", "description": "Column to split on when mode=byColumn"},
				"outputDir":  map[string]any{"type": "string", "description": "Output folder"},
				"outputPath": map[string]any{"type": "string", "description": "Output file path when mode=stack"},
				"ext":        map[string]any{"type": "string", "description": "Extension for the split files, .xlsx by default, .csv is allowed"},
				"maxFiles":   map[string]any{"type": "integer", "description": "Maximum number of files to split into, 100 by default; above that it refuses and reports"},
			},
			"required": []string{"path", "mode"},
		})
	if err != nil {
		return err
	}
	return r.Add(def, func(ctx context.Context, args json.RawMessage) (string, error) {
		var in struct {
			Path       string `json:"path"`
			Sheet      string `json:"sheet"`
			Mode       string `json:"mode"`
			Column     string `json:"column"`
			OutputDir  string `json:"outputDir"`
			OutputPath string `json:"outputPath"`
			Ext        string `json:"ext"`
			MaxFiles   int    `json:"maxFiles"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
		switch strings.ToLower(strings.TrimSpace(in.Mode)) {
		case "bycolumn":
			if in.Column == "" {
				return "", fmt.Errorf("Splitting by column requires column")
			}
			t, err := LoadTable(in.Path, in.Sheet)
			if err != nil {
				return "", err
			}
			out, err := SplitByColumn(t, in.Column, in.OutputDir, in.Ext, in.MaxFiles)
			if err != nil {
				return "", err
			}
			return jsonOut(map[string]any{"files": out, "count": len(out)})
		case "bysheet":
			out, err := SplitSheets(in.Path, in.OutputDir)
			if err != nil {
				return "", err
			}
			return jsonOut(map[string]any{"files": out, "count": len(out)})
		case "stack":
			tables, err := ReadAllSheets(in.Path)
			if err != nil {
				return "", err
			}
			merged := StackSheets(tables)
			target := in.OutputPath
			if target == "" {
				dir := in.OutputDir
				if dir == "" {
					dir = filepath.Join(filepath.Dir(in.Path), "deskkit-out")
				}
				target = filepath.Join(dir, "sheets-merged.xlsx")
			}
			base := in.OutputDir
			if base == "" {
				base = filepath.Dir(in.Path)
			}
			path, err := resolveOutputPath(base, target, in.Path)
			if err != nil {
				return "", err
			}
			p, err := WriteTable(path, merged, WriteOptions{BoldHeader: true, AutoWidth: true, NumbersAsNumbers: true})
			if err != nil {
				return "", err
			}
			return jsonOut(map[string]any{
				"sheets": len(tables), "rows": len(merged.Rows),
				"headers": merged.Headers, "output": p,
			})
		}
		return "", fmt.Errorf("mode must be byColumn / bySheet / stack")
	})
}

// ── 格式转换 ──

func registerConvertTool(r *ai.Registry) error {
	def, err := ai.FunctionTool("convert_table",
		"Convert table formats: xlsx to csv, csv to xlsx, or export one sheet on its own."+
			"CSV output can use GBK encoding (older systems only accept it, and garbled text is usually a wrong encoding).",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":       map[string]any{"type": "string", "description": "Absolute path of the source file"},
				"sheet":      map[string]any{"type": "string"},
				"outputPath": map[string]any{"type": "string", "description": "Output file path; the extension decides the format (.csv or .xlsx)"},
				"encoding":   map[string]any{"type": "string", "description": "CSV output only: utf8 (default, with BOM) or gbk"},
			},
			"required": []string{"path", "outputPath"},
		})
	if err != nil {
		return err
	}
	return r.Add(def, func(ctx context.Context, args json.RawMessage) (string, error) {
		var in ConvertOptions
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
		path, rows, err := Convert(in)
		if err != nil {
			return "", err
		}
		return jsonOut(map[string]any{"output": path, "rows": rows})
	})
}

// jsonOut 统一输出格式。
func jsonOut(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
