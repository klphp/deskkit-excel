// Package boot 应用装配与启动。
//
// 存在的意义：DeskKit 是一条产品线，每个工具（Excel / PDF / …）都是独立的
// 可执行文件，但它们启动时要做的事完全一样 —— 读配置、起本地服务、装 AI、
// 单实例锁、自更新、开主窗口。
//
// 这些逻辑放在 main.go 里时，加第二个工具就要整段复制一遍，然后两边各自
// 长歪（这个项目已经因为两份分叉的壳吃过一次亏）。所以抽到这里：
// 每个工具的 main.go 只剩下「内嵌什么 + 额外注册哪些服务」。
package boot

import (
	"context"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/klphp/deskkit/internal/ai"
	"github.com/klphp/deskkit/internal/api"
	"github.com/klphp/deskkit/internal/config"
	"github.com/klphp/deskkit/internal/server"
	"github.com/klphp/deskkit/internal/services"
	"github.com/klphp/deskkit/internal/update"
	"github.com/klphp/deskkit/internal/window"
)

// Options 启动参数。
type Options struct {
	// AppName 应用名。同时用作配置目录名与日志文件名依据。
	// 每个工具取自己的名字（deskkit-excel / deskkit-pdf），
	// 否则两个工具会共用一份配置和日志，互相踩。
	AppName string

	// ConfigFS 内嵌的配置（必须含 config/app.yaml 与 config/windows.yaml）。
	ConfigFS fs.FS

	// Assets 内嵌的前端产物。
	Assets fs.FS

	// Services 应用自己的额外服务（工具注册表、业务服务…）。
	// 框架自带的服务由 Run 统一注册。
	Services []application.Service

	// After 启动完成后的回调，可选。
	// 用来做「依赖 app 实例」的后置绑定，或者注册工具、预热数据。
	After func(*Runtime) error
}

// Runtime 装配结果。After 回调拿它做后续绑定。
type Runtime struct {
	App     *application.App
	Config  *config.Config
	Server  *server.Server
	API     *api.Manager
	AI      *ai.Manager
	AISvc   *services.AIService
	Updater *update.Manager
	Windows *window.Manager
}

