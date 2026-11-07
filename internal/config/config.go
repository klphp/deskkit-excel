// Package config 配置解析。
//
// 注意：**不在这里做 go:embed**。Go 的 embed 指令不能引用父目录
// （`//go:embed ../../config/*.yaml` 直接编译失败），所以嵌入动作放在
// 根包的 configs.go 里，把字节传给这里的 Parse。
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// App 应用信息。
type App struct {
	Name       string `yaml:"name" json:"name"`
	Version    string `yaml:"version" json:"version"`
	Identifier string `yaml:"identifier" json:"identifier"`

	// SingleInstance 禁止多开。默认 true（只允许一个实例）。
	//
	// 为什么默认开：桌面应用被多开几乎都是意外（双击两次图标、从终端和
	// Finder 各起一次）。而本模板内嵌一个本地 HTTP 服务 —— 两个实例各占
	// 一个端口，外部程序连哪一个都不对，排查起来还很绕。
	//
	// 用指针是为了区分「没写」和「显式写 false」：没写要按 true 处理，
	// 这跟 bool 的零值相反，只有指针能做到。零值语义会让默认值变成 false。
	SingleInstance *bool `yaml:"singleInstance"`
}

// SingleInstanceOrDefault 没配置时默认禁止多开。
func (a App) SingleInstanceOrDefault() bool {
	if a.SingleInstance == nil {
		return true
	}
	return *a.SingleInstance
}

// Server 内嵌本地 HTTP 服务。
type Server struct {
	Enabled    bool   `yaml:"enabled"`
	Addr       string `yaml:"addr"`
	EnableCORS bool   `yaml:"enableCORS"`
	LogLevel   string `yaml:"logLevel"`
}

// Retry 重试策略。
type Retry struct {
	MaxAttempts int           `yaml:"maxAttempts"`
	Backoff     time.Duration `yaml:"backoff"`
}

// API 单个外部 API 的配置。
type API struct {
	BaseURL   string        `yaml:"baseURL"`
	Timeout   time.Duration `yaml:"timeout"`
	APIKeyEnv string        `yaml:"apiKeyEnv"`
	Retry     Retry         `yaml:"retry"`
}

// AppConfig 对应 config/app.yaml。
// Update 是自更新配置。
//
// 三个演进方向共用这一份配置，只改字段不改代码：
//
//	① 盈利期   provider: endpoint + publicKey 指向自建 API（可做 license/灰度）
//	② 试水期   provider: github   + publicKey 留空（无签名，零成本）
//	③ 有签名   provider: endpoint + publicKey 有值，清单托管在 GitHub Pages
//
// 为什么 ③ 不能用 provider: github —— Wails 的 github provider 只填
// Verification.Digest，从不填 Signature，验签无从谈起。要签名就必须走
// endpoint，让清单自己携带 signature 字段。
type Update struct {
	Enabled bool `yaml:"enabled"`

	// Provider 是更新源类型："github" 或 "endpoint"。
	Provider string `yaml:"provider"`

	// Repository 是 provider=github 时的 "owner/repo"。
	Repository string `yaml:"repository"`

	// ManifestURL 是 provider=endpoint 时的清单地址，支持
	// {{platform}} / {{arch}} / {{version}} / {{channel}} 占位符。
	ManifestURL string `yaml:"manifestUrl"`

	// ChecksumAsset 是 provider=github 时的校验和文件名（如 SHA256SUMS）。
	// 它只防下载损坏 —— 文件和校验和放在同一个 release 里，防不住同时篡改
	// 两者的人。要真防篡改必须配 PublicKey。
	ChecksumAsset string `yaml:"checksumAsset"`

	// Prerelease 为 true 时把预发布版也算作可用更新。
	Prerelease bool `yaml:"prerelease"`

	// PublicKey 是 Ed25519 公钥（PKIX PEM 或 base64 的原始 32 字节）。
	// 留空 = 不验签，适合试水阶段。一旦填了就是 fail closed：
	// 没有签名的 release 会被拒绝，不能靠剥签名降级绕过。
	PublicKey string `yaml:"publicKey"`

	// CheckInterval 是后台轮询间隔，0 表示不自动检查。
	// 检查本身很轻（一次 API 请求），默认 6 小时足够。
	CheckInterval time.Duration `yaml:"checkInterval"`
}

