package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klphp/deskkit-excel/internal/config"
)

// 一个会先要工具、再给结论的假服务商。
func toolThenAnswerServer(t *testing.T, calls *int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req Request
		json.Unmarshal(raw, &req)

		n := atomic.AddInt32(calls, 1)
		w.Header().Set("Content-Type", "application/json")

		if n == 1 {
			// 第一阶段：要求调用工具，并断言我们确实把工具定义发出去了
			if len(req.Tools) == 0 {
				t.Error("第一轮请求应当带上 tools 定义")
			}
			io.WriteString(w, `{"choices":[{"message":{
				"content":"",
				"tool_calls":[{"id":"call_1","type":"function","function":{"name":"echo","arguments":"{\"text\":\"hi\"}"}}]
			},"finish_reason":"tool_calls"}]}`)
			return
		}

		// 第二阶段：必须能看到 tool 消息回灌，否则模型无从知道工具结果
		var sawTool bool
		for _, m := range req.Messages {
			if m.Role == RoleTool && m.ToolCallID == "call_1" {
				sawTool = true
			}
		}
		if !sawTool {
			t.Error("第二轮请求里应当有 role=tool 且带 tool_call_id 的消息")
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"已经处理完了"},"finish_reason":"stop"}]}`)
	}))
}

func TestLoopExecutesToolsThenReturns(t *testing.T) {
	var calls int32
	srv := toolThenAnswerServer(t, &calls)
	defer srv.Close()

	client := NewClient("test", config.Provider{
		BaseURL: srv.URL, Model: "m", Timeout: 5 * time.Second,
	}, "k")

	registry := NewRegistry()
	def, err := FunctionTool("echo", "回显一段文本", map[string]any{
		"type":       "object",
		"properties": map[string]any{"text": map[string]any{"type": "string"}},
		"required":   []string{"text"},
	})
	if err != nil {
		t.Fatalf("构造工具失败: %v", err)
	}

	var executed atomic.Int32
	if err := registry.Add(def, func(ctx context.Context, args json.RawMessage) (string, error) {
		executed.Add(1)
		var in struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
		return "echo:" + in.Text, nil
	}); err != nil {
		t.Fatalf("注册工具失败: %v", err)
	}

	var events []Event
	resp, e := NewLoop(client, registry).Run(context.Background(),
		[]Message{{Role: RoleUser, Content: "跑一下"}},
		func(ev Event) { events = append(events, ev) })

	if e != nil {
		t.Fatalf("循环出错: %v", e)
	}
	if resp.Content != "已经处理完了" {
		t.Fatalf("最终内容不对: %q", resp.Content)
	}
	if executed.Load() != 1 {
		t.Fatalf("工具应执行 1 次，实际 %d", executed.Load())
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("应当两轮请求，实际 %d", calls)
	}

	var sawCall, sawResult, sawDone bool
	for _, ev := range events {
		switch ev.Type {
		case EventToolCall:
			sawCall = true
		case EventToolResult:
			if ev.Result != "echo:hi" {
				t.Fatalf("工具结果不对: %q", ev.Result)
			}
			sawResult = true
		case EventDone:
			sawDone = true
		}
	}
	if !sawCall || !sawResult || !sawDone {
		t.Fatalf("事件不全: %+v", events)
	}
}

// 工具报错不能中断循环：错误回灌给模型，让它自己决定下一步。
func TestLoopToolErrorIsFedBack(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			io.WriteString(w, `{"choices":[{"message":{"tool_calls":[{"id":"c1","type":"function","function":{"name":"boom","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), "错误:") {
			t.Errorf("工具失败应以「错误:」前缀回灌，实际请求体: %s", string(raw))
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"那算了"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	client := NewClient("t", config.Provider{BaseURL: srv.URL, Model: "m", Timeout: 5 * time.Second}, "k")
	reg := NewRegistry()
	def, _ := FunctionTool("boom", "总是失败", map[string]any{"type": "object"})
	reg.Add(def, func(ctx context.Context, args json.RawMessage) (string, error) {
		return "", io.ErrUnexpectedEOF
	})

	resp, e := NewLoop(client, reg).Run(context.Background(),
		[]Message{{Role: RoleUser, Content: "go"}}, nil)
	if e != nil {
		t.Fatalf("工具失败不该终止循环: %v", e)
	}
	if resp.Content != "那算了" {
		t.Fatalf("应当走到最终回答，实际 %q", resp.Content)
	}
}

// 模型没完没了地要工具时，必须有轮次上限。
func TestLoopStopsAtMaxRounds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"tool_calls":[{"id":"c","type":"function","function":{"name":"again","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
	}))
	defer srv.Close()

	client := NewClient("t", config.Provider{BaseURL: srv.URL, Model: "m", Timeout: 5 * time.Second}, "k")
	reg := NewRegistry()
	def, _ := FunctionTool("again", "再要一次", map[string]any{"type": "object"})
	reg.Add(def, func(ctx context.Context, args json.RawMessage) (string, error) { return "ok", nil })

	_, e := NewLoop(client, reg).MaxRounds(3).Run(context.Background(),
		[]Message{{Role: RoleUser, Content: "go"}}, nil)
	if e == nil {
		t.Fatal("超过轮次上限应当报错")
	}
	if !strings.Contains(e.Message, "3") {
		t.Fatalf("错误信息应带上限值: %q", e.Message)
	}
}

func TestRegistryRejectsDuplicatesAndBadSchema(t *testing.T) {
	reg := NewRegistry()
	def, _ := FunctionTool("a", "d", map[string]any{"type": "object"})
	if err := reg.Add(def, func(context.Context, json.RawMessage) (string, error) { return "", nil }); err != nil {
		t.Fatalf("首次注册失败: %v", err)
	}
	if err := reg.Add(def, func(context.Context, json.RawMessage) (string, error) { return "", nil }); err == nil {
		t.Fatal("重复注册应当报错")
	}
	if err := reg.Add(Tool{Type: "function", Function: FunctionDef{
		Name:       "bad",
		Parameters: json.RawMessage("{不是 JSON"),
	}}, func(context.Context, json.RawMessage) (string, error) { return "", nil }); err == nil {
		t.Fatal("非法 JSON Schema 应当报错")
	}
	if err := reg.Add(Tool{Function: FunctionDef{Name: "nilfn"}}, nil); err == nil {
		t.Fatal("没有实现函数应当报错")
	}
}

func TestFunctionToolMarshalsParams(t *testing.T) {
	def, err := FunctionTool("x", "d", map[string]any{"type": "object"})
	if err != nil {
		t.Fatalf("失败: %v", err)
	}
	if def.Type != "function" || def.Function.Name != "x" {
		t.Fatalf("定义不对: %+v", def)
	}
	if !json.Valid(def.Function.Parameters) {
		t.Fatalf("Parameters 不是合法 JSON: %s", def.Function.Parameters)
	}
}
