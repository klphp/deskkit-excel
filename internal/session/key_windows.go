//go:build windows

package session

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/klphp/excel-batch-tool/internal/dpapi"
)

// Windows 的密钥存储：DPAPI 加密后落盘。
//
// 为什么不是明文文件：明文 key + 明文旁的密文库，等于把锁和钥匙放同一个抽屉里
// —— 用户把整个目录拷到网盘、或者被别的程序批量读文件时，等于没加密。
// DPAPI 是系统自带的、按「当前登录用户 + 本机」派生的密钥，不用装证书、不用用户设密码。
//
// 边界（和 macOS 钥匙串一致，不要写成更强的承诺）：
//   ✓ 目录被拷到别的机器/别的账户 —— 解不开
//   ✓ 备份泄漏、误传网盘 —— 解不开
//   ✗ 别人用你已登录的账户 —— 能解（这是 DPAPI 的设计边界）
//
// 文件位置：与数据库同目录（`%LOCALAPPDATA%\deskkit\session-db.key.dpapi`），
// 内容是 base64(DPAPI blob)，权限 0600。

const dpapiKeyFile = "session-db.key.dpapi"

// dpapiDir 取密钥目录。用与 store 相同的用户数据目录，保证和库文件在一起。
func dpapiDir() (string, error) {
	base, err := os.UserConfigDir() // Windows: %AppData%；这里退一步用 LocalAppData 更合适
	if err != nil || base == "" {
		base = os.Getenv("LOCALAPPDATA")
	}
	if base == "" {
		return "", errors.New("找不到用户数据目录")
	}
	dir := filepath.Join(base, "deskkit")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// readKey 读回密钥：DPAPI 解密 → 校验 64 位 hex。
func readKey() (string, error) {
	dir, err := dpapiDir()
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(filepath.Join(dir, dpapiKeyFile))
	if err != nil {
		return "", errNoKeyring
	}
	blob, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return "", fmt.Errorf("密钥文件损坏: %w", err)
	}
	plain, err := dpapi.Unprotect(blob)
	if err != nil {
		// 解不开通常意味着密钥文件来自别的用户/别的机器（拷了 AppData）。
		// 挪到 .broken-<时间戳> 留证，再让上层走「生成新密钥」——此时旧库本来也打不开，
		// 但至少不会静默覆盖掉一份可能还有救的文件。
		broken := filepath.Join(dir, fmt.Sprintf("%s.broken-%d", dpapiKeyFile, time.Now().Unix()))
		_ = os.Rename(filepath.Join(dir, dpapiKeyFile), broken)
		return "", fmt.Errorf("DPAPI 解密失败（已备份为 %s）: %w", filepath.Base(broken), err)
	}
	key := string(plain)
	if len(key) != 64 {
		return "", errors.New("密钥长度不合法")
	}
	return key, nil
}

// writeKey 加密后落盘；若存在旧的明文密钥文件，顺手迁移并删除它。
func writeKey(key string) error {
	dir, err := dpapiDir()
	if err != nil {
		return err
	}
	blob, err := dpapi.Protect([]byte(key))
	if err != nil {
		return err
	}
	enc := base64.StdEncoding.EncodeToString(blob)
	path := filepath.Join(dir, dpapiKeyFile)
	if err := os.WriteFile(path, []byte(enc), 0o600); err != nil {
		return err
	}
	// 迁移：老版本把明文密钥写在 session-db.key，成功加密后删掉明文副本。
	plainPath := filepath.Join(dir, "session-db.key")
	if _, statErr := os.Stat(plainPath); statErr == nil {
		if b, readErr := os.ReadFile(plainPath); readErr == nil && string(b) == key {
			_ = os.Remove(plainPath)
		}
	}
	return nil
}

// 编译期护栏：key_windows.go 只在 windows 生效，这里显式引用一次 runtime，
// 便于在日志/报错里带上平台信息时使用（也防止误删 import 后被 gofmt 清掉）。
var _ = runtime.GOOS
