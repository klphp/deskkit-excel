// Package platform 处理「这台机器能不能跑」这类启动前的判断。
//
// 目前只有一件事：Windows 10 以下不给用。
//
// 为什么要在启动前硬拦、还要弹窗：
//
//	· 应用界面用的是 WebView2，Win10 以下没有运行时，硬跑会出现
//	  白窗口、闪退、或者只有一半功能能用 —— 用户看到的是「这软件坏了」
//	· 打包成 GUI 程序后没有控制台，光写日志等于什么都没说
//
// 判断放在启动最前面：配置、服务、窗口都还没起，退出干净、不留痕迹。
package platform

import "fmt"

// MinWindowsMajor 是最低 Windows 主版本号。Windows 10 与 Windows 11
// 都报 10（Win11 的区别在 build 号 ≥ 22000），所以判断主版本即可。
const MinWindowsMajor = 10

// version 描述检测到的系统版本，便于拼提示文案。
type Version struct {
	Major uint32
	Minor uint32
	Build uint32
}

func (v Version) String() string {
	if v.Major == 0 {
		return "未知"
	}
	return fmt.Sprintf("%d.%d (build %d)", v.Major, v.Minor, v.Build)
}

// Supported 判断这个主版本号是否在支持范围内。
//
// 单独抽出来是为了能在任何平台上写测试 —— 真正的版本读取是
// Windows 专属代码，测试机上跑不到。
func Supported(major uint32) bool {
	return major >= MinWindowsMajor
}

// UnsupportedError 返回不支持时给用户看的说明；受支持时返回 nil。
func UnsupportedError(v Version, supported bool) error {
	if supported {
		return nil
	}
	return fmt.Errorf(
		"需要 Windows %d 或更高版本，当前系统是 Windows %s。\n\n"+
			"请升级系统后再运行本程序。",
		MinWindowsMajor, v.String(),
	)
}
