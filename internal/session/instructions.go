package session

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// 每个会话独立的自定义指令。
//
// 为什么按会话存而不是全局：一个会话可能在整理财务报表（结果要进 D:\报表\），
// 另一个在整理下载的视频（结果要进 D:\视频\）。全局一份指令必然互相打架，
// 而用户不会记得每次去改设置。

// MaxInstructions 单个会话的指令长度上限。太长挤占上下文，也说明该拆任务。
const MaxInstructions = 2000

// Instructions 读某个会话的自定义指令。
func (s *Store) Instructions(id string) (string, error) {
	var out string
	err := s.db.QueryRow(`SELECT COALESCE(instructions, '') FROM sessions WHERE id = ?`, id).Scan(&out)
	if errors.Is(err, sql.ErrNoRows) {
		// 会话还没建（新建会话的第一轮）→ 视为「没有指令」，不是错误
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("Failed to read session instructions: %w", err)
	}
	return out, nil
}

// SetInstructions 写某个会话的自定义指令。
func (s *Store) SetInstructions(id, text string) error {
	if len([]rune(text)) > MaxInstructions {
		return fmt.Errorf("Custom instructions are too long (limit is %d characters)", MaxInstructions)
	}
	// 会话不存在时顺手建一个：模型可能在第一轮就写入指令，
	// 而这时前端可能还没来得及 Upsert。
	//
	// 时间戳用 Go 算而不是 SQL 的 unixepoch()：那个函数要 SQLite 3.38+，
	// 我们链接的 SQLCipher 版本不一定有（实测报 no such function）。
	now := time.Now().Unix()
	if _, err := s.db.Exec(
		`INSERT INTO sessions(id, title, created_at, updated_at, instructions)
		 VALUES(?, '', ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET instructions = excluded.instructions`,
		id, now, now, text,
	); err != nil {
		return fmt.Errorf("Failed to save session instructions: %w", err)
	}
	return nil
}
