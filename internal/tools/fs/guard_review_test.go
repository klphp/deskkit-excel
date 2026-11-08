package fs

// TASK-037 审查发现的安全类问题的回归测试：
//   P1-8 符号链接可以越过 Guard.Roots（实测能删到边界外）
//   P2-5 fs_delete 传空 paths 仍生成确认请求
//   P2-6 ~ 不展开，报错路径看不懂

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/klphp/excel-batch-tool/internal/ai"
)

func TestGuardRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("机密"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("这个系统建不了符号链接: %v", err)
	}

	g := Guard{Roots: []string{root}}
	// ① 软链本身指的目录在 roots 之外 → 必须拒绝
	if _, err := g.Check(link); err == nil {
		t.Fatal("符号链接指向 roots 之外，Check 竟然放行了")
	}
	// ② 穿过软链的文件同样要拒绝
	if _, err := g.Check(filepath.Join(link, "secret.txt")); err == nil {
		t.Fatal("穿过符号链接的路径被放行了")
	}
	// ③ 删除路径必须失败，外部文件不能被移走
	done, failed, err := g.Delete([]string{filepath.Join(link, "secret.txt")}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 0 || len(failed) != 1 {
		t.Fatalf("应当拒绝并报失败，实际 done=%v failed=%v", done, failed)
	}
	if _, err := os.Stat(filepath.Join(outside, "secret.txt")); err != nil {
		t.Fatalf("roots 之外的原始文件被删掉了: %v", err)
	}

	// ④ roots 内部的正常路径不能被误伤
	inside := filepath.Join(root, "ok.txt")
	if err := os.WriteFile(inside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Check(inside); err != nil {
		t.Fatalf("root 内的正常路径被拒了: %v", err)
	}
	// 还不存在的目标（新建文件）也要能通过
	if _, err := g.Check(filepath.Join(root, "sub", "new.txt")); err != nil {
		t.Fatalf("root 内尚不存在的目标被拒了: %v", err)
	}
}

func TestGuardExpandsHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("没有用户目录")
	}
	g := Guard{Roots: []string{home}}
	got, err := g.Check("~")
	if err != nil {
		t.Fatalf("~ 应展开成用户目录: %v", err)
	}
	if got != home {
		t.Fatalf("Check(~) = %q，期望 %q", got, home)
	}
	if _, err := g.Check("~/Desktop"); err != nil {
		t.Fatalf("~/Desktop 应被接受（不用真的存在）: %v", err)
	}
}

func TestDeleteToolRejectsEmptyPaths(t *testing.T) {
	r := ai.NewRegistry()
	if err := Register(r); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(context.Background(), "fs_delete", `{}`); err == nil {
		t.Fatal("空 paths 竟然生成了确认请求")
	}
}
