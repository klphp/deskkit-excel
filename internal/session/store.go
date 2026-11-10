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
	// ID 自增主键，前端当分页游标用（"加载更早"时把它传回来）。
	ID        int64  `json:"id"`
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
		return nil, fmt.Errorf("Failed to create data directory: %w", err)
	}
	path := filepath.Join(dir, "sessions.db")

	// key 放 DSN：每个新连接自动带上
	dsn := fmt.Sprintf("%s?_pragma_key=x'%s'&_pragma_cipher_page_size=4096", path, key)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("Failed to open the session store: %w", err)
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
			return fmt.Errorf("Failed to create tables: %w", err)
		}
	}

	// seq 是「最近被操作的顺序」：updated_at 只有秒精度，同一秒里动了两个会话
	// 就分不出先后，排序退化成插入顺序 —— 表现就是「刚聊过的会话没排在最前」。
	if err := s.ensureColumn("sessions", "seq", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE sessions SET seq = rowid WHERE seq = 0`); err != nil {
		return fmt.Errorf("Failed to backfill seq: %w", err)
	}

	// 老库补列（幂等）：instructions 是后加的，老库的 sessions 表没有这一列。
	if err := s.ensureColumn("sessions", "instructions", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}

	// 单位归一（幂等）：早期版本导入过毫秒时间戳，把它们改回秒。
	for _, q := range []string{
		`UPDATE sessions SET created_at = created_at/1000 WHERE created_at > 100000000000`,
		`UPDATE sessions SET updated_at = updated_at/1000 WHERE updated_at > 100000000000`,
		`UPDATE messages SET created_at = created_at/1000 WHERE created_at > 100000000000`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("Failed to normalize timestamps: %w", err)
		}
	}
	return nil
}

// Create 新建会话。已存在则返回错误（id 由前端生成，重复说明有 bug）。
func (s *Store) Create(id, title string) error {
	if id == "" {
		return errors.New("Session id must not be empty")
	}
	now := time.Now().Unix()
	_, err := s.db.Exec(
		`INSERT INTO sessions (id, title, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		id, title, now, now)
	if err != nil {
		return fmt.Errorf("Failed to create session: %w", err)
	}
	return nil
}

// Upsert 不存在就建，存在就更新标题 —— 前端「发送第一条消息时自动起标题」会用到。
func (s *Store) Upsert(id, title string) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(
		`INSERT INTO sessions (id, title, created_at, updated_at, seq)
		 VALUES (?, ?, ?, ?, (SELECT COALESCE(MAX(seq), 0) + 1 FROM sessions))
		 ON CONFLICT(id) DO UPDATE SET title = excluded.title,
		        updated_at = excluded.updated_at, seq = excluded.seq`,
		id, title, now, now)
	if err != nil {
		return fmt.Errorf("Failed to write session: %w", err)
	}
	return nil
}

// Append 追加一条消息。
//
// 一条一条写而不是整段重写：崩溃时已写入的都在，只有最后一条可能丢。
func (s *Store) Append(sessionID string, role Role, content string) error {
	if sessionID == "" {
		return errors.New("Session id must not be empty")
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
		return fmt.Errorf("Failed to write message: %w", err)
	}
	// 会话可能还没建（前端先发消息后建会话），这里补一条
	if _, err := tx.Exec(
		`INSERT INTO sessions (id, title, created_at, updated_at, seq)
		 VALUES (?, '', ?, ?, (SELECT COALESCE(MAX(seq), 0) + 1 FROM sessions))
		 ON CONFLICT(id) DO UPDATE SET updated_at = excluded.updated_at, seq = excluded.seq`,
		sessionID, now, now); err != nil {
		return fmt.Errorf("Failed to update session time: %w", err)
	}
	return tx.Commit()
}

// List 会话列表，按最近更新排（不设上限，仅供内部/测试用）。
func (s *Store) List() ([]Summary, error) {
	items, _, err := s.ListPage(0, 0)
	return items, err
}

