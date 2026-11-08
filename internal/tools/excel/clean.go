package excel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// CleanOptions 清洗参数。
//
// 每个步骤都是独立开关而不是「一键全清」：不同报表的脏法完全不一样
// （财务表怕动数字，HR 表怕动工号的前导零），让用户能关掉某一步
// 比给他一个「智能清洗」有用得多。
type CleanOptions struct {
	Dir string `json:"dir"`

	// OutputDir 输出目录，留空用 <Dir>/deskkit-out。
	OutputDir string `json:"outputDir,omitempty"`

	// DropBlankRows 删除整行为空的行。
	DropBlankRows bool `json:"dropBlankRows"`

	// DropDuplicates 删除重复行。DedupKey 为空时按整行比较。
	DropDuplicates bool     `json:"dropDuplicates"`
	DedupKey       []string `json:"dedupKey,omitempty"`

	// TrimSpaces 去掉单元格首尾空白（含全角空格）。
	TrimSpaces bool `json:"trimSpaces"`

	// NormalizeDates 统一日期格式。DateFormat 支持 YYYY-MM-DD / YYYY/MM/DD。
	NormalizeDates bool   `json:"normalizeDates"`
	DateFormat     string `json:"dateFormat,omitempty"`

	// NumbersAsText 把「被存成文本的数字」转成真正的数值。
	// 会跳过带前导零的值（工号、邮编）—— 那些转成数字就毁了。
	NumbersAsText bool `json:"numbersAsText"`

	// DryRun 只统计不写文件。
	DryRun bool `json:"dryRun"`
}

// CleanResult 清洗结果。逐文件处理，不是合并。
type CleanResult struct {
	OutputDir string     `json:"outputDir"`
	Files     int        `json:"files"`
	RowsIn    int        `json:"rowsIn"`
	RowsOut   int        `json:"rowsOut"`
	BlankRows int        `json:"blankRows"`
	Duplicate int        `json:"duplicateRows"`
	Trimmed   int        `json:"trimmedCells"`
	DateFixed int        `json:"dateFixed"`
	NumFixed  int        `json:"numberFixed"`
	Unparsed  int        `json:"unparsedDates"`
	Skipped   []SkipInfo `json:"skipped,omitempty"`
	Warnings  []string   `json:"warnings,omitempty"`
	DryRun    bool       `json:"dryRun"`
	ElapsedMS int64      `json:"elapsedMs"`
}

// 常见的中文日期写法。按顺序尝试，第一个能解析的就用。
var dateLayouts = []string{
	"2006-01-02", "2006/01/02", "2006/1/2", "2006.1.2", "2006.01.02",
	"2006年1月2日", "2006年1月2号", "20060102",
	"2006-01-02 15:04:05", "2006/01/02 15:04:05",
	"02/01/2006", // 日/月/年（英式），放在最后避免误判
}

var (
	// 形如 1,234.50 / ¥1,234 / -99.5 的数字文本
	numericText = regexp.MustCompile(`^[¥$￥]?\s*[-+]?(\d{1,3}(,\d{3})*|\d+)(\.\d+)?\s*%?$`)
	// 前导零的编号（00123）：绝不能转成数字
	leadingZero = regexp.MustCompile(`^[-+]?0\d`)
)

