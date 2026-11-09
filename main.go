package main

import (
	"embed"
	"log/slog"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/klphp/deskkit-excel/internal/ai"
	"github.com/klphp/deskkit-excel/internal/boot"
	"github.com/klphp/deskkit-excel/internal/packs"
	"github.com/klphp/deskkit-excel/internal/prefs"
	"github.com/klphp/deskkit-excel/internal/services"
	"github.com/klphp/deskkit-excel/internal/session"
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
