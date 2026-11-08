package ai

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := Settings{Provider: "deepseek", Model: "deepseek-chat", APIKey: "sk-secret", BaseURL: "https://x/v1"}
	if err := SaveSettings(dir, in); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	out, err := LoadSettings(dir)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if out != in {
		t.Fatalf("往返不一致: %+v != %+v", out, in)
	}
}

// 密钥文件必须只有本人可读。0644 等于把 key 给同机器所有用户。
func TestSettingsFileMode0600(t *testing.T) {
	dir := t.TempDir()
	if err := SaveSettings(dir, Settings{APIKey: "sk-secret"}); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, settingsFile))
	if err != nil {
		t.Fatalf("文件不存在: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		// Windows 没有 POSIX 权限位（实测 os.Chmod(0600) 后 Stat 仍是 0666），
		// 这个断言只在 POSIX 平台上成立；Windows 上真正的保护靠用户目录 ACL。
		if runtime.GOOS == "windows" {
			t.Logf("跳过权限断言：Windows 无 POSIX 权限位（实际 %o）", perm)
		} else {
			t.Fatalf("权限应为 0600，实际 %o", perm)
		}
	}
}

// 覆盖已存在且权限更宽松的文件时，必须把权限收回来。
func TestSaveTightensExistingPerms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, settingsFile)
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("准备旧文件失败: %v", err)
	}
	if err := SaveSettings(dir, Settings{APIKey: "k"}); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	info, _ := os.Stat(path)
	if perm := info.Mode().Perm(); perm != 0o600 {
		// Windows 没有 POSIX 权限位（实测 os.Chmod(0600) 后 Stat 仍是 0666），
		// 这个断言只在 POSIX 平台上成立；Windows 上真正的保护靠 ACL。
		if runtime.GOOS == "windows" {
			t.Logf("跳过权限断言：Windows 无 POSIX 权限位（实际 %o）", perm)
		} else {
			t.Fatalf("权限应收紧到 0600，实际 %o", perm)
		}
	}
}

func TestLoadSettingsMissingFileIsNotError(t *testing.T) {
	out, err := LoadSettings(t.TempDir())
	if err != nil {
		t.Fatalf("首次运行不该报错: %v", err)
	}
	if out != (Settings{}) {
		t.Fatalf("应当是零值: %+v", out)
	}
}

func TestLoadSettingsBrokenJSONIsTolerated(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, settingsFile), []byte("{ 坏掉的"), 0o600)
	if _, err := LoadSettings(dir); err == nil {
		t.Fatal("坏 JSON 应当返回错误（由调用方决定忽略）")
	}
	// 但 NewManager 要能带病继续跑
	m := NewManager(testAIConfig(), nil, dir)
	if m == nil || m.Settings() != (Settings{}) {
		t.Fatalf("坏设置文件不该让 Manager 起不来: %+v", m.Settings())
	}
}
