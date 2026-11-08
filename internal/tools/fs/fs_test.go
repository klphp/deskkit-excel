package fs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testGuard(t *testing.T) (Guard, string) {
	t.Helper()
	root := t.TempDir()
	return Guard{Roots: []string{root}}, root
}

func TestGuardRejectsSystemDirs(t *testing.T) {
	g := DefaultGuard()
	for _, bad := range []string{"/System/Library/CoreServices", "/usr/bin", "/etc/hosts", "/dev/null"} {
		if _, err := g.Check(bad); err == nil {
			t.Errorf("不该放过系统路径: %s", bad)
		}
	}
}

func TestGuardRejectsOutsideRoots(t *testing.T) {
	g, root := testGuard(t)
	outside := filepath.Join(filepath.Dir(root), "别处.txt")
	if _, err := g.Check(outside); err == nil {
		t.Errorf("不该放过根目录之外的路径: %s", outside)
	}
	if _, err := g.Check(filepath.Join(root, "ok.txt")); err != nil {
		t.Errorf("根目录内的路径应通过: %v", err)
	}
}

func TestListRecursiveAndPattern(t *testing.T) {
	g, root := testGuard(t)
	ctx := context.Background()

	mustWrite(t, filepath.Join(root, "a.xlsx"), "a")
	mustWrite(t, filepath.Join(root, "b.txt"), "b")
	mustWrite(t, filepath.Join(root, "sub", "c.xlsx"), "c")
	mustWrite(t, filepath.Join(root, ".hidden", "d.xlsx"), "d")
	mustWrite(t, filepath.Join(root, "node_modules", "e.xlsx"), "e")

	// 不递归：只看到顶层
	flat, err := g.List(ctx, ListOptions{Path: root})
	if err != nil {
		t.Fatalf("遍历失败: %v", err)
	}
	if flat.Files != 2 {
		t.Errorf("顶层应有 2 个文件，实际 %d", flat.Files)
	}

	// 递归 + 只要 xlsx：应命中 a 与 sub/c；隐藏目录和 node_modules 被跳过
	got, err := g.List(ctx, ListOptions{Path: root, Recursive: true, Pattern: "*.xlsx"})
	if err != nil {
		t.Fatalf("遍历失败: %v", err)
	}
	names := map[string]bool{}
	for _, e := range got.Entries {
		names[e.Name] = true
	}
	if !names["a.xlsx"] || !names["c.xlsx"] {
		t.Errorf("应包含 a.xlsx 与 c.xlsx，实际 %v", names)
	}
	if names["d.xlsx"] || names["e.xlsx"] {
		t.Errorf("隐藏目录 / node_modules 应被跳过，实际 %v", names)
	}
}

func TestListCapsEntries(t *testing.T) {
	g, root := testGuard(t)
	for i := 0; i < 20; i++ {
		mustWrite(t, filepath.Join(root, "f"+string(rune('a'+i))+".txt"), "x")
	}
	res, err := g.List(context.Background(), ListOptions{Path: root, MaxEntries: 5})
	if err != nil {
		t.Fatalf("遍历失败: %v", err)
	}
	if len(res.Entries) != 5 || !res.Truncated {
		t.Fatalf("应截断到 5 条，实际 %d（truncated=%v）", len(res.Entries), res.Truncated)
	}
}

func TestMakeDirIdempotent(t *testing.T) {
	g, root := testGuard(t)
	target := filepath.Join(root, "a", "b", "c")
	if _, err := g.MakeDir(target); err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if _, err := g.MakeDir(target); err != nil {
		t.Fatalf("重复创建应视为成功: %v", err)
	}
	// 同名文件挡路时必须报错，不能假装成功
	blocker := filepath.Join(root, "x")
	mustWrite(t, blocker, "x")
	if _, err := g.MakeDir(blocker); err == nil {
		t.Error("目标是文件时应报错")
	}
}

func TestMoveRefusesOverwrite(t *testing.T) {
	g, root := testGuard(t)
	src := filepath.Join(root, "a.txt")
	dst := filepath.Join(root, "b.txt")
	mustWrite(t, src, "a")
	mustWrite(t, dst, "b")

	if _, err := g.Move(src, dst); err == nil {
		t.Fatal("目标已存在时不该覆盖")
	}
	if b := mustRead(t, dst); b != "b" {
		t.Errorf("目标文件被改动了: %q", b)
	}

	// 改名到不存在的路径应成功
	final := filepath.Join(root, "c.txt")
	if _, err := g.Move(src, final); err != nil {
		t.Fatalf("移动失败: %v", err)
	}
	if mustRead(t, final) != "a" {
		t.Error("内容不对")
	}
}

func TestCopyTree(t *testing.T) {
	g, root := testGuard(t)
	mustWrite(t, filepath.Join(root, "src", "a.txt"), "a")
	mustWrite(t, filepath.Join(root, "src", "nested", "b.txt"), "b")

	dst := filepath.Join(root, "dst")
	if _, err := g.Copy(filepath.Join(root, "src"), dst); err != nil {
		t.Fatalf("复制目录失败: %v", err)
	}
	if mustRead(t, filepath.Join(dst, "a.txt")) != "a" {
		t.Error("顶层文件没复制过去")
	}
	if mustRead(t, filepath.Join(dst, "nested", "b.txt")) != "b" {
		t.Error("子目录没复制过去")
	}
	if _, err := g.Copy(filepath.Join(root, "src"), dst); err == nil {
		t.Error("目标已存在时应拒绝")
	}
}

// 永久删除只在这个测试里用（临时目录），不碰用户废纸篓。
func TestDeletePermanent(t *testing.T) {
	g, root := testGuard(t)
	mustWrite(t, filepath.Join(root, "kill", "a.txt"), "a")

	done, failed, err := g.Delete([]string{filepath.Join(root, "kill")}, true)
	if err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if len(failed) != 0 || len(done) != 1 {
		t.Fatalf("应删除 1 项，实际 done=%d failed=%v", len(done), failed)
	}
	if _, err := os.Stat(filepath.Join(root, "kill")); !os.IsNotExist(err) {
		t.Error("目录应已被删除")
	}
}

func TestDeleteRefusesRootAndOutside(t *testing.T) {
	g, root := testGuard(t)
	done, failed, err := g.Delete([]string{"/", filepath.Dir(root)}, false)
	if err != nil {
		t.Fatalf("不该整体报错: %v", err)
	}
	if len(done) != 0 {
		t.Errorf("不该删除任何东西: %+v", done)
	}
	if len(failed) != 2 {
		t.Errorf("两个路径都应被拒绝，实际 %v", failed)
	}
}

func TestListRejectsFile(t *testing.T) {
	g, root := testGuard(t)
	f := filepath.Join(root, "a.txt")
	mustWrite(t, f, "a")
	if _, err := g.List(context.Background(), ListOptions{Path: f}); err == nil {
		t.Error("对文件调用遍历应报错")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读不到 %s: %v", path, err)
	}
	return strings.TrimSpace(string(b))
}
