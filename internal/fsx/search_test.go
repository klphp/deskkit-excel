package fsx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klphp/deskkit-excel/internal/fsx/xtext"
)

func TestSearchBasic(t *testing.T) {
	g, root := testGuard(t)
	mustWrite(t, filepath.Join(root, "a.txt"), "alpha\nTODO: 修一下\nomega\n")
	mustWrite(t, filepath.Join(root, "sub", "b.txt"), "no match\nTODO: 另一个\ntail\n")
	mustWrite(t, filepath.Join(root, "sub", "c.log"), "无关内容\n")

	res, err := g.Search(context.Background(), SearchOptions{Path: root, Pattern: "TODO"})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(res.Matches) != 2 {
		t.Fatalf("应命中 2 条，实际 %d: %+v", len(res.Matches), res.Matches)
	}
	// 按路径排序：a.txt 在 sub/b.txt 之前
	if filepath.Base(res.Matches[0].Path) != "a.txt" || res.Matches[0].Line != 2 {
		t.Errorf("第一条命中不对: %+v", res.Matches[0])
	}
	if filepath.Base(res.Matches[1].Path) != "b.txt" || res.Matches[1].Line != 2 {
		t.Errorf("第二条命中不对: %+v", res.Matches[1])
	}
	if !strings.Contains(res.Matches[0].Text, "修一下") {
		t.Errorf("命中行内容不对: %q", res.Matches[0].Text)
	}
	if res.Stats.FilesScanned != 3 {
		t.Errorf("应扫 3 个文件，实际 %d", res.Stats.FilesScanned)
	}
	if res.Stats.FilesMatched != 2 {
		t.Errorf("应命中 2 个文件，实际 %d", res.Stats.FilesMatched)
	}
}

func TestSearchGBK(t *testing.T) {
	g, root := testGuard(t)
	gbk, err := xtext.UTF8ToGB18030("标题,金额\n差旅费,100\n采购,200\n")
	if err != nil {
		t.Fatalf("造 GBK 样本失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "报错.csv"), gbk, 0o644); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "utf8.csv"), "差旅费,300\n")

	// 中文关键字必须能在 GBK 文件里命中 —— 这是国内用户最常踩的坑
	res, err := g.Search(context.Background(), SearchOptions{Path: root, Pattern: "差旅费"})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(res.Matches) != 2 {
		t.Fatalf("GBK 与 UTF-8 文件都应命中，实际 %d 条: %+v", len(res.Matches), res.Matches)
	}
	for _, m := range res.Matches {
		if !strings.Contains(m.Text, "差旅费") {
			t.Errorf("命中行是乱码: %q", m.Text)
		}
	}
}

func TestSearchSkipsBinaryAndBig(t *testing.T) {
	g, root := testGuard(t)
	if err := os.WriteFile(filepath.Join(root, "blob.bin"), []byte{0x00, 'T', 'O', 'D', 'O', 0x00}, 0o644); err != nil {
		t.Fatal(err)
	}
	// 超过 maxFileSize 的文本文件，且内容里有关键字
	bigContent := strings.Repeat("TODO 大文件里的关键字\n", 100)
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte(bigContent), 0o644); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "small.txt"), "TODO 小文件\n")

	res, err := g.Search(context.Background(), SearchOptions{
		Path: root, Pattern: "TODO", MaxFileSize: 100,
	})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if res.Stats.FilesSkippedBig != 1 {
		t.Errorf("应跳过 1 个超大文件，实际 %d", res.Stats.FilesSkippedBig)
	}
	if res.Stats.FilesSkippedBin != 1 {
		t.Errorf("应跳过 1 个二进制文件，实际 %d", res.Stats.FilesSkippedBin)
	}
	if len(res.Matches) != 1 || filepath.Base(res.Matches[0].Path) != "small.txt" {
		t.Fatalf("只应命中 small.txt，实际 %+v", res.Matches)
	}
	if !strings.Contains(res.Hint, "oversized files") && !strings.Contains(res.Hint, "binary files") {
		t.Errorf("the hint must say what was skipped: %s", res.Hint)
	}
}

