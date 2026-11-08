package services

// 会话服务：给前端读写会话历史。
//
// 前端不再用 localStorage —— 历史属于用户资料，必须落盘、可备份、
// 换机器能带走；而且要加密存储（库文件整体密文）。

import (
	"sync"

	"github.com/klphp/excel-batch-tool/internal/session"
)

// SessionService 会话读写门面。
type SessionService struct {
	mu    sync.RWMutex
	store *session.Store
}

// BindStore 由启动流程注入（store 需要用户目录，属于运行时依赖）。
func (s *SessionService) BindStore(st *session.Store) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store = st
}

func (s *SessionService) get() (*session.Store, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.store == nil {
		return nil, errSessionUnavailable
	}
	return s.store, nil
}

// Ready 会话库是否就绪（前端可据此决定要不要显示历史栏）。
func (s *SessionService) Ready() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.store != nil
}

// List 会话列表，最近更新的排前面。
func (s *SessionService) List() ([]session.Summary, error) {
	st, err := s.get()
	if err != nil {
		return nil, err
	}
	return st.List()
}

// Load 读一个会话的完整消息。
func (s *SessionService) Load(id string) (session.Session, error) {
	st, err := s.get()
	if err != nil {
		return session.Session{}, err
	}
	return st.Load(id)
}

// Upsert 新建或更新会话标题。
func (s *SessionService) Upsert(id, title string) error {
	st, err := s.get()
	if err != nil {
		return err
	}
	return st.Upsert(id, title)
}

// Append 追加一条消息。
func (s *SessionService) Append(id, role, content string) error {
	st, err := s.get()
	if err != nil {
		return err
	}
	return st.Append(id, session.Role(role), content)
}

// Rename 改标题。
func (s *SessionService) Rename(id, title string) error {
	st, err := s.get()
	if err != nil {
		return err
	}
	return st.Rename(id, title)
}

// Delete 删除会话。
func (s *SessionService) Delete(id string) error {
	st, err := s.get()
	if err != nil {
		return err
	}
	return st.Delete(id)
}

// Import 把旧版 localStorage 的历史导进来，返回导入条数。
func (s *SessionService) Import(items []session.Session) (int, error) {
	st, err := s.get()
	if err != nil {
		return 0, err
	}
	return st.Import(items)
}

// StoragePath 库文件路径（「关于」页面显示用，也方便用户备份）。
func (s *SessionService) StoragePath() string {
	st, err := s.get()
	if err != nil {
		return ""
	}
	return st.Path()
}
