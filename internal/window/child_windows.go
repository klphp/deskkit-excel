//go:build windows

package window

import (
	"syscall"
	"unsafe"
)

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	procSetWindowLongPtr = user32.NewProc("SetWindowLongPtrW")
)

// GWLP_HWNDPARENT = -8。
//
// 名字里是 "parent"，语义其实是 **owner**：设置之后
//
//	· 子窗口始终显示在 owner 之上（不会因为点 owner 而沉下去）
//	· owner 最小化时子窗口一起隐藏
//	· owner 关闭时子窗口一起销毁
//
// 但**不会**像真正的 child/POPUP 那样把窗口裁剪在父窗口内部 —— 正是我们要的：
// 子窗口可以拖到父窗口外面去。
//
// 拖动跟随时机由系统处理，不走应用层，所以没有 IPC 延迟。
const GWLP_HWNDPARENT = -8

func setWindowLongPtr(hwnd uintptr, index int32, value uintptr) uintptr {
	ret, _, _ := procSetWindowLongPtr.Call(hwnd, uintptr(index), value)
	return ret
}

// attachChildWindow 把 child 的 owner 设成 parent。
func attachChildWindow(parent, child unsafe.Pointer) {
	if parent == nil || child == nil {
		return
	}
	setWindowLongPtr(uintptr(child), GWLP_HWNDPARENT, uintptr(parent))
}

// detachChildWindow 解除 owner 关系（owner 置 0 = 无主窗口）。
func detachChildWindow(parent, child unsafe.Pointer) {
	if child == nil {
		return
	}
	setWindowLongPtr(uintptr(child), GWLP_HWNDPARENT, 0)
}
