//go:build windows

package dpapi

import (
	"bytes"
	"testing"
)

// 往返 + 密文不含明文。作用域可能是 user 或 machine（取决于运行上下文里
// 用户配置文件是否加载），两者都必须能解回原文。
func TestProtectRoundTrip(t *testing.T) {
	plain := []byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	ct, err := Protect(plain)
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	if len(ct) < 2 {
		t.Fatalf("密文太短: %d", len(ct))
	}
	scope := ct[0]
	if scope != scopeUser && scope != scopeMachine {
		t.Fatalf("未知作用域: 0x%02x", scope)
	}
	t.Logf("实际作用域: 0x%02x（0x01=user, 0x02=machine）", scope)
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
	for _, bad := range [][]byte{
		[]byte("not-a-dpapi-blob-at-all"),
		{0x09, 1, 2, 3},               // 未知作用域
		{scopeUser, 1, 2, 3},          // 合法前缀但内容垃圾
		{scopeMachine, 1, 2, 3, 4, 5}, // 同理
	} {
		if _, err := Unprotect(bad); err == nil {
			t.Fatalf("垃圾数据必须报错: %x", bad)
		}
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
