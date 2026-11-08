//go:build !windows

package platform

// current 在非 Windows 平台不做版本判断。
//
// macOS / Linux 没有「Windows 10 以下」这个问题，而且开发机就是 macOS —
// 这里保持空实现，检查逻辑才能在所有平台上编译通过。
func current() Version { return Version{} }

// Warn 在非 Windows 平台没有系统消息框可用，退回到标准输出。
// 实际上这条路径跑不到（非 Windows 永远受支持），只是为了让接口完整。
func Warn(title, text string) {
	println(title + ": " + text)
}
