package session

// 密钥管理：应用首次启动时生成一个随机密钥，存进系统钥匙串，之后每次启动取回来。
//
// 用户全程无感 —— 不用输密码，也不用知道密钥存在。
//
// 这个方案防的是什么，要说清楚：
//   ✓ 数据库文件被拷走、备份泄漏、误传到网盘 —— 拿到文件也打不开
//   ✗ 别人用你已登录的电脑账户 —— 钥匙串对同一个登录用户是可读的
//
// 要防后者只能让用户自己设密码（密钥由密码派生、不落盘）。那是另一个决策，
// 当前版本明确不做，不要在文档里写成更强的承诺。

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// keyService 钥匙串条目的服务名。账号名固定，服务名带应用标识。
const (
	keyService = "com.klphp.deskkit"
	keyAccount = "session-db-key"
)

// keychainRead / keychainWrite 钥匙串读写。
//
// 提成变量是为了能测「没有钥匙串」的平台（Windows / Linux / CI）：
// 直接调 readKey/writeKey 的话，macOS 上钥匙串总是可用，
// 那条路径在开发机上永远测不到（Windows 上实测就是这么翻的车）。
var (
	keychainRead  = readKey
	keychainWrite = writeKey
)

// loadOrCreateKey 取回密钥；没有就生成一个并落盘到平台钥匙串。
func loadOrCreateKey(dir string) (string, error) {
	if k, err := keychainRead(); err == nil && len(k) == 64 {
		return k, nil
	}

	// ★ 钥匙串不可用时，**先读本地的密钥文件**，别急着生成新密钥。
	// 少了这一步的后果（Windows 实测）：每次 Open 都生成一把新密钥并覆盖
	// session-db.key，而库文件是上一把密钥加密的 —— 于是重启后打不开，
	// 报 "file is not a database"，用户的会话历史全丢。
	if k, err := readKeyFile(dir); err == nil && len(k) == 64 {
		return k, nil
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成密钥失败: %w", err)
	}
	key := hex.EncodeToString(buf) // 64 位 hex，SQLCipher 的 raw key 格式

	if err := keychainWrite(key); err != nil {
		// 钥匙串不可用（无桌面会话、CI、容器、Windows）时退化成本地文件。
		// 这不是理想状态，但总比「应用起不来」好，而且文件是 0600。
		if ferr := writeKeyFile(dir, key); ferr != nil {
			return "", fmt.Errorf("保存密钥失败（钥匙串: %v；文件: %v）", err, ferr)
		}
	}
	return key, nil
}

// writeKeyFile 本地文件兜底，权限 0600。
func writeKeyFile(dir, key string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, "session-db.key")
	return os.WriteFile(path, []byte(key), 0o600)
}

// readKeyFile 读本地文件兜底。
func readKeyFile(dir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "session-db.key"))
	if err != nil {
		return "", err
	}
	if len(string(b)) != 64 {
		return "", errors.New("密钥文件内容不合法")
	}
	return string(b), nil
}
