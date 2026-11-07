// Package window 配置驱动的窗口管理。
//
// 目标是「加一个窗口只改 YAML」：
//
//	# config/windows.yaml
//	settings:
//	  title: 设置
//	  width: 760
//	  height: 620
//	  url: /?view=settings
//	  singleton: true
//
// 然后 Go 里 windowManager.Open("settings")，前端 WindowService.Open("settings")。
// 不需要为每个窗口写一份 NewWithOptions。
//
// # 为什么所有窗口共用同一个 SPA
//
// url 里带 ?view=xxx，前端读这个参数决定渲染哪个视图。好处是：
//
//   · 只构建一次前端，多个窗口共享资源（WebView 会缓存）
//   · 新增窗口不用动构建配置
//   · 窗口之间可以共享 store / 主题 / 组件
//
// 代价是子窗口要自己判断 view 参数 —— 但这段逻辑集中在一处，不散。
package window

import (
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/klphp/deskkit/internal/config"
)

// Manager 窗口管理器。并发安全（bindings 可能被多个窗口同时调）。
type Manager struct {
	app *application.App
	cfg config.WindowsConfig

	mu   sync.Mutex
	open map[string]application.Window // name → 窗口实例
}

// NewManager 创建管理器。
func NewManager(app *application.App, cfg config.WindowsConfig) *Manager {
	return &Manager{
		app:  app,
		cfg:  cfg,
		open: make(map[string]application.Window),
	}
}

// Main 打开主窗口。
func (m *Manager) Main() error {
	name, ok := mainName(m.cfg)
	if !ok {
		return fmt.Errorf("没有配置主窗口（config/windows.yaml 里需要一个 main: true）")
	}
	return m.Open(name)
}

// mainName 主窗口的名字。优先找标记 main: true 的，其次退回叫 "main" 的。
func mainName(cfg config.WindowsConfig) (string, bool) {
	for name, w := range cfg.Windows {
		if w.Main {
			return name, true
		}
	}
	if _, ok := cfg.Windows["main"]; ok {
		return "main", true
	}
	return "", false
}

