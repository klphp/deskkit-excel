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

// PickPaths 弹系统选择框，**文件和文件夹都能选，而且可以多选**。
//
// 为什么不替用户决定「单文件还是批量」：这取决于他一句话怎么说，
// 我们猜不出来。所以选择器只管放开（单个文件、多个文件、一个文件夹都行），
// 工具层两种能力都摆在那里，由 AI 根据用户的话去挑。
func (s *ToolService) PickPaths(title string) ([]string, error) {
	app := s.application()
	if app == nil || app.Dialog == nil {
		return nil, errors.New("对话框不可用")
	}
	if title == "" {
		title = "选择要处理的文件或文件夹"
	}
	dialog := app.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		Title:                   title,
		CanChooseFiles:          true,
		CanChooseDirectories:    true,
		AllowsMultipleSelection: true,
		Filters: []application.FileFilter{
			{DisplayName: "表格与全部文件", Pattern: "*.xlsx;*.xlsm;*.csv;*.*"},
		},
	})
	paths, err := dialog.PromptForMultipleSelection()
	if err != nil {
		return nil, err
	}
	return paths, nil
}

// PickFile 弹系统文件选择框，返回单个绝对路径（保留：仍需精确选一个文件时用）。
//
// 第一版只做单文件：批量要处理「哪些文件、按什么顺序、输出到哪」，
// 那是另一套界面和另一套错误处理，等单文件跑顺了再说。
func (s *ToolService) PickFile(title string) (string, error) {
	app := s.application()
	if app == nil || app.Dialog == nil {
		return "", errors.New("对话框不可用")
	}
	if title == "" {
		title = "选择要处理的表格"
	}
	dialog := app.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		Title:                   title,
		CanChooseFiles:          true,
		CanChooseDirectories:    false,
		AllowsMultipleSelection: false,
		Filters: []application.FileFilter{
			{DisplayName: "Excel 表格", Pattern: "*.xlsx;*.xlsm"},
			{DisplayName: "全部文件", Pattern: "*.*"},
		},
	})
	return dialog.PromptForSingleSelection()
}

// InspectFile 打开一个文件，返回表结构（给界面显示「几列几行」，也给模型用）。
func (s *ToolService) InspectFile(ctx context.Context, path string) (excel.SingleInfo, error) {
	db, info, err := excel.LoadWorkbookFile(ctx, path)
	if db != nil {
		db.Close()
	}
	return info, err
}

// Packs 当前可用的能力包，界面上的「功能选择」用它渲染列表。
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
