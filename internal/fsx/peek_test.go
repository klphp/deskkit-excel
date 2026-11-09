package fsx

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

func writeTemp(t *testing.T, name string, content []byte) (string, Guard) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	// Guard 限定在临时目录，避免误碰用户主目录
	return path, Guard{Roots: []string{dir}}
}

func TestPeekUTF8CSV(t *testing.T) {
	body := "姓名,部门,金额\n张一,技术,100\n张二,销售,200\n"
	path, g := writeTemp(t, "a.csv", []byte(body))

	got, err := g.Peek(context.Background(), PeekOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if got.Encoding != "utf-8" {
		t.Fatalf("编码应为 utf-8，实际 %s", got.Encoding)
	}
	if got.Delimiter != "," {
		t.Fatalf("分隔符应为逗号，实际 %q", got.Delimiter)
	}
	if got.Columns != 3 {
		t.Fatalf("列数应为 3，实际 %d", got.Columns)
	}
	if len(got.Header) != 3 || got.Header[0] != "姓名" {
		t.Fatalf("表头不对: %+v", got.Header)
	}
}

// 国内导出的 CSV 十有八九是 GBK —— 这是最容易踩的一脚。
func TestPeekGBKCSV(t *testing.T) {
	utf8Body := "姓名,部门\n张一,技术\n"
	gbk, _, err := transform.Bytes(simplifiedchinese.GBK.NewEncoder(), []byte(utf8Body))
	if err != nil {
		t.Fatal(err)
	}
	path, g := writeTemp(t, "gbk.csv", gbk)

	got, err := g.Peek(context.Background(), PeekOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if got.Encoding != "gb18030" {
		t.Fatalf("应识别为国标编码，实际 %s", got.Encoding)
	}
	if len(got.Lines) == 0 || got.Lines[0] != "姓名,部门" {
		t.Fatalf("解码后首行应正确，实际 %q", got.Lines)
	}
}

func TestPeekUTF8BOM(t *testing.T) {
	path, g := writeTemp(t, "bom.csv", append([]byte{0xEF, 0xBB, 0xBF}, []byte("a,b\n1,2\n")...))
	got, err := g.Peek(context.Background(), PeekOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if got.Encoding != "utf-8-bom" {
		t.Fatalf("应识别 BOM，实际 %s", got.Encoding)
	}
	if got.Header[0] != "a" {
		t.Fatalf("BOM 应该被剥掉，实际表头 %+v", got.Header)
	}
}

func TestPeekSemicolonAndTab(t *testing.T) {
	cases := []struct{ body, delim string }{
		{"a;b;c\n1;2;3\n", ";"},
		{"a\tb\tc\n1\t2\t3\n", "\t"},
		{"a|b|c\n1|2|3\n", "|"},
	}
	for _, c := range cases {
		path, g := writeTemp(t, "x.csv", []byte(c.body))
		got, err := g.Peek(context.Background(), PeekOptions{Path: path})
		if err != nil {
			t.Fatal(err)
		}
		if got.Delimiter != c.delim {
			t.Errorf("期望分隔符 %q，实际 %q（内容 %q）", c.delim, got.Delimiter, c.body)
		}
	}
}

// 散文里偶尔出现一个逗号，不该被当成表格。
func TestPeekProseIsNotTabular(t *testing.T) {
	path, g := writeTemp(t, "note.txt", []byte("这是一段说明文字，里面有逗号。\n第二行很短。\n"))
	got, err := g.Peek(context.Background(), PeekOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if got.Delimiter != "" {
		t.Fatalf("散文不该识别成表格，实际分隔符 %q", got.Delimiter)
	}
}

func TestPeekBinary(t *testing.T) {
	path, g := writeTemp(t, "x.bin", []byte{0x50, 0x4B, 0x03, 0x04, 0x00, 0x00, 0x01})
	got, err := g.Peek(context.Background(), PeekOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "binary" {
		t.Fatalf("应识别为二进制，实际 %s", got.Kind)
	}
}

func TestPeekEmpty(t *testing.T) {
	path, g := writeTemp(t, "empty.csv", nil)
	got, err := g.Peek(context.Background(), PeekOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "empty" {
		t.Fatalf("应识别为空文件，实际 %s", got.Kind)
	}
}

func TestPeekRejectsOutsideRoots(t *testing.T) {
	_, g := writeTemp(t, "a.csv", []byte("a,b\n"))
	if _, err := g.Peek(context.Background(), PeekOptions{Path: "/etc/hosts"}); err == nil {
		t.Fatal("Roots 之外的路径应该被拒绝")
	}
}
