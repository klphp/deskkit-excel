package config

import "testing"

const appBase = `
app:
  name: TestApp
  version: 1.0.0
  identifier: com.klphp.testapp
`

func parse(t *testing.T, appYAML, winYAML string) *Config {
	t.Helper()
	cfg, err := Parse([]byte(appYAML), []byte(winYAML))
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	return cfg
}

// 不写 singleInstance 时必须默认禁止多开 —— 这里用的是 *bool，
// 换成 bool 的话零值 false 会把默认值翻掉，这条测试就是防那个回归。
func TestSingleInstanceDefaultsToTrue(t *testing.T) {
	cfg := parse(t, appBase, `
windows:
  main:
    title: 主窗口
    main: true
`)
	if !cfg.App.SingleInstanceOrDefault() {
		t.Fatal("没配置 singleInstance 时应当默认禁止多开（true）")
	}
}

func TestSingleInstanceExplicitFalse(t *testing.T) {
	cfg := parse(t, appBase+"  singleInstance: false\n", `
windows:
  main:
    title: 主窗口
    main: true
`)
	if cfg.App.SingleInstanceOrDefault() {
		t.Fatal("显式写 false 时应当允许多开")
	}
}

func TestSingleInstanceExplicitTrue(t *testing.T) {
	cfg := parse(t, appBase+"  singleInstance: true\n", `
windows:
  main:
    title: 主窗口
    main: true
`)
	if !cfg.App.SingleInstanceOrDefault() {
		t.Fatal("显式写 true 时应当禁止多开")
	}
}

// 单实例回调要把主窗口拉到前面，需要的是名字而不是窗口定义。
func TestMainWindowNamePrefersMainFlag(t *testing.T) {
	cfg := parse(t, appBase, `
windows:
  main:
    title: 主窗口
  console:
    title: 控制台
    main: true
`)
	name, ok := cfg.MainWindowName()
	if !ok || name != "console" {
		t.Fatalf("应取标记 main: true 的窗口，得到 %q (%v)", name, ok)
	}
	if w, ok := cfg.MainWindow(); !ok || w.Title != "控制台" {
		t.Fatalf("MainWindow 与 MainWindowName 不一致: %+v", w)
	}
}

// 没有 main: true 时退回名字叫 main 的窗口，和 MainWindow 的兜底保持一致。
func TestMainWindowNameFallsBackToMain(t *testing.T) {
	cfg := parse(t, appBase, `
windows:
  main:
    title: 主窗口
  settings:
    title: 设置
`)
	name, ok := cfg.MainWindowName()
	if !ok || name != "main" {
		t.Fatalf("应退回 main，得到 %q (%v)", name, ok)
	}
}

// 没有任何窗口时 Parse 直接报错，这条顺带锁住那个校验。
func TestParseRequiresMainWindow(t *testing.T) {
	if _, err := Parse([]byte(appBase), []byte("windows: {}\n")); err == nil {
		t.Fatal("没有主窗口时 Parse 应当报错")
	}
}
