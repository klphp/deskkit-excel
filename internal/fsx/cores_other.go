//go:build !darwin

package fsx

// physicalCores 非 darwin 平台返回 0，调用方回退到 runtime.NumCPU()。
func physicalCores() int { return 0 }
