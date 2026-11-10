// Package prefs 存用户偏好：那些「我们猜不出来、只有用户自己知道」的设置。
//
// 目前只有一项：自定义指令。用户会写「处理后的文件存到 D:\处理结果\，
// 原文件别动」这类规则 —— 我们给十个目录选择器也猜不到他的真实习惯，
// 但让他写一句话就解决了。
package prefs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// fileName 存在用户数据目录，不进安装目录也不进 git。
const fileName = "prefs.json"

// Prefs 用户偏好。
type Prefs struct {
	// CustomInstructions 自定义指令，会拼到每轮系统提示里。
	CustomInstructions string `json:"customInstructions,omitempty"`
}

// MaxInstructionsLen 上限。太长的指令会挤占上下文，也说明用户该拆成几条。
const MaxInstructionsLen = 2000

// Store 偏好的读写门面。
type Store struct {
	dir string
}

// NewStore dir 是用户数据目录（应用启动时算好的那个）。
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

func (s *Store) path() string {
	return filepath.Join(s.dir, fileName)
}

// Load 读偏好。文件不存在、坏掉、权限不足都返回零值 ——
// 偏好读不出来不该让应用起不来，更不该覆盖用户已有内容。
func (s *Store) Load() Prefs {
	raw, err := os.ReadFile(s.path())
	if err != nil {
		return Prefs{}
	}
	var p Prefs
	if err := json.Unmarshal(raw, &p); err != nil {
		return Prefs{}
	}
	return p
}

// Save 写偏好。0600：内容可能包含用户本机的目录结构，不必给同机其他用户看。
func (s *Store) Save(p Prefs) error {
	if len([]rune(p.CustomInstructions)) > MaxInstructionsLen {
		return fmt.Errorf("Custom instructions are too long (limit is %d characters)", MaxInstructionsLen)
	}
	p.CustomInstructions = strings.TrimSpace(p.CustomInstructions)

	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("Failed to create directory: %w", err)
	}
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path() + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("Failed to write preferences: %w", err)
	}
	// 先写临时文件再改名：写一半被打断不会留下半个坏文件
	return os.Rename(tmp, s.path())
}

// Instructions 只取自定义指令这一项（调用方多数只关心它）。
func (s *Store) Instructions() string {
	return s.Load().CustomInstructions
}
