package session

import (
	"context"
	"fmt"
	"time"
)

// 会话保留策略。
//
// 为什么需要：会话库只增不减 —— 每天聊几轮，一年下来几千条消息。
// 表大了查询变慢、库文件变大、备份变重，用户却根本不会回看半年前的问题。
// 所以按「最后一次使用时间」定期清理，保留期由用户在设置里选。
//
// 为什么把设置存在库自己身上（meta 表）而不是另一个配置文件：
// 它是这条数据的属性（保留多久），存在一处就不会出现「配置说 7 天、库里
// 却按 30 天清」这种不一致；也少一个要跟用户目录同步的文件。

// 允许的保留天数。界面只给这四档，不接受任意值 ——
// 用户手滑存进 0 或负数会变成「每次都清空历史」。
var allowedRetention = []int{3, 7, 15, 30}

// DefaultRetentionDays 默认保留 30 天。
const DefaultRetentionDays = 30

// RetentionDays 读当前保留天数。没设置过或值不合法时返回默认值。
func (s *Store) RetentionDays() int {
	var raw string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = 'retention_days'`).Scan(&raw)
	if err != nil {
		return DefaultRetentionDays
	}
	var days int
	if _, err := fmt.Sscanf(raw, "%d", &days); err != nil {
		return DefaultRetentionDays
	}
	for _, ok := range allowedRetention {
		if days == ok {
			return days
		}
	}
	return DefaultRetentionDays
}

// SetRetentionDays 设置保留天数。只接受 3 / 7 / 15 / 30。
func (s *Store) SetRetentionDays(days int) error {
	valid := false
	for _, ok := range allowedRetention {
		if days == ok {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("保留天数只支持 3 / 7 / 15 / 30，收到 %d", days)
	}
	_, err := s.db.Exec(
		`INSERT INTO meta(key, value) VALUES('retention_days', ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		fmt.Sprintf("%d", days),
	)
	if err != nil {
		return fmt.Errorf("保存保留天数失败: %w", err)
	}
	return nil
}

// Prune 删掉超过保留期的会话，返回删掉的会话数。
//
// 判定依据是 updated_at（最后一次使用），不是 created_at：
// 一个三个月前建、但昨天还在聊的会话属于「正在用」，不该清掉。
func (s *Store) Prune(days int) (int, error) {
	if days <= 0 {
		days = DefaultRetentionDays
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()

	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// 先数一遍：SQLite 的 RowsAffected 对 DELETE ... WHERE 是准的，
	// 但消息是第二次删除，会话数要在第一次拿到。
	res, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE updated_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("清理旧会话失败: %w", err)
	}
	n, _ := res.RowsAffected()

	// 消息按「会话已不存在」清 —— 比再算一次时间安全：
	// 万一历史上出现过孤儿消息，这一步顺手收干净。
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM messages WHERE session_id NOT IN (SELECT id FROM sessions)`); err != nil {
		return 0, fmt.Errorf("清理孤立消息失败: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(n), nil
}

// PruneByRetention 按当前保留设置清理，返回删掉的会话数。
func (s *Store) PruneByRetention() (int, error) {
	return s.Prune(s.RetentionDays())
}

// ClearAll 清空所有会话与消息，返回删掉的会话数。
//
// 不可恢复：这就是用户自己点的「清空」，不做软删除、不写回收站 ——
// 留一份「删了但还在」的副本反而让人以为数据还在。界面上必须先确认。
func (s *Store) ClearAll() (int, error) {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `DELETE FROM sessions`)
	if err != nil {
		return 0, fmt.Errorf("清空会话失败: %w", err)
	}
	n, _ := res.RowsAffected()
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages`); err != nil {
		return 0, fmt.Errorf("清空消息失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	// 空闲页回收：不 VACUUM 的话文件大小不降，用户会以为没清干净
	_, _ = s.db.ExecContext(ctx, `VACUUM`)
	return int(n), nil
}
