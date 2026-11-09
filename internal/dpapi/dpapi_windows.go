//go:build windows

// Package dpapi 用 Windows 的 DPAPI（数据保护 API）加解密小块敏感数据。
//
// 用途：会话库的加密密钥。Windows 上没有 macOS 那样的通用钥匙串命令行，
// 但 DPAPI 是系统自带的、按「当前登录用户 + 本机」派生的加密：
//
//	✓ 密钥文件被拷到别的机器 / 别的用户账户 —— 解不开
//	✓ 不用让用户设密码，也不用装证书
//	✗ 防不住「别人用你已登录的账户」—— 和 macOS 钥匙串的边界一样
//
// 刻意只做「加密一个小 blob」这一件事：不碰文件系统、不管路径，
// 让调用方（session 包）自己决定存哪儿，这样在 macOS/Linux 上也能编译与测试。
package dpapi

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ErrUnsupported 在非 Windows 平台返回，便于调用方退化到其它方案。
var ErrUnsupported = errors.New("dpapi: 本平台不支持")

// entropy 附加熵：让 blobs 只能由知道这串常量的程序解开。
// 换个值等于让历史数据全部失效，不要随手改。
var entropy = []byte("com.klphp.deskkit/session-db-key/v1")

func blob(b []byte) windows.DataBlob {
	if len(b) == 0 {
		return windows.DataBlob{Size: 0, Data: nil}
	}
	return windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

// Protect 加密：只有同一台机器上的同一个用户能 Unprotect。
//
// flags 用 CRYPTPROTECT_UI_FORBIDDEN：不出任何提示框（服务/无界面场景下
// 弹框会卡死调用方）。
func Protect(plain []byte) ([]byte, error) {
	if len(plain) == 0 {
		return nil, errors.New("dpapi: 输入为空")
	}
	in := blob(plain)
	ent := blob(entropy)
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, &ent, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, fmt.Errorf("dpapi: 加密失败: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	res := make([]byte, out.Size)
	copy(res, unsafe.Slice(out.Data, out.Size))
	return res, nil
}

// Unprotect 解密。换了用户账户、换了机器、或附加熵变了都会失败——
// 这是特性，不是 bug：调用方应当把它当成「密钥不可用」，而不是静默换新密钥。
func Unprotect(secret []byte) ([]byte, error) {
	if len(secret) == 0 {
		return nil, errors.New("dpapi: 输入为空")
	}
	in := blob(secret)
	ent := blob(entropy)
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, &ent, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, fmt.Errorf("dpapi: 解密失败（密钥可能来自其它用户或其它机器）: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	res := make([]byte, out.Size)
	copy(res, unsafe.Slice(out.Data, out.Size))
	return res, nil
}
