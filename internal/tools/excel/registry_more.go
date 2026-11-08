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

	"github.com/klphp/excel-batch-tool/internal/ai"
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
		"给一张表做「体检」：每列是什么类型、有多少空值、有多少不重复的值、"+
			"数值列的合计/平均/中位数、以及值得警惕的问题（前导零、货币符号、全角数字、类型混杂）。"+
			"用户问「这表数据怎么样」「能不能直接算」时先调它。",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":  map[string]any{"type": "string", "description": "文件绝对路径（xlsx 或 csv）"},
				"sheet": map[string]any{"type": "string", "description": "工作表名，留空取第一个"},
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
		"按规则检查一张表：必填为空、该唯一的有重复、不是数字、不是日期、出现负数、不符合正则。"+
			"每条问题都带行号和原值，用户能一条条去改。用户说「检查一下有没有问题」时用它。",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":        map[string]any{"type": "string", "description": "文件绝对路径"},
				"sheet":       map[string]any{"type": "string", "description": "工作表名，留空取第一个"},
				"required":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "必须填的列"},
				"unique":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "不允许重复的列（多个列合起来判重）"},
				"numeric":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "必须是数字的列"},
				"dates":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "必须是日期的列"},
				"notNegative": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "不允许负数的列"},
				"pattern": map[string]any{
					"type": "object", "description": "正则校验：列名 → 正则表达式",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"maxIssues": map[string]any{"type": "integer", "description": "最多返回多少条，默认 50"},
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
		"找出重复行。可以按指定列判重（比如只看工号），留空则整行比较。"+
			"返回重复了多少行以及前若干条样本。用户问「有没有重复的」时用它，不要自己写 SQL 猜。",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "文件绝对路径"},
				"sheet":   map[string]any{"type": "string"},
				"columns": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "判重依据的列；留空则整行比较"},
				"limit":   map[string]any{"type": "integer", "description": "返回多少条样本，默认 20"},
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
			"sampleNote":    "样本是每组重复里的第一条，可用来判断是不是真的重复",
		})
	})
}

// ── 两表比对 ──

func registerCompareTool(r *ai.Registry) error {
	def, err := ai.FunctionTool("compare_tables",
		"按 key 列比对两张表，给出新增、删除、修改（哪一行哪一列从什么变成什么）。"+
			"用户说「这个月和上个月有什么变化」「这两份有什么区别」时用它。"+
			"给了 outputDir 会把三份差异各写一个文件。",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"oldPath":        map[string]any{"type": "string", "description": "旧表路径"},
				"newPath":        map[string]any{"type": "string", "description": "新表路径"},
				"oldSheet":       map[string]any{"type": "string"},
				"newSheet":       map[string]any{"type": "string"},
				"keys":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "用来对齐行的列（两边同名），如工号"},
				"compareColumns": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "要比对的列；留空则比两表共有的所有列"},
				"outputDir":      map[string]any{"type": "string", "description": "把差异导出到这个目录；留空只返回统计和样本"},
				"limit":          map[string]any{"type": "integer", "description": "返回多少条样本，默认 20"},
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
			if err := write(res.AddedTable, "新增.xlsx"); err != nil {
				return "", err
			}
			if err := write(res.RemovedTable, "删除.xlsx"); err != nil {
				return "", err
			}
			if err := write(res.ChangedTable, "修改明细.xlsx"); err != nil {
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
	actionNames := "select(只保留列) / drop(去掉列) / rename(改名,配 mapping) / reorder(重排列) / " +
		"split(一列按分隔符拆成多列,配 column+sep+into) / concat(多列拼一列,配 columns+sep+as) / " +
		"fillDown(向下填充空值,配 columns) / transpose(行列转置) / " +
		"normalize(文本规范化,配 trim/toHalfWidth/collapseSpaces/stripCurrency/lower/upper) / " +
		"dateParts(从日期取年月日,配 column+parts) / computed(新增计算列,配 expr+as,例如 [单价]*[数量] 或 round([金额]/3,2)) / " +
		"sort(排序,配 keys) / filter(筛选,配 conditions+matchAll) / dedup(按 columns 去重) / dropEmptyRows(删空行)"

	def, err := ai.FunctionTool("transform_table",
		"对一张表做变形：选列、丢列、改名、重排、拆列、合列、向下填充、转置、文本规范化、"+
			"日期拆分、计算列、排序、筛选、去重、删空行。steps 按顺序依次执行。"+
			"不给 outputPath 时只预览（返回前几行和行数列数），给了才写文件。"+
			"动作清单："+actionNames,
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":       map[string]any{"type": "string", "description": "源文件绝对路径（不会被修改）"},
				"sheet":      map[string]any{"type": "string"},
				"outputPath": map[string]any{"type": "string", "description": "输出文件路径；.csv 结尾输出 CSV，否则 xlsx。留空=只预览不写"},
				"sheetName":  map[string]any{"type": "string", "description": "输出工作簿里的表名，默认「结果」"},
				"steps": map[string]any{
					"type": "array", "description": "变形步骤，按顺序执行",
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
				return "", fmt.Errorf("第 %d 步 %s 失败: %w", i+1, st.Action, err)
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
			out["note"] = "只预览，没有写文件。确认后给 outputPath 再调一次。"
			return jsonOut(out)
		}
		path, err := WriteTable(in.OutputPath, t, WriteOptions{
			Sheet: in.SheetName, BoldHeader: true, AutoWidth: true, NumbersAsNumbers: true,
		})
		if err != nil {
			return "", err
		}
		out["output"] = path
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
		return fmt.Errorf("不认识的动作 %q", st.Action)
	}
	return err
}

