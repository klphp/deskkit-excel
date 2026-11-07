package window

import (
	"fmt"

	"github.com/wailsapp/wails/v3/pkg/application"
	"sync"
	"sync/atomic"
)

// 全局管理器持有者。
//
// # 为什么要绕这一下
//
// Wails 的 Service 必须在 application.New() 时注册，而 Manager 需要
// app 实例才能建窗口 —— 顺序上 Manager 只能在 New 之后创建。所以：
//
//	① New 时注册一个「空壳」Service（ServiceProvider）
//	② New 之后把真正的 Manager 塞进来（Bind）
//	③ 前端调用时从 holder 取 Manager
//
// 用 atomic.Pointer 而不是普通变量：前端可能在 Manager 绑定完成前就
// 触发调用（窗口刚出现就 render），读写竞争会 panic。
var managerPtr atomic.Pointer[Manager]

// Bind 绑定真正的管理器。由 main 在 app.New 之后调用。
func Bind(m *Manager) {
	managerPtr.Store(m)
}

func getManager() (*Manager, error) {
	if m := managerPtr.Load(); m != nil {
		return m, nil
	}
	return nil, fmt.Errorf("窗口管理器还没初始化（应用启动未完成）")
}

// ServiceProvider 注册给 Wails 的空壳服务，实际逻辑转到 Manager。
type ServiceProvider struct{}

// 初始化完成前的调用统一返回错误而不是 panic —— 前端在挂载阶段
// 就可能调用 Open/IsMaximised，那时 Manager 可能还没绑定好。
var bindOnce sync.Once

// Open 按名字打开窗口（名字对应 config/windows.yaml 里的键）。
//
// 已打开则聚焦到前面，不会开出第二个。
func (s *ServiceProvider) Open(name string) error {
	m, err := getManager()
	if err != nil {
		return err
	}
	return m.Open(name)
}

// Close 关闭指定窗口。
func (s *ServiceProvider) Close(name string) error {
	m, err := getManager()
	if err != nil {
		return err
	}
	return m.Close(name)
}

// Opened 当前已打开的窗口名。
func (s *ServiceProvider) Opened() ([]string, error) {
	m, err := getManager()
	if err != nil {
		return nil, err
	}
	return m.List(), nil
}

// CenterOnParent 把当前窗口重新摆到父窗口正中。
//
// 前端 ResizeToContent 改了窗口高度后要调一次 —— 窗口长大了却还贴着
// 原来的左上角，看起来会往下偏。
func (s *ServiceProvider) CenterOnParent() error {
	m, err := getManager()
	if err != nil {
		return err
	}
	return m.CenterOnParent(application.Get().Window.Current().Name())
}

// Names 配置里定义的全部窗口名（前端可据此动态生成菜单）。
func (s *ServiceProvider) Names() ([]string, error) {
	m, err := getManager()
	if err != nil {
		return nil, err
	}
	return m.Names(), nil
}
