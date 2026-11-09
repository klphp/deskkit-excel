package config

import (
	"os"
	"path/filepath"
	"testing"
)

// 发布用的真实配置里，自更新的三个值必须和 CI 产物对得上，
// 否则用户点了「检查更新」只会得到一句没用的报错。
func TestShippedConfigCanSelfUpdate(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "app.yaml"))
	if err != nil {
		t.Fatalf("读 config/app.yaml: %v", err)
	}
	win, err := os.ReadFile(filepath.Join("..", "..", "config", "windows.yaml"))
	if err != nil {
		t.Fatalf("读 config/windows.yaml: %v", err)
	}
	cfg, err := Parse(raw, win)
	if err != nil {
		t.Fatalf("解析配置: %v", err)
	}

	if !cfg.Update.Enabled {
		t.Error("update.enabled 为 false：发出去的包里「检查更新」不会有任何反应")
	}
	if cfg.Update.Provider != "github" {
		t.Errorf("provider = %q，期望 github", cfg.Update.Provider)
	}
	// owner/repo 写错 = 查到别人的仓库或 404，两种都不会报错，只会静默失效
	if cfg.Update.Repository != "klphp/deskkit-excel" {
		t.Errorf("repository = %q，期望 klphp/deskkit-excel", cfg.Update.Repository)
	}
	// 必须与 .github/workflows/release.yml 里汇总出来的文件名一致
	if cfg.Update.ChecksumAsset != "SHA256SUMS" {
		t.Errorf("checksumAsset = %q，期望 SHA256SUMS", cfg.Update.ChecksumAsset)
	}
}
