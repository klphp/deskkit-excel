//go:build windows

// Package dpapi 用 Windows 的 DPAPI（数据保护 API）加解密小块敏感数据。
//
// 用途：会话库的加密密钥。Windows 上没有 macOS 那样的通用钥匙串命令行，
// 但 DPAPI 是系统自带的、按「登录用户 + 本机」派生的加密：
//
//	✓ 密钥文件被拷到别的机器 / 别的用户账户 —— 解不开
//	✓ 不用让用户设密码，也不用装证书
//	✗ 防不住「别人用你已登录的账户」—— 和 macOS 钥匙串的边界一样
//
// 两种作用域，按可靠性降级：
//
//	user    （CRYPTPROTECT 默认）当前用户可解 —— 首选
//	machine （CRYPTPROTECT_LOCAL_MACHINE）本机任意账户可解 —— 兜底
//
// 为什么要有兜底：DPAPI 的 user 作用域要求**用户配置文件已加载**。
// 在无交互登录的上下文（服务、SSH session 0、部分计划任务）里调它会直接
// 返回「拒绝访问」—— 实测 PowerShell 的 ProtectedData 同样如此，
// 所以不是调用方式的问题，是环境限制。真机上正常双击运行不受影响，
// 但把兜底做进去，应用就不会因为跑在奇怪上下文里而拿不到密钥。
//
// 输出格式：[1 字节作用域][DPAPI blob]，作用域 0x01=user、0x02=machine。
// 前缀让 Unprotect 知道该用哪个标志解，也方便日后加新作用域。
package dpapi

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ErrUnsupported 在非 Windows 平台返回，便于调用方退化到其它方案。
var ErrUnsupported = errors.New("dpapi: not supported on this platform")

const (
	scopeUser    byte = 0x01
	scopeMachine byte = 0x02
)

// entropy 附加熵：让 blob 只能由知道这串常量的程序解开。
// 换个值等于让历史数据全部失效，不要随手改。
var entropy = []byte("com.klphp.deskkit/session-db-key/v1")

func blob(b []byte) windows.DataBlob {
	if len(b) == 0 {
		return windows.DataBlob{Size: 0, Data: nil}
	}
	return windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

// protectOnce 用指定标志加密一次。
func protectOnce(plain []byte, flags uint32) ([]byte, error) {
	in := blob(plain)
	ent := blob(entropy)
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, &ent, 0, nil, flags|windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	res := make([]byte, out.Size)
	copy(res, unsafe.Slice(out.Data, out.Size))
	return res, nil
}

// Protect 加密。优先用户作用域；用户配置文件不可用时退化到本机作用域。
func Protect(plain []byte) ([]byte, error) {
	if len(plain) == 0 {
		return nil, errors.New("dpapi: input is empty")
	}
	if ct, err := protectOnce(plain, 0); err == nil {
		return append([]byte{scopeUser}, ct...), nil
	} else if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return nil, fmt.Errorf("dpapi: encryption failed: %w", err)
	}
	ct, err := protectOnce(plain, windows.CRYPTPROTECT_LOCAL_MACHINE)
	if err != nil {
		return nil, fmt.Errorf("dpapi: encryption failed (neither user nor machine scope is available): %w", err)
	}
	return append([]byte{scopeMachine}, ct...), nil
}

// Unprotect 解密。换了用户账户、换了机器、或附加熵变了都会失败——
// 这是特性，不是 bug：调用方应当把它当成「密钥不可用」，而不是静默换新密钥。
func Unprotect(secret []byte) ([]byte, error) {
	if len(secret) == 0 {
		return nil, errors.New("dpapi: input is empty")
	}
	if len(secret) < 2 {
		return nil, errors.New("dpapi: data was not written by this app")
	}
	scope, body := secret[0], secret[1:]
	var flags uint32
	switch scope {
	case scopeUser:
		flags = 0
	case scopeMachine:
		flags = windows.CRYPTPROTECT_LOCAL_MACHINE
	default:
		return nil, fmt.Errorf("dpapi: unknown scope 0x%02x", scope)
	}
	in := blob(body)
	ent := blob(entropy)
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, &ent, 0, nil, flags|windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, fmt.Errorf("dpapi: decryption failed (the key may come from another user or machine): %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	res := make([]byte, out.Size)
	copy(res, unsafe.Slice(out.Data, out.Size))
	return res, nil
}
