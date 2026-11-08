package services

// 用户偏好的服务门面：目前只有「自定义指令」。

import (
	"sync"

	"github.com/klphp/excel-batch-tool/internal/prefs"
)

// PrefsService 自定义指令的读写。
//
// 和 ToolService 一样是「壳 + 后置绑定」：Store 需要用户数据目录，
// 那是运行时才知道的，而 Services 列表在应用启动前就建好了。
type PrefsService struct {
	mu    sync.RWMutex
	store *prefs.Store
}

// BindStore 由启动流程注入。
func (s *PrefsService) BindStore(st *prefs.Store) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store = st
}

func (s *PrefsService) get() *prefs.Store {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.store
}

// GetInstructions 读自定义指令。没设置过返回空串。
func (s *PrefsService) GetInstructions() string {
	st := s.get()
	if st == nil {
		return ""
	}
	return st.Instructions()
}

// SetInstructions 保存自定义指令。
func (s *PrefsService) SetInstructions(text string) error {
	st := s.get()
	if st == nil {
		return errSessionUnavailable
	}
	p := st.Load()
	p.CustomInstructions = text
	return st.Save(p)
}
