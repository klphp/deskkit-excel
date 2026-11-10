package fsx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klphp/deskkit-excel/internal/fsx/xtext"
)

func TestReadBasicSegment(t *testing.T) {
	g, root := testGuard(t)
	f := filepath.Join(root, "a.txt")
	mustWrite(t, f, "l1\nl2\nl3\nl4\nl5\n")

	res, err := g.Read(context.Background(), ReadOptions{Path: f, Offset: 2, Limit: 2})
	if err != nil {
		t.Fatalf("读失败: %v", err)
	}
	if res.Encoding != xtext.EncUTF8 {
		t.Errorf("编码应为 utf-8，实际 %s", res.Encoding)
	}
	if res.TotalLines != 5 {
		t.Errorf("总行数应为 5，实际 %d", res.TotalLines)
	}
	if !res.TotalLinesExact {
		t.Error("小文件的总行数应是精确的")
	}
	if res.StartLine != 2 || res.EndLine != 3 {
		t.Errorf("行号区间应为 2-3，实际 %d-%d", res.StartLine, res.EndLine)
	}
	if got := strings.Join(res.Lines, ","); got != "l2,l3" {
		t.Errorf("内容不对: %q", got)
	}
	if !res.Truncated {
		t.Error("只读了 2 行，后面还有内容，应标 truncated")
	}
}

func TestReadDefaultsAndCaps(t *testing.T) {
	g, root := testGuard(t)
	f := filepath.Join(root, "many.txt")
	var sb strings.Builder
	for i := 0; i < 300; i++ {
		sb.WriteString("line\n")
	}
	mustWrite(t, f, sb.String())

	// 默认 limit 200
	res, err := g.Read(context.Background(), ReadOptions{Path: f})
	if err != nil {
		t.Fatalf("读失败: %v", err)
	}
	if len(res.Lines) != defaultReadLimit {
		t.Errorf("默认应返回 %d 行，实际 %d", defaultReadLimit, len(res.Lines))
	}
	if !res.Truncated {
		t.Error("还有 100 行没读，应标 truncated")
	}

	// limit 超上限被夹到 5 万
	res, err = g.Read(context.Background(), ReadOptions{Path: f, Limit: 999999})
	if err != nil {
		t.Fatalf("读失败: %v", err)
	}
	if len(res.Lines) != 300 {
		t.Errorf("文件只有 300 行，应全部返回，实际 %d", len(res.Lines))
	}
	if res.Truncated {
		t.Error("读完了，不该标 truncated")
	}
}

