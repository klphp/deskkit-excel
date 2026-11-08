package xtext

import (
	"testing"
)

func TestDetectEncodings(t *testing.T) {
	gbk, err := UTF8ToGB18030("姓名,部门\n张一,技术\n")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		in   []byte
		text string
	}{
		{"纯 UTF-8", []byte("姓名,部门\n"), "姓名,部门\n"},
		{"UTF-8 BOM", append([]byte{0xEF, 0xBB, 0xBF}, []byte("姓名")...), "姓名"},
		{"GBK", gbk, "姓名,部门\n张一,技术\n"},
		{"UTF-16LE", []byte{0xFF, 0xFE, 0xD3, 0x59, 0x0D, 0x54}, "姓名"},
	}
	for _, c := range cases {
		text, enc, isText := Detect(c.in)
		if !isText {
			t.Fatalf("%s: 被判成非文本（%s）", c.name, enc)
		}
		if text != c.text {
			t.Fatalf("%s: 解出 %q，应为 %q（编码判成 %s）", c.name, text, c.text, enc)
		}
		if enc == "" || enc == EncBinary || enc == EncUnknown {
			t.Fatalf("%s: 编码名不该是 %q", c.name, enc)
		}
	}
}

// chardet 的价值在于能认出 GBK 之外的中文编码 —— 这是自己写字节启发式做不到的。
func TestDetectTraditionalChinese(t *testing.T) {
	// Big5：繁體中文
	big5 := []byte{0xA9, 0x63, 0xA6, 0x57, 0x2C, 0xBE, 0xFA, 0xAA, 0xF9, 0x0A} // 姓名,部門

	text, enc, isText := Detect(big5)
	if !isText {
		t.Fatalf("Big5 被判成非文本（%s）", enc)
	}
	if text == "" || text == string(big5) {
		t.Fatalf("Big5 没解出来，编码判成 %s，内容 %q", enc, text)
	}
}

func TestDetectBinary(t *testing.T) {
	// PNG 头 + NUL：不能当文本读，否则后面满屏乱码
	if _, enc, isText := Detect([]byte{0x89, 'P', 'N', 'G', 0x00, 0x1A, 0x0A}); isText {
		t.Fatalf("二进制被当成文本了（判成 %s）", enc)
	}
}
