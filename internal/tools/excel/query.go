package excel

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/xuri/excelize/v2"
)

// ─────────────────────────────────────────────────────────────
// SQL 查询层
//
// 为什么要有这一层：合并 / 清洗 / 汇总都是「几种固定玩法」，
// 但用户想做的事情是无穷的（按两个条件筛、算差值、分组排名、
// 多表关联、按规则改写某个字段…）。与其为每种玩法加一个表单，
// 不如把表格装进一个 SQL 引擎 —— AI 很擅长写 SQL，用户只管说
// 要什么，剩下的它自己组合。
//
// 用纯 Go 的 modernc.org/sqlite：不依赖 cgo，交叉编译到 Windows
// 不会出问题。
// ─────────────────────────────────────────────────────────────

// TableInfo 一张可查询的表。名字是 AI 写 SQL 时要用的。
type TableInfo struct {
	Table   string   `json:"table"`
	File    string   `json:"file"`
	Sheet   string   `json:"sheet"`
	Rows    int      `json:"rows"`
	Columns []string `json:"columns"`
	// Warnings 装载时自动修正过什么（补表头 / 重名列改名）。
	// 必须转述给模型：不然查询结果里平白多出「列3」，没人知道它从哪来。
	Warnings []string `json:"warnings,omitempty"`
}

// Catalog 目录里所有表的清单。
type Catalog struct {
	Dir    string      `json:"dir"`
	Tables []TableInfo `json:"tables"`
	Note   string      `json:"note,omitempty"`
}

// QueryResult 查询结果。
type QueryResult struct {
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
	Count   int        `json:"count"`
	Elapsed int64      `json:"elapsedMs"`
	// Truncated 命中返回行数硬上限，Rows 只是前一部分。
	Truncated bool `json:"truncated,omitempty"`
	// Note 给模型看的补充说明（例如「已截断，请用 write_query_result 落盘」）。
	Note string `json:"note,omitempty"`
}

// MaxQueryRows 单次查询返回给模型的行数硬上限。
//
// 为什么必须有：limit 是模型给的，它一高兴写 10000000，一次调用就能吐
// 95067 字节 ≈ 31.7k tokens，把上下文直接顶爆（实测）。要全量结果请走
// write_query_result 落盘 —— 那才是「大结果」该走的路径。
const MaxQueryRows = 500

var unsafeIdent = regexp.MustCompile(`[^0-9a-zA-Z_\p{Han}]`)

// tableName 把「文件名 + 工作表名」拼成一个安全的 SQL 标识符。
//
// 保留中文：SQLite 允许，而且 AI 读到「销售部-10月_Sheet1」比读
// 「t3」更容易写对查询。
func tableName(file, sheet string) string {
	stem := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	name := unsafeIdent.ReplaceAllString(stem+"_"+sheet, "_")
	if name == "" {
		name = "sheet"
	}
	if r := name[0]; r >= '0' && r <= '9' {
		name = "t_" + name
	}
	return name
}

// columnName 列名净化。空表头补 c1、c2…，重名加后缀。
func columnName(raw string, idx int, used map[string]int) string {
	name := strings.TrimSpace(raw)
	if name == "" {
		name = fmt.Sprintf("c%d", idx+1)
	}
	name = strings.TrimSpace(unsafeIdent.ReplaceAllString(name, "_"))
	if name == "" || name == "_" {
		name = fmt.Sprintf("c%d", idx+1)
	}
	if r := name[0]; r >= '0' && r <= '9' {
		name = "c_" + name
	}
	if n, ok := used[name]; ok {
		used[name] = n + 1
		name = fmt.Sprintf("%s_%d", name, n+1)
	} else {
		used[name] = 1
	}
	return name
}

// numericColumn 判断一整列能不能按数字处理。
//
// 判断偏保守：只要有一个非空值看着像编号（前导零）就不当数字，
// 否则 00123 会被存成 123 —— 和清洗那边同一条理由。
func numericColumn(values []string) bool {
	found := 0
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if leadingZero.MatchString(v) {
			return false
		}
		// 16 位以上纯整数（身份证 / 银行卡）不能进 REAL 列：
		// SQLite 的 REAL 就是 float64，只有 15 位有效数字，存进去末位就变了。
		if isLongInteger(v) {
			return false
		}
		if _, ok := numericValue(v); !ok {
			return false
		}
		found++
	}
	return found > 0
}

