package fsai

// 危险操作必须过「用户确认」这道门：没有令牌一步都不许执行。
//
// 这里同时盯住一件事：fsx.ConfirmPrefix 必须和 ai.ConfirmPrefix 逐字节相同。
// 界面的对话循环靠 ai.ConfirmPrefix 识别「这轮在等用户点确认」，而令牌本身
// 现在由 fs 包（能力核心，不带桌面依赖）发放。两边一旦不一致，删除会静默
// 失效 —— 用户点确认也没反应，或者更糟：不确认就删了。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klphp/excel-batch-tool/internal/ai"
	"github.com/klphp/fsx"
)

func TestConfirmPrefixMatchesAI(t *testing.T) {
	if fsx.ConfirmPrefix != ai.ConfirmPrefix {
		t.Fatalf("fsx.ConfirmPrefix(%q) 必须等于 ai.ConfirmPrefix(%q)：否则界面收不到确认请求",
			fsx.ConfirmPrefix, ai.ConfirmPrefix)
	}
}

func testGuard(t *testing.T) (fsx.Guard, string) {
	t.Helper()
	root := t.TempDir()
	return fsx.Guard{Roots: []string{root}}, root
}

func TestDeleteRequiresConfirmToken(t *testing.T) {
	reg := ai.NewRegistry()
	g, dir := testGuard(t)
	if err := RegisterWithGuard(reg, g); err != nil {
		t.Fatal(err)
	}

	// 造两个文件
	var paths []string
	for _, name := range []string{"a.csv", "b.csv"} {
		full := filepath.Join(dir, name)
		if err := os.WriteFile(full, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, full)
	}

	args, _ := json.Marshal(map[string]any{"paths": paths, "permanent": true})

	// ① 第一次：没有令牌 → 不执行，返回确认清单
	out, err := reg.Execute(context.Background(), "fs_delete", string(args))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, ai.ConfirmPrefix) {
		t.Fatalf("应该要求确认，实际返回: %s", out)
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("没有确认前文件不该被删: %s", p)
		}
	}

	// 清单里每一行都要是完整路径（用户靠路径判断是不是这几个）
	var cf struct {
		Token string   `json:"token"`
		Items []string `json:"items"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(out, ai.ConfirmPrefix)), &cf); err != nil {
		t.Fatal(err)
	}
	if len(cf.Items) != 2 {
		t.Fatalf("清单应有 2 项，实际 %+v", cf.Items)
	}
	for i, p := range paths {
		if !strings.Contains(cf.Items[i], p) {
			t.Fatalf("第 %d 项应包含完整路径 %s，实际 %q", i, p, cf.Items[i])
		}
	}
	if cf.Token == "" {
		t.Fatal("应返回令牌")
	}

	// ② 带令牌再调一次 → 真删（参数必须与确认时完全一致，令牌才认）
	args2, _ := json.Marshal(map[string]any{"paths": paths, "permanent": true, "confirmToken": cf.Token})
	if _, err := reg.Execute(context.Background(), "fs_delete", string(args2)); err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("确认后文件应被删除: %s", p)
		}
	}

	// ③ 同一个令牌不能用第二次
	args3, _ := json.Marshal(map[string]any{"paths": []string{filepath.Join(dir, "c.csv")}, "permanent": true, "confirmToken": cf.Token})
	out3, err := reg.Execute(context.Background(), "fs_delete", string(args3))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out3, ai.ConfirmPrefix) {
		t.Fatal("令牌用一次就该失效，不该直接执行")
	}
}

// 换一组参数（哪怕只是顺序不同）令牌也要失效。
func TestConfirmTokenBoundToArgs(t *testing.T) {
	reg := ai.NewRegistry()
	g, dir := testGuard(t)
	if err := RegisterWithGuard(reg, g); err != nil {
		t.Fatal(err)
	}

	f1 := filepath.Join(dir, "one.csv")
	f2 := filepath.Join(dir, "two.csv")
	for _, f := range []string{f1, f2} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	args, _ := json.Marshal(map[string]any{"paths": []string{f1}})
	out, _ := reg.Execute(context.Background(), "fs_delete", string(args))
	var cf struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal([]byte(strings.TrimPrefix(out, ai.ConfirmPrefix)), &cf)

	// 拿确认 f1 的令牌去删 f2 → 必须被拦
	args2, _ := json.Marshal(map[string]any{"paths": []string{f2}, "permanent": true, "confirmToken": cf.Token})
	out2, err := reg.Execute(context.Background(), "fs_delete", string(args2))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out2, ai.ConfirmPrefix) {
		t.Fatal("令牌绑定了参数，删别的文件必须重新确认")
	}
	if _, err := os.Stat(f2); err != nil {
		t.Fatal("f2 不该被删")
	}
}