// Open 按名字打开窗口。已存在则聚焦（不会开出第二个）。
//
// 前端调用：await WindowService.Open("settings")
func (m *Manager) Open(name string) error {
	def, ok := m.cfg.Windows[name]
	if !ok {
		// 把可用的名字列出来，比只报「不存在」好排查
		avail := make([]string, 0, len(m.cfg.Windows))
		for n := range m.cfg.Windows {
			avail = append(avail, n)
		}
		return fmt.Errorf("窗口 %q 没有配置（可用: %v）", name, avail)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// 已经开着：拉到前面，不再新建
	if existing, ok := m.open[name]; ok {
		existing.Focus()
		if existing.IsMinimised() {
			existing.Restore()
		}
		return nil
	}

	win := m.app.Window.NewWithOptions(buildOptions(name, def))
	m.open[name] = win

	// 窗口关闭后从表里移除，否则再次 Open 会聚焦一个已销毁的窗口
	win.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		m.mu.Lock()
		delete(m.open, name)
		m.mu.Unlock()
	})

	// 子窗口相对父窗口居中。
	//
	// 默认的 InitialPosition: WindowCentered 是**相对屏幕**居中 —— 父窗口
	// 要是不在屏幕中间（用户拖过），子窗口就会从父窗口旁边蹦出来，
	// 视觉上和父窗口毫无关联。子窗口应该长在父窗口上。
	//
	// 返回值带着 parent 和偏移，下面模态那块直接用 —— 偏移只有这里算得准。
	centerParent, centerOffX, centerOffY := m.centerOnParent(win, def)

	// 模态：禁用父窗口的鼠标交互，直到本窗口关闭。
	//
	// ★ 这里刻意**不用** Window.AttachModal。
	//
	// AttachModal 在 macOS 上走的是 NSWindow modal sheet —— sheet 是从
	// 父窗口标题栏滑下来的那种面板，**系统不画红绿灯**。结果就是子窗口
	// 没有任何系统关闭控件，只能自绘一个假的 X，和 macOS 习惯完全不符。
	//
	// 改成「拦点击靠前端遮罩，不碰原生窗口状态」：
	//   · 子窗口是正常 NSWindow / HWND，红绿灯、圆角、阴影全都在
	//   · 父窗口的内容区铺一层遮罩吃掉点击，视觉上等价于模态
	//   · 父窗口标题栏不受影响 —— 仍可拖动、仍可最小化/关闭
	//   · 不依赖 AlwaysOnTop，不会盖住别的应用
	//
	// 为什么不用 SetIgnoreMouseEvents：
	//   它是**整个窗口**的 OS 级开关，一开连标题栏拖动都失效。用户想
	//   在子窗口开着时挪一下主窗口，那个 API 做不到。
	//
	// 顺带一提：Wails 的 Window.SetEnabled 在 macOS 上是空实现
	// （webview_window_darwin.go 里 windowSetEnabled 只有 // TODO），
	// 所以也没法用它做精确控制。
	//
	// 事件名带 deskkit 前缀，避免和宿主应用自己的事件撞名。
	if def.Modal {
		// 用 centerOnParent 已经解析好的父窗口 —— 再调一次 resolveParent 可能
		// 因为 Current() 变了而拿到另一个窗口，两边不一致会错得很隐蔽。
		if parent := centerParent; parent != nil {
			slog.Info("模态窗口：父窗口内容区将显示遮罩", "modal", name, "parent", parent.Name())
			m.emitBlockState(true, name)

			// ── 让子窗口和父窗口成为「一体的」──
			//
			// 两条路，按平台选：
			//
			//   macOS / Windows  原生 owner-child（addChildWindow /
			//                    GWLP_HWNDPARENT）。移动由系统处理，
			//                    原子、零延迟 —— 这就是 Qt 那种一体感。
			//                    不需要应用层算坐标。
			//
			//   其它平台         退回「监听移动事件 + 重设位置」，会有
			//                    轻微滞后（事件 + IPC 两跳）。
			//
			// 注意两者**不能同时开**：原生关系已经在管位置了，再叠加事件
			// 驱动的 SetPosition 会互相打架，表现为抖动。
			if def.FollowParent == nil || *def.FollowParent {
				if pw, ok := parent.(*application.WebviewWindow); ok && useNativeChild {
					m.bindNativeChild(pw, win, name)

					// ★ Windows 的 owner 关系**不含位置跟随**。
					//
					// GWLP_HWNDPARENT 设的是 owner，不是 WS_CHILD：
					//   · owner 提供：z-order 恒在 owner 之上、最小化联动、销毁联动
					//   · owner 不提供：位置跟随（那才是 WS_CHILD 的行为）
					//
					// 而 WS_CHILD 会把子窗口裁剪在父窗口矩形内、拖不出去，不能要。
					// 所以 Windows 上只能叠加「事件跟随」补位置 —— 会有轻微滞后，
					// 这是平台限制：macOS 的 addChildWindow 原生带跟随，Windows 没有
					// 等价的「既跟随又不裁剪」机制。
					if runtime.GOOS == "windows" {
						m.followParent(parent, win, name, centerOffX, centerOffY)
					}
				} else {
					m.followParent(parent, win, name, centerOffX, centerOffY)
				}
			}

			// 主窗口关 → 子窗口跟着关。
			//
			// 原生 owner 关系（GWLP_HWNDPARENT）本来就会让 owned window 随
			// owner 销毁，但那是平台行为、不保证在所有窗口样式下都成立。
			// 用户明确要求「主窗口关了应该退出」，所以这里再显式兜一层。
			// Close() 自带 isDestroyed 检查，重复调用无害。
			parent.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
				win.Close()
				slog.Debug("父窗口关闭，一并关闭子窗口", "window", name)
			})

			// 子窗口关掉后撤掉遮罩，否则父窗口会永久点不动
			win.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
				m.emitBlockState(false, name)
				slog.Debug("模态窗口已关闭，撤掉父窗口遮罩", "parent", parent.Name())
			})
		} else {
			slog.Warn("模态窗口找不到父窗口，退化为普通窗口", "modal", name)
		}
	}

	return nil
}

