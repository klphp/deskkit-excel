package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// ToolFunc 工具实现。参数是模型给的原始 JSON，返回值会作为 tool 消息回灌给模型。
//
// 返回值用 string 而不是结构化数据，是因为模型最终看到的就是文本；
// 需要结构化就在实现里自己 marshal。这样调用方不必关心协议细节。
type ToolFunc func(ctx context.Context, args json.RawMessage) (string, error)

// Registry 工具注册表。
//
// 这是整个「AI 操作应用」的核心：UI 按钮和 AI 调的是同一批函数，
// 所以行为一致、可测试、可批量。AI 不应该去模拟点界面。
type Registry struct {
	mu   sync.RWMutex
	defs []Tool
	fns  map[string]ToolFunc
}

// NewRegistry 空注册表。
func NewRegistry() *Registry {
	return &Registry{fns: make(map[string]ToolFunc)}
}

// Add 注册一个工具。
//
// 三处校验都是提前失败，而不是等模型调用时才炸：
// 名字为空、函数为 nil、Parameters 不是合法 JSON Schema。
func (r *Registry) Add(def Tool, fn ToolFunc) error {
	if def.Function.Name == "" {
		return fmt.Errorf("工具名为空")
	}
	if fn == nil {
		return fmt.Errorf("工具 %s 没有实现函数", def.Function.Name)
	}
	if len(def.Function.Parameters) > 0 && !json.Valid(def.Function.Parameters) {
		return fmt.Errorf("工具 %s 的 Parameters 不是合法 JSON", def.Function.Name)
	}
	if def.Type == "" {
		def.Type = "function"
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.fns[def.Function.Name]; dup {
		return fmt.Errorf("工具 %s 重复注册", def.Function.Name)
	}
	r.defs = append(r.defs, def)
	r.fns[def.Function.Name] = fn
	return nil
}

// Definitions 供请求使用的工具定义列表。
func (r *Registry) Definitions() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.defs) == 0 {
		return nil
	}
	out := make([]Tool, len(r.defs))
	copy(out, r.defs)
	return out
}

// Len 已注册工具数。
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.defs)
}

// Execute 执行一次工具调用。
//
// 错误不往上抛给调用方终止循环，而是包装成文本回给模型 ——
// 模型看到「文件不存在」可能会换个路径重试，而直接中止就没机会了。
func (r *Registry) Execute(ctx context.Context, name, args string) (string, error) {
	r.mu.RLock()
	fn, ok := r.fns[name]
	r.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("模型要求调用未注册的工具: %s", name)
	}
	raw := json.RawMessage(args)
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	return fn(ctx, raw)
}

// FunctionTool 构造一个工具定义，省去手写 JSON Schema 字符串。
//
//	params := map[string]any{
//	    "type": "object",
//	    "properties": map[string]any{"path": map[string]any{"type": "string"}},
//	    "required": []string{"path"},
//	}
//	def, _ := ai.FunctionTool("read_sheet", "读取一个 xlsx 的表头与样例行", params)
func FunctionTool(name, description string, params any) (Tool, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return Tool{}, fmt.Errorf("工具 %s 的 Parameters 序列化失败: %w", name, err)
	}
	return Tool{
		Type: "function",
		Function: FunctionDef{
			Name:        name,
			Description: description,
			Parameters:  raw,
		},
	}, nil
}

// Event 循环过程中的事件，用于把进度推给前端。
type Event struct {
	// Type: assistant / tool_call / tool_result / done
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// Tool 与 Args 仅 tool_call 时有值；Result 与 Err 仅 tool_result 时有值。
	Tool   string `json:"tool,omitempty"`
	Args   string `json:"args,omitempty"`
	Result string `json:"result,omitempty"`
	Err    string `json:"error,omitempty"`
	Round  int    `json:"round,omitempty"`
}

// 事件类型。
const (
	EventAssistant  = "assistant"
	EventToolCall   = "tool_call"
	EventToolResult = "tool_result"
	EventDone       = "done"
)

// Loop 工具调用循环。
//
// 放 Go 侧而不是前端：同一套循环既能服务内置对话面板，也能直接服务
// MCP / HTTP 出口。放前端的话，加一个出口就要重写一遍调度。
type Loop struct {
	client    *Client
	registry  *Registry
	maxRounds int
}

// NewLoop 构造。maxRounds <= 0 时用 8 —— 足够处理「查表 → 汇总 → 汇报」
// 这类多步任务，又能挡住模型陷入无限工具调用的死循环。
func NewLoop(client *Client, registry *Registry) *Loop {
	return &Loop{client: client, registry: registry, maxRounds: 8}
}

// MaxRounds 设置最大轮次。
func (l *Loop) MaxRounds(n int) *Loop {
	if n > 0 {
		l.maxRounds = n
	}
	return l
}

// Run 跑完一次完整对话（含工具调用）。
//
// msgs 不会被修改（内部复制一份再追加），调用方可以放心复用历史。
func (l *Loop) Run(ctx context.Context, msgs []Message, emit func(Event)) (*Response, *Error) {
	if l.client == nil {
		return nil, &Error{Kind: KindConfig, Message: "AI 客户端未初始化"}
	}

	history := make([]Message, 0, len(msgs)+l.maxRounds*2)
	history = append(history, msgs...)

	var tools []Tool
	if l.registry != nil {
		tools = l.registry.Definitions()
	}

	var last *Response
	for round := 1; round <= l.maxRounds; round++ {
		if err := ctx.Err(); err != nil {
			return last, fromTransport(err)
		}

		resp, e := l.client.Chat(ctx, Request{Messages: history, Tools: tools})
		if e != nil {
			return last, e
		}
		last = resp

		if resp.Content != "" && emit != nil {
			emit(Event{Type: EventAssistant, Text: resp.Content, Round: round})
		}
		if len(resp.ToolCalls) == 0 {
			if emit != nil {
				emit(Event{Type: EventDone, Text: resp.Content, Round: round})
			}
			return resp, nil
		}

		// 把助手的工具调用意图记进历史，否则下一轮模型不知道自己刚才要干什么
		history = append(history, Message{
			Role:      RoleAssistant,
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		for _, call := range resp.ToolCalls {
			if emit != nil {
				emit(Event{Type: EventToolCall, Tool: call.Function.Name, Args: call.Function.Arguments, Round: round})
			}

			out, err := l.registry.Execute(ctx, call.Function.Name, call.Function.Arguments)
			if err != nil {
				// 工具失败也回灌，让模型自己决定换个做法还是如实告诉用户
				out = "错误: " + err.Error()
				if emit != nil {
					emit(Event{Type: EventToolResult, Tool: call.Function.Name, Err: err.Error(), Round: round})
				}
			} else if emit != nil {
				emit(Event{Type: EventToolResult, Tool: call.Function.Name, Result: out, Round: round})
			}

			history = append(history, Message{
				Role:       RoleTool,
				ToolCallID: call.ID,
				Name:       call.Function.Name,
				Content:    out,
			})
		}
	}

	if emit != nil {
		emit(Event{Type: EventDone, Text: "已达到最大工具调用轮次，先停在这里。", Round: l.maxRounds})
	}
	return last, &Error{
		Kind:    KindUpstream,
		Message: fmt.Sprintf("工具调用超过 %d 轮仍未结束", l.maxRounds),
		Hint:    "任务可能过于发散；或者工具一直报错导致模型反复重试。",
	}
}
