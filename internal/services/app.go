package services

import (
	"runtime"

	"github.com/klphp/deskkit-excel/internal/config"
	"github.com/klphp/deskkit-excel/internal/server"
)

// AppService 应用级信息。
type AppService struct {
	Config *config.Config
	Server *server.Server
}

// Info 应用信息，前端显示在标题栏/关于页。
type Info struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Platform string `json:"platform"`
	Arch     string `json:"arch"`
	// ServerURL 内嵌 HTTP 服务的基地址。为空表示没启用或被环境变量关掉了。
	ServerURL string `json:"serverURL"`

	// I18n 是前端初始化翻译所需的全部信息。
	I18n I18nInfo `json:"i18n"`

	// ShowTitle / ShowSettingsButton 来自主窗口的配置，控制标题栏元素显隐。
	// 前端拿它决定渲染什么 —— 窗口自己的标题栏由 JS 画，Go 侧没法管。
	ShowTitle          bool `json:"showTitle"`
	ShowSettingsButton bool `json:"showSettingsButton"`
}

// I18nInfo 前端初始化翻译用的配置快照。
type I18nInfo struct {
	Enabled bool `json:"enabled"`
	// Locale 是当前生效语言；Enabled=false 时等于 DefaultLocale。
	Locale   string   `json:"locale"`
	Fallback string   `json:"fallback"`
	Locales  []string `json:"locales"`
}

// GetInfo 取应用信息。
func (s *AppService) GetInfo() Info {
	info := Info{
		Name:     s.Config.App.Name,
		Version:  s.Config.App.Version,
		Platform: runtime.GOOS,
		Arch:     runtime.GOARCH,
	}
	if s.Server != nil {
		info.ServerURL = s.Server.URL()
	}

	// ── i18n ──
	loc := s.Config.I18n.DefaultLocale
	if loc == "" {
		loc = "zh-CN"
	}
	fb := s.Config.I18n.FallbackLocale
	if fb == "" {
		fb = loc
	}
	info.I18n = I18nInfo{
		Enabled:  s.Config.I18n.Enabled,
		Locale:   loc,
		Fallback: fb,
		Locales:  s.Config.I18n.Locales,
	}
	if len(info.I18n.Locales) == 0 {
		info.I18n.Locales = []string{loc}
	}

	// 标题栏元素的显隐取主窗口的配置。子窗口的标题栏由前端按 isSubWindow
	// 单独处理，不读这里。
	info.ShowTitle = true
	info.ShowSettingsButton = true
	for _, w := range s.Config.Windows.Windows {
		if !w.Main {
			continue
		}
		if w.ShowTitle != nil {
			info.ShowTitle = *w.ShowTitle
		}
		if w.ShowSettingsButton != nil {
			info.ShowSettingsButton = *w.ShowSettingsButton
		}
		break
	}

	return info
}
