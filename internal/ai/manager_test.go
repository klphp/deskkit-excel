package ai

import (
	"context"
	"testing"
	"time"

	"github.com/klphp/excel-batch-tool/internal/config"
)

func testAIConfig() config.AI {
	on := true
	return config.AI{
		Enabled: &on,
		Default: "cloud",
		Providers: map[string]config.Provider{
			"cloud": {
				Kind:      "openai",
				Label:     "Cloud",
				BaseURL:   "https://api.example.com/v1",
				Model:     "example-model",
				APIKeyEnv: "EXAMPLE_KEY",
				Timeout:   5 * time.Second,
				Retry:     config.Retry{MaxAttempts: 1},
			},
			"ollama": {
				Kind:    "ollama",
				BaseURL: "http://127.0.0.1:11434",
				Model:   "qwen2.5:7b",
			},
		},
		Local: config.LocalAI{Enabled: false},
	}.WithDefaults()
}

func TestManagerRequiresKey(t *testing.T) {
	m := NewManager(testAIConfig(), func(string) string { return "" }, t.TempDir())
	if _, e := m.Client(context.Background(), "cloud"); e == nil || e.Kind != KindConfig {
		t.Fatalf("没有密钥时应报 config，实际 %+v", e)
	}
}

func TestManagerUsesEnvKey(t *testing.T) {
	m := NewManager(testAIConfig(), func(name string) string {
		if name == "cloud" {
			return "sk-env"
		}
		return ""
	}, t.TempDir())
	c, e := m.Client(context.Background(), "cloud")
	if e != nil {
		t.Fatalf("有环境变量密钥时不该报错: %v", e)
	}
	if c.Name() != "cloud" || c.Model() != "example-model" {
		t.Fatalf("客户端不对: %s / %s", c.Name(), c.Model())
	}
}

// 优先级：用户设置 > 环境变量。
func TestManagerSettingsOverrideEnv(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(testAIConfig(), func(string) string { return "sk-env" }, dir)
	if err := m.SaveSettings(Settings{
		Provider: "cloud",
		APIKey:   "sk-user",
		Model:    "user-model",
		BaseURL:  "https://user.example.com/v1",
	}); err != nil {
		t.Fatalf("保存设置失败: %v", err)
	}
	if got := m.Key("cloud"); got != "sk-user" {
		t.Fatalf("用户设置应覆盖环境变量，实际 %q", got)
	}
	c, e := m.Client(context.Background(), "cloud")
	if e != nil {
		t.Fatalf("不该报错: %v", e)
	}
	if c.Model() != "user-model" {
		t.Fatalf("模型应被用户设置覆盖，实际 %q", c.Model())
	}
}

// 本地服务不需要密钥。
func TestManagerLocalProviderNeedsNoKey(t *testing.T) {
	cfg := testAIConfig()
	m := NewManager(cfg, func(string) string { return "" }, t.TempDir())
	c, e := m.Client(context.Background(), "ollama")
	if e != nil {
		t.Fatalf("本地服务不该要求密钥: %v", e)
	}
	if c.Name() != "ollama" {
		t.Fatalf("名字不对: %s", c.Name())
	}
}

func TestManagerResolveDefault(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(testAIConfig(), func(string) string { return "k" }, dir)
	// 空名字 → 配置默认
	if _, e := m.Client(context.Background(), ""); e != nil {
		t.Fatalf("应当回退到默认服务商: %v", e)
	}
	// 有用户设置时优先用用户选的
	if err := m.SaveSettings(Settings{Provider: "ollama"}); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	c, e := m.Client(context.Background(), "")
	if e != nil || c.Name() != "ollama" {
		t.Fatalf("应当用用户选的服务商，实际 %v / %v", c.Name(), e)
	}
}

func TestManagerUnknownProvider(t *testing.T) {
	m := NewManager(testAIConfig(), func(string) string { return "k" }, t.TempDir())
	_, e := m.Client(context.Background(), "nope")
	if e == nil || e.Kind != KindConfig {
		t.Fatalf("未知服务商应报 config，实际 %+v", e)
	}
}

// 探测器关掉时不应该去连本机端口（否则每次打开设置页都白等）。
func TestManagerProbeDisabled(t *testing.T) {
	cfg := testAIConfig()
	cfg.Local.Enabled = false
	m := NewManager(cfg, nil, t.TempDir())
	if got := m.Probe(context.Background()); got != nil {
		t.Fatalf("探测器关闭时应返回 nil，实际 %+v", got)
	}
}

func TestAIConfigDefaults(t *testing.T) {
	cfg := config.AI{
		Providers: map[string]config.Provider{"x": {BaseURL: "http://a"}},
	}.WithDefaults()
	if !cfg.IsEnabled() {
		t.Fatal("没写 enabled 但给了 providers，应当自动启用")
	}
	if cfg.Default != "x" {
		t.Fatalf("默认服务商应自动选中唯一的那个，实际 %q", cfg.Default)
	}
	p := cfg.Providers["x"]
	if p.Kind != "openai" || p.Label != "x" || p.Timeout != 60*time.Second || p.Retry.MaxAttempts != 3 {
		t.Fatalf("默认值没补上: %+v", p)
	}
	// 完全空配置保持关闭
	if (config.AI{}).WithDefaults().IsEnabled() {
		t.Fatal("空配置不该自动启用 AI")
	}

	// 显式写 false 必须压过「配了 providers」的自动启用
	off := false
	explicitOff := config.AI{
		Enabled:   &off,
		Providers: map[string]config.Provider{"x": {BaseURL: "http://a"}},
	}.WithDefaults()
	if explicitOff.IsEnabled() {
		t.Fatal("显式 enabled: false 不该被 providers 覆盖")
	}
}

// 指向 localhost 但没探测到服务时，不能算「可用」——
// 否则前端会显示「已就绪」，用户一点就报错。
func TestLocalProviderNotReadyWithoutProbe(t *testing.T) {
	cfg := testAIConfig()
	cfg.Local.Enabled = false // 关掉探测 = 探测结果为空
	m := NewManager(cfg, func(string) string { return "" }, t.TempDir())

	for _, info := range m.Providers(context.Background()) {
		if info.Name != "ollama" {
			continue
		}
		if info.Ready {
			t.Fatal("没有探测到本地服务时不该标记为可用")
		}
		if info.Error == "" {
			t.Fatal("不可用时应说明原因")
		}
		return
	}
	t.Fatal("没找到 ollama 服务商")
}

func TestEndpointOfNormalizes(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:11434":    "http://127.0.0.1:11434",
		"http://127.0.0.1:11434/":   "http://127.0.0.1:11434",
		"http://127.0.0.1:1234/v1":  "http://127.0.0.1:1234",
		"http://127.0.0.1:1234/v1/": "http://127.0.0.1:1234",
	}
	for in, want := range cases {
		if got := endpointOf(in); got != want {
			t.Errorf("%q → %q，期望 %q", in, got, want)
		}
	}
}
