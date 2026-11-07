package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/klphp/deskkit/internal/config"
)

func testClient(t *testing.T, baseURL string, attempts int) *Client {
	t.Helper()
	return NewClient("test", config.Provider{
		BaseURL: baseURL,
		Model:   "test-model",
		Timeout: 5 * time.Second,
		Retry:   config.Retry{MaxAttempts: attempts, Backoff: time.Millisecond},
	}, "sk-test")
}

func chatHandler(t *testing.T, body string, status int) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("意外路径: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization 头不对: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	}
}

func TestChatParsesResponse(t *testing.T) {
	srv := httptest.NewServer(chatHandler(t, `{
		"model":"test-model",
		"choices":[{"message":{"content":"你好"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":11,"completion_tokens":3,"total_tokens":14}
	}`, 200))
	defer srv.Close()

	resp, e := testClient(t, srv.URL, 1).Chat(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if e != nil {
		t.Fatalf("不该出错: %v", e)
	}
	if resp.Content != "你好" || resp.FinishReason != "stop" {
		t.Fatalf("解析不对: %+v", resp)
	}
	if resp.Usage.PromptTokens != 11 || resp.Usage.CompletionTokens != 3 {
		t.Fatalf("用量解析不对: %+v", resp.Usage)
	}
}

func TestChatClassifiesAuthError(t *testing.T) {
	srv := httptest.NewServer(chatHandler(t, `{"error":{"message":"invalid api key"}}`, 401))
	defer srv.Close()

	_, e := testClient(t, srv.URL, 1).Chat(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if e == nil {
		t.Fatal("401 应当报错")
	}
	if e.Kind != KindAuth {
		t.Fatalf("应归类为 auth，实际 %s", e.Kind)
	}
	if e.Hint == "" {
		t.Fatal("认证失败必须给 Hint，否则用户不知道怎么办")
	}
}

func TestChatRetriesOnServerError(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(500)
			io.WriteString(w, `{"error":{"message":"boom"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	resp, e := testClient(t, srv.URL, 3).Chat(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if e != nil {
		t.Fatalf("重试后应当成功: %v", e)
	}
	if calls != 2 {
		t.Fatalf("期望请求 2 次，实际 %d", calls)
	}
	if resp.Content != "ok" {
		t.Fatalf("内容不对: %q", resp.Content)
	}
}

// 4xx 不该重试：key 错了重试一百次还是错，只会让用户多等。
func TestChatDoesNotRetryOnBadRequest(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(400)
		io.WriteString(w, `{"error":{"message":"bad model"}}`)
	}))
	defer srv.Close()

	_, e := testClient(t, srv.URL, 3).Chat(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if e == nil {
		t.Fatal("400 应当报错")
	}
	if calls != 1 {
		t.Fatalf("400 不应重试，实际请求 %d 次", calls)
	}
	if e.Kind != KindConfig {
		t.Fatalf("应归类为 config，实际 %s", e.Kind)
	}
}

func TestChatRequiresModel(t *testing.T) {
	c := NewClient("x", config.Provider{BaseURL: "http://127.0.0.1:1"}, "")
	_, e := c.Chat(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	if e == nil || e.Kind != KindConfig {
		t.Fatalf("缺模型名应报 config 错误，实际 %+v", e)
	}
}

// 流式：文本累加 + 工具调用分片拼接。
func TestChatStreamAccumulatesContentAndToolCalls(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"合并"}}]}`,
		``,
		`data: {"choices":[{"delta":{"content":"完成"}}]}`,
		``,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"echo","arguments":""}}]}}]}`,
		``,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"x\":"}}]}}]}`,
		``,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]}}]}`,
		``,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sse)
	}))
	defer srv.Close()

	var got strings.Builder
	resp, e := testClient(t, srv.URL, 1).ChatStream(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	}, func(d Delta) error {
		got.WriteString(d.Content)
		return nil
	})
	if e != nil {
		t.Fatalf("流式出错: %v", e)
	}
	if resp.Content != "合并完成" {
		t.Fatalf("聚合内容不对: %q", resp.Content)
	}
	if got.String() != "合并完成" {
		t.Fatalf("增量回调不对: %q", got.String())
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("期望 1 个工具调用，实际 %d", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call_1" || tc.Function.Name != "echo" {
		t.Fatalf("工具调用元信息不对: %+v", tc)
	}
	// 参数是分三片到的，必须拼成完整 JSON
	if tc.Function.Arguments != `{"x":1}` {
		t.Fatalf("参数拼接不对: %q", tc.Function.Arguments)
	}
	if resp.FinishReason != "tool_calls" {
		t.Fatalf("finish_reason 不对: %q", resp.FinishReason)
	}
}

func TestEndpointNormalization(t *testing.T) {
	cases := map[string]string{
		"https://api.deepseek.com/v1": "https://api.deepseek.com/v1/chat/completions",
		"https://api.openai.com":      "https://api.openai.com/v1/chat/completions",
		"http://127.0.0.1:11434":      "http://127.0.0.1:11434/v1/chat/completions",
		"http://x/v1/":                "http://x/v1/chat/completions",
		"http://x/chat/completions":   "http://x/chat/completions",
	}
	for in, want := range cases {
		c := NewClient("t", config.Provider{BaseURL: in}, "")
		if got := c.endpoint(); got != want {
			t.Errorf("baseURL %q → %q，期望 %q", in, got, want)
		}
	}
}

// 上游把错误放在 error 对象里，要能挖出 message 给人看。
func TestErrorExtractsUpstreamMessage(t *testing.T) {
	e := classify(400, []byte(`{"error":{"message":"model not found: foo","type":"invalid_request_error"}}`))
	if !strings.Contains(e.Message, "model not found") {
		t.Fatalf("没挖出上游原文: %q", e.Message)
	}
	// 字符串形态也要能处理
	e2 := classify(500, []byte(`{"error":"internal boom"}`))
	if e2.Message != "internal boom" {
		t.Fatalf("字符串错误体没解析: %q", e2.Message)
	}
	// 完全不是 JSON 时至少保留片段
	e3 := classify(502, []byte("<html>bad gateway</html>"))
	if !strings.Contains(e3.Raw, "bad gateway") {
		t.Fatalf("非 JSON 错误体应保留原文: %q", e3.Raw)
	}
}

func TestBackoffGrowsAndCaps(t *testing.T) {
	if d := backoff(100*time.Millisecond, 1); d != 100*time.Millisecond {
		t.Fatalf("第 1 次退避应等于基数，实际 %v", d)
	}
	if d := backoff(100*time.Millisecond, 3); d != 400*time.Millisecond {
		t.Fatalf("第 3 次退避应为 400ms，实际 %v", d)
	}
	if d := backoff(5*time.Second, 10); d != 8*time.Second {
		t.Fatalf("退避应封顶 8s，实际 %v", d)
	}
}

func TestChatStreamJSONShape(t *testing.T) {
	// 保证 stream 字段确实被发出去（有些服务商缺这个字段会当非流式处理）
	var sawStream bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(raw, &req)
		sawStream = req["stream"] == true
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	_, e := testClient(t, srv.URL, 1).ChatStream(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	}, nil)
	if e != nil {
		t.Fatalf("出错: %v", e)
	}
	if !sawStream {
		t.Fatal("请求里 stream 必须为 true")
	}
}