func TestReadGBK(t *testing.T) {
	g, root := testGuard(t)
	f := filepath.Join(root, "gbk.txt")
	gbk, err := xtext.UTF8ToGB18030("第一行\n第二行中文\n第三行\n")
	if err != nil {
		t.Fatalf("造 GBK 样本失败: %v", err)
	}
	if err := os.WriteFile(f, gbk, 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := g.Read(context.Background(), ReadOptions{Path: f})
	if err != nil {
		t.Fatalf("GBK 文件应能读: %v", err)
	}
	if res.Encoding != xtext.EncGB18030 {
		t.Errorf("应识别为 gb18030，实际 %s", res.Encoding)
	}
	if got := strings.Join(res.Lines, "|"); got != "第一行|第二行中文|第三行" {
		t.Errorf("GBK 转码内容不对: %q", got)
	}
	if !strings.Contains(res.Hint, "national-standard encoding") {
		t.Errorf("the hint must say this is a national-standard encoding: %s", res.Hint)
	}
}

func TestReadUTF16AndBOM(t *testing.T) {
	g, root := testGuard(t)

	// UTF-8 BOM
	bom := filepath.Join(root, "bom.txt")
	body := append([]byte{0xEF, 0xBB, 0xBF}, []byte("带BOM的行1\n行2\n")...)
	if err := os.WriteFile(bom, body, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := g.Read(context.Background(), ReadOptions{Path: bom})
	if err != nil {
		t.Fatalf("BOM 文件读取失败: %v", err)
	}
	if res.Encoding != xtext.EncUTF8BOM {
		t.Errorf("应识别为 utf-8-bom，实际 %s", res.Encoding)
	}
	if strings.Contains(res.Lines[0], "\ufeff") {
		t.Errorf("BOM 不该出现在正文里: %q", res.Lines[0])
	}
	if res.Lines[0] != "带BOM的行1" {
		t.Errorf("首行不对: %q", res.Lines[0])
	}

	// UTF-16LE（带 BOM）
	u16 := filepath.Join(root, "u16.txt")
	raw := []byte{0xFF, 0xFE}
	for _, r := range "第一行\n第二行\n" {
		raw = append(raw, byte(r), byte(r>>8))
	}
	if err := os.WriteFile(u16, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = g.Read(context.Background(), ReadOptions{Path: u16})
	if err != nil {
		t.Fatalf("UTF-16 文件读取失败: %v", err)
	}
	if res.Encoding != xtext.EncUTF16LE {
		t.Errorf("应识别为 utf-16le，实际 %s", res.Encoding)
	}
	if got := strings.Join(res.Lines, "|"); got != "第一行|第二行" {
		t.Errorf("UTF-16 内容不对: %q", got)
	}
}

func TestReadRejectsBinary(t *testing.T) {
	g, root := testGuard(t)
	f := filepath.Join(root, "bin.dat")
	if err := os.WriteFile(f, []byte{0x00, 0x01, 0x02, 0x00, 0xFF, 0x00}, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := g.Read(context.Background(), ReadOptions{Path: f})
	if err == nil {
		t.Fatal("二进制文件必须拒绝")
	}
	if !strings.Contains(err.Error(), "binary") {
		t.Errorf("the error must say the file is binary: %v", err)
	}
}

func TestReadOffsetOutOfRange(t *testing.T) {
	g, root := testGuard(t)
	f := filepath.Join(root, "short.txt")
	mustWrite(t, f, "只有一行\n")

	if _, err := g.Read(context.Background(), ReadOptions{Path: f, Offset: 99}); err == nil {
		t.Fatal("offset 超出总行数应报错")
	}
	// offset 从 1 开始：负数/0 当 1 处理，不能 panic
	res, err := g.Read(context.Background(), ReadOptions{Path: f, Offset: -5})
	if err != nil {
		t.Fatalf("offset 非法值应退回 1 而不是报错: %v", err)
	}
	if res.StartLine != 1 || len(res.Lines) != 1 {
		t.Errorf("应退回从第 1 行读，实际 start=%d lines=%d", res.StartLine, len(res.Lines))
	}
}

func TestReadMaxBytesWindow(t *testing.T) {
	g, root := testGuard(t)
	f := filepath.Join(root, "big.txt")
	// 每行 20 字节，200 行 = 4000 字节
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString("0123456789012345678\n")
	}
	mustWrite(t, f, sb.String())

	res, err := g.Read(context.Background(), ReadOptions{Path: f, MaxBytes: 1000})
	if err != nil {
		t.Fatalf("读失败: %v", err)
	}
	if res.TotalLinesExact {
		t.Error("只读了前 1000 字节，总行数不该标成精确")
	}
	if !res.Truncated {
		t.Error("文件没读完，应标 truncated")
	}
	if !strings.Contains(res.Hint, "maxBytes") {
		t.Errorf("提示里应告诉调用方可以调大 maxBytes: %s", res.Hint)
	}
	// offset 落在窗口外：应给出可理解的错误而不是空结果
	if _, err := g.Read(context.Background(), ReadOptions{Path: f, Offset: 190, MaxBytes: 1000}); err == nil {
		t.Error("offset 超出已读窗口时应报错并说明原因")
	}
}

func TestReadLongLineCut(t *testing.T) {
	g, root := testGuard(t)
	f := filepath.Join(root, "oneline.txt")
	mustWrite(t, f, strings.Repeat("啊", readLineCap+50)+"\n第二行\n")

	res, err := g.Read(context.Background(), ReadOptions{Path: f})
	if err != nil {
		t.Fatalf("读失败: %v", err)
	}
	if res.LongLinesCut != 1 {
		t.Errorf("应记录 1 行被截断，实际 %d", res.LongLinesCut)
	}
	if n := len([]rune(res.Lines[0])); n > readLineCap+20 {
		t.Errorf("超长行没被截断，长度 %d", n)
	}
	if res.Lines[1] != "第二行" {
		t.Errorf("第二行不该受影响: %q", res.Lines[1])
	}
}

func TestReadCRLFAndEmpty(t *testing.T) {
	g, root := testGuard(t)
	f := filepath.Join(root, "crlf.txt")
	mustWrite(t, f, "a\r\nb\r\nc\r\n")
	res, err := g.Read(context.Background(), ReadOptions{Path: f})
	if err != nil {
		t.Fatalf("读失败: %v", err)
	}
	if got := strings.Join(res.Lines, ","); got != "a,b,c" {
		t.Errorf("CRLF 应被正确处理: %q（总行数 %d）", got, res.TotalLines)
	}
	if res.TotalLines != 3 {
		t.Errorf("CRLF 文件应有 3 行，实际 %d", res.TotalLines)
	}

	empty := filepath.Join(root, "empty.txt")
	mustWrite(t, empty, "")
	res, err = g.Read(context.Background(), ReadOptions{Path: empty})
	if err != nil {
		t.Fatalf("空文件不该报错: %v", err)
	}
	if res.TotalLines != 0 || len(res.Lines) != 0 {
		t.Errorf("空文件应返回 0 行，实际 %d", res.TotalLines)
	}
}

func TestReadRejectsOutsideGuard(t *testing.T) {
	g, root := testGuard(t)
	outside := filepath.Join(filepath.Dir(root), "外面.txt")
	mustWrite(t, outside, "x")
	if _, err := g.Read(context.Background(), ReadOptions{Path: outside}); err == nil {
		t.Fatal("越界路径必须拒绝")
	}
	if _, err := g.Read(context.Background(), ReadOptions{Path: root}); err == nil {
		t.Fatal("对目录调用 fs_read 应报错")
	}
}

func TestReadManyParallelMatchesSerial(t *testing.T) {
	g, root := testGuard(t)
	var paths []string
	for i := 0; i < 40; i++ {
		p := filepath.Join(root, "f"+string(rune('a'+i%26))+string(rune('a'+i/26))+".txt")
		mustWrite(t, p, "第一行\n目标\n第三行\n")
		paths = append(paths, p)
	}
	// 混一个不存在的，验证单文件失败不影响其它文件
	paths = append(paths, filepath.Join(root, "不存在.txt"))

	serial, err := g.ReadMany(context.Background(), paths, ReadOptions{Concurrency: 1})
	if err != nil {
		t.Fatalf("串行批量读失败: %v", err)
	}
	par, err := g.ReadMany(context.Background(), paths, ReadOptions{Concurrency: 8})
	if err != nil {
		t.Fatalf("并行批量读失败: %v", err)
	}
	if len(serial) != len(par) || len(par) != len(paths) {
		t.Fatalf("结果条数不一致: serial=%d par=%d want=%d", len(serial), len(par), len(paths))
	}
	for i := range serial {
		a, b := serial[i], par[i]
		if a.Path != b.Path {
			t.Errorf("#%d 路径不一致: %s vs %s", i, a.Path, b.Path)
		}
		if (a.Error == "") != (b.Error == "") {
			t.Errorf("#%d 错误状态不一致: %q vs %q", i, a.Error, b.Error)
		}
		if a.Result != nil && b.Result != nil {
			if a.Result.TotalLines != b.Result.TotalLines ||
				strings.Join(a.Result.Lines, "|") != strings.Join(b.Result.Lines, "|") {
				t.Errorf("#%d 内容不一致", i)
			}
		}
	}
	// 最后一个文件不存在：错误记在条目里，条数不变
	last := par[len(par)-1]
	if last.Error == "" || last.Result != nil {
		t.Errorf("不存在的文件应只记错误: %+v", last)
	}
}

func TestReadManyEmptyPaths(t *testing.T) {
	g, _ := testGuard(t)
	if _, err := g.ReadMany(context.Background(), nil, ReadOptions{}); err == nil {
		t.Fatal("paths 为空应报错")
	}
}
