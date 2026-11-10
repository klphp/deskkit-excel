package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
// Registry 工具表。destructive 记录需要用户确认才能执行的工具。
type Registry struct {
	mu   sync.RWMutex
	defs []Tool
	fns  map[string]ToolFunc
	// destructive 需要用户点确认才能执行的工具名。
	destructive map[string]bool
}

// NewRegistry 空注册表。
func NewRegistry() *Registry {
	return &Registry{fns: make(map[string]ToolFunc), destructive: make(map[string]bool)}
}

// Add 注册一个工具。
//
// 三处校验都是提前失败，而不是等模型调用时才炸：
// 名字为空、函数为 nil、Parameters 不是合法 JSON Schema。
// MarkDestructive 把某个工具标成「危险操作」：调用它之前必须有用户点过的确认令牌。
//
// 为什么不只在提示词里交代：提示词是约束，不是闸门。用户说一句「别问了直接删」，
// 模型很可能就真照做 —— 而那正是最不该出事的时候。这里要让它在机制上做不到。
func (r *Registry) MarkDestructive(name string) {
	if r.destructive == nil {
		r.destructive = make(map[string]bool)
	}
	r.destructive[name] = true
}

// IsDestructive 这个工具是否危险操作。
func (r *Registry) IsDestructive(name string) bool {
	return r.destructive != nil && r.destructive[name]
}

func (r *Registry) Add(def Tool, fn ToolFunc) error {
	if def.Function.Name == "" {
		return fmt.Errorf("Tool name is empty")
	}
	if fn == nil {
		return fmt.Errorf("Tool %s has no implementation", def.Function.Name)
	}
	if len(def.Function.Parameters) > 0 && !json.Valid(def.Function.Parameters) {
		return fmt.Errorf("Parameters for tool %s is not valid JSON", def.Function.Name)
	}
	if def.Type == "" {
		def.Type = "function"
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.fns[def.Function.Name]; dup {
		return fmt.Errorf("Tool %s is registered twice", def.Function.Name)
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
		return "", fmt.Errorf("Model asked to call an unregistered tool: %s", name)
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
		return Tool{}, fmt.Errorf("Failed to serialize Parameters for tool %s: %w", name, err)
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
	// Type: assistant / tool_call / tool_result / awaiting / done
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
	// EventAwaiting 表示「问了用户一个问题，正在等回答」。
	// 循环到此为止：模型已经停下，接下来的输入来自界面上的一次点击。
	EventAwaiting = "awaiting"
	// EventConfirm 危险操作待用户确认。载荷是 ai.ConfirmRequest 的 JSON。
	EventConfirm = "confirm"
	// EventDelta 流式增量：Text 是这一小段新增的文本。
	// 有了它用户能看着字一个个出来，而不是等整段生成完。
	EventDelta = "delta"
)

// AwaitPrefix 是 ask_user 工具返回结果的标记前缀。
//
// 为什么用前缀而不是新加一种返回类型：ToolFunc 的签名是
// func(ctx, args) (string, error)，全项目几十个工具都按这个写。
// 为一个工具改签名不值得；用一段几乎不可能自然出现的前缀标记，
// 循环层看到它就把回合交给用户。
const AwaitPrefix = "\x00await_user\x00"

// Loop 工具调用循环。
//
// 放 Go 侧而不是前端：同一套循环既能服务内置对话面板，也能直接服务
// MCP / HTTP 出口。放前端的话，加一个出口就要重写一遍调度。
type Loop struct {
	// stream 为真时用 ChatStream，边收边推 delta 事件。
	// 默认关：单元测试里的假服务器只实现了非流式响应，
	// 默认开会让所有现有测试都要跟着改，而收益只在真实调用时才有。
	stream    bool
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
// Streaming 打开或关闭流式输出。
//
// 开的收益：用户看着字一个个出来，长回答不再干等十几秒。
// 默认关是为了不惊动单元测试里的假服务器（它们只实现了非流式响应）。
func (l *Loop) Streaming(v bool) *Loop {
	l.stream = v
	return l
}

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
		return nil, &Error{Kind: KindConfig, Message: "AI client is not initialized"}
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

		var resp *Response
		var e *Error
		if l.stream {
			// 流式：每来一小段就推一个 delta，用户看到字在长
			var streamed bool
			resp, e = l.client.ChatStream(ctx, Request{Messages: history, Tools: tools}, func(d Delta) error {
				if d.Content == "" || emit == nil {
					return nil
				}
				streamed = true
				emit(Event{Type: EventDelta, Text: d.Content, Round: round})
				return nil
			})
			if e == nil && !streamed && resp != nil && resp.Content != "" && emit != nil {
				// 有些网关不支持流式，会一次性返回整段 —— 兜底推一次，别让界面空白
				emit(Event{Type: EventDelta, Text: resp.Content, Round: round})
			}
		} else {
			resp, e = l.client.Chat(ctx, Request{Messages: history, Tools: tools})
			if e == nil && resp != nil && resp.Content != "" && emit != nil {
				emit(Event{Type: EventAssistant, Text: resp.Content, Round: round})
			}
		}
		if e != nil {
			return last, e
		}
		last = resp
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
			// 危险操作没带用户确认令牌：不执行，把影响清单交给界面。
			if strings.HasPrefix(out, ConfirmPrefix) {
				if emit != nil {
					emit(Event{Type: EventConfirm, Text: strings.TrimPrefix(out, ConfirmPrefix), Tool: call.Function.Name, Round: round})
				}
				return last, nil
			}
			// ask_user 问了用户一个问题：把回合交给用户，不要自己替用户答。
			// 这里必须停 —— 继续下一轮的话，模型会在没有答案的情况下编一个。
			if strings.HasPrefix(out, AwaitPrefix) {
				if emit != nil {
					emit(Event{Type: EventAwaiting, Text: strings.TrimPrefix(out, AwaitPrefix), Tool: call.Function.Name, Round: round})
				}
				return last, nil
			}
			if err != nil {
				// 工具失败也回灌，让模型自己决定换个做法还是如实告诉用户
				out = "Error: " + err.Error()
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
		emit(Event{Type: EventDone, Text: "Reached the maximum number of tool rounds; stopping here.", Round: l.maxRounds})
	}
	return last, &Error{
		Kind:    KindUpstream,
		Message: fmt.Sprintf("Tool calls still not finished after %d rounds", l.maxRounds),
		Hint:    "The task may be too broad, or a tool keeps failing so the model keeps retrying.",
	}
}
