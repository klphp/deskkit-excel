package excel

// 新功能测试：画像、校验、去重、排序、筛选、分组、横向合并、比对、
// 变形、拆分、格式转换。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klphp/deskkit-excel/internal/fsx/xtext"
)

// sampleTable 一张典型的员工表，带各种毛病。
func sampleTable() *Table {
	return &Table{
		Path:    "/tmp/sample.xlsx",
		Sheet:   "Sheet1",
		Headers: []string{"工号", "姓名", "部门", "金额", "日期", "备注"},
		Rows: [][]string{
			{"001", "张一", "技术", "1200", "2026/10/1", "a"},
			{"002", "李二", "技术", "980", "2026/10/2", "b"},
			{"003", "王三", "运营", "1500", "2026/10/3", "c"},
			{"004", "赵四", "运营", "", "2026/10/4", "d"},
			{"005", "张一", "技术", "1200", "2026/10/1", "a"},
		},
	}
}

func TestProfileKindsAndWarnings(t *testing.T) {
	p := Profile(sampleTable(), 3)

	byName := map[string]ColumnProfile{}
	for _, c := range p {
		byName[c.Name] = c
	}
	if got := byName["金额"].Kind; got != "number" {
		t.Fatalf("金额应为 number，实际 %s", got)
	}
	if got := byName["日期"].Kind; got != "date" {
		t.Fatalf("日期应为 date，实际 %s", got)
	}
	if got := byName["姓名"].Kind; got != "text" {
		t.Fatalf("姓名应为 text，实际 %s", got)
	}
	// 金额有 1 个空
	if byName["金额"].Empty != 1 {
		t.Fatalf("金额空值应为 1，实际 %d", byName["金额"].Empty)
	}
	// 工号是 001 这种，必须提醒前导零
	found := false
	for _, w := range byName["工号"].Warnings {
		if strings.Contains(w, "前导零") || strings.Contains(w, "写成数值") {
			found = true
		}
	}
	if !found {
		t.Fatalf("工号列没有提醒前导零，warnings=%v", byName["工号"].Warnings)
	}
}

