package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/klphp/deskkit/internal/config"
)

// Client 一个服务商的对话客户端。
//
// 两个 http 层细节值得记下来：
//
//  1. 不用 http.Client.Timeout。它统计的是「包括读完 body」的总时长，
//     流式响应会被它拦腰截断 —— 表现为「回复到一半突然超时」。
//     改为每次请求自己用 context.WithTimeout 控制，流式则交给调用方的 ctx。
//  2. ResponseHeaderTimeout 单独设。它只约束「等响应头」这一段，
//     正好能抓住「连上了但对端不说话」这种最常见的卡死。
type Client struct {
	name    string
	kind    string
	baseURL string
	model   string
	apiKey  string
	headers map[string]string
	retry   config.Retry
	timeout time.Duration
	http    *http.Client
}

// NewClient 从配置构造。apiKey 由调用方解析（环境变量或用户设置）。
func NewClient(name string, p config.Provider, apiKey string) *Client {
	kind := p.Kind
	if kind == "" {
		kind = "openai"
	}
	timeout := p.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return &Client{
		name:    name,
		kind:    kind,
		baseURL: strings.TrimRight(strings.TrimSpace(p.BaseURL), "/"),
		model:   p.Model,
		apiKey:  strings.TrimSpace(apiKey),
		headers: p.Headers,
		retry:   p.Retry,
		timeout: timeout,
		http: &http.Client{
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DialContext:           dialer.DialContext,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 60 * time.Second,
				ForceAttemptHTTP2:     true,
				MaxIdleConnsPerHost:   4,
			},
		},
	}
}

// Name 服务商名（配置里的 key 或 local@地址）。
func (c *Client) Name() string { return c.name }

// Model 默认模型。
func (c *Client) Model() string { return c.model }

// endpoint 拼出对话接口地址。
//
// 用户填的 BaseURL 有三种常见形态，都要能接住：
//
//	https://api.deepseek.com/v1        → /v1/chat/completions
//	https://api.openai.com             → /v1/chat/completions（自动补 /v1）
//	http://127.0.0.1:11434/v1          → /v1/chat/completions
//	.../chat/completions               → 原样用（用户写全了）
func (c *Client) endpoint() string {
	base := c.baseURL
	switch {
	case strings.HasSuffix(base, "/chat/completions"):
		return base
	case strings.HasSuffix(base, "/v1"):
		return base + "/chat/completions"
	default:
		return base + "/v1/chat/completions"
	}
}

// Chat 非流式对话。
func (c *Client) Chat(ctx context.Context, req Request) (*Response, *Error) {
	prepared, e := c.prepare(req)
	if e != nil {
		return nil, e
	}
	body, err := json.Marshal(prepared)
	if err != nil {
		return nil, &Error{Kind: KindConfig, Message: "请求序列化失败: " + err.Error()}
	}

	var last *Error
	for attempt := 0; attempt < c.attempts(); attempt++ {
		if attempt > 0 {
			if err := sleepCtx(ctx, backoff(c.retry.Backoff, attempt)); err != nil {
				return nil, fromTransport(err)
			}
		}
		resp, e := c.once(ctx, body)
		if e == nil {
			return resp, nil
		}
		last = e
		if !retryable(e) {
			return nil, e
		}
	}
	return nil, last
}

// ChatStream 流式对话。onDelta 每收到一段文本调用一次，可为 nil。
//
// 流式不重试：一旦有内容已经吐给用户，重发会得到重复文本，体验更差。
// 连接建立前的失败会重试（那时还没有任何输出）。
func (c *Client) ChatStream(ctx context.Context, req Request, onDelta func(Delta) error) (*Response, *Error) {
	prepared, e := c.prepare(req)
	if e != nil {
		return nil, e
	}
	prepared.Stream = true
	body, err := json.Marshal(prepared)
	if err != nil {
		return nil, &Error{Kind: KindConfig, Message: "请求序列化失败: " + err.Error()}
	}

	var last *Error
	for attempt := 0; attempt < c.attempts(); attempt++ {
		if attempt > 0 {
			if err := sleepCtx(ctx, backoff(c.retry.Backoff, attempt)); err != nil {
				return nil, fromTransport(err)
			}
		}
		resp, e, sent := c.streamOnce(ctx, body, onDelta)
		if e == nil {
			return resp, nil
		}
		last = e
		if sent || !retryable(e) {
			// 已经开始收数据，或者错误本身不该重试
			return nil, e
		}
	}
	return nil, last
}

// Models 列举可用模型。
func (c *Client) Models(ctx context.Context) ([]string, *Error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	url := c.modelsEndpoint()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, &Error{Kind: KindConfig, Message: err.Error()}
	}
	c.applyHeaders(httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fromTransport(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, classify(resp.StatusCode, raw)
	}

	// OpenAI 兼容形态：{"data":[{"id":"gpt-4o"}]}
	var wire struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		// Ollama 原生 /api/tags：{"models":[{"name":"llama3"}]}
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, &Error{Kind: KindUpstream, Message: "模型列表不是合法 JSON", Raw: clip(string(raw), 200)}
	}
	out := make([]string, 0, len(wire.Data)+len(wire.Models))
	for _, d := range wire.Data {
		if d.ID != "" {
			out = append(out, d.ID)
		}
	}
	for _, m := range wire.Models {
		if m.Name != "" {
			out = append(out, m.Name)
		}
	}
	return out, nil
}

