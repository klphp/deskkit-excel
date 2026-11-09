//go:build !windows

package dpapi

import (
	"errors"
	"testing"
)

// 非 Windows 平台必须干净地返回「不支持」，不能 panic、不能返回半成品，
// 否则调用方没法判断该不该退化到文件方案。
func TestUnsupportedOnNonWindows(t *testing.T) {
	if _, err := Protect([]byte("k")); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Protect 应返回 ErrUnsupported，实际: %v", err)
	}
	if _, err := Unprotect([]byte("k")); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Unprotect 应返回 ErrUnsupported，实际: %v", err)
	}
}
