package xtext

import "testing"

func TestDetectEncodings(t *testing.T) {
	gbk, err := UTF8ToGB18030("姓名,部门\n张一,技术\n")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		in   []byte
		want string
		text string
	}{
		{"纯 UTF-8", []byte("姓名,部门\n"), EncUTF8, "姓名,部门\n"},
		{"UTF-8 BOM", append([]byte{0xEF, 0xBB, 0xBF}, []byte("姓名")...), EncUTF8BOM, "姓名"},
		{"GBK", gbk, EncGB18030, "姓名,部门\n张一,技术\n"},
		{"UTF-16LE", []byte{0xFF, 0xFE, 0xD3, 0x59, 0x0D, 0x54}, EncUTF16LE, "姓名"},
	}
	for _, c := range cases {
		text, enc, isText := Detect(c.in)
		if !isText {
			t.Fatalf("%s: 被判成非文本", c.name)
		}
		if enc != c.want {
			t.Fatalf("%s: 编码判成 %s，应为 %s", c.name, enc, c.want)
		}
		if text != c.text {
			t.Fatalf("%s: 解出 %q，应为 %q", c.name, text, c.text)
		}
	}
}

func TestDetectBinary(t *testing.T) {
	// PNG 头 + NUL 字节：不能当文本读，否则后面满屏乱码
	if _, enc, isText := Detect([]byte{0x89, 'P', 'N', 'G', 0x00, 0x1A, 0x0A}); isText {
		t.Fatalf("二进制被当成文本了（判成 %s）", enc)
	}
}

func TestGuessDelimiter(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{"逗号", []string{"a,b,c", "1,2,3", "4,5,6"}, ","},
		{"分号", []string{"a;b;c", "1;2;3"}, ";"},
		{"制表符", []string{"a\tb\tc", "1\t2\t3"}, "\t"},
		{"竖线", []string{"a|b|c", "1|2|3"}, "|"},
		// 散文里逗号只出现一次、且每行次数不一致 → 不该判成表格
		{"散文", []string{"今天天气不错，我们出去走走吧", "他说明天再来"}, ""},
	}
	for _, c := range cases {
		if got := GuessDelimiter(c.lines); got != c.want {
			t.Fatalf("%s: 判成 %q，应为 %q", c.name, got, c.want)
		}
	}
}