func TestSearchExtensionsAndCase(t *testing.T) {
	g, root := testGuard(t)
	mustWrite(t, filepath.Join(root, "a.txt"), "Error happened\n")
	mustWrite(t, filepath.Join(root, "b.go"), "error happened\n")
	mustWrite(t, filepath.Join(root, "c.md"), "ERROR happened\n")

	// 只要 .go，大小写不敏感默认命中
	res, err := g.Search(context.Background(), SearchOptions{Path: root, Pattern: "error", Extensions: []string{"go"}})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(res.Matches) != 1 || filepath.Base(res.Matches[0].Path) != "b.go" {
		t.Fatalf("后缀过滤不对: %+v", res.Matches)
	}

	// 大小写敏感：只有 c.md 是全大写 ERROR
	res, err = g.Search(context.Background(), SearchOptions{Path: root, Pattern: "ERROR", CaseSensitive: true})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(res.Matches) != 1 || filepath.Base(res.Matches[0].Path) != "c.md" {
		t.Fatalf("大小写敏感结果不对: %+v", res.Matches)
	}

	// 后缀带点也认
	res, err = g.Search(context.Background(), SearchOptions{Path: root, Pattern: "happened", Extensions: []string{".txt", ".md"}})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(res.Matches) != 2 {
		t.Errorf("应命中 2 个文件，实际 %+v", res.Matches)
	}
}

func TestSearchMaxResultsTruncated(t *testing.T) {
	g, root := testGuard(t)
	for i := 0; i < 20; i++ {
		mustWrite(t, filepath.Join(root, fmt.Sprintf("f%02d.txt", i)), "hit\nhit\nhit\n")
	}
	res, err := g.Search(context.Background(), SearchOptions{Path: root, Pattern: "hit", MaxResults: 7})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(res.Matches) != 7 {
		t.Errorf("应截断到 7 条，实际 %d", len(res.Matches))
	}
	if !res.Truncated {
		t.Error("应标 truncated")
	}
}

func TestSearchSingleFile(t *testing.T) {
	g, root := testGuard(t)
	f := filepath.Join(root, "solo.txt")
	mustWrite(t, f, "aaa\nbbb\nccc\n")
	res, err := g.Search(context.Background(), SearchOptions{Path: f, Pattern: "bbb"})
	if err != nil {
		t.Fatalf("单文件搜索失败: %v", err)
	}
	if len(res.Matches) != 1 || res.Matches[0].Line != 2 {
		t.Fatalf("单文件命中不对: %+v", res.Matches)
	}
}

func TestSearchErrorsAndGuard(t *testing.T) {
	g, root := testGuard(t)
	mustWrite(t, filepath.Join(root, "a.txt"), "x\n")

	if _, err := g.Search(context.Background(), SearchOptions{Path: root, Pattern: "  "}); err == nil {
		t.Error("空 pattern 应报错")
	}
	outside := filepath.Join(filepath.Dir(root), "外面目录")
	if _, err := g.Search(context.Background(), SearchOptions{Path: outside, Pattern: "x"}); err == nil {
		t.Error("越界路径应拒绝")
	}
	if _, err := g.Search(context.Background(), SearchOptions{Path: filepath.Join(root, "没有这个目录"), Pattern: "x"}); err == nil {
		t.Error("不存在的路径应报错")
	}
	// 不存在的路径不该 panic，也不该返回半成品
}

