package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/klphp/excel-batch-tool/internal/config"
)

// newTestServer 起一个只测路由的实例（不监听端口）。
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	s := New(config.Server{})
	// registerRoutes 已经把路由挂在 s.http.Handler 上
	ts := httptest.NewServer(s.http.Handler)
	t.Cleanup(ts.Close)
	return ts
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("请求失败 %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// 接口清单：外部 agent 靠它发现本机有哪些能力。
func TestCapabilitiesEndpoint(t *testing.T) {
	ts := newTestServer(t)
	code, body := get(t, ts.URL+"/api/v1/capabilities")
	if code != http.StatusOK {
		t.Fatalf("状态码 %d: %s", code, body)
	}

	var out struct {
		Capabilities []struct {
			ID        string   `json:"id"`
			Name      string   `json:"name"`
			Bundled   bool     `json:"bundled"`
			ToolCount int      `json:"toolCount"`
			Tools     []string `json:"tools"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("返回不是合法 JSON: %v", err)
	}
	if len(out.Capabilities) < 2 {
		t.Fatalf("应至少有表格与文件操作两个能力包，实际 %d", len(out.Capabilities))
	}

	byID := map[string]bool{}
	for _, c := range out.Capabilities {
		byID[c.ID] = true
		if c.ToolCount == 0 || c.ToolCount != len(c.Tools) {
			t.Errorf("%s 的工具数不对: count=%d tools=%v", c.ID, c.ToolCount, c.Tools)
		}
		if !c.Bundled {
			t.Errorf("%s 是纯 Go 实现，应标为内置", c.ID)
		}
	}
	if !byID["excel"] || !byID["files"] {
		t.Fatalf("缺少能力包: %v", byID)
	}
}

// 用法说明必须真的列出工具和参数 —— 它是模型唯一的说明书。
func TestCapabilityUsageMarkdown(t *testing.T) {
	ts := newTestServer(t)
	code, body := get(t, ts.URL+"/api/v1/capabilities/excel/usage?format=md")
	if code != http.StatusOK {
		t.Fatalf("状态码 %d: %s", code, body)
	}
	for _, want := range []string{"inspect_file", "query_file", "write_file_result", "clean_file", "参数", "调用示例"} {
		if !strings.Contains(body, want) {
			t.Errorf("用法说明里缺少 %q", want)
		}
	}
	// 必须有 JSON Schema（模型据此知道字段类型）
	if !strings.Contains(body, "\"path\"") || !strings.Contains(body, "\"type\"") {
		t.Error("用法里缺少参数 schema")
	}
}

func TestCapabilityUsageJSON(t *testing.T) {
	ts := newTestServer(t)
	code, body := get(t, ts.URL+"/api/v1/capabilities/files/usage")
	if code != http.StatusOK {
		t.Fatalf("状态码 %d", code)
	}
	var doc struct {
		ID    string `json:"id"`
		Tools []struct {
			Name       string          `json:"name"`
			Parameters json.RawMessage `json:"parameters"`
			Example    string          `json:"example"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("JSON 不合法: %v", err)
	}
	if doc.ID != "files" {
		t.Errorf("id 不对: %s", doc.ID)
	}

	found := false
	for _, tl := range doc.Tools {
		if tl.Name == "fs_delete" {
			found = true
			if len(tl.Parameters) == 0 {
				t.Error("fs_delete 没有参数定义")
			}
			// 删除默认进废纸篓这个语义必须写进说明，否则模型会顺手传 permanent
			if !strings.Contains(tl.Example, "paths") {
				t.Errorf("示例里应体现 paths: %q", tl.Example)
			}
		}
	}
	if !found {
		t.Error("没有找到 fs_delete")
	}
}

// 一次请求拿到整本说明书（可以整段塞进提示词）。
func TestUsageAllEndpoint(t *testing.T) {
	ts := newTestServer(t)
	code, body := get(t, ts.URL+"/api/v1/usage")
	if code != http.StatusOK {
		t.Fatalf("状态码 %d", code)
	}
	for _, want := range []string{"# DeskKit 能力说明", "表格处理", "文件操作", "fs_list", "inspect_file"} {
		if !strings.Contains(body, want) {
			t.Errorf("整本说明里缺少 %q", want)
		}
	}
}

// OpenAI tools 形态的清单：外部 agent 直接拿去用。
func TestToolsJSON(t *testing.T) {
	ts := newTestServer(t)
	code, body := get(t, ts.URL+"/api/v1/tools.json")
	if code != http.StatusOK {
		t.Fatalf("状态码 %d", code)
	}
	var out struct {
		App          string `json:"app"`
		Capabilities []struct {
			ID    string `json:"id"`
			Tools []struct {
				Name       string          `json:"name"`
				Parameters json.RawMessage `json:"parameters"`
			} `json:"tools"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("JSON 不合法: %v", err)
	}
	if out.App == "" {
		t.Error("缺少 app 字段")
	}
	total := 0
	for _, c := range out.Capabilities {
		for _, tl := range c.Tools {
			total++
			if tl.Name == "" {
				t.Errorf("%s 里有工具没有名字", c.ID)
			}
			var schema map[string]any
			if err := json.Unmarshal(tl.Parameters, &schema); err != nil {
				t.Errorf("%s 的参数不是合法 JSON: %v", tl.Name, err)
				continue
			}
			if schema["type"] != "object" {
				t.Errorf("%s 的参数 schema 应为 object，实际 %v", tl.Name, schema["type"])
			}
		}
	}
	if total < 10 {
		t.Errorf("工具总数太少（%d），接口没铺开", total)
	}
}

func TestUnknownCapabilityReturns404(t *testing.T) {
	ts := newTestServer(t)
	code, _ := get(t, ts.URL+"/api/v1/capabilities/不存在/usage")
	if code != http.StatusNotFound {
		t.Errorf("未知能力包应返回 404，实际 %d", code)
	}
}

func TestHealthz(t *testing.T) {
	ts := newTestServer(t)
	code, body := get(t, ts.URL+"/healthz")
	if code != http.StatusOK || !strings.Contains(body, "ok") {
		t.Fatalf("健康检查异常: %d %s", code, body)
	}
}
