//go:build darwin

package session

import (
	"errors"
	"os/exec"
	"strings"
)

// readKey 从 macOS 钥匙串取密钥。
//
// 用 /usr/bin/security 而不是 cgo 调 Security.framework：
// 免 cgo、免签名相关权限申请，行为在 macOS 各版本上稳定。
func readKey() (string, error) {
	out, err := exec.Command("/usr/bin/security",
		"find-generic-password", "-s", keyService, "-a", keyAccount, "-w").Output()
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(string(out))
	if len(key) != 64 {
		return "", errors.New("The key stored in the keychain has a wrong format")
	}
	return key, nil
}

// writeKey 写入钥匙串。-U 表示已存在就更新，避免重复条目。
func writeKey(key string) error {
	return exec.Command("/usr/bin/security",
		"add-generic-password", "-s", keyService, "-a", keyAccount, "-w", key, "-U").Run()
}
