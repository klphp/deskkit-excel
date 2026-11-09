package fs

// 工具注册层测试：确保 fs_read / fs_search 确实挂进了 AI 的工具表，
// 并且参数错误时返回错误而不是 panic（模型传错参数是常态）。

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klphp/excel-batch-tool/internal/ai"
)

func TestRegisterIncludesReadAndSearch(t *testing.T) {
	g, root := testGuard(t)
	reg := ai.NewRegistry()
	if err := RegisterWithGuard(reg, g); err != nil {
		t.Fatalf("注册失败: %v", err)
	}

	want := []string{"fs_list", "fs_peek", "fs_read", "fs_search", "fs_mkdir", "fs_move", "fs_copy", "fs_delete"}
	got := map[string]bool{}
	for _, def := range reg.Definitions() {
		got[def.Function.Name] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("工具 %s 没注册上（当前 %v）", name, keys(got))
		}
	}

	mustWrite(t, filepath.Join(root, "a.txt"), "第一行\n关键字在这里\n")
	mustWrite(t, filepath.Join(root, "b.txt"), "无关\n")

	// fs_read
	out, err := reg.Execute(context.Background(), "fs_read", `{"path":`+quote(filepath.Join(root, "a.txt"))+`,"limit":10}`)
	if err != nil {
		t.Fatalf("fs_read 执行失败: %v", err)
	}
	var read ReadResult
	if err := json.Unmarshal([]byte(out), &read); err != nil {
		t.Fatalf("fs_read 返回的不是合法 JSON: %v（%s）", err, out)
	}
	if read.TotalLines != 2 || len(read.Lines) != 2 {
		t.Errorf("fs_read 结果不对: %+v", read)
	}

	// fs_search
	out, err = reg.Execute(context.Background(), "fs_search", `{"path":`+quote(root)+`,"pattern":"关键字"}`)
	if err != nil {
		t.Fatalf("fs_search 执行失败: %v", err)
	}
	var sr SearchResult
	if err := json.Unmarshal([]byte(out), &sr); err != nil {
		t.Fatalf("fs_search 返回的不是合法 JSON: %v（%s）", err, out)
	}
	if len(sr.Matches) != 1 || sr.Matches[0].Line != 2 {
		t.Errorf("fs_search 结果不对: %+v", sr.Matches)
	}

	// 参数错误：非法 JSON、缺 pattern、越界路径 —— 都要返回错误，不许 panic
	bad := []struct{ tool, args string }{
		{"fs_read", `{"path":`},
		{"fs_read", `{"path":"/etc/hosts"}`},
		{"fs_search", `{"path":` + quote(root) + `}`},
		{"fs_search", `{"path":"/usr/bin","pattern":"x"}`},
	}
	for _, c := range bad {
		if _, err := reg.Execute(context.Background(), c.tool, c.args); err == nil {
			t.Errorf("%s 参数 %s 应该报错", c.tool, c.args)
		}
	}
}

func keys(m map[string]bool) string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return strings.Join(out, ",")
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
