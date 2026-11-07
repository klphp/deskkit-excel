package services

import (
	"context"
	"sync"

	"github.com/klphp/deskkit/internal/update"
)

// UpdateService 是自更新在前端的门面。
//
// 为什么不直接把 update.Manager 注册成 service：
// Manager 要持有 *application.App，而 App 在 application.New 里才存在 ——
// 那时候 Services 列表已经建好了。所以这里留一个可后置绑定的壳，
// 和 window.ServiceProvider 用的是同一招。
type UpdateService struct {
	mu  sync.RWMutex
	mgr *update.Manager
}

// BindManager 后置绑定真实的 Manager。由 main 在 application.New 之后调用。
func (s *UpdateService) BindManager(m *update.Manager) {
	s.mu.Lock()
	s.mgr = m
	s.mu.Unlock()
}

func (s *UpdateService) manager() *update.Manager {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mgr
}

// Enabled 报告自更新是否在配置里启用。前端据此决定要不要显示「检查更新」。
func (s *UpdateService) Enabled() bool {
	if m := s.manager(); m != nil {
		return m.Enabled()
	}
	return false
}

// Check 手动检查一次更新。只查不装 —— 装不装由用户点按钮决定。
func (s *UpdateService) Check(ctx context.Context) (*update.Result, error) {
	m := s.manager()
	if m == nil {
		return &update.Result{Available: false}, nil
	}
	return m.Check(ctx)
}

// Install 执行更新。Windows 上下载并自动替换后重启；其它平台打开下载页。
func (s *UpdateService) Install(ctx context.Context) error {
	m := s.manager()
	if m == nil {
		return nil
	}
	return m.Install(ctx)
}