// LoadCatalog 读取目录并把每张表装进内存 SQLite。
//
// 调用方负责关闭返回的 *sql.DB。
func LoadCatalog(ctx context.Context, dir string) (*sql.DB, Catalog, error) {
	cat := Catalog{Dir: dir}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, cat, fmt.Errorf("读目录失败: %w", err)
	}

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, cat, fmt.Errorf("打开内存库失败: %w", err)
	}
	// 单连接：内存库是 per-connection 的，多连接会各自看到一个空库
	db.SetMaxOpenConns(1)

	for _, entry := range entries {
		if ctx.Err() != nil {
			db.Close()
			return nil, cat, ctx.Err()
		}
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, "~$") || strings.HasPrefix(name, ".") {
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		// ★ 走 supported 而不是只认 .xlsx/.xlsm：openWorkbook 已经支持 CSV 与 .xls，
		// 但这里以前直接 continue，于是用户说「这个文件夹里的表」时，
		// CSV 里的金额被静默漏掉、模型还回答「没有可读的表格」。
		if !supported[ext] {
			continue
		}

		tables, err := loadWorkbook(ctx, db, filepath.Join(dir, name), name)
		if err != nil {
			cat.Note = strings.Join(nonEmpty(cat.Note, fmt.Sprintf("%s: %v", name, err)), "；")
			continue
		}
		cat.Tables = append(cat.Tables, tables...)
		// 补表头 / 重名列改名的说明也要出现在 note 里：只留在每张表的
		// JSON 内部字段里，模型基本不会读，用户就永远不知道结果里为什么
		// 多出一列「列3」。
		cat.Note = noteWarnings(cat.Note, tables)
	}

	if len(cat.Tables) == 0 {
		cat.Note = strings.Join(nonEmpty(cat.Note, "目录里没有可读的表格"), "；")
	}
	return db, cat, nil
}

// noteWarnings 把「装载时自动修正过什么」汇总成一句给人看的 note。
//
// 格式「文件/工作表: 说明」，多条用「；」连起来 —— 与其它 note 一致，
// 模型和用户都能直接从返回体里看到，不用去扒每张表的 warnings 字段。
func noteWarnings(note string, tables []TableInfo) string {
	for _, t := range tables {
		for _, w := range t.Warnings {
			note = strings.Join(nonEmpty(note, fmt.Sprintf("%s/%s: %s", t.File, t.Sheet, w)), "；")
		}
	}
	return note
}

func loadWorkbook(ctx context.Context, db *sql.DB, path, file string) ([]TableInfo, error) {
	f, err := openWorkbook(path)
	if err != nil {
		return nil, fmt.Errorf("打不开: %w", err)
	}
	defer f.Close()

	var out []TableInfo
	for _, sheet := range f.GetSheetList() {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		rows, err := f.GetRows(sheet)
		if err != nil || len(rows) == 0 {
			continue
		}

		// ★ 表头必须和 LoadTable 走同一套归一，再按**所有行**的最大宽度补齐：
		// 这里以前直接拿 rows[0] 当表头、又按表头宽度建列，于是「表头 2 列、
		// 数据 4 列」的 CSV 被建成 2 列的表 —— 第 3/4 列整列静默消失，
		// 用户问「这个月支出多少」拿到的是少算的合计（R-001）。
		headers, warns := normalizeHeadersWarn(rows[0])
		body := rows[1:]
		if len(headers) == 0 {
			continue
		}
		width := len(headers)
		for _, row := range body {
			if len(row) > width {
				width = len(row)
			}
		}
		if width > len(headers) {
			// 复用 LoadTable 的补列实现，补名规则与「处理说明」文案完全一致
			hdr := &Table{Headers: headers}
			hdr.extendHeaders(width)
			headers = hdr.Headers
			warns = append(warns, hdr.Warnings...)
		}

		used := map[string]int{}
		cols := make([]string, len(headers))
		for i, h := range headers {
			cols[i] = columnName(h, i, used)
		}

		// 按列收集，判断数值型
		columnValues := make([][]string, len(cols))
		for r, row := range body {
			for c := range cols {
				cell := ""
				if c < len(row) {
					cell = row[c]
				}
				columnValues[c] = append(columnValues[c], cell)
			}
			_ = r
		}
		isNum := make([]bool, len(cols))
		for c := range cols {
			isNum[c] = numericColumn(columnValues[c])
		}

		table := tableName(file, sheet)
		defs := make([]string, len(cols))
		for i, c := range cols {
			typ := "TEXT"
			if isNum[i] {
				typ = "REAL"
			}
			defs[i] = fmt.Sprintf("%q %s", c, typ) // 用双引号包住，中文和空格都安全
		}
		ddl := fmt.Sprintf("CREATE TABLE %q (%s)", table, strings.Join(defs, ", "))
		if _, err := db.Exec(ddl); err != nil {
			return out, fmt.Errorf("建表 %s 失败: %w", table, err)
		}

		placeholder := "(" + strings.TrimSuffix(strings.Repeat("?,", len(cols)), ",") + ")"
		insert := fmt.Sprintf("INSERT INTO %q VALUES %s", table, placeholder)

		tx, err := db.Begin()
		if err != nil {
			return out, err
		}
		stmt, err := tx.Prepare(insert)
		if err != nil {
			tx.Rollback()
			return out, err
		}
		count := 0
		for _, row := range body {
			args := make([]any, len(cols))
			blank := true
			for c := range cols {
				cell := ""
				if c < len(row) {
					cell = row[c]
				}
				if isNum[c] {
					if num, ok := numericValue(cell); ok {
						args[c] = num
					} else {
						args[c] = nil
					}
				} else {
					args[c] = cell
				}
				if strings.TrimSpace(cell) != "" {
					blank = false
				}
			}
			if blank {
				continue // 整行空的没意义，不进库
			}
			if _, err := stmt.Exec(args...); err != nil {
				stmt.Close()
				tx.Rollback()
				return out, fmt.Errorf("写入 %s 失败: %w", table, err)
			}
			count++
		}
		stmt.Close()
		if err := tx.Commit(); err != nil {
			return out, err
		}

		out = append(out, TableInfo{
			Table: table, File: file, Sheet: sheet, Rows: count, Columns: cols,
			Warnings: warns,
		})
	}
	return out, nil
}

