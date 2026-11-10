package platform

import (
	"strings"
	"testing"
)

func TestSupported(t *testing.T) {
	cases := []struct {
		major uint32
		want  bool
	}{
		{5, false}, // Windows 2000
		{6, false}, // XP / Vista / 7 / 8 / 8.1
		{10, true}, // Windows 10 / 11
		{11, true}, // 将来的版本不该被误拦
	}
	for _, c := range cases {
		if got := Supported(c.major); got != c.want {
			t.Errorf("Supported(%d) = %v，期望 %v", c.major, got, c.want)
		}
	}
}

func TestUnsupportedErrorMessage(t *testing.T) {
	err := UnsupportedError(Version{Major: 6, Minor: 1, Build: 7601}, Supported(6))
	if err == nil {
		t.Fatal("Windows 7 应该被拒绝")
	}
	msg := err.Error()
	for _, want := range []string{"Windows 10", "6.1", "7601"} {
		if !strings.Contains(msg, want) {
			t.Errorf("提示里应包含 %q，实际: %s", want, msg)
		}
	}
}

func TestUnsupportedErrorNilWhenSupported(t *testing.T) {
	if err := UnsupportedError(Version{Major: 10, Minor: 0, Build: 19045}, Supported(10)); err != nil {
		t.Fatalf("Windows 10 不该报错: %v", err)
	}
}

func TestVersionStringUnknown(t *testing.T) {
	if got := (Version{}).String(); got != "unknown" {
		t.Errorf("a zero-value version must show 'unknown', actual %q", got)
	}
}
