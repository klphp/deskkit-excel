package session

// 会话存储。
//
// 用 SQLCipher 加密的 SQLite：库文件整体密文（连表结构都看不见），
// 密钥由应用首次启动时随机生成、存进系统钥匙串，用户全程无感。
//
// ★ 两个必须记住的坑（都踩过）：
//   1. key 必须放进 DSN，不能只在打开后 Exec("PRAGMA key") ——
//      database/sql 会开新连接，新连接没 key，读到的就是乱码
//   2. 加密生效与否必须验证：明文 SQLite 的头是 "SQLite format 3"，
//      如果 PRAGMA key 被静默忽略，你会以为加密了其实没有

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mutecomm/go-sqlcipher/v4"
)

// Role 消息角色。
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

// Message 一条消息。
type Message struct {
	Role      Role   `json:"role"`
	Content   string `json:"content"`
	CreatedAt int64  `json:"createdAt"`
}

// Session 一个会话（含消息）。
type Session struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt int64     `json:"createdAt"`
	UpdatedAt int64     `json:"updatedAt"`
	Messages  []Message `json:"messages"`
}

// Summary 列表用的轻量信息，不带消息。
type Summary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	Count     int    `json:"count"`
}

// Store 会话库。
type Store struct {
	db   *sql.DB
	path string
}

// Open 打开（必要时创建）加密会话库。
func Open(dir string) (*Store, error) {
	key, err := loadOrCreateKey(dir)
	if err != nil {
		// 钥匙串和文件都失败时，退到最后一步：读钥匙串失败但文件里也许有
		if k2, ferr := readKeyFile(dir); ferr == nil {
			key = k2
		} else {
			return nil, err
		}
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %w", err)
	}
	path := filepath.Join(dir, "sessions.db")

	// key 放 DSN：每个新连接自动带上
	dsn := fmt.Sprintf("%s?_pragma_key=x'%s'&_pragma_cipher_page_size=4096", path, key)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开会话库失败: %w", err)
	}
	db.SetMaxOpenConns(1) // 桌面场景并发极低，单连接最简单也最稳

	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close 关闭。
func (s *Store) Close() error { return s.db.Close() }

// Path 库文件路径（测试与排查用）。
func (s *Store) Path() string { return s.path }

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, id)`,
		// 库自身的设置（保留天数等）。单独一张表而不是塞 JSON 文件：
		// 少一个要跟用户目录同步的东西，也不会出现「配置说 7 天、库按 30 天清」。
		`CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("建表失败: %w", err)
		}
	}

	// 单位归一（幂等）：早期版本导入过毫秒时间戳，把它们改回秒。
	for _, q := range []string{
		`UPDATE sessions SET created_at = created_at/1000 WHERE created_at > 100000000000`,
		`UPDATE sessions SET updated_at = updated_at/1000 WHERE updated_at > 100000000000`,
		`UPDATE messages SET created_at = created_at/1000 WHERE created_at > 100000000000`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("时间戳归一失败: %w", err)
		}
	}
	return nil
}

