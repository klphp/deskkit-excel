package ai

import "encoding/json"

// Role 是消息角色。
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Message 一条对话消息（OpenAI 兼容形态）。
//
// ToolCalls 只在 assistant 消息上有值；ToolCallID 只在 tool 消息上有值
// （把工具执行结果回灌给模型时必须带上它，否则模型不知道这是对哪次调用的答复）。
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// Tool 是暴露给模型的工具定义。
type Tool struct {
	Type     string      `json:"type"` // 目前只有 "function"
	Function FunctionDef `json:"function"`
}

// FunctionDef 工具的函数签名。
//
// Parameters 用 json.RawMessage 而不是 map：JSON Schema 里允许嵌套 anyOf、
// required、enum 这些结构，逐层建模既啰嗦又容易丢字段。
type FunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ToolCall 模型发起的一次工具调用。
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall 工具名与参数（参数是 JSON 字符串，不是对象 —— 这是协议规定的）。
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Usage token 用量。部分服务商流式返回时不提供，此时为零值。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Request 一次对话请求。
//
// Model 留空时用客户端默认模型；Temperature 用指针，是为了区分
// 「没设」和「显式设为 0」—— 0 是合法且有意义的取值。
type Request struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []Tool    `json:"tools,omitempty"`
	Temperature *float64  `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Stream      bool      `json:"stream,omitempty"`
}

// Response 一次对话的结果（流式结束后也会聚合成这个形态）。
type Response struct {
	Model     string     `json:"model"`
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"toolCalls,omitempty"`
	Usage     Usage      `json:"usage"`
	// FinishReason 常见值：stop / length / tool_calls。
	FinishReason string `json:"finishReason,omitempty"`
}

// Delta 流式增量。
//
// 工具调用是分片到达的：第一片带 id 与 name，后续片只带 arguments 的片段。
// 所以这里按 Index 交给聚合器拼，而不是假设一次到齐。
type Delta struct {
	Content   string     `json:"content,omitempty"`
	ToolCalls []ToolCall `json:"toolCalls,omitempty"`
	Index     int        `json:"index,omitempty"`
}