// Clean 逐文件清洗，每个输入文件输出一个 <原名>-clean.xlsx。
//
// 为什么不是合并：清洗和合并是两件事。合并会改变行的归属（哪个文件来的），
// 而清洗应该保持每个文件独立 —— 用户往往要的是「把这 200 个表各自弄干净」，
// 而不是「变成一张表」。
func Clean(ctx context.Context, opt CleanOptions) (CleanResult, error) {
	start := time.Now()
	res := CleanResult{DryRun: opt.DryRun}

	layout := parseDateFormat(opt.DateFormat)

	scan, err := Scan(ctx, opt.Dir)
	if err != nil {
		return res, err
	}
	if len(scan.Files) == 0 {
		return res, fmt.Errorf("目录里没有可处理的表格")
	}

	outDir := opt.OutputDir
	if outDir == "" {
		outDir = filepath.Join(opt.Dir, "deskkit-out")
	}
	res.OutputDir = outDir

	if !opt.DryRun {
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return res, fmt.Errorf("创建输出目录失败: %w", err)
		}
	}

	for _, info := range scan.Files {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		if info.Err != "" {
			res.Skipped = append(res.Skipped, SkipInfo{Name: info.Name, Reason: info.Err})
			continue
		}

		headers, rows, err := readRaw(info)
		if err != nil {
			res.Skipped = append(res.Skipped, SkipInfo{Name: info.Name, Reason: err.Error()})
			continue
		}

		res.Files++
		res.RowsIn += len(rows)

		colIdx := columnIndex(headers)
		keyIdx := []int(nil)
		if opt.DropDuplicates {
			if len(opt.DedupKey) == 0 {
				keyIdx = []int{} // 空切片表示「整行比较」，与 nil 区分
			} else {
				for _, k := range opt.DedupKey {
					i, ok := colIdx[k]
					if !ok {
						res.Warnings = append(res.Warnings, fmt.Sprintf("%s 没有列 %q，本次按整行去重", info.Name, k))
						keyIdx = []int{}
						break
					}
					keyIdx = append(keyIdx, i)
				}
			}
		}

		seen := make(map[string]struct{})
		cleaned := make([][]string, 0, len(rows))
		for _, row := range rows {
			// ① 去首尾空白（先做，后面判断空行才准）
			if opt.TrimSpaces {
				for i, cell := range row {
					trimmed := strings.TrimSpace(cell)
					if trimmed != cell {
						res.Trimmed++
						row[i] = trimmed
					}
				}
			}

			// ② 空行
			if opt.DropBlankRows && isBlankRow(row) {
				res.BlankRows++
				continue
			}

			// ③ 日期
			if opt.NormalizeDates {
				for i, cell := range row {
					if cell == "" {
						continue
					}
					if fixed, ok := normalizeDate(cell, layout); ok && fixed != cell {
						row[i] = fixed
						res.DateFixed++
					} else if !ok && looksLikeDate(cell) {
						res.Unparsed++
					}
				}
			}

			// ④ 数字文本
			if opt.NumbersAsText {
				for i, cell := range row {
					if cell == "" || leadingZero.MatchString(cell) {
						continue
					}
					if converted, ok := numericValue(cell); ok {
						row[i] = converted
						res.NumFixed++
					}
				}
			}

			// ⑤ 去重（放在最后：先清洗再判重，才能识别「空格不同但内容相同」）
			if keyIdx != nil {
				key := rowKey(row, keyIdx)
				if _, dup := seen[key]; dup {
					res.Duplicate++
					continue
				}
				seen[key] = struct{}{}
			}

			cleaned = append(cleaned, row)
		}

		res.RowsOut += len(cleaned)

		if opt.DryRun {
			continue
		}

		path, err := writeCleaned(outDir, info.Name, headers, cleaned)
		if err != nil {
			res.Skipped = append(res.Skipped, SkipInfo{Name: info.Name, Reason: err.Error()})
			continue
		}
		_ = path
	}

	res.ElapsedMS = time.Since(start).Milliseconds()
	return res, nil
}

