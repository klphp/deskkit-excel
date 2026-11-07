//go:build !darwin && !windows

package window

import "unsafe"

// 这两个平台没有对应的原生 owner-child 机制（至少 Wails 没暴露），
// 跟随由 followParent 的「事件 + 重设位置」兜底。

func attachChildWindow(parent, child unsafe.Pointer) {}
func detachChildWindow(parent, child unsafe.Pointer) {}
