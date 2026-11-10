//go:build !windows

package dpapi

import "errors"

// ErrUnsupported 在非 Windows 平台返回，调用方据此退化到文件方案。
var ErrUnsupported = errors.New("dpapi: not supported on this platform")

// Protect 在非 Windows 平台恒返回 ErrUnsupported。
func Protect([]byte) ([]byte, error) { return nil, ErrUnsupported }

// Unprotect 在非 Windows 平台恒返回 ErrUnsupported。
func Unprotect([]byte) ([]byte, error) { return nil, ErrUnsupported }