// I18n 多语言配置。
//
// 默认关闭（Enabled=false），此时前端只按 DefaultLocale 取文案，行为和
// 没有 i18n 完全一样 —— 模板不该一上来就背着一套翻译系统的负担。
//
// 要用的时候：Enabled 置 true，Locales 里列出可用语言，翻译文件放
// frontend/src/i18n/locales/<locale>.ts。语言切换入口在设置窗口里，
// 只列出 Locales 中的语言。
type I18n struct {
	Enabled bool `yaml:"enabled"`

	// DefaultLocale 是缺省语言，也是找不到词条时的兜底。
	DefaultLocale string `yaml:"defaultLocale"`

	// FallbackLocale 在某个语言缺词条时用它的翻译。留空则用 DefaultLocale。
	FallbackLocale string `yaml:"fallbackLocale"`

	// Locales 是允许切换的语言列表（BCP 47，如 zh-CN / en-US / ja-JP）。
	// Enabled=true 但这里为空时，前端只能显示 DefaultLocale。
	Locales []string `yaml:"locales"`
}

type AppConfig struct {
	App    App            `yaml:"app"`
	Server Server         `yaml:"server"`
	APIs   map[string]API `yaml:"apis"`
	Update Update         `yaml:"update"`
	I18n   I18n           `yaml:"i18n"`
	AI     AI             `yaml:"ai"`
}

// Window 单个窗口的定义，对应 config/windows.yaml。
type Window struct {
	Title     string `yaml:"title"`
	Width     int    `yaml:"width"`
	Height    int    `yaml:"height"`
	MinWidth  int    `yaml:"minWidth"`
	MinHeight int    `yaml:"minHeight"`
	URL       string `yaml:"url"`

	// Frameless 去掉系统边框。macOS 走「隐藏标题栏但保留红绿灯」，
	// Windows 走真正的无边框 + 前端自绘控件。
	Frameless bool `yaml:"frameless"`

	Resizable   *bool `yaml:"resizable"`
	Singleton   bool  `yaml:"singleton"`
	AlwaysOnTop bool  `yaml:"alwaysOnTop"`
	Main        bool  `yaml:"main"`

	// KeepAboveParent 让模态子窗口始终压住父窗口。
	//
	// 默认 true。不设的话，点一下父窗口父窗口就升到前面，把子窗口盖住
	// —— 模态窗口被它阻挡的那个窗口盖住，看着就是「子窗口跑后面去了」。
	//
	// 代价（Wails 的能力边界，绕不过）：macOS 用 WindowLevel=floating、
	// Windows 用 AlwaysOnTop，都是**全局层级**，不是 owner-window 关系。
	// 所以切到别的应用时，子窗口也会浮在它们上面。想要「只在自家应用内
	// 更高、切走就一起沉下去」需要真正的子窗口关系（macOS 的
	// addChildWindow / Windows 的 owned window），Wails v3 没暴露。
	//
	// 不想让子窗口盖住别的应用就显式写 false，换来的是「点父窗口后
	// 子窗口会沉到后面」。
	KeepAboveParent *bool `yaml:"keepAboveParent"`

	// FollowParent 让子窗口跟随父窗口移动。
	//
	// 默认 true：模态子窗口跟着父窗口一起挪，视觉上像一个整体。
	// 如果父窗口动了而子窗口不动，子窗口会显得「飘」在外面、
	// 失去从属关系。要固定位置就显式写 false。
	FollowParent *bool `yaml:"followParent"`

	// ShowTitle 控制标题栏里是否显示应用名和版本号。
	//
	// 默认为 true。设为 false 后标题栏仍然存在（它是 frameless 窗口唯一的
	// 拖动入口和窗口按钮载体），只是空出来 —— 适合想把注意力完全放在
	// 内容上的应用。
	ShowTitle *bool `yaml:"showTitle"`

	// ShowSettingsButton 控制标题栏右侧是否显示设置齿轮。
	//
	// 默认为 true。应用没有设置项时可以关掉；关掉后用户仍能从侧边栏
	// 的「设置」进去（那是内容区的导航，不依赖标题栏）。
	ShowSettingsButton *bool `yaml:"showSettingsButton"`

	// Modal 模态窗口：打开后**阻止操作父窗口**，直到它关闭。
	//
	// 和 AlwaysOnTop 是两件完全不同的事：
	//
	//	Modal        阻止父窗口交互，只在父窗口之上，不影响其它应用
	//	AlwaysOnTop  全局置顶，会盖住别的程序（对弹窗来说很烦，别用）
	//
	// 用 Wails 的 Window.AttachModal 实现，两个平台都是原生的：
	// macOS 走 NSWindow modal session，Windows 走禁用父 HWND。
	Modal bool `yaml:"modal"`

	// Parent 父窗口名。Modal 为 true 时必填，否则默认用当前活动窗口
	// （即「从哪个窗口点的，就挂在哪」）。
	Parent string `yaml:"parent"`
}