// Run 装配并运行应用，阻塞到退出。
func Run(opts Options) error {
	if opts.AppName == "" {
		return errf("boot: AppName 不能为空")
	}

	// 日志必须最先就位：后面任何一步失败都要能落盘。
	// Windows GUI 构建（-H windowsgui）没有控制台，只写 stderr 等于日志蒸发。
	if path := setupLogging(opts.AppName); path != "" {
		slog.Info("日志已启用", "path", path)
	} else {
		slog.Warn("日志文件不可用，仅输出到控制台（Windows GUI 下将看不到任何日志）")
	}

	cfg, err := loadConfig(opts.ConfigFS)
	if err != nil {
		return errf("加载配置失败: %v", err)
	}

	// ── 内嵌本地 HTTP 服务 ──
	// 先启动再开窗口：窗口里的前端可能立刻要访问本地接口，
	// 顺序反了会有一段时间接口还没起来。
	srv := server.New(cfg.Server)
	if err := srv.Start(); err != nil {
		// 本地服务起不来不该让整个应用挂掉 —— bindings 仍然可用。
		slog.Error("本地服务启动失败，HTTP 接口不可用（bindings 不受影响）", "err", err)
	} else if srv.Addr() != "" {
		slog.Info("本地接口就绪", "url", srv.URL())
	}

	// 关掉禁止多开、又写死端口 → 多开时第二个实例监听失败。
	warnFixedPort(cfg)

	// ── 外部 API 客户端 ──
	apiMgr := api.NewManager(cfg, cfg.APIs)

	// ── 自更新 / AI 的服务壳 ──
	// 它们的 Manager 依赖 app 实例（application.New 之后才有），
	// 而 Services 列表在那之前就要建好，所以先建壳、后绑定。
	updateSvc := &services.UpdateService{}
	aiMgr := ai.NewManager(cfg.AI, cfg.AIKey, userDataDir(opts.AppName))
	aiSvc := &services.AIService{}

	if cfg.AI.IsEnabled() {
		if err := cfg.AI.Validate(); err != nil {
			slog.Warn("AI 配置有问题，AI 功能可能不可用", "err", err)
		}
		slog.Info("AI 已启用",
			"default", aiMgr.Default(),
			"providers", len(cfg.AI.Providers),
			"localProbe", cfg.AI.Local.Enabled)
	} else {
		slog.Info("AI 未启用（config/app.yaml 的 ai.enabled）")
	}

	// ── Wails 应用 ──
	//
	// wmRef 供单实例回调激活主窗口。回调可能早于窗口管理器创建就触发
	// （第二个实例启动得很快），所以用原子指针 + 轮询兜底 ——
	// 直接闭包捕获到 nil 就永久失效了。
	var wmRef atomic.Pointer[window.Manager]

	appOpts := application.Options{
		Name:        cfg.App.Name,
		Description: cfg.App.Identifier,
		Services: append([]application.Service{
			// 依赖通过构造注入，而不是让服务内部去 Get() 全局单例 ——
			// 全局单例看着省事，但单测没法替换，多实例也没法隔离
			application.NewService(&services.WindowService{}),
			application.NewService(&window.ServiceProvider{}),
			application.NewService(&services.AppService{Config: cfg, Server: srv}),
			application.NewService(updateSvc),
			application.NewService(aiSvc),
		}, opts.Services...),
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(opts.Assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	}

	// ── 禁止多开（单实例）──
	if cfg.App.SingleInstanceOrDefault() {
		appOpts.SingleInstance = &application.SingleInstanceOptions{
			UniqueID: instanceID(cfg, opts.AppName),
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				// 第二个实例已把参数递过来并自行退出，这里只负责
				// 把第一实例的窗口拉到前面。不写这段，用户双击毫无反应。
				slog.Info("检测到第二个实例，激活主窗口", "args", data.Args, "workdir", data.WorkingDir)
				activate := func() bool {
					m := wmRef.Load()
					if m == nil {
						return false
					}
					if name, ok := cfg.MainWindowName(); ok {
						if err := m.Open(name); err != nil {
							slog.Warn("激活主窗口失败", "name", name, "err", err)
						}
					}
					return true
				}
				if !activate() {
					go func() {
						for i := 0; i < 30; i++ {
							time.Sleep(100 * time.Millisecond)
							if activate() {
								return
							}
						}
						slog.Warn("主窗口迟迟未就绪，放弃激活")
					}()
				}
			},
		}
	} else {
		slog.Info("已关闭禁止多开（app.singleInstance=false）")
	}

	app := application.New(appOpts)

	// ── 自更新 ──
	// 未启用时 Setup 直接返回；启用但配置不全只告警，不让应用起不来。
	updMgr := update.New(app, cfg.Update, cfg.App.Version, releasePageURL(cfg.Update))
	if err := updMgr.Setup(); err != nil {
		slog.Error("自更新不可用（主功能不受影响）", "err", err)
	}
	updateSvc.BindManager(updMgr)

	// AI：Manager 不依赖 app，但发事件（流式增量）需要 app 实例
	aiSvc.BindApp(app)
	aiSvc.BindManager(aiMgr)

	// 窗口管理器需要 app 实例，所以放在 New 之后
	windowManager := window.NewManager(app, cfg.Windows)
	wmRef.Store(windowManager)
	window.Bind(windowManager)

	rt := &Runtime{
		App: app, Config: cfg, Server: srv, API: apiMgr,
		AI: aiMgr, AISvc: aiSvc, Updater: updMgr, Windows: windowManager,
	}
	if opts.After != nil {
		if err := opts.After(rt); err != nil {
			return errf("启动回调失败: %v", err)
		}
	}

	if err := windowManager.Main(); err != nil {
		return errf("打开主窗口失败: %v", err)
	}

	// 调试开关：AUTO_OPEN=settings 启动后自动打开指定窗口。
	// 必须在 Run() 已跑起来之后再开 —— 在 Run() 之前建窗口，
	// 窗口对象建出来了但原生窗口没准备好，表现为「创建成功但看不见」。
	if autoOpen := os.Getenv("AUTO_OPEN"); autoOpen != "" {
		go func() {
			time.Sleep(800 * time.Millisecond)
			if err := windowManager.Open(autoOpen); err != nil {
				slog.Error("AUTO_OPEN 打开窗口失败", "name", autoOpen, "err", err)
			}
		}()
	}

	// 退出时关掉本地服务，别留悬挂的 listener
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Stop(ctx)
	}()

	return app.Run()
}

// ── 内部助手 ────────────────────────────────────────────

type bootError string

func (e bootError) Error() string { return string(e) }
func errf(format string, args ...any) error {
	return bootError(sprintf(format, args...))
}

// loadConfig 解析内嵌配置。
func loadConfig(configFS fs.FS) (*config.Config, error) {
	appYAML, err := fs.ReadFile(configFS, "config/app.yaml")
	if err != nil {
		return nil, err
	}
	winYAML, err := fs.ReadFile(configFS, "config/windows.yaml")
	if err != nil {
		return nil, err
	}
	return config.Parse(appYAML, winYAML)
}