// readRaw 读出表头与全部数据行（不做对齐，保留原始列序）。
func readRaw(info SheetInfo) ([]string, [][]string, error) {
	f, err := openWorkbook(info.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("打开失败: %w", err)
	}
	defer f.Close()

	sheet := info.Sheet
	if sheet == "" {
		sheets := f.GetSheetList()
		if len(sheets) == 0 {
			return nil, nil, fmt.Errorf("没有工作表")
		}
		sheet = sheets[0]
	}

	rows, err := f.Rows(sheet)
	if err != nil {
		return nil, nil, fmt.Errorf("读取行失败: %w", err)
	}
	defer rows.Close()

	var headers []string
	var out [][]string
	for rows.Next() {
		cells, err := rows.Columns()
		if err != nil {
			return nil, nil, fmt.Errorf("读取行内容失败: %w", err)
		}
		if headers == nil {
			headers = normalizeHeaders(cells)
			continue
		}
		// 补齐到表头宽度，避免后面的列号越界
		for len(cells) < len(headers) {
			cells = append(cells, "")
		}
		out = append(out, cells)
	}
	if err := rows.Error(); err != nil {
		return nil, nil, fmt.Errorf("遍历行失败: %w", err)
	}
	return headers, out, nil
}

// writeCleaned 写一个清洗后的副本。
func writeCleaned(outDir, srcName string, headers []string, rows [][]string) (string, error) {
	base := strings.TrimSuffix(srcName, filepath.Ext(srcName))
	path := filepath.Join(outDir, base+"-clean.xlsx")

	// 统一走 WriteTable：数字写成数值、表头加粗冻结、附「处理说明」。
	// 自己拼 SetSheetRow 会把所有单元格写成文本，清洗完的金额在 Excel 里没法求和。
	written, err := WriteTable(path, &Table{
		Headers: headers,
		Rows:    rows,
	}, WriteOptions{
		Sheet:            "cleaned",
		BoldHeader:       true,
		AutoWidth:        true,
		NumbersAsNumbers: true,
		Meta: []string{
			"操作：清洗（去空行 / 去重 / 去空白 / 统一日期 / 文本数字转数值）",
			"来源：" + srcName,
		},
	})
	if err != nil {
		return "", err
	}
	return written, nil
}

// parseDateFormat 把界面上的写法转成 Go 的布局。
func parseDateFormat(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "YYYY/MM/DD", "YYYY/M/D":
		return "2006/01/02"
	case "YYYY年M月D日":
		return "2006年1月2日"
	case "", "YYYY-MM-DD":
		return "2006-01-02"
	default:
		return "2006-01-02"
	}
}

// normalizeDate 尝试把各种写法归一成目标格式。
//
// 已经是目标格式的直接返回 ok=true 且不改变值（上层据此跳过计数）。
func normalizeDate(value, layout string) (string, bool) {
	v := strings.TrimSpace(value)
	if v == "" {
		return v, true
	}
	if t, err := time.Parse(layout, v); err == nil {
		return t.Format(layout), true
	}
	for _, l := range dateLayouts {
		if t, err := time.ParseInLocation(l, v, time.Local); err == nil {
			return t.Format(layout), true
		}
	}
	return value, false
}

// looksLikeDate 判断「像日期但没解析成功」，用来提示用户有漏网的格式。
func looksLikeDate(v string) bool {
	if len(v) < 6 || len(v) > 30 {
		return false
	}
	digits, seps := 0, 0
	for _, r := range v {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '-' || r == '/' || r == '.' || r == '年' || r == '月' || r == '日':
			seps++
		}
	}
	return digits >= 6 && digits <= 8 && seps >= 1
}

// numericValue 把「¥1,234.50」「1 234」「15%」这类文本转成数值字符串。
//
// 返回的是规范化后的数字文本（去掉货币符号与千分位），不是 float ——
// 保持字符串模型，避免浮点精度在小数很多的金额上出问题。
func numericValue(v string) (string, bool) {
	if !numericText.MatchString(v) {
		return v, false
	}
	percent := strings.HasSuffix(v, "%")
	s := strings.TrimSpace(strings.TrimSuffix(v, "%"))
	s = strings.TrimLeft(s, "¥$￥ ")
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", "")
	s = strings.ReplaceAll(s, " ", "")
	if _, err := strconv.ParseFloat(s, 64); err != nil {
		return v, false
	}
	if percent {
		// 百分比保留原样：转成小数会改变语义，用户看着也会懵
		return v, false
	}
	return s, true
}