// Create 新建会话。已存在则返回错误（id 由前端生成，重复说明有 bug）。
func (s *Store) Create(id, title string) error {
	if id == "" {
		return errors.New("会话 id 不能为空")
	}
	now := time.Now().Unix()
	_, err := s.db.Exec(
		`INSERT INTO sessions (id, title, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		id, title, now, now)
	if err != nil {
		return fmt.Errorf("新建会话失败: %w", err)
	}
	return nil
}

// Upsert 不存在就建，存在就更新标题 —— 前端「发送第一条消息时自动起标题」会用到。
func (s *Store) Upsert(id, title string) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(
		`INSERT INTO sessions (id, title, created_at, updated_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET title = excluded.title, updated_at = excluded.updated_at`,
		id, title, now, now)
	if err != nil {
		return fmt.Errorf("写入会话失败: %w", err)
	}
	return nil
}

// Append 追加一条消息。
//
// 一条一条写而不是整段重写：崩溃时已写入的都在，只有最后一条可能丢。
func (s *Store) Append(sessionID string, role Role, content string) error {
	if sessionID == "" {
		return errors.New("会话 id 不能为空")
	}
	now := time.Now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`INSERT INTO messages (session_id, role, content, created_at) VALUES (?, ?, ?, ?)`,
		sessionID, string(role), content, now); err != nil {
		return fmt.Errorf("写入消息失败: %w", err)
	}
	// 会话可能还没建（前端先发消息后建会话），这里补一条
	if _, err := tx.Exec(
		`INSERT INTO sessions (id, title, created_at, updated_at) VALUES (?, '', ?, ?)
		 ON CONFLICT(id) DO UPDATE SET updated_at = excluded.updated_at`,
		sessionID, now, now); err != nil {
		return fmt.Errorf("更新会话时间失败: %w", err)
	}
	return tx.Commit()
}

// List 会话列表，按最近更新排。
func (s *Store) List() ([]Summary, error) {
	rows, err := s.db.Query(`
		SELECT s.id, s.title, s.created_at, s.updated_at,
		       (SELECT COUNT(*) FROM messages m WHERE m.session_id = s.id)
		FROM sessions s ORDER BY s.updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("读取会话列表失败: %w", err)
	}
	defer rows.Close()

	out := make([]Summary, 0, 16)
	for rows.Next() {
		var it Summary
		if err := rows.Scan(&it.ID, &it.Title, &it.CreatedAt, &it.UpdatedAt, &it.Count); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// Load 读一个会话的完整消息。
func (s *Store) Load(id string) (Session, error) {
	var out Session
	err := s.db.QueryRow(
		`SELECT id, title, created_at, updated_at FROM sessions WHERE id = ?`, id).
		Scan(&out.ID, &out.Title, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return out, fmt.Errorf("会话不存在: %s", id)
	}
	if err != nil {
		return out, err
	}

	rows, err := s.db.Query(
		`SELECT role, content, created_at FROM messages WHERE session_id = ? ORDER BY id`, id)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var m Message
		var role string
		if err := rows.Scan(&role, &m.Content, &m.CreatedAt); err != nil {
			return out, err
		}
		m.Role = Role(role)
		out.Messages = append(out.Messages, m)
	}
	return out, rows.Err()
}

// Rename 改标题。
func (s *Store) Rename(id, title string) error {
	_, err := s.db.Exec(`UPDATE sessions SET title = ?, updated_at = ? WHERE id = ?`,
		title, time.Now().Unix(), id)
	return err
}

// Delete 删会话及其消息。
func (s *Store) Delete(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM messages WHERE session_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// Import 批量导入（把旧版 localStorage 里的历史搬进来）。
//
// 已经存在的会话跳过，不覆盖 —— 用户可能在导入后又继续聊过。
func (s *Store) Import(items []Session) (int, error) {
	n := 0
	for _, it := range items {
		if it.ID == "" {
			continue
		}
		var exists int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = ?`, it.ID).Scan(&exists); err != nil {
			return n, err
		}
		if exists > 0 {
			continue
		}
		created := normalizeTS(it.CreatedAt)
		if created == 0 {
			created = time.Now().Unix()
		}
		updated := normalizeTS(it.UpdatedAt)
		if updated == 0 {
			updated = created
		}
		title := it.Title
		if title == "" {
			for _, m := range it.Messages {
				if m.Role == RoleUser {
					title = truncate(m.Content, 24)
					break
				}
			}
		}
		if _, err := s.db.Exec(
			`INSERT INTO sessions (id, title, created_at, updated_at) VALUES (?, ?, ?, ?)`,
			it.ID, title, created, updated); err != nil {
			return n, err
		}
		for _, m := range it.Messages {
			ts := normalizeTS(m.CreatedAt)
			if ts == 0 {
				ts = created
			}
			if _, err := s.db.Exec(
				`INSERT INTO messages (session_id, role, content, created_at) VALUES (?, ?, ?, ?)`,
				it.ID, string(m.Role), m.Content, ts); err != nil {
				return n, err
			}
		}
		n++
	}
	return n, nil
}

// normalizeTS 把时间戳统一到秒。
//
// 前端历史版本存的是毫秒（Date.now()），本包写的是秒。两种混在一起时
// 按 updated_at DESC 排序，毫秒值（约 1.79e12）永远压过秒值（约 1.79e9），
// 那条老会话会永远钉在列表第一 —— 实测踩到过。
func normalizeTS(ts int64) int64 {
	if ts > 1e11 { // 毫秒（约公元 5138 年之前都成立）
		return ts / 1000
	}
	return ts
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