var readOnlyQuery = regexp.MustCompile(`(?is)^\s*(select|with)\b`)

// Query 执行只读查询。
//
// 只放行 SELECT / WITH：装了引擎就等于允许任意 SQL，所以这里必须
// 卡死写入和 DDL —— 用户的原始文件不在库里，但内存库也不该被当成
// 可以随便改的地方。
func Query(ctx context.Context, db *sql.DB, query string, limit int) (QueryResult, error) {
	return runQuery(ctx, db, query, limit, MaxQueryRows)
}

// queryAll 内部用：写文件要全量，不受 MaxQueryRows 约束。
func queryAll(ctx context.Context, db *sql.DB, query string, limit int) (QueryResult, error) {
	return runQuery(ctx, db, query, limit, 0)
}

func runQuery(ctx context.Context, db *sql.DB, query string, limit, maxRows int) (QueryResult, error) {
	var res QueryResult
	if strings.TrimSpace(query) == "" {
		return res, fmt.Errorf("SQL 不能为空")
	}
	if !readOnlyQuery.MatchString(query) {
		return res, fmt.Errorf("只支持 SELECT / WITH 查询")
	}
	if limit <= 0 {
		limit = 200
	}
	capped := false
	if maxRows > 0 && limit > maxRows {
		limit = maxRows
		capped = true
	}

	// 外面套一层 LIMIT：用户（模型）写的 SQL 不一定带 LIMIT，
	// 一张十万行的表直接全量返回会把界面和上下文都撑爆。
	// 上限命中时多取一行，用来判断「是否真的还有更多」。
	probe := limit
	if capped {
		probe = limit + 1
	}
	wrapped := fmt.Sprintf("SELECT * FROM (%s) LIMIT %d", strings.TrimSuffix(strings.TrimSpace(query), ";"), probe)
	rows, err := db.QueryContext(ctx, wrapped)
	if err != nil {
		return res, fmt.Errorf("查询失败: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return res, err
	}
	res.Columns = cols

	for rows.Next() {
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return res, err
		}
		rec := make([]string, len(cols))
		for i, v := range raw {
			rec[i] = cellText(v)
		}
		res.Rows = append(res.Rows, rec)
	}
	if err := rows.Err(); err != nil {
		return res, err
	}
	if len(res.Rows) > limit {
		res.Rows = res.Rows[:limit]
		res.Truncated = true
	}
	if res.Truncated {
		res.Note = fmt.Sprintf(
			"结果超过 %d 行，只返回了前 %d 行。要看/要全部结果请用 write_query_result 落盘成文件。",
			limit, limit)
	}
	res.Count = len(res.Rows)
	return res, nil
}