// ListPage 分页读会话列表。limit<=0 表示不限。
//
// 为什么要分页：会话是只增不删的，用一年就是几千条。一次全查出来前端要
// 渲染几千个 DOM 节点，进设置页再返回都得顿一下 —— 而用户真正看的永远是
// 最近那几十条。
func (s *Store) ListPage(limit, offset int) ([]Summary, bool, error) {
	q := `
		SELECT s.id, s.title, s.created_at, s.updated_at,
		       (SELECT COUNT(*) FROM messages m WHERE m.session_id = s.id)
		FROM sessions s ORDER BY s.updated_at DESC, s.seq DESC`
	args := []any{}
	if limit > 0 {
		// 多取一条用来判断还有没有下一页
		q += ` LIMIT ? OFFSET ?`
		args = append(args, limit+1, offset)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, false, fmt.Errorf("Failed to read the session list: %w", err)
	}
	defer rows.Close()

	out := make([]Summary, 0, 32)
	for rows.Next() {
		var it Summary
		if err := rows.Scan(&it.ID, &it.Title, &it.CreatedAt, &it.UpdatedAt, &it.Count); err != nil {
			return nil, false, err
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	hasMore := false
	if limit > 0 && len(out) > limit {
		out = out[:limit]
		hasMore = true
	}
	return out, hasMore, nil
}

// Load 读一个会话的完整消息（会话很长时不建议用，见 MessagesPage）。
func (s *Store) Load(id string) (Session, error) {
	var out Session
	err := s.db.QueryRow(
		`SELECT id, title, created_at, updated_at FROM sessions WHERE id = ?`, id).
		Scan(&out.ID, &out.Title, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return out, fmt.Errorf("Session does not exist: %s", id)
	}
	if err != nil {
		return out, err
	}

	msgs, _, err := s.MessagesPage(id, 0, 0)
	if err != nil {
		return out, err
	}
	out.Messages = msgs
	return out, nil
}

// MessagesPage 读一个会话的最近若干条消息（游标分页）。
//
// beforeID = 0 表示「最新的一页」；否则取 id 小于它的更早一页。
// 返回的 hasMore 表示还有没有更早的。
//
// 为什么按「最近」而不是「从头」：打开会话时用户要看的是最后在聊什么。
// 一个聊了几百轮的会话，从头加载既慢又白费 —— 他要翻回去时再往上翻。
func (s *Store) MessagesPage(sessionID string, limit int, beforeID int64) ([]Message, bool, error) {
	if limit <= 0 {
		limit = 100
	}

	var (
		rows *sql.Rows
		err  error
	)
	if beforeID > 0 {
		rows, err = s.db.Query(`
			SELECT id, role, content, created_at FROM messages
			WHERE session_id = ? AND id < ?
			ORDER BY id DESC LIMIT ?`, sessionID, beforeID, limit+1)
	} else {
		rows, err = s.db.Query(`
			SELECT id, role, content, created_at FROM messages
			WHERE session_id = ?
			ORDER BY id DESC LIMIT ?`, sessionID, limit+1)
	}
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var desc []Message
	for rows.Next() {
		var m Message
		var role string
		if err := rows.Scan(&m.ID, &role, &m.Content, &m.CreatedAt); err != nil {
			return nil, false, err
		}
		m.Role = Role(role)
		desc = append(desc, m)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	hasMore := false
	if len(desc) > limit {
		desc = desc[:limit]
		hasMore = true
	}
	// 倒序取出来的，翻回正序给界面
	out := make([]Message, len(desc))
	for i, m := range desc {
		out[len(desc)-1-i] = m
	}
	return out, hasMore, nil
}

// Rename 改标题。
func (s *Store) Rename(id, title string) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET title = ?, updated_at = ?,
		        seq = (SELECT COALESCE(MAX(seq), 0) + 1 FROM sessions) WHERE id = ?`,
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

// ensureColumn 补列（幂等）。SQLite 没有 ADD COLUMN IF NOT EXISTS，
// 靠 pragma_table_info 先看一眼。
func (s *Store) ensureColumn(table, column, decl string) error {
	rows, err := s.db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return fmt.Errorf("Failed to read table schema: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		if name == column {
			return nil // 已经有了
		}
	}
	if _, err := s.db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, decl)); err != nil {
		return fmt.Errorf("Failed to add column %s.%s: %w", table, column, err)
	}
	return nil
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
