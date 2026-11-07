//go:build !darwin && !windows

package window

// useNativeChild 为 false 时退回「监听移动事件 + 重设位置」。
const useNativeChild = false
