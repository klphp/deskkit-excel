//go:build darwin

package window

// useNativeChild 表示当前平台能用原生 owner-child 关系（NSWindow.addChildWindow）。
const useNativeChild = true
