package config

import (
	"os"
	"path/filepath"
	"testing"
)

// 第一版对外只呈现英文形态：语言切换器出现的条件是「可切换语言多于一种」，
// 所以仓库里的真实配置必须只有 en-US。这条用真实文件断言，
// 避免以后再出现「提交信息说改了、配置其实没改」的情况。
func TestShippedConfigIsEnglishOnly(t *testing.T) {
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

	if got := cfg.I18n.DefaultLocale; got != "en-US" {
		t.Errorf("defaultLocale = %q，期望 en-US", got)
	}
	if got := cfg.I18n.FallbackLocale; got != "en-US" {
		t.Errorf("fallbackLocale = %q，期望 en-US", got)
	}
	if len(cfg.I18n.Locales) != 1 || cfg.I18n.Locales[0] != "en-US" {
		t.Errorf("locales = %v，期望只有 en-US（多一种前端就会显示语言切换）", cfg.I18n.Locales)
	}
}