func (c *Client) modelsEndpoint() string {
	base := c.baseURL
	if strings.HasSuffix(base, "/v1") {
		return base + "/models"
	}
	if c.kind == "ollama" && !strings.HasSuffix(base, "/v1") {
		return base + "/api/tags"
	}
	return base + "/v1/models"
}

// prepare 补齐请求默认值并做前置校验。
func (c *Client) prepare(req Request) (Request, *Error) {
	if req.Model == "" {
		req.Model = c.model
	}
	if req.Model == "" {
		return req, &Error{
			Kind:    KindConfig,
			Message: "没有指定模型",
			Hint:    "在设置里填写模型名（例如 gpt-4o-mini、deepseek-chat、qwen-plus）。",
		}
	}
	if len(req.Messages) == 0 {
		return req, &Error{Kind: KindConfig, Message: "消息为空"}
	}
	req.Stream = false
	return req, nil
}

func (c *Client) once(ctx context.Context, body []byte) (*Response, *Error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	httpReq, err := c.newRequest(ctx, body)
	if err != nil {
		return nil, &Error{Kind: KindConfig, Message: err.Error()}
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fromTransport(err)
	}
	defer resp.Body.Close()

	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, classify(resp.StatusCode, raw)
	}
	if readErr != nil {
		return nil, fromTransport(readErr)
	}

	var wire wireResponse
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, &Error{Kind: KindUpstream, Message: "响应不是合法 JSON", Raw: clip(string(raw), 300)}
	}
	return wire.toResponse(), nil
}

// streamOnce 发一次流式请求。sent 表示「是否已经开始把内容交给调用方」——
// 用它决定还能不能重试。
func (c *Client) streamOnce(ctx context.Context, body []byte, onDelta func(Delta) error) (*Response, *Error, bool) {
	httpReq, err := c.newRequest(ctx, body)
	if err != nil {
		return nil, &Error{Kind: KindConfig, Message: err.Error()}, false
	}
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fromTransport(err), false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, classify(resp.StatusCode, raw), false
	}

	sent := false
	out, e := consumeSSE(resp.Body, func(d Delta) error {
		sent = true
		if onDelta != nil {
			return onDelta(d)
		}
		return nil
	})
	if e != nil {
		return nil, e, sent
	}
	return out, nil, sent
}

func (c *Client) newRequest(ctx context.Context, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	c.applyHeaders(req)
	return req, nil
}

func (c *Client) applyHeaders(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
}

func (c *Client) attempts() int {
	if c.retry.MaxAttempts <= 0 {
		return 1
	}
	return c.retry.MaxAttempts
}

// ── 响应形态 ──────────────────────────────────────────────

type wireResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
}

func (w wireResponse) toResponse() *Response {
	out := &Response{Model: w.Model, Usage: w.Usage}
	if len(w.Choices) > 0 {
		out.Content = w.Choices[0].Message.Content
		out.ToolCalls = w.Choices[0].Message.ToolCalls
		out.FinishReason = w.Choices[0].FinishReason
	}
	return out
}

// streamChunk 一个 SSE 分片。
//
// Index 用指针：协议里它是可选字段，缺省时表示「第 0 个工具调用」，
// 而「缺省」和「显式写 0」在分片语义上不等价，用值类型会丢这个区别。
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    *int   `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage `json:"usage"`
}

// consumeSSE 解析 SSE 流并聚合成完整响应。
//
// 工具调用是**分片到达**的：第一片带 id 与 name，后续片只带 arguments 片段。
// 所以按 index 累积、最后按首次出现顺序输出 —— 顺序错了，多工具调用的
// 结果会串到别的调用上。
func consumeSSE(r io.Reader, onDelta func(Delta) error) (*Response, *Error) {
	out := &Response{}
	calls := map[int]*ToolCall{}
	var order []int
	var text strings.Builder

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4<<20) // 单行上限 4MB，长文本分片够用

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue // 空行是分片分隔符；冒号开头是注释（心跳）
		}
		payload, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue // event: / id: 这类字段用不上
		}
		payload = strings.TrimSpace(payload)
		if payload == "[DONE]" {
			break
		}

		var chunk streamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return nil, &Error{Kind: KindStream, Message: "流式分片不是合法 JSON", Raw: clip(payload, 200)}
		}
		if chunk.Usage != nil {
			out.Usage = *chunk.Usage
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				text.WriteString(choice.Delta.Content)
				if err := onDelta(Delta{Content: choice.Delta.Content}); err != nil {
					return nil, fromTransport(err)
				}
			}
			for _, tc := range choice.Delta.ToolCalls {
				idx := 0
				if tc.Index != nil {
					idx = *tc.Index
				}
				cur, seen := calls[idx]
				if !seen {
					cur = &ToolCall{Type: "function"}
					calls[idx] = cur
					order = append(order, idx)
				}
				if tc.ID != "" {
					cur.ID = tc.ID
				}
				if tc.Type != "" {
					cur.Type = tc.Type
				}
				cur.Function.Name += tc.Function.Name
				cur.Function.Arguments += tc.Function.Arguments
			}
			if choice.FinishReason != "" {
				out.FinishReason = choice.FinishReason
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fromTransport(err)
	}

	out.Content = text.String()
	for _, i := range order {
		out.ToolCalls = append(out.ToolCalls, *calls[i])
	}
	return out, nil
}

// ── 小工具 ────────────────────────────────────────────────

func backoff(base time.Duration, attempt int) time.Duration {
	if base <= 0 {
		base = 500 * time.Millisecond
	}
	d := base
	for i := 1; i < attempt; i++ {
		d *= 2
		if d > 8*time.Second {
			return 8 * time.Second
		}
	}
	return d
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
