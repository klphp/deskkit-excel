package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// 加密必须真的生效：明文 SQLite 的头是 "SQLite format 3"。
// 这条测试是本包最重要的一个 —— 加密悄无声息地失效时，别的测试全都照过。
func TestDatabaseIsActuallyEncrypted(t *testing.T) {
	dir := tempDir(t)
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("打开失败: %v", err)
	}
	if err := s.Upsert("s1", "测试会话"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if err := s.Append("s1", RoleUser, "机密内容 secret-payload"); err != nil {
		t.Fatalf("追加失败: %v", err)
	}
	path := s.Path()
	s.Close()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读库文件失败: %v", err)
	}
	if strings.HasPrefix(string(raw), "SQLite format 3") {
		t.Fatal("数据库是明文的 —— PRAGMA key 被忽略了")
	}
	// 明文内容不能出现在文件里
	if strings.Contains(string(raw), "secret-payload") {
		t.Fatal("文件里能搜到明文消息内容")
	}
}

// 用错密钥必须打不开。
func TestWrongKeyCannotOpen(t *testing.T) {
	dir := tempDir(t)
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("打开失败: %v", err)
	}
	s.Upsert("s1", "x")
	path := s.Path()
	s.Close()

	bad := path + "?_pragma_key=x'" + strings.Repeat("ab", 32) + "'&_pragma_cipher_page_size=4096"
	db, err := openRaw(bad)
	if err != nil {
		t.Fatalf("打开失败: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n); err == nil {
		t.Fatal("错误密钥不该能读出来")
	}
}

// 重启后（重新 Open）历史还在 —— 密钥从钥匙串/文件取回。
func TestReopenKeepsHistory(t *testing.T) {
	dir := tempDir(t)
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("打开失败: %v", err)
	}
	if err := s.Append("s1", RoleUser, "第一句"); err != nil {
		t.Fatalf("追加失败: %v", err)
	}
	if err := s.Append("s1", RoleAssistant, "第二句"); err != nil {
		t.Fatalf("追加失败: %v", err)
	}
	s.Close()

	again, err := Open(dir)
	if err != nil {
		t.Fatalf("重开失败: %v", err)
	}
	defer again.Close()

	got, err := again.Load("s1")
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("应有 2 条消息，实际 %d", len(got.Messages))
	}
	if got.Messages[0].Content != "第一句" || got.Messages[1].Role != RoleAssistant {
		t.Fatalf("消息内容不对: %+v", got.Messages)
	}
}

func TestAppendCreatesSessionAndBumpsTime(t *testing.T) {
	dir := tempDir(t)
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("打开失败: %v", err)
	}
	defer s.Close()

	// 前端可能先发消息后建会话，Append 要能兜住
	if err := s.Append("new-one", RoleUser, "你好"); err != nil {
		t.Fatalf("追加失败: %v", err)
	}
	list, err := s.List()
	if err != nil {
		t.Fatalf("列表失败: %v", err)
	}
	if len(list) != 1 || list[0].ID != "new-one" || list[0].Count != 1 {
		t.Fatalf("列表不对: %+v", list)
	}
}

func TestListOrdersByRecent(t *testing.T) {
	dir := tempDir(t)
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("打开失败: %v", err)
	}
	defer s.Close()

	s.Upsert("a", "旧")
	s.Upsert("b", "新")
	// 再动一次 a，让它变成最近
	if err := s.Append("a", RoleUser, "让 a 变新"); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List()
	if len(list) != 2 || list[0].ID != "a" {
		t.Fatalf("应把最近更新的排前面，实际 %+v", list)
	}
}

func TestRenameAndDelete(t *testing.T) {
	dir := tempDir(t)
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("打开失败: %v", err)
	}
	defer s.Close()

	s.Append("x", RoleUser, "hi")
	if err := s.Rename("x", "改过的标题"); err != nil {
		t.Fatalf("改名失败: %v", err)
	}
	got, _ := s.Load("x")
	if got.Title != "改过的标题" {
		t.Fatalf("标题没改: %q", got.Title)
	}

	if err := s.Delete("x"); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, err := s.Load("x"); err == nil {
		t.Fatal("删除后不该还能读到")
	}
	list, _ := s.List()
	if len(list) != 0 {
		t.Fatalf("删除后列表应为空: %+v", list)
	}
}

