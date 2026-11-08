package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/klphp/excel-batch-tool/internal/capability"
	"github.com/klphp/excel-batch-tool/internal/packs"
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
func (s *ToolService) Packs() []PackInfo {
	out := make([]PackInfo, 0, 4)
	for _, p := range packs.Enabled() {
		out = append(out, PackInfo{
			ID:          p.ID,
			Name:        p.Name,
			Description: p.Description,
			Requires:    p.Requires,
			Missing:     p.Missing(),
			Bundled:     p.Bundled(),
		})
	}
	return out
}

// PickPaths 弹系统选择框：文件和文件夹都能选，可以多选，**不限类型**。
//
// 为什么不设 Filters：这个工具处理的是本地文件本身，用户选什么就是什么。
// 替他预设「只看表格」既多余也误导 —— 他不知道我们要不要视频，我们也不知道
// 他接下来要处理表格还是视频。真选错了，AI 看一眼内容会直接告诉他。
func (s *ToolService) PickPaths(title string) ([]string, error) {
	app := s.application()
	if app == nil || app.Dialog == nil {
		return nil, errors.New("对话框不可用")
	}
	if title == "" {
		title = "选择文件或文件夹"
	}
	dialog := app.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		Title:                   title,
		CanChooseFiles:          true,
		CanChooseDirectories:    true,
		AllowsMultipleSelection: true,
	})
	paths, err := dialog.PromptForMultipleSelection()
	if err != nil {
		return nil, err
	}
	return paths, nil
}

// PackInfo 给界面用的能力包描述。
type PackInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Requires    []string `json:"requires,omitempty"`
	Missing     []string `json:"missing,omitempty"`
	// Bundled 为 true 表示功能组件已随应用内置，首次使用不需要下载。
	Bundled bool `json:"bundled"`
}

// CapabilityUsage 返回某个能力包的用法说明（markdown）。
//
// 界面在用户选中一个功能时把它塞进系统提示 —— 这样模型一开始就知道
// 这批工具怎么调，不用先花一轮去试错。外部 agent 走
// GET /api/v1/capabilities/:id/usage 拿同一份内容。
func (s *ToolService) CapabilityUsage(id string) (string, error) {
	for _, p := range packs.Enabled() {
		if p.ID != id {
			continue
		}
		doc, err := capability.BuildUsage(p)
		if err != nil {
			return "", err
		}
		return doc.Markdown, nil
	}
	return "", errors.New("没有这个能力包: " + id)
}
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
