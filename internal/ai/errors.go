// Package ai AI 接入层。
//
// 只做三件事：把「任意 OpenAI 兼容端点」抽象成客户端、把错误翻译成人话、
// 把工具调用循环跑通。不做提示词工程、不做会话历史管理 —— 那是业务的事。
//
// 为什么统一走 OpenAI 兼容形态：OpenAI、DeepSeek、通义、Kimi、Groq 以及
// 本地 Ollama / LM Studio 都实现了 /chat/completions。为每家写适配器等于
// 给它们的字段差异做长期维护，收益远小于成本。
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
)

// Kind 错误类别。前端按它决定 UI：auth 引导去设置页，rate 提示稍后重试。
type Kind string

const (
	KindConfig   Kind = "config"    // 配置缺失（没填 key、没选模型）
	KindAuth     Kind = "auth"      // 401/403
	KindNotFound Kind = "not_found" // 404：地址错、模型不存在
	KindRate     Kind = "rate"      // 429
	KindNetwork  Kind = "network"   // 连不上、DNS、连接被重置
	KindTimeout  Kind = "timeout"   // 超时
	KindCanceled Kind = "canceled"  // 用户取消
	KindUpstream Kind = "upstream"  // 上游 5xx 或协议错误
	KindStream   Kind = "stream"    // 流式解析失败
)

// Error 是 AI 层的统一错误。
//
// 带 Hint 是刻意的：错误信息给开发者看，Hint 给用户看。
// 区分开之后，前端可以只展示 Hint，日志里保留 Message 与 Raw。
type Error struct {
	Kind    Kind   `json:"kind"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	Status  int    `json:"status,omitempty"`
	Raw     string `json:"raw,omitempty"`
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("ai: %s（HTTP %d）: %s", e.Kind, e.Status, e.Message)
	}
	return fmt.Sprintf("ai: %s: %s", e.Kind, e.Message)
}

// upstreamError 是各家 OpenAI 兼容服务的通用错误体。
//
//	{"error":{"message":"...","type":"...","code":"..."}}
//	{"error":"..."}          ← 少数实现直接给字符串
type upstreamError struct {
	Error json.RawMessage `json:"error"`
}

func classify(status int, body []byte) *Error {
	msg := extractMessage(body)
	if msg == "" {
		msg = strings.TrimSpace(string(body))
	}
	if len(msg) > 500 {
		msg = msg[:500] + "…"
	}

	e := &Error{Message: msg, Status: status, Raw: string(body)}

	switch {
	case status == 401 || status == 403:
		e.Kind = KindAuth
		e.Message = "Authentication failed"
		e.Hint = "Check that the API Key is correct and not expired."
	case status == 404:
		e.Kind = KindNotFound
		e.Hint = "Check that the Base URL includes /v1, and that the model name exists on this provider."
	case status == 429:
		e.Kind = KindRate
		e.Hint = "Rate limited; retry later or switch to another provider in Settings."
	case status == 400 || status == 422:
		e.Kind = KindConfig
		e.Hint = "Request rejected; usually a wrong model name, out-of-range parameters, or a model that does not support tool calls."
	case status >= 500:
		e.Kind = KindUpstream
		e.Hint = "Provider-side failure; usually just wait and retry."
	default:
		e.Kind = KindUpstream
	}
	return e
}

// extractMessage 尽量从各家五花八门的错误体里挖出一句能看的话。
func extractMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var wrap upstreamError
	if err := json.Unmarshal(body, &wrap); err != nil || len(wrap.Error) == 0 {
		return ""
	}
	// 字符串形态
	var s string
	if err := json.Unmarshal(wrap.Error, &s); err == nil {
		return strings.TrimSpace(s)
	}
	// 对象形态
	var obj struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	}
	if err := json.Unmarshal(wrap.Error, &obj); err == nil && obj.Message != "" {
		return strings.TrimSpace(obj.Message)
	}
	return ""
}

// fromTransport 把传输层错误归一。
//
// 顺序重要：context.Canceled 与 DeadlineExceeded 都必须先判，
// 否则会被当成普通网络错误，重试也救不回来，用户只看到「一直转圈」。
func fromTransport(err error) *Error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled):
		return &Error{Kind: KindCanceled, Message: "Canceled"}
	case errors.Is(err, context.DeadlineExceeded):
		return &Error{Kind: KindTimeout, Message: "Request timed out", Hint: "Network or provider is slow; you can raise the timeout in Settings."}
	}

	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return &Error{Kind: KindTimeout, Message: "Request timed out", Hint: "Network or provider is slow; you can raise the timeout in Settings."}
	}
	return &Error{Kind: KindNetwork, Message: err.Error(), Hint: "Check the network, any proxy, and whether the Base URL is reachable."}
}

// retryable 判断该不该重试。
//
// 4xx（除 408/429）一律不重试：key 错了重试一百次还是错的，
// 只会让用户多等几秒并产生无用请求。
func retryable(e *Error) bool {
	if e == nil {
		return false
	}
	switch e.Kind {
	case KindNetwork, KindTimeout, KindRate, KindUpstream:
		return true
	}
	return false
}
