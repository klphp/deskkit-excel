package ai

import (
	"context"
	"strings"
	"sync"

	"github.com/klphp/deskkit/internal/config"
)

// localPrefix 本地探测出来的服务商名前缀，形如 local@http://127.0.0.1:11434。
//
// 用前缀而不是另开一个 map：前端拿到的就是一个字符串名字，
// 传回来时不需要额外字段，接口保持简单。
const localPrefix = "local@"

// ProviderInfo 给前端用的服务商快照。
type ProviderInfo struct {
	Name    string   `json:"name"`
	Label   string   `json:"label"`
	Kind    string   `json:"kind"`
	BaseURL string   `json:"baseURL"`
	Model   string   `json:"model"`
	HasKey  bool     `json:"hasKey"` // 是否已拿到密钥（环境变量或用户设置）
	Local   bool     `json:"local"`  // 是否为本地探测出来的
	Ready   bool     `json:"ready"`  // 当前能否直接用
	Error   string   `json:"error,omitempty"`
	Models  []string `json:"models,omitempty"`
}

// Manager 配置 + 用户设置的合并视图，也是服务的唯一入口。
//
// 为什么不把「读设置、取密钥、建客户端」散到服务层：这三件事的
// 优先级规则（用户设置 > 配置文件 > 环境变量）必须只有一处实现，
// 否则调试时永远说不清哪个值生效了。
type Manager struct {
	cfg    config.AI
	keyEnv func(string) string
	dir    string

	mu        sync.RWMutex
	st        Settings
	lastProbe []LocalEndpoint
}

// NewManager 构造。keyEnv 用于按 provider 名取环境变量里的密钥。
func NewManager(cfg config.AI, keyEnv func(string) string, dir string) *Manager {
	if keyEnv == nil {
		keyEnv = func(string) string { return "" }
	}
	m := &Manager{cfg: cfg, keyEnv: keyEnv, dir: dir}
	// 设置读不出来时用零值继续跑：不能因为一个坏掉的 ai.json 让应用起不来
	if st, err := LoadSettings(dir); err == nil {
		m.st = st
	}
	return m
}

// Enabled 报告 AI 功能是否启用。
func (m *Manager) Enabled() bool { return m.cfg.IsEnabled() }

// Default 默认服务商名。
func (m *Manager) Default() string { return m.cfg.Default }

// Settings 当前用户设置（只读快照）。
func (m *Manager) Settings() Settings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.st
}

// SaveSettings 保存并立即生效。返回的是脱敏后的快照，密钥不回传前端。
func (m *Manager) SaveSettings(s Settings) error {
	if err := SaveSettings(m.dir, s); err != nil {
		return err
	}
	m.mu.Lock()
	m.st = s
	m.mu.Unlock()
	return nil
}

// Probe 探测本地模型服务并缓存结果（供 local@ 客户端解析用）。
func (m *Manager) Probe(ctx context.Context) []LocalEndpoint {
	if !m.cfg.Local.Enabled {
		return nil
	}
	if m.Settings().DisableLocalProbe {
		return nil
	}
	out := ProbeLocal(ctx, m.cfg.Local.Endpoints, m.cfg.Local.ProbeTimeout)
	m.mu.Lock()
	m.lastProbe = out
	m.mu.Unlock()
	return out
}

// Providers 列出所有可选服务商：配置里的 + 本地探测到的。
func (m *Manager) Providers(ctx context.Context) []ProviderInfo {
	st := m.Settings()
	out := make([]ProviderInfo, 0, len(m.cfg.Providers)+3)

	for name, p := range m.cfg.Providers {
		info := ProviderInfo{
			Name:    name,
			Label:   p.Label,
			Kind:    p.Kind,
			BaseURL: p.BaseURL,
			Model:   p.Model,
		}
		if st.Provider == name {
			if st.BaseURL != "" {
				info.BaseURL = st.BaseURL
			}
			if st.Model != "" {
				info.Model = st.Model
			}
		}
		info.HasKey = m.Key(name) != ""
		info.Ready = info.HasKey || isLocalURL(info.BaseURL)
		out = append(out, info)
	}

	for _, ep := range m.Probe(ctx) {
		info := ProviderInfo{
			Name:    localPrefix + ep.Endpoint,
			Label:   "本地 · " + ep.Endpoint,
			Kind:    ep.Kind,
			BaseURL: ep.Endpoint,
			Local:   true,
			Models:  ep.Models,
			Error:   ep.Error,
		}
		if len(ep.Models) > 0 {
			info.Model = ep.Models[0]
			info.Ready = true
		}
		out = append(out, info)
	}
	return out
}

