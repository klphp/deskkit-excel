package platform

// Check 判断当前系统是否受支持。不受支持时返回带说明的错误。
//
// 非 Windows 平台恒为 nil。
func Check() error {
	ver := current()
	if ver.Major == 0 {
		// 读不到版本（非 Windows，或极端情况下的调用失败）→ 放行。
		// 宁可让少数异常机器跑起来，也不要在正常机器上误拦。
		return nil
	}
	return UnsupportedError(ver, Supported(ver.Major))
}