// followParent 让 win 跟着 parent 一起移动，保持打开时的相对位置。
//
// ★ 关键在于「什么时候算基准」和「用绝对还是相对」。
//
// 试过两种错法：
//
//	① Open 时算一次 offset = win - parent，之后 SetPosition(parent+offset)。
//	   错在 centerOnParent 走异步 IPC —— 回到这里读 win.Position() 时窗口
//	   还没定位，拿到 (0,0)，offset 整个是错的，一拖就飞到屏幕外。
//
//	② 记父窗口上次位置，每次把 delta 加到 win.Position() 上。
//	   错在拖动时高频调用，win.Position() 可能返回滞后的值，wx+dx 的误差
//	   会逐帧累积 —— 拖得越久偏得越远，最后同样飞出屏幕。单次跳变测不出
//	   这个问题（只有一次累加），必须连续拖动才暴露。
//
// 现在的做法：**第一次真正移动时**才读子窗口位置建立基准（这时两个窗口
// 都已经定位完成），之后一律用「父窗口绝对位置 + 固定偏移」。绝对位置不
// 累积误差，拖多久都不会漂。
func (m *Manager) followParent(parent application.Window, win *application.WebviewWindow, name string, offX, offY int) {
	var mu sync.Mutex

	stop := parent.OnWindowEvent(events.Common.WindowDidMove, func(*application.WindowEvent) {
		mu.Lock()
		defer mu.Unlock()

		px, py := parent.Position()
		win.SetPosition(px+offX, py+offY)
	})

	win.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		stop()
		slog.Debug("子窗口已关闭，停止跟随父窗口", "window", name)
	})

	slog.Debug("子窗口将跟随父窗口移动", "window", name, "offsetX", offX, "offsetY", offY)
}

// bindNativeChild 建立原生 owner-child 关系。
//
// 时机很关键：NativeWindow() 在窗口刚 New 出来时可能是 nil —— 底层
// NSWindow / HWND 还没真正创建。所以要等一小会儿再挂。这里不用事件
// （Wails 没有「原生窗口就绪」这种事件），一个短延迟足够，而且即使
// 第一次拿到 nil 也会重试几次，不该因为时机问题静默失败。
func (m *Manager) bindNativeChild(parent, win *application.WebviewWindow, name string) {
	go func() {
		// 先让出一小段时间，避开窗口初始化的高峰。
		//
		// addChildWindow 会让 AppKit 做一次父子层级重排。如果这次重排和
		// 窗口首次绘制撞在一起，用户看到的就是「打开子窗口时卡一下」。
		// 等窗口画完第一帧再绑，visual 上的跳变就没了 —— 绑定本身很快。
		time.Sleep(180 * time.Millisecond)

		var (
			pHandle, wHandle unsafe.Pointer
		)
		for i := 0; i < 25; i++ {
			pHandle = parent.NativeWindow()
			wHandle = win.NativeWindow()
			if pHandle != nil && wHandle != nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if pHandle == nil || wHandle == nil {
			slog.Warn("拿不到原生窗口句柄，退回事件跟随", "window", name)
			return
		}

		attachChildWindow(pHandle, wHandle)
		slog.Debug("已建立原生父子窗口关系", "window", name)

		// 子窗口关闭前解除关系，否则父窗口会持有已销毁的窗口引用
		win.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
			if p := parent.NativeWindow(); p != nil {
				detachChildWindow(p, wHandle)
			}
			slog.Debug("已解除原生父子窗口关系", "window", name)
		})
	}()
}

