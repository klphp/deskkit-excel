package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Settings 用户运行时设置（BYOK）。
//
// 为什么不放 config/app.yaml：那份配置会被打包进二进制、也会进 git。
// 用户的密钥只能落在本机用户目录，权限 0600。
//
// 只存「当前选中的服务商」这一组。多服务商的密钥同时留着会带来
// 「哪一个在生效」的歧义，而用户实际只用一个。
type Settings struct {
	Provider          string `json:"provider,omitempty"`
	BaseURL           string `json:"baseURL,omitempty"`
	Model             string `json:"model,omitempty"`
	APIKey            string `json:"apiKey,omitempty"`
	DisableLocalProbe bool   `json:"disableLocalProbe,omitempty"`
}

// settingsFile 设置文件名（用户配置目录下）。
const settingsFile = "ai.json"

// LoadSettings 读取设置。文件不存在不算错误 —— 首次运行的正常状态。
func LoadSettings(dir string) (Settings, error) {
	var s Settings
	if dir == "" {
		return s, nil
	}
	raw, err := os.ReadFile(filepath.Join(dir, settingsFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return s, err
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		// 文件坏了就当没有，别让应用起不来。用户重填一次即可。
		return Settings{}, fmt.Errorf("ai.json 解析失败（已忽略）: %w", err)
	}
	return s, nil
}

// SaveSettings 写入设置，权限 0600。
//
// 先写临时文件再 rename：直接覆盖时进程被杀会留下半截 JSON，
// 下次启动就变成「设置莫名丢失」。rename 在同一文件系统内是原子的。
func SaveSettings(dir string, s Settings) error {
	if dir == "" {
		return errors.New("配置目录为空")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	final := filepath.Join(dir, settingsFile)
	tmp := final + ".tmp"
	// 先以 0600 创建，避免短暂出现一个 0644 的文件
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(raw); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	// 老文件可能是更宽松的权限，rename 会保留旧 inode 的权限，
	// 所以这里对最终文件显式再 chmod 一次。
	if err := os.Chmod(tmp, 0o600); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, final)
}
