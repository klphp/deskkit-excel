package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Provider 一个 AI 服务商的配置。
//
// 设计成「OpenAI 兼容优先」：只要对方实现 /chat/completions 且支持
// Bearer 鉴权，就能接进来 —— OpenAI、DeepSeek、通义、Kimi、Groq、
// 以及本地的 Ollama / LM Studio 全在这一个形态里。
// 不为每家服务商写一个适配器：那等于给每家的字段差异做长期维护。
type Provider struct {
	// Kind 区分协议形态：openai（默认）或 ollama。
	// ollama 的差异只在列举模型时体现（/api/tags vs /v1/models），
	// 对话接口本身它兼容 OpenAI。
	Kind string `yaml:"kind"`

	// BaseURL 是 API 根地址，不含 /chat/completions。
	// 例：https://api.deepseek.com/v1
	BaseURL string `yaml:"baseURL"`

	// Model 默认模型名。前端可以按次覆盖。
	Model string `yaml:"model"`

	// Models 常见模型清单，给界面做下拉用。
	//
	// 为什么要写死在配置里：拉 /v1/models 需要先有 key，而且各家返回的
	// 名字经常夹带一堆用户根本不会选的条目。给几个「确定能用」的候选，
	// 用户点一下就行；想用清单外的，界面上还能自己填。
	Models []string `yaml:"models"`

	// APIKeyEnv 从环境变量读密钥的变量名。
	//
	// 密钥不写进配置文件：配置会进 git、会被打包进二进制。
	// 桌面应用的用户密钥另有一套：见 internal/ai 的 Settings（存用户目录，0600）。
	APIKeyEnv string `yaml:"apiKeyEnv"`

	// Label 是给人看的名字。留空则用配置里的 key。
	Label string `yaml:"label"`

	// Timeout 单次请求超时。0 = 60s。
	Timeout time.Duration `yaml:"timeout"`

	// Retry 只对网络错误与 5xx/429 生效，鉴权失败一类的 4xx 不重试 ——
	// 重试改变不了「key 是错的」这个事实，只会让用户多等几秒。
	Retry Retry `yaml:"retry"`

	// Headers 额外请求头。
	// 少数服务商用自定义头表达协议细节（例如某些网关要求 session 头），
	// 配置化比改代码省事。
	Headers map[string]string `yaml:"headers"`
}

// LocalAI 本地模型探测配置。
//
// 为什么默认开启：本地模型是「数据不出机器」这个卖点的技术前提。
// 探测只是对 127.0.0.1 发一次 GET，没有隐私问题，失败也静默。
type LocalAI struct {
	Enabled bool `yaml:"enabled"`

	// Endpoints 待探测地址，按顺序试。
	// 默认覆盖 Ollama（11434）与 LM Studio / 其它 OpenAI 兼容服务（1234、8080）。
	Endpoints []string `yaml:"endpoints"`

	// ProbeTimeout 单个地址的探测超时。0 = 400ms。
	// 短是刻意的：探测发生在打开设置页时，不能让界面等。
	ProbeTimeout time.Duration `yaml:"probeTimeout"`
}

// AI AI 总配置。
type AI struct {
	// Enabled 是否启用 AI。**用指针**，理由和 app.singleInstance 一样：
	// 要看得出「没写」和「显式写 false」的区别。
	//
	// 没写时的行为：配了 providers 或开了本地探测就自动启用，否则关闭 ——
	// 模板不该要求用户先理解 AI 配置才能跑起来。
	//
	// 显式写 false：无论配了什么都不启用。曾经这里是 bool，WithDefaults
	// 见到 providers 就把值改成 true，把用户的 false 吃掉了 —— 表现为
	// 「配置里明明关着，日志却说已启用」。
	Enabled *bool `yaml:"enabled"`

	// Default 默认服务商名（Providers 的 key）。
	Default string `yaml:"default"`

	Providers map[string]Provider `yaml:"providers"`

	Local LocalAI `yaml:"local"`
}

// IsEnabled 最终是否启用。
//
// 显式配置优先；没配就按「有没有可用的服务商」推断 ——
// 完全空的 ai 段意味着模板保持干净，一个字节都不联网。
func (a AI) IsEnabled() bool {
	if a.Enabled != nil {
		return *a.Enabled
	}
	return len(a.Providers) > 0 || a.Local.Enabled
}

// WithDefaults 补齐零值。配置尽量少写，缺的用合理默认。
func (a AI) WithDefaults() AI {
	if len(a.Providers) == 0 && !a.Local.Enabled {
		// 完全没配置时保持原样，模板不背 AI 的负担。
		return a
	}

	if a.Default == "" {
		for name := range a.Providers {
			a.Default = name
			break
		}
	}

	providers := make(map[string]Provider, len(a.Providers))
	for name, p := range a.Providers {
		if p.Kind == "" {
			p.Kind = "openai"
		}
		if p.Label == "" {
			p.Label = name
		}
		if p.Timeout == 0 {
			p.Timeout = 60 * time.Second
		}
		if p.Retry.MaxAttempts == 0 {
			p.Retry.MaxAttempts = 3
		}
		if p.Retry.Backoff == 0 {
			p.Retry.Backoff = 800 * time.Millisecond
		}
		providers[name] = p
	}
	a.Providers = providers

	if a.Local.ProbeTimeout == 0 {
		a.Local.ProbeTimeout = 400 * time.Millisecond
	}
	if len(a.Local.Endpoints) == 0 {
		a.Local.Endpoints = []string{
			"http://127.0.0.1:11434", // Ollama
			"http://127.0.0.1:1234",  // LM Studio
			"http://127.0.0.1:8080",  // llama.cpp server / 其它
		}
	}
	return a
}

// AIKey 按 provider 的 apiKeyEnv 取环境变量里的密钥。
func (c *Config) AIKey(provider string) string {
	p, ok := c.AI.Providers[provider]
	if !ok || p.APIKeyEnv == "" {
		return ""
	}
	return strings.TrimSpace(os.Getenv(p.APIKeyEnv))
}

// Validate 启动时校验。只在启用时检查，且只告警不拦启动 ——
// AI 配错了不该让整个应用起不来。
func (a AI) Validate() error {
	if !a.IsEnabled() {
		return nil
	}
	if len(a.Providers) == 0 && !a.Local.Enabled {
		return fmt.Errorf("ai.enabled 为 true，但既没有配置 providers，也没有开本地探测")
	}
	if a.Default != "" {
		if _, ok := a.Providers[a.Default]; !ok {
			return fmt.Errorf("ai.default 指向不存在的 provider: %s", a.Default)
		}
	}
	return nil
}