// emitBlockState 通知前端「父窗口该不该显示遮罩」。
//
// 走事件总线而不是改成 bindings 调用：主窗口是被动方，它不需要知道是
// 谁把它挡住的，只需要知道「现在挡/不挡」。事件正好是这个语义。
func (m *Manager) emitBlockState(blocked bool, by string) {
	if m.app == nil || m.app.Event == nil {
		return
	}
	evtName := "deskkit:window:unblocked"
	if blocked {
		evtName = "deskkit:window:blocked"
	}
	// ★ EventManager.Emit 返回的是 event.IsCancelled()，不是「有没有人接收」。
	//   false = 正常发出去了；true = 被某个 hook 取消了。别把语义读反。
	cancelled := m.app.Event.Emit(evtName, by)
	if cancelled {
		slog.Debug("遮罩状态事件被 hook 取消", "event", evtName)
	}
}

// resolveParent 找模态窗口的父窗口。
//
// 优先级：配置里写死的 parent → 当前活动窗口。
// 用「当前活动窗口」作为兜底是对的语义：从设置窗口里再点开一个对话框，
// 它应该挂在设置窗口上，而不是飞回主窗口。
func (m *Manager) resolveParent(def config.Window) application.Window {
	if def.Parent != "" {
		if p, ok := m.open[def.Parent]; ok {
			return p
		}
		slog.Warn("配置的父窗口没打开，回退到当前活动窗口", "parent", def.Parent)
	}

	// Current() 可能返回已销毁的窗口，判一下
	if cur := m.app.Window.Current(); cur != nil {
		return cur
	}

	// 再退一步：拿任意一个已打开的窗口当父窗口，总比没有好
	for _, w := range m.open {
		return w
	}
	return nil
}

// centerOnParent 把窗口摆到父窗口正中。
//
// 窗口尺寸用配置值；如果前端随后用 ResizeToContent 改了高度，会再调
// CenterOnParent 重新贴合一次（见 WindowService.CenterOnParent）。
// 返回值是父窗口和「子窗口相对父窗口的偏移」。
//
// 偏移由这里算出来返回，而不是让调用方去读 win.Position() —— 那是异步的，
// 窗口刚创建时读到的还没定位。用本函数算出的 target 反推偏移才是准的。
func (m *Manager) centerOnParent(win application.Window, def config.Window) (application.Window, int, int) {
	// 主窗口自己就是「父窗口」，不该相对自己居中 —— 它是屏幕居中的
	if def.Main {
		return nil, 0, 0
	}

	parent := m.resolveParent(def)
	if parent == nil {
		return nil, 0, 0
	}

	w, h := def.Width, def.Height
	if w <= 0 {
		w = 1024
	}
	if h <= 0 {
		h = 700
	}

	px, py := parent.Position()
	pw, ph := parent.Width(), parent.Height()

	x := px + (pw-w)/2
	y := py + (ph-h)/2

	// 别让子窗口跑出屏幕（父窗口贴边时会出现）
	if screen, err := parent.GetScreen(); err == nil && screen != nil {
		area := screen.WorkArea
		if x < area.X {
			x = area.X
		}
		if y < area.Y {
			y = area.Y
		}
		if x+w > area.X+area.Width {
			x = area.X + area.Width - w
		}
		if y+h > area.Y+area.Height {
			y = area.Y + area.Height - h
		}
	}

	win.SetPosition(x, y)

	// 反推偏移：调用方拿这个去跟随父窗口，不要再读一次 win.Position()
	offX, offY := x-px, y-py

	slog.Info("子窗口居中",
		"window", win.Name(),
		"parent", parent.Name(),
		"parent_pos", fmt.Sprintf("%d,%d", px, py),
		"parent_size", fmt.Sprintf("%dx%d", pw, ph),
		"self_size", fmt.Sprintf("%dx%d", w, h),
		"target", fmt.Sprintf("%d,%d", x, y),
	)

	return parent, offX, offY
}

// CenterOnParent 把指定窗口重新摆到父窗口正中（外部调用）。
func (m *Manager) CenterOnParent(name string) error {
	m.mu.Lock()
	win, ok := m.open[name]
	def := m.cfg.Windows[name]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("窗口 %q 没打开", name)
	}
	m.centerOnParent(win, def)
	return nil
}

