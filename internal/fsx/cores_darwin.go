package fsx

import "golang.org/x/sys/unix"

// physicalCores 通过 sysctl 取物理核数（仅 darwin 实现）。
func physicalCores() int {
	n, err := unix.SysctlUint32("hw.physicalcpu")
	if err != nil || n == 0 {
		return 0
	}
	return int(n)
}
