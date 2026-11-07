package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/klphp/excel-batch-tool/internal/tools/excel"
)

// ToolService Excel 能力在前端的门面。
//
// 界面上的按钮走这里，AI 的对话走 internal/ai 的工具循环 —— 但两者最终
// 都调到 internal/tools/excel 的同一批函数，行为不会分叉。
type ToolService struct {
	mu  sync.RWMutex
	app *application.App
}

// BindApp 后置绑定 app（原生对话框需要它）。
func (s *ToolService) BindApp(app *application.App) {
	s.mu.Lock()
	s.app = app
	s.mu.Unlock()
}

func (s *ToolService) application() *application.App {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.app
}

// PickFolder 弹系统文件夹选择框，返回绝对路径。
//
// 为什么必须走原生对话框：WebView 的 File 对象拿不到完整路径
// （浏览器安全模型），只有原生侧能给出真实路径。所以界面上的「拖入」
// 只能看文件内容，要处理整个文件夹就必须走这里。
func (s *ToolService) PickFolder(title string) (string, error) {
	app := s.application()
	if app == nil || app.Dialog == nil {
		return "", errors.New("对话框不可用")
	}
	if title == "" {
		title = "选择存放 Excel 的文件夹"
	}
	dialog := app.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		Title:                   title,
		CanChooseFiles:          false,
		CanChooseDirectories:    true,
		AllowsMultipleSelection: false,
	})
	return dialog.PromptForSingleSelection()
}

// Scan 扫描目录，返回文件清单与统计。
func (s *ToolService) Scan(ctx context.Context, dir string) (excel.ScanResult, error) {
	return excel.Scan(ctx, dir)
}

// MakeSample 生成一份示例数据，返回目录与文件清单。
//
// 位置在用户配置目录下（~/Library/Application Support/deskkit/sample 一类），
// 不往桌面或文档里乱写 —— 那个位置用户找不到也不好删。
func (s *ToolService) MakeSample() (excel.SampleInfo, error) {
	base := ""
	if dir, err := os.UserConfigDir(); err == nil {
		base = filepath.Join(dir, "deskkit")
	} else {
		base = os.TempDir()
	}
	return excel.EnsureSample(base)
}

// Clean 清洗。DryRun=true 时只统计不写文件。
func (s *ToolService) Clean(ctx context.Context, opt excel.CleanOptions) (excel.CleanResult, error) {
	return excel.Clean(ctx, opt)
}

// Merge 合并。DryRun=true 时只预览不写文件。
func (s *ToolService) Merge(ctx context.Context, opt excel.MergeOptions) (excel.MergeResult, error) {
	return excel.Merge(ctx, opt)
}
