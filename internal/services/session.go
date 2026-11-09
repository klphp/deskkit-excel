package services

// 会话服务：给前端读写会话历史。
//
// 前端不再用 localStorage —— 历史属于用户资料，必须落盘、可备份、
// 换机器能带走；而且要加密存储（库文件整体密文）。

import (
	"sync"

	"github.com/klphp/deskkit-excel/internal/session"
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

// SessionPage 会话列表的一页。
type SessionPage struct {
	Items   []session.Summary `json:"items"`
	HasMore bool              `json:"hasMore"`
}

// MessagePage 消息的一页。
//
// 用结构体而不是 (slice, bool, error) 三返回值：Wails 生成 TS 绑定时
// 多返回值要拆成元组，调用处难读也难改。
type MessagePage struct {
	Messages []session.Message `json:"messages"`
	HasMore  bool              `json:"hasMore"`
}

// ListPage 分页读会话列表（limit<=0 不限）。
func (s *SessionService) ListPage(limit, offset int) (SessionPage, error) {
	st, err := s.get()
	if err != nil {
		return SessionPage{}, err
	}
	items, more, err := st.ListPage(limit, offset)
	if err != nil {
		return SessionPage{}, err
	}
	return SessionPage{Items: items, HasMore: more}, nil
}

// MessagesPage 游标分页读消息。beforeID=0 表示最新一页。
func (s *SessionService) MessagesPage(id string, limit int, beforeID int64) (MessagePage, error) {
	st, err := s.get()
	if err != nil {
		return MessagePage{}, err
	}
	msgs, more, err := st.MessagesPage(id, limit, beforeID)
	if err != nil {
		return MessagePage{}, err
	}
	return MessagePage{Messages: msgs, HasMore: more}, nil
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

// Retention 当前会话保留天数（默认 30）。
func (s *SessionService) Retention() int {
	st, err := s.get()
	if err != nil {
		return 30
	}
	return st.RetentionDays()
}

// SetRetention 设置会话保留天数（只接受 3 / 7 / 15 / 30）。
//
// 只改设置，不立刻清库：清理在启动时后台做，用户改完设置点关闭就走，
// 不该为了「马上生效」让界面等一次删库。
func (s *SessionService) SetRetention(days int) error {
	st, err := s.get()
	if err != nil {
		return err
	}
	return st.SetRetentionDays(days)
}

// ClearAll 清空全部会话历史，返回删掉的会话数。不可恢复。
func (s *SessionService) ClearAll() (int, error) {
	st, err := s.get()
	if err != nil {
		return 0, err
	}
	return st.ClearAll()
}

// SessionInstructions 读某个会话的自定义指令。
func (s *SessionService) SessionInstructions(id string) (string, error) {
	st, err := s.get()
	if err != nil {
		return "", err
	}
	return st.Instructions(id)
}

// SetSessionInstructions 写某个会话的自定义指令。
func (s *SessionService) SetSessionInstructions(id, text string) error {
	st, err := s.get()
	if err != nil {
		return err
	}
	return st.SetInstructions(id, text)
}

// StoragePath 库文件路径（「关于」页面显示用，也方便用户备份）。
func (s *SessionService) StoragePath() string {
	st, err := s.get()
	if err != nil {
		return ""
	}
	return st.Path()
}