// ── 分组汇总 ──

func registerGroupTool(r *ai.Registry) error {
	def, err := ai.FunctionTool("group_summary",
		"按列分组做汇总（不用写 SQL）：合计、平均、最大、最小、计数、去重计数、合并去重文本。"+
			"用户说「按部门汇总金额」「每个月多少单」「谁的单最多」时用它，比拼 SQL 更不容易出错。"+
			"给了 outputPath 会写文件。",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string"},
				"sheet":   map[string]any{"type": "string"},
				"groupBy": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "分组列，可以多个"},
				"aggregations": map[string]any{
					"type": "array", "description": "聚合规则",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"column": map[string]any{"type": "string", "description": "要聚合的列（count 可留空）"},
							"func":   map[string]any{"type": "string", "description": "sum/avg/min/max/count/countDistinct/first/last/join"},
							"as":     map[string]any{"type": "string", "description": "输出列名"},
							"sep":    map[string]any{"type": "string", "description": "join 用的分隔符"},
						},
						"required": []string{"func"},
					},
				},
				"outputPath": map[string]any{"type": "string", "description": "输出路径；留空只返回结果（最多 50 行）"},
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
			p, err := WriteTable(in.OutputPath, res.Table, WriteOptions{BoldHeader: true, AutoWidth: true, NumbersAsNumbers: true})
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
		"横向合并两张表：按 key 列把另一张表的列带到这张表右边（就是 VLOOKUP）。"+
			"会明确告诉你匹配上多少行、没匹配上多少、右表 key 有没有重复。"+
			"给了 outputPath 就写文件，否则只返回统计和预览。",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"leftPath":   map[string]any{"type": "string", "description": "主表（保留它的行）"},
				"rightPath":  map[string]any{"type": "string", "description": "要带列进来的表"},
				"leftSheet":  map[string]any{"type": "string"},
				"rightSheet": map[string]any{"type": "string"},
				"leftKey":    map[string]any{"type": "string", "description": "主表的匹配列"},
				"rightKey":   map[string]any{"type": "string", "description": "另一张表的匹配列（可与 leftKey 不同名）"},
				"bring":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "带哪些列；留空=除 key 外全部"},
				"how":        map[string]any{"type": "string", "description": "left(默认,保留主表全部) / inner(只要匹配上的) / right(还要没匹配上的)"},
				"prefix":     map[string]any{"type": "string", "description": "带过来的列名前缀，默认「右表.」"},
				"outputPath": map[string]any{"type": "string", "description": "输出路径；留空只预览"},
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
			"preview": OffsetPaging(res.Table, 0, 10).Rows,
		}
		if res.RightDupKeys > 0 {
			out["warning"] = fmt.Sprintf("右表有 %d 个 key 对应多行，只取第一条带入；"+
				"如果这些应该是唯一值，右表本身有重复，需要先确认", res.RightDupKeys)
		}
		if in.OutputPath != "" {
			p, err := WriteTable(in.OutputPath, res.Table, WriteOptions{BoldHeader: true, AutoWidth: true, NumbersAsNumbers: true})
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
		"拆分或合并工作表：按某列的每个值拆成多个文件（每个部门一个）、"+
			"把一个文件里的每个 sheet 拆成独立文件、把多个 sheet 纵向合并成一张。"+
			"用户说「按部门拆开发给我」「这几个 sheet 合成一张」时用它。",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":       map[string]any{"type": "string", "description": "文件绝对路径"},
				"sheet":      map[string]any{"type": "string"},
				"mode":       map[string]any{"type": "string", "description": "byColumn(按列值拆) / bySheet(每个 sheet 拆一个文件) / stack(多个 sheet 合成一张)"},
				"column":     map[string]any{"type": "string", "description": "mode=byColumn 时按这列拆"},
				"outputDir":  map[string]any{"type": "string", "description": "输出目录"},
				"outputPath": map[string]any{"type": "string", "description": "mode=stack 时的输出文件路径"},
				"ext":        map[string]any{"type": "string", "description": "拆出来的扩展名，默认 .xlsx，可给 .csv"},
				"maxFiles":   map[string]any{"type": "integer", "description": "最多拆几个文件，默认 100；超过会拒绝并报告"},
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
				return "", fmt.Errorf("按列拆要给 column")
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
			p, err := WriteTable(target, merged, WriteOptions{BoldHeader: true, AutoWidth: true, NumbersAsNumbers: true})
			if err != nil {
				return "", err
			}
			return jsonOut(map[string]any{
				"sheets": len(tables), "rows": len(merged.Rows),
				"headers": merged.Headers, "output": p,
			})
		}
		return "", fmt.Errorf("mode 只能是 byColumn / bySheet / stack")
	})
}

// ── 格式转换 ──

func registerConvertTool(r *ai.Registry) error {
	def, err := ai.FunctionTool("convert_table",
		"转换表格格式：xlsx 转 csv、csv 转 xlsx、把某个 sheet 单独导出。"+
			"CSV 输出可选 GBK 编码（老系统导入只认这个，验证码/乱码问题多半就是编码不对）。",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":       map[string]any{"type": "string", "description": "源文件绝对路径"},
				"sheet":      map[string]any{"type": "string"},
				"outputPath": map[string]any{"type": "string", "description": "输出文件路径，扩展名决定格式（.csv 或 .xlsx）"},
				"encoding":   map[string]any{"type": "string", "description": "仅 CSV 输出：utf8(默认,带BOM) 或 gbk"},
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