// releasePageURL 最坏情况下给前端一个能点的地址。
//
// github provider 正常会从 release metadata 里取到 htmlURL，这个只是兜底：
// 首次检查失败、或 endpoint provider 没给发布页字段时用。
func releasePageURL(cfg config.Update) string {
	if cfg.Repository != "" {
		return "https://github.com/" + cfg.Repository + "/releases/latest"
	}
	return cfg.ManifestURL
}

// userDataDir 用户配置目录（AI 设置、密钥都放这里，0600）。
//
// 三个候选依次退让：标准配置目录 → 可执行文件旁边 → 当前目录。
// 拿不到目录不该让应用起不来，最坏情况只是设置存不住。
func userDataDir(appName string) string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, appName)
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	return "."
}

// instanceID 单实例锁的标识。
//
// 必须稳定且唯一 —— 改了它等于放开多开。优先用 identifier（本来就是
// 全局唯一的 bundle id）；没配才退化到应用名。
//
// 调试期两个实例会互抢锁（wails3 dev 起的和装好的正式版用同一个 ID）：
//
//	APP_INSTANCE_ID=myapp.dev   # 临时换一个锁
//	wails3 dev -tags dev        # 或加构建标签，自动加 .dev 后缀
func instanceID(cfg *config.Config, appName string) string {
	if v := strings.TrimSpace(os.Getenv("APP_INSTANCE_ID")); v != "" {
		return v
	}
	base := strings.TrimSpace(cfg.App.Identifier)
	if base == "" {
		base = strings.TrimSpace(cfg.App.Name)
	}
	if base == "" {
		base = appName
	}
	if devBuild {
		base += ".dev"
	}
	return base
}

// warnFixedPort 检查「关闭多开 + 固定端口」这个会打架的组合。
//
// 内嵌服务默认 127.0.0.1:0（系统分配），正常情况下碰不到。
// 只有当用户既关多开、又把端口写成固定值时才提醒。
// 没用内嵌服务（server.enabled=false）的应用与这条无关。
func warnFixedPort(cfg *config.Config) {
	if cfg.App.SingleInstanceOrDefault() || !cfg.Server.Enabled {
		return
	}
	_, port, err := net.SplitHostPort(cfg.Server.Addr)
	if err != nil || port == "" || port == "0" {
		return
	}
	slog.Warn("已关闭禁止多开，但内嵌服务用了固定端口：多开时第二个实例会监听失败",
		"addr", cfg.Server.Addr,
		"建议", "把 server.addr 的端口写回 0（系统分配），或重新开启 app.singleInstance")
}

// ignoreWriteErr 包一层 writer，吞掉写入错误。
//
// 专给 Windows GUI 下的 os.Stderr 用：那个句柄无效，每次写都报错，而
// io.MultiWriter 的语义是「任何一个 writer 报错就整体失败、不再往下写」，
// 会把真正要写的日志文件一起拖死。
type ignoreWriteErr struct{ w io.Writer }

func (i ignoreWriteErr) Write(p []byte) (int, error) {
	n, _ := i.w.Write(p)
	if n == 0 {
		return len(p), nil // 假装成功，别让 MultiWriter 短路
	}
	return n, nil
}

// setupLogging 把日志同时写进文件和控制台。
//
// 顺序极其关键：文件放前面。曾写成 io.MultiWriter(os.Stderr, f)，结果
// stderr 写失败把 MultiWriter 短路，日志文件虽然创建出来却永远 0 字节 ——
// OpenFile 也没报错，看着一切正常。这个 bug 排查了整整一个晚上。
func setupLogging(appName string) string {
	lvl := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		lvl = slog.LevelDebug
	}
	handlerOpts := &slog.HandlerOptions{Level: lvl}

	var candidates []string
	if dir, err := os.UserConfigDir(); err == nil {
		candidates = append(candidates, filepath.Join(dir, appName))
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Dir(exe))
	}

	for _, dir := range candidates {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			continue
		}
		f, err := os.OpenFile(filepath.Join(dir, "app.log"),
			os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			continue
		}
		w := io.MultiWriter(f, ignoreWriteErr{os.Stderr})
		log.SetOutput(w)
		slog.SetDefault(slog.New(slog.NewTextHandler(w, handlerOpts)))
		return filepath.Join(dir, "app.log")
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, handlerOpts)))
	return ""
}