// 并行正确性：串行与并行必须给出**完全相同**的结果。
func TestSearchParallelEqualsSerial(t *testing.T) {
	g, root := testGuard(t)
	for i := 0; i < 120; i++ {
		dir := filepath.Join(root, fmt.Sprintf("d%02d", i%7))
		content := fmt.Sprintf("line one\nline two %d\nNEEDLE here %d\n", i, i)
		mustWrite(t, filepath.Join(dir, fmt.Sprintf("f%03d.txt", i)), content)
	}
	// 混入二进制与 GBK，验证并行下分类统计也一致
	if err := os.WriteFile(filepath.Join(root, "bin.dat"), []byte{0, 1, 2, 3}, 0o644); err != nil {
		t.Fatal(err)
	}
	gbk, _ := xtext.UTF8ToGB18030("NEEDLE 国标\n")
	if err := os.WriteFile(filepath.Join(root, "gbk.txt"), gbk, 0o644); err != nil {
		t.Fatal(err)
	}

	opt := SearchOptions{Path: root, Pattern: "needle", MaxResults: 5000}
	optSer, optPar := opt, opt
	optSer.Concurrency = 1
	optPar.Concurrency = 16

	ser, err := g.Search(context.Background(), optSer)
	if err != nil {
		t.Fatalf("串行搜索失败: %v", err)
	}
	par, err := g.Search(context.Background(), optPar)
	if err != nil {
		t.Fatalf("并行搜索失败: %v", err)
	}

	if len(ser.Matches) != len(par.Matches) {
		t.Fatalf("命中数不一致: 串行 %d vs 并行 %d", len(ser.Matches), len(par.Matches))
	}
	for i := range ser.Matches {
		if ser.Matches[i] != par.Matches[i] {
			t.Fatalf("#%d 命中不一致: 串行 %+v vs 并行 %+v", i, ser.Matches[i], par.Matches[i])
		}
	}
	if ser.Stats.FilesScanned != par.Stats.FilesScanned ||
		ser.Stats.FilesSkippedBin != par.Stats.FilesSkippedBin ||
		ser.Stats.FilesMatched != par.Stats.FilesMatched {
		t.Errorf("统计不一致: 串行 %+v vs 并行 %+v", ser.Stats, par.Stats)
	}
	if len(ser.Matches) != 121 {
		t.Errorf("应命中 121 条（120 个 txt + 1 个 GBK），实际 %d", len(ser.Matches))
	}
}

