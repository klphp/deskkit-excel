//go:build windows

package platform

import (
	"syscall"
	"unsafe"
)

// osVersionInfoExW 对应 RtlGetVersion 的 RTL_OSVERSIONINFOW。
//
// 为什么用 RtlGetVersion 而不是 GetVersionEx：
// GetVersionEx 会被应用的兼容性清单（manifest）欺骗 —— 没声明支持 Win10 的
// 老程序会被告知「你是 Windows 8」(6.2)，判断就失效了。
// RtlGetVersion 直接返回真实版本，不受 manifest 影响。
type osVersionInfoExW struct {
	dwOSVersionInfoSize uint32
	dwMajorVersion      uint32
	dwMinorVersion      uint32
	dwBuildNumber       uint32
	dwPlatformID        uint32
	szCSDVersion        [128]uint16
}

var (
	ntdll          = syscall.NewLazyDLL("ntdll.dll")
	procRtlGetVer  = ntdll.NewProc("RtlGetVersion")
	user32         = syscall.NewLazyDLL("user32.dll")
	procMessageBox = user32.NewProc("MessageBoxW")
)

// current 读取真实系统版本。
func current() Version {
	v := osVersionInfoExW{}
	v.dwOSVersionInfoSize = uint32(unsafe.Sizeof(v))
	ret, _, _ := procRtlGetVer.Call(uintptr(unsafe.Pointer(&v)))
	if ret != 0 { // 0 = STATUS_SUCCESS
		return Version{}
	}
	return Version{Major: v.dwMajorVersion, Minor: v.dwMinorVersion, Build: v.dwBuildNumber}
}

// Warn 弹一个系统消息框。
//
// 必须用原生 MessageBox：应用还没起来，前端不存在，界面库也用不了；
// 而 Windows GUI 程序没有控制台，不弹框用户就只看到「双击没反应」。
func Warn(title, text string) {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(text)
	const mbIconWarning = 0x00000030
	const mbOK = 0x00000000
	// 最后一个参数忽略返回值，用户看完点确定程序就退出
	procMessageBox.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), mbIconWarning|mbOK)
}