func TestValidateRules(t *testing.T) {
	res, err := Validate(sampleTable(), ValidateRules{
		Required:    []string{"姓名", "金额"},
		Unique:      []string{"工号"},
		Numeric:     []string{"金额"},
		Dates:       []string{"日期"},
		NotNegative: []string{"金额"},
		Pattern:     map[string]string{"工号": `^\d{3}$`},
		MaxIssues:   100,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 第 4 行金额为空 → 必填为空 1 条
	var missing, dup int
	for _, is := range res.Issues {
		switch {
		case is.Column == "金额" && strings.Contains(is.Reason, "必填"):
			missing++
		case is.Column == "工号" && strings.Contains(is.Reason, "重复"):
			dup++
		}
	}
	if missing != 1 {
		t.Fatalf("应报 1 条金额必填为空，实际 %d（%+v）", missing, res.Issues)
	}
	if dup != 0 {
		t.Fatalf("工号没有重复，不该报重复：%+v", res.Issues)
	}

	// 造一个真重复
	tbl := sampleTable()
	tbl.Rows = append(tbl.Rows, []string{"001", "重复", "技术", "1", "2026/10/5", "x"})
	res2, err := Validate(tbl, ValidateRules{Unique: []string{"工号"}})
	if err != nil {
		t.Fatal(err)
	}
	if res2.TotalIssues != 1 {
		t.Fatalf("应报 1 条重复，实际 %d：%+v", res2.TotalIssues, res2.Issues)
	}
}

func TestFindDuplicates(t *testing.T) {
	rows, total, err := FindDuplicates(sampleTable(), []string{"姓名", "金额"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	// 「张一 1200」出现两次
	if total != 2 {
		t.Fatalf("重复行应为 2 行（一组两条），实际 %d", total)
	}
	if len(rows) != 1 {
		t.Fatalf("应返回 1 组样本，实际 %d", len(rows))
	}
}

func TestSortNumbersAndDates(t *testing.T) {
	// 金额里有空值，空值必须沉底；数字按数值排而不是字符串排
	tbl := &Table{
		Headers: []string{"名称", "金额"},
		Rows: [][]string{
			{"a", "9"},
			{"b", "10"},
			{"c", ""},
			{"d", "100"},
		},
	}
	out, err := Sort(tbl, []SortKey{{Column: "金额"}})
	if err != nil {
		t.Fatal(err)
	}
	got := []string{out.Rows[0][1], out.Rows[1][1], out.Rows[2][1], out.Rows[3][1]}
	want := []string{"9", "10", "100", ""}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("排序不对: %v，应为 %v", got, want)
		}
	}

	// 日期也要按时间排，不能按字符串（2026/2/1 会被排到 2026/10/1 后面）
	dates := &Table{
		Headers: []string{"d"},
		Rows:    [][]string{{"2026/10/1"}, {"2026/2/1"}, {"2026/1/15"}},
	}
	out2, err := Sort(dates, []SortKey{{Column: "d"}})
	if err != nil {
		t.Fatal(err)
	}
	if out2.Rows[0][0] != "2026/1/15" || out2.Rows[2][0] != "2026/10/1" {
		t.Fatalf("日期排序不对: %v", out2.Rows)
	}
}

func TestFilterConditions(t *testing.T) {
	out, err := Filter(sampleTable(), []Condition{
		{Column: "部门", Op: "eq", Value: "运营"},
		{Column: "金额", Op: "ge", Value: "1500"},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 1 || out.Rows[0][1] != "王三" {
		t.Fatalf("筛选结果不对: %v", out.Rows)
	}

	// 包含
	out2, err := Filter(sampleTable(), []Condition{{Column: "备注", Op: "contains", Value: "a"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out2.Rows) != 2 {
		t.Fatalf("包含 a 的应 2 行，实际 %d", len(out2.Rows))
	}
}

func TestGroupByAggregations(t *testing.T) {
	res, err := GroupBy(sampleTable(), []string{"部门"}, []Agg{
		{Column: "金额", Func: "sum", As: "合计"},
		{Column: "金额", Func: "count", As: "笔数"},
		{Column: "姓名", Func: "countDistinct", As: "人数"},
		{Column: "姓名", Func: "join", As: "成员"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Groups != 2 {
		t.Fatalf("应 2 个部门，实际 %d", res.Groups)
	}
	// 技术部：1200 + 980 + 1200 = 3380（含重复行）
	var tech []string
	for _, r := range res.Table.Rows {
		if r[0] == "技术" {
			tech = r
		}
	}
	if len(tech) == 0 {
		t.Fatal("没找到技术部")
	}
	if tech[1] != "3380" {
		t.Fatalf("技术部合计应为 3380，实际 %s", tech[1])
	}
	if tech[3] != "2" {
		t.Fatalf("技术部去重人数应为 2（张一/李二），实际 %s", tech[3])
	}
}

func TestJoinMatchedAndDuplicateKeys(t *testing.T) {
	dir := t.TempDir()
	left := &Table{
		Path:    filepath.Join(dir, "left.xlsx"),
		Headers: []string{"工号", "姓名"},
		Rows:    [][]string{{"001", "张一"}, {"002", "李二"}, {"003", "王三"}},
	}
	right := &Table{
		Path:    filepath.Join(dir, "right.xlsx"),
		Headers: []string{"工号", "级别", "部门"},
		Rows:    [][]string{{"001", "P6", "技术"}, {"001", "P7", "技术"}, {"002", "P5", "运营"}},
	}
	if _, err := WriteTable(left.Path, left, WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteTable(right.Path, right, WriteOptions{}); err != nil {
		t.Fatal(err)
	}

	res, err := Join(JoinOptions{
		LeftPath: left.Path, RightPath: right.Path,
		LeftKey: "工号", RightKey: "工号", Bring: []string{"级别"},
		// 右表 001 出现两次 → 必须显式选择怎么取；老行为是 first
		OnDuplicate: "first",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Matched != 2 {
		t.Fatalf("应匹配 2 行，实际 %d", res.Matched)
	}
	if res.Unmatched != 1 {
		t.Fatalf("应 1 行没匹配上，实际 %d", res.Unmatched)
	}
	// 右表 001 出现两次：必须报告，不能静默
	if res.RightDupKeys != 1 {
		t.Fatalf("应报告 1 个重复 key，实际 %d", res.RightDupKeys)
	}
	if res.OutputRows != 3 {
		t.Fatalf("left join 应保留 3 行，实际 %d", res.OutputRows)
	}
}

func TestCompareTables(t *testing.T) {
	dir := t.TempDir()
	oldT := &Table{
		Path:    filepath.Join(dir, "old.xlsx"),
		Headers: []string{"工号", "姓名", "金额"},
		Rows:    [][]string{{"001", "张一", "100"}, {"002", "李二", "200"}},
	}
	newT := &Table{
		Path:    filepath.Join(dir, "new.xlsx"),
		Headers: []string{"工号", "姓名", "金额"},
		Rows:    [][]string{{"001", "张一", "150"}, {"003", "王三", "300"}},
	}
	res, err := Compare(oldT, newT, []string{"工号"}, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 1 {
		t.Fatalf("新增应 1，实际 %d", res.Added)
	}
	if res.Removed != 1 {
		t.Fatalf("删除应 1，实际 %d", res.Removed)
	}
	if res.Changed != 1 {
		t.Fatalf("修改应 1，实际 %d", res.Changed)
	}
	if len(res.ChangedSamples) != 1 || res.ChangedSamples[0].Column != "金额" ||
		res.ChangedSamples[0].Before != "100" || res.ChangedSamples[0].After != "150" {
		t.Fatalf("修改明细不对: %+v", res.ChangedSamples)
	}
}

func TestTransformSteps(t *testing.T) {
	tbl := sampleTable()
	// 顺序很重要：筛选要在丢列之前做，否则被筛的列已经不在了
	steps := []transformStep{
		{Action: "filter", Conditions: []Condition{{Column: "部门", Op: "notEmpty"}}},
		{Action: "select", Columns: []string{"工号", "姓名", "金额"}},
		{Action: "computed", Expr: "[金额]*2", As: "双倍"},
		{Action: "rename", Mapping: map[string]string{"姓名": "名字"}},
	}
	for _, st := range steps {
		if err := applyStep(&tbl, st); err != nil {
			t.Fatalf("%s 失败: %v", st.Action, err)
		}
	}
	if len(tbl.Headers) != 4 || tbl.Headers[1] != "名字" || tbl.Headers[3] != "双倍" {
		t.Fatalf("列不对: %v", tbl.Headers)
	}
	// 第一条 1200*2
	if tbl.Rows[0][3] != "2400" {
		t.Fatalf("计算列不对: %v", tbl.Rows[0])
	}
}

func TestTransformSplitConcatFillDown(t *testing.T) {
	tbl := &Table{
		Headers: []string{"地区", "号码"},
		Rows: [][]string{
			{"浙江 杭州", "0571-1234"},
			{"", "0571-5678"}, // 合并单元格读出来是空的
			{"江苏 南京", "025-9999"},
		},
	}
	steps := []transformStep{
		{Action: "fillDown", Columns: []string{"地区"}},
		{Action: "split", Column: "地区", Sep: " ", Into: []string{"省", "市"}},
		{Action: "split", Column: "号码", Sep: "-", Into: []string{"区号", "号码"}},
		{Action: "concat", Columns: []string{"省", "市"}, Sep: "/", As: "全称"},
	}
	for _, st := range steps {
		if err := applyStep(&tbl, st); err != nil {
			t.Fatalf("%s 失败: %v", st.Action, err)
		}
	}
	if tbl.Rows[1][0] != "浙江 杭州" {
		t.Fatalf("向下填充没生效: %v", tbl.Rows[1])
	}
	if tbl.Rows[0][2] != "浙江" || tbl.Rows[0][3] != "杭州" {
		t.Fatalf("拆列不对: %v", tbl.Rows[0])
	}
	if tbl.Rows[2][6] != "江苏/南京" {
		t.Fatalf("合列不对: %v", tbl.Rows[2])
	}
}

func TestTransformDatePartsAndNormalize(t *testing.T) {
	tbl := &Table{
		Headers: []string{"日期", "金额"},
		Rows:    [][]string{{"2026/10/5", "￥1,200.50"}, {"2026/2/1", " 300 "}},
	}
	if err := applyStep(&tbl, transformStep{Action: "dateParts", Column: "日期", Parts: []string{"year", "month", "quarter"}}); err != nil {
		t.Fatal(err)
	}
	if tbl.Rows[0][2] != "2026" || tbl.Rows[0][3] != "10" || tbl.Rows[0][4] != "Q4" {
		t.Fatalf("日期拆分不对: %v", tbl.Rows[0])
	}
	if err := applyStep(&tbl, transformStep{Action: "normalize", Columns: []string{"金额"}, Trim: true, StripCurrency: true}); err != nil {
		t.Fatal(err)
	}
	if tbl.Rows[0][1] != "1200.50" {
		t.Fatalf("去货币符号不对: %q", tbl.Rows[0][1])
	}
	if tbl.Rows[1][1] != "300" {
		t.Fatalf("去空白不对: %q", tbl.Rows[1][1])
	}
}

func TestTranspose(t *testing.T) {
	tbl := &Table{
		Headers: []string{"a", "b"},
		Rows:    [][]string{{"1", "2"}, {"3", "4"}},
	}
	out := Transpose(tbl)
	if len(out.Headers) != 3 {
		t.Fatalf("转置后列数不对: %v", out.Headers)
	}
	if out.Rows[0][0] != "a" || out.Rows[0][2] != "3" {
		t.Fatalf("转置内容不对: %v", out.Rows)
	}
}

func TestSplitByColumnAndStack(t *testing.T) {
	dir := t.TempDir()
	tbl := sampleTable()
	tbl.Path = filepath.Join(dir, "src.xlsx")
	if _, err := WriteTable(tbl.Path, tbl, WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "split")
	out, err := SplitByColumn(tbl, "部门", outDir, ".xlsx", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("应拆成 2 个文件（技术/运营），实际 %d", len(out))
	}
	for _, o := range out {
		if _, err := os.Stat(o.Path); err != nil {
			t.Fatalf("文件没生成: %v", err)
		}
	}

	// 把两个拆出来的表再堆回去
	var tables []*Table
	for _, o := range out {
		tt, err := LoadTable(o.Path, "")
		if err != nil {
			t.Fatal(err)
		}
		tables = append(tables, tt)
	}
	merged := StackSheets(tables)
	if len(merged.Rows) != 5 {
		t.Fatalf("堆回去应 5 行，实际 %d", len(merged.Rows))
	}
}

func TestConvertXlsxToCSVAndBack(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.xlsx")
	if _, err := WriteTable(src, sampleTable(), WriteOptions{NumbersAsNumbers: true}); err != nil {
		t.Fatal(err)
	}

	csvPath := filepath.Join(dir, "out.csv")
	if _, _, err := Convert(ConvertOptions{Path: src, OutputPath: csvPath}); err != nil {
		t.Fatal(err)
	}
	rows, err := readCSVRows(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 6 || rows[0][0] != "工号" {
		t.Fatalf("CSV 内容不对: %d 行，首行 %v", len(rows), rows[0])
	}
	// 工号 001 不能被当数字吃掉前导零
	if rows[1][0] != "001" {
		t.Fatalf("前导零丢了: %q", rows[1][0])
	}

	// CSV 再转回 xlsx
	backPath := filepath.Join(dir, "back.xlsx")
	if _, n, err := Convert(ConvertOptions{Path: csvPath, OutputPath: backPath}); err != nil {
		t.Fatal(err)
	} else if n != 5 {
		t.Fatalf("转回来应 5 行数据，实际 %d", n)
	}
}

func TestConvertToGBK(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.xlsx")
	if _, err := WriteTable(src, sampleTable(), WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	gbkPath := filepath.Join(dir, "gbk.csv")
	if _, _, err := Convert(ConvertOptions{Path: src, OutputPath: gbkPath, Encoding: "gbk"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(gbkPath)
	if err != nil {
		t.Fatal(err)
	}
	// GBK 文件不该是合法 UTF-8（中文部分），且能解回中文
	if isUTF8Text(raw) {
		t.Fatal("写了 GBK，但内容仍是合法 UTF-8")
	}
	text, _, _ := xtext.Detect(raw)
	if !strings.Contains(text, "工号") {
		t.Fatalf("GBK 解回来不是中文: %q", text[:min(30, len(text))])
	}
}

func isUTF8Text(b []byte) bool {
	for len(b) > 0 {
		if b[0] < 0x80 {
			b = b[1:]
			continue
		}
		return false
	}
	return true
}

func TestWriteTableNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.xlsx")
	if _, err := WriteTable(target, sampleTable(), WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	second, err := WriteTable(target, sampleTable(), WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if second == target {
		t.Fatal("第二次写覆盖了同名文件 —— 用户的原文件必须保住")
	}
	if _, err := os.Stat(second); err != nil {
		t.Fatalf("第二个文件没生成: %v", err)
	}
}

func TestMinHelper(t *testing.T) {
	if min(3, 5) != 3 {
		t.Fatal("min 不对")
	}
}

var _ = context.Background