// 取消必须及时退出：给一个足够大的目录树，超时后立即返回。
func TestSearchCancelStopsPromptly(t *testing.T) {
	g, root := testGuard(t)
	payload := strings.Repeat("填充内容用来撑大文件\n", 2000)
	for i := 0; i < 60; i++ {
		mustWrite(t, filepath.Join(root, fmt.Sprintf("f%02d.txt", i)), payload)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	if _, err := g.Search(ctx, SearchOptions{Path: root, Pattern: "填充", Concurrency: 4}); err == nil {
		t.Log("已取消的 ctx 仍返回结果（全部任务在取消前跑完），可接受")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("取消后应在 2 秒内返回，实际 %v", d)
	}
}

func TestSearchSkipsHiddenAndNodeModules(t *testing.T) {
	g, root := testGuard(t)
	mustWrite(t, filepath.Join(root, "ok.txt"), "target\n")
	mustWrite(t, filepath.Join(root, ".git", "config"), "target\n")
	mustWrite(t, filepath.Join(root, "node_modules", "x.js"), "target\n")

	res, err := g.Search(context.Background(), SearchOptions{Path: root, Pattern: "target"})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(res.Matches) != 1 || filepath.Base(res.Matches[0].Path) != "ok.txt" {
		t.Fatalf(".git / node_modules 应被跳过，实际 %+v", res.Matches)
	}
	if res.Stats.DirsSkipped != 2 {
		t.Errorf("应记录跳过 2 个目录，实际 %d", res.Stats.DirsSkipped)
	}
}

func TestClipLine(t *testing.T) {
	long := strings.Repeat("中", searchLineCap+10)
	got := clipLine(long, searchLineCap)
	if n := len([]rune(got)); n > searchLineCap+1 {
		t.Errorf("截断后长度 %d 超预期", n)
	}
	if !strings.HasSuffix(got, "…") {
		t.Error("截断应有省略号")
	}
	if got := clipLine("短行  ", searchLineCap); got != "短行" {
		t.Errorf("尾部空白应去掉: %q", got)
	}
}

func TestSearchRegexMatchesDecodedGBK(t *testing.T) {
	dir := t.TempDir()
	// GBK 编码的中文：grep 在默认环境下搜不到这种文件里的中文，靠正则也白搭
	gbk := "凭证号,摘要,金额\n001001,采购办公用品,1234.50\n001002,仓储租金,8800.00\n001003,差旅报销,300.00\n"
	enc, encErr := xtext.UTF8ToGB18030(gbk)
	if encErr != nil {
		t.Fatal(encErr)
	}
	if err := os.WriteFile(filepath.Join(dir, "流水.csv"), enc, 0o644); err != nil {
		t.Fatal(err)
	}
	g := Guard{Roots: []string{dir}}

	// 纯文本：能搜到（证明解码生效）
	plain, err := g.Search(context.Background(), SearchOptions{Path: dir, Pattern: "仓储"})
	if err != nil {
		t.Fatalf("纯文本搜索失败: %v", err)
	}
	if len(plain.Matches) != 1 {
		t.Fatalf("纯文本应命中 1 行，实际 %d 行: %+v", len(plain.Matches), plain.Matches)
	}

	// 正则：两条命中，正则作用在解码后的文本上
	re, err := g.Search(context.Background(), SearchOptions{Path: dir, Pattern: "仓储|采购", Regex: true})
	if err != nil {
		t.Fatalf("正则搜索失败: %v", err)
	}
	if len(re.Matches) != 2 {
		t.Fatalf("正则应命中 2 行，实际 %d 行: %+v", len(re.Matches), re.Matches)
	}
	// 行号必须准确指向原文行
	for _, m := range re.Matches {
		if m.Line != 2 && m.Line != 3 {
			t.Errorf("命中行号不对: %+v", m)
		}
	}

	// 正则锚点：只匹配以 001002 开头的行
	anchored, err := g.Search(context.Background(), SearchOptions{Path: dir, Pattern: "^001002,", Regex: true})
	if err != nil {
		t.Fatalf("锚点正则失败: %v", err)
	}
	if len(anchored.Matches) != 1 || anchored.Matches[0].Line != 3 {
		t.Fatalf("锚点正则应命中第 3 行，实际 %+v", anchored.Matches)
	}

	// 大小写：默认不敏感，显式区分时锚点仍能工作
	upper, err := g.Search(context.Background(), SearchOptions{Path: dir, Pattern: "仓储租金", Regex: true, CaseSensitive: true})
	if err != nil || len(upper.Matches) != 1 {
		t.Fatalf("区分大小写正则失败: %v %+v", err, upper.Matches)
	}
}

func TestSearchRegexInvalidPattern(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := Guard{Roots: []string{dir}}
	_, err := g.Search(context.Background(), SearchOptions{Path: dir, Pattern: "a(b", Regex: true})
	if err == nil {
		t.Fatal("非法正则应当报错，而不是静默不命中")
	}
	if !strings.Contains(err.Error(), "Invalid regular expression") {
		t.Fatalf("错误信息应说明正则不合法: %v", err)
	}
}

func TestSearchRegexFallsBackToPlainText(t *testing.T) {
	dir := t.TempDir()
	// 纯文本模式下 "a.b" 要按字面匹配，不能当成正则通配
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("axb\na.b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := Guard{Roots: []string{dir}}
	res, err := g.Search(context.Background(), SearchOptions{Path: dir, Pattern: "a.b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 1 || res.Matches[0].Line != 2 {
		t.Fatalf("纯文本模式应按字面匹配第 2 行，实际 %+v", res.Matches)
	}
}
