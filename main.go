package main

import (
	"context"
	"embed"
	"encoding/json"
	"log/slog"
	"os"
	"runtime"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/klphp/deskkit-excel/internal/ai"
	"github.com/klphp/deskkit-excel/internal/boot"
	"github.com/klphp/deskkit-excel/internal/config"
	"github.com/klphp/deskkit-excel/internal/packs"
	"github.com/klphp/deskkit-excel/internal/prefs"
	"github.com/klphp/deskkit-excel/internal/services"
	"github.com/klphp/deskkit-excel/internal/session"
	"github.com/klphp/deskkit-excel/internal/update"
)

// 内核要用的配置（app.yaml + windows.yaml）。
//
// embed 必须写在根包：Go 的 //go:embed 不允许引用父目录
// （`//go:embed ../../config/*.yaml` 直接编译失败）。
//
//go:embed config/app.yaml config/windows.yaml
var configFS embed.FS

// 前端构建产物。用 all: 前缀是为了把 public/ 里的点文件和子目录一起带上。
//
//go:embed all:frontend/dist
var assets embed.FS

// appName 配置目录名与日志文件的依据。改名时只改这一处。
const appName = "deskkit"

func main() {
	// 自检模式：不打开窗口、不安装，只回答"能不能查到新版本、会挑哪个包、
	// 校验和对不对"。给开发期调试自更新用，避免每轮都要构建+发版+点按钮。
	if wantsUpdateProbe(os.Args[1:]) {
		os.Exit(runUpdateProbe())
	}

	// 装配全在 boot 里：每个工具（Excel / PDF / …）的 main.go 都长这样，
	// 只是 AppName、内嵌的配置和额外注册的服务不同。
	// 工具服务：界面按钮走它；AI 走同一批函数（见下面的 excel.Register）
	toolSvc := &services.ToolService{}
	sessSvc := &services.SessionService{}
	prefsSvc := &services.PrefsService{}

	if err := boot.Run(boot.Options{
		AppName:  appName,
		ConfigFS: configFS,
		Assets:   assets,
		Services: []application.Service{
			application.NewService(toolSvc),
			application.NewService(sessSvc),
			application.NewService(prefsSvc),
		},
		// After 在 app 建好、窗口出来之前执行：把「需要 app 实例的东西」接上
		After: func(rt *boot.Runtime) error {
			toolSvc.BindApp(rt.App)

			// 用户偏好（自定义指令）：普通 JSON，0600
			prefsSvc.BindStore(prefs.NewStore(rt.DataDir()))

			// 会话库：加密存储，密钥存系统钥匙串（用户无感）
			sessStore, err := session.Open(rt.DataDir())
			if err != nil {
				// 会话历史不是核心功能，起不来也先让应用能用表格
				slog.Warn("会话存储不可用，历史功能关闭", "err", err)
			} else {
				sessSvc.BindStore(sessStore)
				// 模型调 remember_instructions 时，把内容落到当前会话上
				ai.SetInstructionSaver(func(sessionID, text string) error {
					return sessStore.SetInstructions(sessionID, text)
				})

				// 清理过期会话：丢到协程里做，不挡启动。
				// 库可能很大（几千条消息），同步跑会让窗口晚几百毫秒才出来，
				// 而这件事用户根本不等着看结果。
				go func() {
					days := sessStore.RetentionDays()
					if days == 0 {
						return // 永久保留，什么都不用做
					}
					n, err := sessStore.Prune(days)
					if err != nil {
						slog.Warn("清理过期会话失败", "err", err)
						return
					}
					if n > 0 {
						slog.Info("已清理过期会话", "removed", n, "retentionDays", days)
					}
				}()
			}

			// 能力包：今天只有表格，之后视频 / PDF 各自是一个包，
			// 在这里加一行就能进同一个对话框。
			reg := ai.NewRegistry()
			loaded, err := packs.Register(reg, rt.Config.Tools.Packs)
			if err != nil {
				return err
			}
			for _, p := range loaded {
				slog.Info("能力包已装载", "id", p.ID, "name", p.Name, "tools", reg.Len())
				if miss := p.Missing(); len(miss) > 0 {
					// 依赖没装也先让应用起来：表格功能不依赖外部程序，
					// 只是相关的工具调用会失败，界面上会显示错误。
					slog.Warn("能力包缺少外部依赖", "id", p.ID, "missing", miss)
				}
			}
			rt.AISvc.SetRegistry(reg)
			return nil
		},
	}); err != nil {
		slog.Error("启动失败", "err", err)
		return
	}
}

// wantsUpdateProbe 判断是否走自检模式。
func wantsUpdateProbe(args []string) bool {
	for _, a := range args {
		if strings.TrimSpace(a) == "--check-update" {
			return true
		}
	}
	return false
}

// runUpdateProbe 打印一行 JSON 结果退出。0 = 检查成功（有无更新都算成功），
// 1 = 检查失败（网络/限流/配置错），方便 CI 直接判成功失败。
func runUpdateProbe() int {
	// DESKKIT_CONFIG 指向外部 app.yaml 时优先用它：调自更新时不必重新构建，
	// 改完配置文件直接再跑一次即可。只在自检模式下生效，不影响正常启动。
	raw, err := configFS.ReadFile("config/app.yaml")
	if p := os.Getenv("DESKKIT_CONFIG"); p != "" {
		if b, e := os.ReadFile(p); e == nil {
			raw = b
			slog.Info("使用外部配置", "path", p)
		} else {
			slog.Warn("外部配置读不到，回退内嵌配置", "path", p, "err", e)
		}
	}
	if err != nil {
		slog.Error("读不到内嵌配置", "err", err)
		return 1
	}
	win, _ := configFS.ReadFile("config/windows.yaml")
	cfg, err := config.Parse(raw, win)
	if err != nil {
		slog.Error("配置解析失败", "err", err)
		return 1
	}

	// 允许指定平台/版本：一台机器就能替所有目标平台验一遍（CI 里也这么用）
	platform, arch, current := runtime.GOOS, runtime.GOARCH, cfg.App.Version
	for i, a := range os.Args {
		next := func() string {
			if i+1 < len(os.Args) {
				return os.Args[i+1]
			}
			return ""
		}
		switch a {
		case "--platform":
			if v := next(); v != "" {
				platform = v
			}
		case "--arch":
			if v := next(); v != "" {
				arch = v
			}
		case "--current":
			if v := next(); v != "" {
				current = v
			}
		}
	}

	res := update.Probe(context.Background(), cfg.Update, current, platform, arch)
	out, _ := json.MarshalIndent(res, "", "  ")
	os.Stdout.Write(out)
	os.Stdout.Write([]byte("\n"))
	if res.Err != "" {
		return 1
	}
	return 0
}