func TestLoadMissingSession(t *testing.T) {
	dir := tempDir(t)
	s, _ := Open(dir)
	defer s.Close()
	if _, err := s.Load("nope"); err == nil {
		t.Fatal("不存在的会话应报错")
	}
}

// 迁移：把旧 localStorage 的历史搬进来，重复导入不重复插入。
func TestImportFromLocalStorage(t *testing.T) {
	dir := tempDir(t)
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("打开失败: %v", err)
	}
	defer s.Close()

	items := []Session{
		{ID: "old1", CreatedAt: 1700000000, UpdatedAt: 1700000100, Messages: []Message{
			{Role: RoleUser, Content: "以前问的第一句，这段要变成标题"},
			{Role: RoleAssistant, Content: "以前的回答"},
		}},
		{ID: "old2", Title: "已有标题", Messages: []Message{{Role: RoleUser, Content: "x"}}},
	}
	n, err := s.Import(items)
	if err != nil {
		t.Fatalf("导入失败: %v", err)
	}
	if n != 2 {
		t.Fatalf("应导入 2 个，实际 %d", n)
	}

	// 重复导入不新增
	n2, _ := s.Import(items)
	if n2 != 0 {
		t.Fatalf("重复导入应跳过，实际 %d", n2)
	}

	got, err := s.Load("old1")
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if !strings.HasPrefix(got.Title, "以前问的第一句") {
		t.Fatalf("标题应从第一条用户消息生成，实际 %q", got.Title)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("消息数不对: %d", len(got.Messages))
	}
	if it, _ := s.Load("old2"); it.Title != "已有标题" {
		t.Fatalf("原有标题不该被覆盖: %q", it.Title)
	}
}

// 密钥文件兜底路径：钥匙串不可用时（CI / 无桌面会话）仍要能工作。
func TestKeyFileFallback(t *testing.T) {
	dir := tempDir(t)
	if err := writeKeyFile(dir, strings.Repeat("cd", 32)); err != nil {
		t.Fatalf("写密钥文件失败: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "session-db.key"))
	if err != nil {
		t.Fatalf("密钥文件不存在: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("密钥文件权限应为 0600，实际 %o", perm)
	}
	if k, err := readKeyFile(dir); err != nil || len(k) != 64 {
		t.Fatalf("读回失败: %q %v", k, err)
	}
}

// 毫秒时间戳导入后必须归一成秒，否则那条会话会永远钉在列表最前面。
func TestImportNormalizesMilliseconds(t *testing.T) {
	dir := tempDir(t)
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("打开失败: %v", err)
	}
	defer s.Close()

	// 毫秒值（老前端用 Date.now()）
	n, err := s.Import([]Session{
		{ID: "legacy", CreatedAt: 1700000000000, UpdatedAt: 1700000000000,
			Messages: []Message{{Role: RoleUser, Content: "老会话"}}},
	})
	if err != nil || n != 1 {
		t.Fatalf("导入失败: %v n=%d", err, n)
	}
	// 再建一条新的（秒）
	if err := s.Append("fresh", RoleUser, "新会话"); err != nil {
		t.Fatal(err)
	}

	list, _ := s.List()
	if len(list) != 2 {
		t.Fatalf("应有 2 条: %+v", list)
	}
	if list[0].ID != "fresh" {
		t.Fatalf("最新会话应排第一，实际 %q（时间戳没归一？%d）", list[0].ID, list[0].UpdatedAt)
	}
	for _, it := range list {
		if it.UpdatedAt > 1e11 {
			t.Fatalf("%s 的时间戳还是毫秒: %d", it.ID, it.UpdatedAt)
		}
	}
}
