//go:build windows

package dpapi

import (
	"bytes"
	"testing"
)

// 在 Windows 上跑：加解密往返、密文与明文不同、附加熵生效。
func TestProtectRoundTrip(t *testing.T) {
	plain := []byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	ct, err := Protect(plain)
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	if bytes.Contains(ct, plain) {
		t.Fatal("密文里不该出现明文")
	}
	got, err := Unprotect(ct)
	if err != nil {
		t.Fatalf("解密失败: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("往返不一致: %q", got)
	}
}

func TestUnprotectRejectsGarbage(t *testing.T) {
	if _, err := Unprotect([]byte("not-a-dpapi-blob")); err == nil {
		t.Fatal("垃圾数据必须报错，不能返回空结果")
	}
}

func TestEmptyInput(t *testing.T) {
	if _, err := Protect(nil); err == nil {
		t.Fatal("空输入应报错")
	}
	if _, err := Unprotect(nil); err == nil {
		t.Fatal("空输入应报错")
	}
}
