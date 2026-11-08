package session

import (
	"testing"
	"time"
)

func TestRetentionDefault(t *testing.T) {
	s, err := Open(tempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := s.RetentionDays(); got != DefaultRetentionDays {
		t.Fatalf("默认保留天数应为 %d，实际 %d", DefaultRetentionDays, got)
	}
}

func TestRetentionSetAndReject(t *testing.T) {
	s, err := Open(tempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for _, days := range []int{3, 7, 15, 30} {
		if err := s.SetRetentionDays(days); err != nil {
			t.Fatalf("设置 %d 天失败: %v", days, err)
		}
		if got := s.RetentionDays(); got != days {
			t.Fatalf("期望 %d，读回 %d", days, got)
		}
	}

	// 非法值必须被拒 —— 存进 0 会变成「每次启动清空历史」
	for _, bad := range []int{0, -1, 1000} {
		if err := s.SetRetentionDays(bad); err == nil {
			t.Fatalf("%d 应该被拒绝", bad)
		}
	}
	// 拒绝之后原值不变
	if got := s.RetentionDays(); got != 30 {
		t.Fatalf("非法设置不应覆盖原值，实际 %d", got)
	}
}

func TestPruneRemovesOnlyOldSessions(t *testing.T) {
	dir := tempDir(t)
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	old := time.Now().Add(-40 * 24 * time.Hour).Unix()
	recent := time.Now().Add(-2 * 24 * time.Hour).Unix()

	// 直接写库，构造「40 天前用过」和「2 天前用过」两种会话
	if _, err := s.db.Exec(
		`INSERT INTO sessions(id, title, created_at, updated_at) VALUES
		 ('old','很久以前',?,?), ('recent','刚聊过',?,?)`,
		old, old, recent, recent,
	); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"old", "recent"} {
		if _, err := s.db.Exec(
			`INSERT INTO messages(session_id, role, content, created_at) VALUES(?,?,?,?)`,
			id, "user", "内容", recent,
		); err != nil {
			t.Fatal(err)
		}
	}

	n, err := s.Prune(30)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("应删掉 1 条，实际 %d", n)
	}

	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "recent" {
		t.Fatalf("应只剩 recent，实际 %+v", list)
	}

	// 消息必须跟着走 —— 否则库只增不减，白清
	var orphans int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM messages WHERE session_id = 'old'`).Scan(&orphans); err != nil {
		t.Fatal(err)
	}
	if orphans != 0 {
		t.Fatalf("旧会话的消息没删掉，剩 %d 条", orphans)
	}
}

func TestPruneKeepsLongLivedSession(t *testing.T) {
	// 三个月前建、昨天还在聊的会话属于「正在用」，不能按创建时间清掉
	dir := tempDir(t)
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	created := time.Now().Add(-90 * 24 * time.Hour).Unix()
	updated := time.Now().Add(-24 * time.Hour).Unix()
	if _, err := s.db.Exec(
		`INSERT INTO sessions(id, title, created_at, updated_at) VALUES('live','老会话还在用',?,?)`,
		created, updated,
	); err != nil {
		t.Fatal(err)
	}

	n, err := s.Prune(7)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("不该删，实际删了 %d 条", n)
	}
}

func TestClearAll(t *testing.T) {
	s, err := Open(tempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for _, id := range []string{"a", "b", "c"} {
		if err := s.Append(id, RoleUser, "内容"); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.ClearAll()
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("应清掉 3 条会话，实际 %d", n)
	}
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("清空后还有 %d 条", len(list))
	}
	var msgs int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&msgs); err != nil {
		t.Fatal(err)
	}
	if msgs != 0 {
		t.Fatalf("消息没清干净，剩 %d 条", msgs)
	}
}