// Models 列举某个服务商的模型。
func (m *Manager) Models(ctx context.Context, name string) ([]string, *Error) {
	if ep, ok := strings.CutPrefix(m.resolveName(name), localPrefix); ok {
		for _, r := range m.cachedProbe(ctx) {
			if r.Endpoint == ep && len(r.Models) > 0 {
				return r.Models, nil
			}
		}
		return nil, &Error{Kind: KindConfig, Message: "本地服务没有可用模型", Hint: "先 ollama pull 一个模型。"}
	}
	c, e := m.Client(ctx, name)
	if e != nil {
		return nil, e
	}
	return c.Models(ctx)
}

// Client 造一个客户端。name 为空时用默认（用户设置优先于配置）。
func (m *Manager) Client(ctx context.Context, name string) (*Client, *Error) {
	name = m.resolveName(name)
	if name == "" {
		return nil, &Error{
			Kind:    KindConfig,
			Message: "没有可用的 AI 服务商",
			Hint:    "在 config/app.yaml 的 ai.providers 里配一个，或在设置里填入地址与密钥。",
		}
	}

	// 本地服务：地址即以 local@ 之后的部分为准
	if ep, ok := strings.CutPrefix(name, localPrefix); ok {
		return m.localClient(ctx, ep)
	}

	p, ok := m.cfg.Providers[name]
	if !ok {
		return nil, &Error{Kind: KindConfig, Message: "未知的服务商: " + name, Hint: "检查设置里选中的服务商是否还存在。"}
	}

	// 用户设置覆盖配置文件：填了就用填的，没填沿用配置
	st := m.Settings()
	if st.Provider == name {
		if st.BaseURL != "" {
			p.BaseURL = st.BaseURL
		}
		if st.Model != "" {
			p.Model = st.Model
		}
	}

	key := m.Key(name)
	if key == "" && !isLocalURL(p.BaseURL) {
		envHint := ""
		if p.APIKeyEnv != "" {
			envHint = "，或设置环境变量 " + p.APIKeyEnv
		}
		return nil, &Error{
			Kind:    KindConfig,
			Message: "服务商 " + name + " 还没有 API Key",
			Hint:    "在设置里填入密钥" + envHint + "。",
		}
	}
	return NewClient(name, p, key), nil
}

// Key 取某个服务商的密钥：用户设置优先，其次环境变量。
func (m *Manager) Key(name string) string {
	st := m.Settings()
	if st.Provider == name && strings.TrimSpace(st.APIKey) != "" {
		return strings.TrimSpace(st.APIKey)
	}
	p, ok := m.cfg.Providers[name]
	if !ok || p.APIKeyEnv == "" {
		return ""
	}
	return strings.TrimSpace(m.keyEnv(name))
}

func (m *Manager) resolveName(name string) string {
	name = strings.TrimSpace(name)
	if name != "" {
		return name
	}
	if st := m.Settings(); st.Provider != "" {
		return st.Provider
	}
	return m.cfg.Default
}

func (m *Manager) cachedProbe(ctx context.Context) []LocalEndpoint {
	m.mu.RLock()
	last := m.lastProbe
	m.mu.RUnlock()
	if len(last) > 0 {
		return last
	}
	return m.Probe(ctx)
}

func (m *Manager) localClient(ctx context.Context, endpoint string) (*Client, *Error) {
	var found *LocalEndpoint
	for _, r := range m.cachedProbe(ctx) {
		if r.Endpoint == endpoint {
			cp := r
			found = &cp
			break
		}
	}
	if found == nil || len(found.Models) == 0 {
		return nil, &Error{Kind: KindConfig, Message: "本地服务不可用: " + endpoint, Hint: "确认 Ollama / LM Studio 已启动，并已拉取模型。"}
	}

	base := endpoint
	if found.Kind != "ollama" {
		base = endpoint + "/v1" // OpenAI 兼容实现要带 /v1
	}
	return NewClient(localPrefix+endpoint, config.Provider{
		Kind:    found.Kind,
		BaseURL: base,
		Model:   found.Models[0],
		Label:   "本地",
	}, ""), nil
}

func isLocalURL(u string) bool {
	return strings.Contains(u, "127.0.0.1") || strings.Contains(u, "localhost") || strings.Contains(u, "[::1]")
}