// ResizableOrDefault resizable 默认 true（没写就是 true）。
func (w Window) ResizableOrDefault() bool {
	if w.Resizable == nil {
		return true
	}
	return *w.Resizable
}

// WindowsConfig 对应 config/windows.yaml。
type WindowsConfig struct {
	Windows map[string]Window `yaml:"windows"`
}

// Config 全部配置。
type Config struct {
	AppConfig
	Windows WindowsConfig
}

// Parse 解析内嵌的配置字节。
func Parse(appYAML, windowsYAML []byte) (*Config, error) {
	cfg := &Config{}

	if err := yaml.Unmarshal(appYAML, &cfg.AppConfig); err != nil {
		return nil, fmt.Errorf("解析 app.yaml 失败: %w", err)
	}
	if err := yaml.Unmarshal(windowsYAML, &cfg.Windows); err != nil {
		return nil, fmt.Errorf("解析 windows.yaml 失败: %w", err)
	}

	// 端口 0 合法（系统分配），但 addr 不能空
	if cfg.Server.Enabled && cfg.Server.Addr == "" {
		cfg.Server.Addr = "127.0.0.1:0"
	}

	// 环境变量覆盖：打包后不改配置也能调端口
	if v := os.Getenv("APP_SERVER_ADDR"); v != "" && cfg.Server.Enabled {
		cfg.Server.Addr = v
	}

	// AI 配置补默认值（kind/timeout/retry/本地探测地址）。
	// 放在 Parse 里而不是各个服务里补，是为了让所有取用方看到同一份配置。
	cfg.AI = cfg.AI.WithDefaults()

	// 至少得有一个主窗口，否则应用起来是个空壳
	if _, ok := cfg.MainWindow(); !ok {
		return nil, fmt.Errorf("config/windows.yaml 里必须有一个标记 main: true 的窗口")
	}

	return cfg, nil
}

// MainWindow 主窗口（关闭即退出）。
func (c *Config) MainWindow() (Window, bool) {
	if name, ok := c.MainWindowName(); ok {
		return c.Windows.Windows[name], true
	}
	return Window{}, false
}

// MainWindowName 主窗口的名字。
//
// 和 MainWindow 的区别：单实例回调里要「把主窗口拉到前面」，那需要的是
// 名字（喂给 windowManager.Open），不是窗口定义。
// 判定顺序与 MainWindow 保持一致：先看 main: true，再退回叫 main 的那个。
func (c *Config) MainWindowName() (string, bool) {
	for name, w := range c.Windows.Windows {
		if w.Main {
			return name, true
		}
	}
	if _, ok := c.Windows.Windows["main"]; ok {
		return "main", true
	}
	return "", false
}

// APIKey 取某个 API 的密钥（按配置里的 apiKeyEnv 从环境变量读）。
//
// 密钥不放配置文件：配置文件会进 git，密钥只该在运行环境里。
func (c *Config) APIKey(name string) string {
	api, ok := c.APIs[name]
	if !ok || api.APIKeyEnv == "" {
		return ""
	}
	return strings.TrimSpace(os.Getenv(api.APIKeyEnv))
}