// Close 关闭指定窗口。
func (m *Manager) Close(name string) error {
	m.mu.Lock()
	win, ok := m.open[name]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("窗口 %q 没打开", name)
	}
	win.Close()
	return nil
}

// List 当前打开的窗口名。
func (m *Manager) List() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.open))
	for n := range m.open {
		out = append(out, n)
	}
	return out
}

// Names 配置里定义的全部窗口名（不含是否打开）。
func (m *Manager) Names() []string {
	out := make([]string, 0, len(m.cfg.Windows))
	for n := range m.cfg.Windows {
		out = append(out, n)
	}
	return out
}

// buildOptions 把配置翻译成 Wails 窗口选项。
//
// 平台差异集中在这里 —— 两个平台的「无边框」机制根本不同：
//
//	macOS   不能真 Frameless，那样连红绿灯一起没了。正确做法是隐藏
//	        标题栏但保留原生按钮（MacTitleBar.Hide + FullSizeContent），
//	        红绿灯浮在内容左上角。
//
//	Windows 反过来，用 Frameless 去掉整条原生标题栏，前端自绘控件。
//
// 强行统一的结果是某个平台上窗口关不掉。
func buildOptions(name string, def config.Window) application.WebviewWindowOptions {
	url := def.URL
	if url == "" {
		url = "/"
	}

	opts := application.WebviewWindowOptions{
		Name:            name,
		Title:           def.Title,
		Width:           def.Width,
		Height:          def.Height,
		MinWidth:        def.MinWidth,
		MinHeight:       def.MinHeight,
		URL:             url,
		AlwaysOnTop:     def.AlwaysOnTop,
		DisableResize:   !def.ResizableOrDefault(),
		InitialPosition: application.WindowCentered,
		BackgroundColour: application.NewRGB(24, 24, 27),
	}

	// 尺寸兜底：配置漏写时给个能用的默认值，别开出一个 0×0 的窗口
	if opts.Width <= 0 {
		opts.Width = 1024
	}
	if opts.Height <= 0 {
		opts.Height = 700
	}

	switch runtime.GOOS {
	case "darwin":
		if def.Frameless {
			opts.Mac = application.MacWindow{
				InvisibleTitleBarHeight: 38,
				TitleBar: application.MacTitleBar{
					AppearsTransparent: true,
					HideTitle:          true,
					FullSizeContent:    true,
				},
			}
		} else {
			opts.Mac = application.MacWindow{
				TitleBar: application.MacTitleBar{HideTitle: false},
			}
		}
	default:
		if def.Frameless {
			opts.Frameless = true
			opts.Windows = application.WindowsWindow{
				Theme:        application.SystemDefault,
				// 保留 Win11 圆角和 Aero 阴影，全关掉窗口是硬边矩形
				DisableFramelessWindowDecorations: false,
				// 让 CSS 拖动区域走原生命中测试，拖动跟手且能触发贴边
				NonClientRegionSupport: true,
			}
		}
	}

	// 让模态子窗口压住父窗口。
	//
	// 不这么做的话：点一下父窗口 → 父窗口获得焦点升到前面 → 子窗口被盖住。
	// 模态窗口被「它正在阻挡的那个窗口」盖住，逻辑上说不通。
	//
	// macOS 用 WindowLevel=floating 而不是 AlwaysOnTop：floating 在 AppKit
	// 里的语义是「浮动工具窗口」，比 AlwaysOnTop 更贴近这里想要的效果。
	// 两者都是全局层级，代价见 config.Window.KeepAboveParent 的注释。
	if def.Modal && (def.KeepAboveParent == nil || *def.KeepAboveParent) {
		if runtime.GOOS == "darwin" {
			opts.Mac.WindowLevel = application.MacWindowLevelFloating
		} else {
			opts.AlwaysOnTop = true
		}
	}

	return opts
}