// cellText 把 SQLite 的任意值转成字符串，避免出现 <nil> 或者
// 1.2345678e+06 这种在表格里难看的形式。
func cellText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(t)
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", t), "0"), ".")
	default:
		return fmt.Sprint(t)
	}
}

// WriteQuery 把查询结果写成新的文件。
//
// 单独一个函数而不是给 Query 加参数：查询是「看一眼」，写文件是
// 「落地」，两件事分开，AI 也就必须分两步做 —— 先给你看结果，
// 你说行它再写。
//
// protect 传源文件路径：目标路径命中任何一个源文件就直接报错。
// 这是全项目唯一曾经能覆盖用户原文件的落盘路径（实测「结果存回原目录、
// 名字就叫 销售.csv」会把源 CSV 直接替换掉，无备份无提示）。
func WriteQuery(ctx context.Context, db *sql.DB, query, outputDir, fileName string, protect ...string) (string, int, error) {
	res, err := queryAll(ctx, db, query, 1_000_000)
	if err != nil {
		return "", 0, err
	}
	if len(res.Columns) == 0 {
		return "", 0, fmt.Errorf("查询没有返回任何列")
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", 0, fmt.Errorf("创建输出目录失败: %w", err)
	}

	// 输出格式由文件名决定：.csv 写 CSV，其余写 xlsx。
	// 用文件名而不是另加一个 format 参数 —— 用户和模型说的都是「保存成 xxx.csv」，
	// 多一个参数只会多一种不一致的可能。
	wantCSV := IsCSV(fileName)
	if strings.TrimSpace(fileName) == "" {
		fileName = "query-" + time.Now().Format("20060102-150405") + ".xlsx"
		wantCSV = false
	}
	name := fileName
	if wantCSV && !IsCSV(name) {
		name += ".csv"
	}
	if !wantCSV && strings.ToLower(filepath.Ext(name)) != ".xlsx" {
		name += ".xlsx"
	}
	// 文件名不许带路径（模型给 ../ 就能在任意位置落盘）
	raw, err := safeOutputPath(outputDir, name)
	if err != nil {
		return "", 0, err
	}
	// ★ 硬校验：目标就是某个源文件 → 直接拒绝，别靠加后缀「悄悄」避开
	for _, src := range protect {
		if sameFile(raw, src) {
			return "", 0, fmt.Errorf(
				"拒绝写入源文件 %s：这会直接覆盖你的原始文件。请换一个文件名，或让它输出到 %s",
				filepath.Base(raw), filepath.Join(outputDir, "deskkit-out"))
		}
	}
	// 剩下的同名情况走 uniquePath（-1、-2），绝不覆盖已有文件
	path := uniquePath(raw)

	if IsCSV(path) {
		table := make([][]string, 0, len(res.Rows)+1)
		table = append(table, res.Columns)
		table = append(table, res.Rows...)
		if err := WriteCSV(path, table); err != nil {
			return "", 0, err
		}
		return path, len(res.Rows), nil
	}

	f := excelize.NewFile()
	defer f.Close()
	sheet := f.GetSheetName(0)

	write := func(idx int, values []string) error {
		cell, err := excelize.CoordinatesToCellName(1, idx)
		if err != nil {
			return err
		}
		row := make([]any, len(values))
		for i, v := range values {
			row[i] = v
		}
		return f.SetSheetRow(sheet, cell, &row)
	}

	if err := write(1, res.Columns); err != nil {
		return "", 0, err
	}
	for i, r := range res.Rows {
		if err := write(i+2, r); err != nil {
			return "", 0, err
		}
	}

	if style, err := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"EEEEEE"}},
	}); err == nil {
		last, _ := excelize.CoordinatesToCellName(len(res.Columns), 1)
		_ = f.SetCellStyle(sheet, "A1", last, style)
	}
	// 列宽按内容估：不追求精确，只求打开时不至于全是 ###
	for i, c := range res.Columns {
		width := len([]rune(c)) + 4
		for _, r := range res.Rows {
			if i < len(r) {
				if n := len([]rune(r[i])) + 2; n > width {
					width = n
				}
			}
		}
		if width > 40 {
			width = 40
		}
		col, _ := excelize.ColumnNumberToName(i + 1)
		_ = f.SetColWidth(sheet, col, col, float64(width))
	}

	if err := f.SaveAs(path); err != nil {
		return "", 0, fmt.Errorf("保存失败: %w", err)
	}
	return path, res.Count, nil
}

func nonEmpty(items ...string) []string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}
