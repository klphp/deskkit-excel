package services

import (
	"fmt"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// WindowService 主窗口的原生操作。
//
// 和 internal/window 的分工：
//
//	WindowService      控制**当前窗口**（最小化/最大化/关闭/全屏）
//	window.ServiceProvider  管理**多窗口**（按配置打开新窗口）
//
// 分成两个是因为语义不同：标题栏按钮永远操作当前窗口，而「打开设置」
// 是从任意窗口都能做的全局动作。
type WindowService struct{}

func current() application.Window {
	return application.Get().Window.Current()
}

// Minimise 最小化。
func (s *WindowService) Minimise() { current().Minimise() }

// ToggleMaximise 最大化 / 还原，返回切换后的状态。
func (s *WindowService) ToggleMaximise() bool {
	w := current()
	if w.IsMaximised() {
		w.Restore()
		return false
	}
	w.Maximise()
	return true
}

// ToggleFullscreen 全屏切换。
func (s *WindowService) ToggleFullscreen() bool {
	w := current()
	w.Fullscreen()
	return w.IsFullscreen()
}

// Close 关闭当前窗口。
func (s *WindowService) Close() { current().Close() }

// IsMaximised 当前是否最大化（前端挂载时初始化按钮图标）。
func (s *WindowService) IsMaximised() bool { return current().IsMaximised() }

// ResizeToContent 把当前窗口调整到容纳内容所需的最小尺寸。
//
// # 为什么需要这个
//
// 固定尺寸的子窗口（比如「关于」460×360）一旦内容高度比窗口小，
// 右侧就挂着一条永远用不上的滚动条；内容比窗口大又要滚。两种都难看。
//
// 正确做法是让前端量出内容实际尺寸，再让窗口跟着变 —— 内容少则窗口小，
// 内容多则窗口大（到上限后仍可滚动，比如长列表）。
//
// width/height 由前端传入（内容尺寸 + 标题栏），这里只做边界保护：
// 太小会切掉内容，太大可能超出屏幕。
func (s *WindowService) ResizeToContent(width, height int) error {
	w := current()

	// 0 表示「保持当前值」。
	//
	// 前端拿 window.outerWidth 在 Wails 的 WebView 里不一定有值，
	// 硬要求传宽度会让调用被边界检查拦下（而错误又被前端 catch 住），
	// 表现为「什么都没发生」——排查起来很费劲。
	if width <= 0 {
		width = w.Width()
	}
	if height <= 0 {
		height = w.Height()
	}

	// 下限：低于这个尺寸标题栏加几个按钮就挤成一团了
	if width < 320 || height < 200 {
		return fmt.Errorf("尺寸过小（%dx%d），至少 320x200", width, height)
	}

	// 上限：留出屏幕可用区域，别让窗口超出屏幕
	if scr, err := w.GetScreen(); err == nil && scr != nil {
		area := scr.WorkArea
		if width > area.Width {
			width = area.Width
		}
		// 扣掉 Dock 和菜单栏后还要留一点余量
		maxH := area.Height - 40
		if height > maxH {
			height = maxH
		}
	}

	w.SetSize(width, height)
	return nil
}

// Platform 运行平台。
//
// 前端也能读 window._wails.environment.OS，但纯浏览器 `npm run dev`
// 调试时那个字段不存在，走 Go 侧更稳。
func (s *WindowService) Platform() string { return runtime.GOOS }

// Quit 退出应用。
func (s *WindowService) Quit() { application.Get().Quit() }
