package services

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/klphp/excel-batch-tool/internal/ai"
)

// AI 相关事件名。前端 wails.Events.On(...) 订阅。
//
// 流式用事件而不是让 ChatStream 返回一大块文本：用户要看到字逐渐出现，
// 而不是等十秒后一次蹦出来。事件也是唯一能在流式过程中推进度的通道。
const (
	EventAIDelta = "ai:delta" // {content}
	EventAIDone  = "ai:done"  // {content, usage, finishReason}
	EventAIError = "ai:error" // {kind, message, hint}
	EventAITool  = "ai:tool"  // ai.Event（tool_call / tool_result）
	// EventAIAsk 模型向用户提问，等界面上的一次点击。
	// 载荷 {question, options[], detail}：前端渲染成按钮，点了就把那一条
	// 当作新的用户消息发回去。
	EventAIAsk = "ai:ask"
	// EventAIConfirm 危险操作待确认。载荷 {token, title, items[], note}：
	// 前端渲染成确认条，用户点「确认执行」后带着 token 继续。
	EventAIConfirm = "ai:confirm"
)

// AIService AI 在前端的门面。
//
// 和 UpdateService 一样是「壳 + 后置绑定」：Manager 不依赖 app，
// 但发事件需要 app 实例，而 Services 列表在 application.New 之前就建好了。
type AIService struct {
	mu  sync.RWMutex
	mgr *ai.Manager
	app *application.App
	// registry 是应用注册的工具集合（Excel 合并等）。
	// 由 main 在装配时注入 —— 服务层不该知道有哪些具体工具。
	registry *ai.Registry
}

// BindManager 后置绑定。由 main 在 application.New 之后调用。
func (s *AIService) BindManager(m *ai.Manager) {
	s.mu.Lock()
	s.mgr = m
	s.mu.Unlock()
}

// BindApp 绑定 app 实例（用于发事件）。
func (s *AIService) BindApp(app *application.App) {
	s.mu.Lock()
	s.app = app
	s.mu.Unlock()
}

// SetRegistry 注入工具集合。
func (s *AIService) SetRegistry(r *ai.Registry) {
	s.mu.Lock()
	s.registry = r
	s.mu.Unlock()
}

func (s *AIService) tools() *ai.Registry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.registry
}

func (s *AIService) manager() *ai.Manager {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mgr
}

func (s *AIService) emit(name string, payload any) {
	s.mu.RLock()
	app := s.app
	s.mu.RUnlock()
	if app == nil || app.Event == nil {
		return
	}
	app.Event.Emit(name, payload)
}

// ── 配置与探测 ────────────────────────────────────────────

// Enabled AI 功能是否启用。前端据此决定显不显示入口。
func (s *AIService) Enabled() bool {
	if m := s.manager(); m != nil {
		return m.Enabled()
	}
	return false
}

// Providers 可选服务商（含本地探测结果）。
func (s *AIService) Providers(ctx context.Context) ([]ai.ProviderInfo, error) {
	m := s.manager()
	if m == nil {
		return nil, nil
	}
	return m.Providers(ctx), nil
}

// AISettings 给前端的设置视图。**不回传密钥原文**，只说有没有。
type AISettings struct {
	Provider          string `json:"provider"`
	BaseURL           string `json:"baseURL"`
	Model             string `json:"model"`
	HasKey            bool   `json:"hasKey"`
	DisableLocalProbe bool   `json:"disableLocalProbe"`
}

// Settings 当前设置。
func (s *AIService) Settings() (AISettings, error) {
	m := s.manager()
	if m == nil {
		return AISettings{}, nil
	}
	st := m.Settings()
	return AISettings{
		Provider:          st.Provider,
		BaseURL:           st.BaseURL,
		Model:             st.Model,
		HasKey:            strings.TrimSpace(st.APIKey) != "",
		DisableLocalProbe: st.DisableLocalProbe,
	}, nil
}

// SaveSettingsInput 保存设置的入参。
//
// APIKey 为空表示「保持原样」而不是「清空」—— 前端拿不到原文，
// 每次保存都回传空字符串是常态，若把它当清空就会悄悄删掉用户的密钥。
// 真要清空用 ClearKey。
type SaveSettingsInput struct {
	Provider          string `json:"provider"`
	BaseURL           string `json:"baseURL"`
	Model             string `json:"model"`
	APIKey            string `json:"apiKey"`
	ClearKey          bool   `json:"clearKey"`
	DisableLocalProbe bool   `json:"disableLocalProbe"`
}

// SaveSettings 保存设置（写用户目录 ai.json，0600）。
func (s *AIService) SaveSettings(in SaveSettingsInput) error {
	m := s.manager()
	if m == nil {
		return errors.New("AI 未初始化")
	}
	st := m.Settings()
	st.Provider = in.Provider
	st.BaseURL = in.BaseURL
	st.Model = in.Model
	st.DisableLocalProbe = in.DisableLocalProbe
	switch {
	case in.ClearKey:
		st.APIKey = ""
	case strings.TrimSpace(in.APIKey) != "":
		st.APIKey = strings.TrimSpace(in.APIKey)
	}
	return m.SaveSettings(st)
}

// Models 列举某个服务商的模型。
func (s *AIService) Models(ctx context.Context, provider string) ([]string, error) {
	m := s.manager()
	if m == nil {
		return nil, errors.New("AI 未初始化")
	}
	models, e := m.Models(ctx, provider)
	if e != nil {
		return nil, errors.New(e.Message)
	}
	return models, nil
}

// TestResult 连通性测试结果。
type TestResult struct {
	OK        bool   `json:"ok"`
	Model     string `json:"model"`
	LatencyMS int64  `json:"latencyMs"`
	Reply     string `json:"reply,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Error     string `json:"error,omitempty"`
	Hint      string `json:"hint,omitempty"`
}

// Test 用一句话验证服务商是否真的能用。
//
// 不做「只连一下」的假测试：那种能通过但真正对话时因为模型名不对、
// 不支持工具调用而失败。这里直接发一条最小请求。
func (s *AIService) Test(ctx context.Context, provider string) (TestResult, error) {
	m := s.manager()
	if m == nil {
		return TestResult{Error: "AI 未初始化"}, nil
	}
	client, e := m.Client(ctx, provider)
	if e != nil {
		return TestResult{OK: false, Kind: string(e.Kind), Error: e.Message, Hint: e.Hint}, nil
	}

	start := time.Now()
	resp, e := client.Chat(ctx, ai.Request{
		Messages:    []ai.Message{{Role: ai.RoleUser, Content: "回复 ok 两个字符，不要任何其它内容。"}},
		MaxTokens:   8,
		Temperature: floatPtr(0),
	})
	elapsed := time.Since(start).Milliseconds()

	if e != nil {
		return TestResult{OK: false, Model: client.Model(), LatencyMS: elapsed, Kind: string(e.Kind), Error: e.Message, Hint: e.Hint}, nil
	}
	return TestResult{OK: true, Model: resp.Model, LatencyMS: elapsed, Reply: strings.TrimSpace(resp.Content)}, nil
}

// ── 对话 ─────────────────────────────────────────────────

// ChatInput 对话入参。
type ChatInput struct {
	// SessionID 当前会话 id。工具层要靠它把「用户说的偏好」写到这个会话上，
	// 而不是全局设置里（每个会话可能是完全不同的任务）。
	SessionID   string       `json:"sessionId,omitempty"`
	Provider    string       `json:"provider"`
	Model       string       `json:"model"`
	Messages    []ai.Message `json:"messages"`
	Tools       []ai.Tool    `json:"tools,omitempty"`
	Temperature *float64     `json:"temperature,omitempty"`
	MaxTokens   int          `json:"maxTokens,omitempty"`
}

// ChatResult 对话结果（前端可直接渲染）。
type ChatResult struct {
	Content      string        `json:"content"`
	ToolCalls    []ai.ToolCall `json:"toolCalls,omitempty"`
	Model        string        `json:"model"`
	FinishReason string        `json:"finishReason"`
	PromptTokens int           `json:"promptTokens"`
	OutputTokens int           `json:"outputTokens"`
}

// Chat 非流式对话。
func (s *AIService) Chat(ctx context.Context, in ChatInput) (ChatResult, error) {
	client, e := s.client(ctx, in.Provider)
	if e != nil {
		s.emit(EventAIError, map[string]any{"kind": string(e.Kind), "message": e.Message, "hint": e.Hint})
		return ChatResult{}, errors.New(e.Message)
	}
	resp, e := client.Chat(ctx, in.toRequest())
	if e != nil {
		s.emit(EventAIError, map[string]any{"kind": string(e.Kind), "message": e.Message, "hint": e.Hint})
		return ChatResult{}, errors.New(e.Message)
	}
	return toChatResult(resp), nil
}

// ChatStream 流式对话。文本增量通过 ai:delta 事件推送。
//
// 返回的 ChatResult 是聚合后的最终结果 —— 前端即使用事件渲染，
// 也应该用返回值落库，否则断线时历史里只有半句话。
func (s *AIService) ChatStream(ctx context.Context, in ChatInput) (ChatResult, error) {
	client, e := s.client(ctx, in.Provider)
	if e != nil {
		s.emit(EventAIError, map[string]any{"kind": string(e.Kind), "message": e.Message, "hint": e.Hint})
		return ChatResult{}, errors.New(e.Message)
	}

	resp, e := client.ChatStream(ctx, in.toRequest(), func(d ai.Delta) error {
		if d.Content != "" {
			s.emit(EventAIDelta, map[string]any{"content": d.Content})
		}
		return nil
	})
	if e != nil {
		s.emit(EventAIError, map[string]any{"kind": string(e.Kind), "message": e.Message, "hint": e.Hint})
		return ChatResult{}, errors.New(e.Message)
	}

	out := toChatResult(resp)
	s.emit(EventAIDone, map[string]any{
		"content":      out.Content,
		"model":        out.Model,
		"finishReason": out.FinishReason,
		"usage": map[string]int{
			"promptTokens": out.PromptTokens,
			"outputTokens": out.OutputTokens,
		},
	})
	return out, nil
}

// ChatAgent 带工具循环的对话 —— 产品的主要交互方式。
//
// 与 ChatStream 的区别：模型可以调用工具（扫描目录、预览合并、执行合并），
// 循环由 Go 侧驱动（ai.Loop），每一步都通过 ai:tool 事件推给前端，
// 用户在界面上能看到「它在做什么」，而不是干等一句结论。
func (s *AIService) ChatAgent(ctx context.Context, in ChatInput) (ChatResult, error) {
	client, e := s.client(ctx, in.Provider)
	if e != nil {
		s.emit(EventAIError, map[string]any{"kind": string(e.Kind), "message": e.Message, "hint": e.Hint})
		return ChatResult{}, errors.New(e.Message)
	}

	// 把会话 id 放进 context：remember_instructions 工具要靠它定位写到哪个会话
	if in.SessionID != "" {
		ctx = ai.WithSessionID(ctx, in.SessionID)
	}

	loop := ai.NewLoop(client, s.tools()).Streaming(true)
	resp, e := loop.Run(ctx, in.toRequest().Messages, func(ev ai.Event) {
		switch ev.Type {
		case ai.EventToolCall, ai.EventToolResult:
			s.emit(EventAITool, ev)
		case ai.EventDelta:
			// 流式增量：直接转发给前端，边收边渲染
			if ev.Text != "" {
				s.emit(EventAIDelta, map[string]any{"content": ev.Text})
			}
		case ai.EventAssistant:
			// 非流式的回合：整段文字一次推给前端
			if ev.Text != "" {
				s.emit(EventAIDelta, map[string]any{"content": ev.Text})
			}
		case ai.EventConfirm:
			var cf map[string]any
			if err := json.Unmarshal([]byte(ev.Text), &cf); err != nil {
				cf = map[string]any{"title": "需要确认", "items": []string{ev.Text}}
			}
			s.emit(EventAIConfirm, cf)
		case ai.EventAwaiting:
			// 模型问了用户一个问题，等界面上的一次点击。
			// 原样把 question/options/detail 推给前端，前端渲染成按钮。
			var ask map[string]any
			if err := json.Unmarshal([]byte(ev.Text), &ask); err != nil {
				ask = map[string]any{"question": ev.Text, "options": []string{}}
			}
			s.emit(EventAIAsk, ask)
		}
	})
	if e != nil {
		s.emit(EventAIError, map[string]any{"kind": string(e.Kind), "message": e.Message, "hint": e.Hint})
		return ChatResult{}, errors.New(e.Message)
	}

	out := toChatResult(resp)
	s.emit(EventAIDone, map[string]any{
		"content": out.Content, "model": out.Model, "finishReason": out.FinishReason,
	})
	return out, nil
}

func (s *AIService) client(ctx context.Context, provider string) (*ai.Client, *ai.Error) {
	m := s.manager()
	if m == nil {
		return nil, &ai.Error{Kind: ai.KindConfig, Message: "AI 未初始化"}
	}
	return m.Client(ctx, provider)
}

func (in ChatInput) toRequest() ai.Request {
	return ai.Request{
		Model:       in.Model,
		Messages:    in.Messages,
		Tools:       in.Tools,
		Temperature: in.Temperature,
		MaxTokens:   in.MaxTokens,
	}
}

func toChatResult(resp *ai.Response) ChatResult {
	if resp == nil {
		return ChatResult{}
	}
	return ChatResult{
		Content:      resp.Content,
		ToolCalls:    resp.ToolCalls,
		Model:        resp.Model,
		FinishReason: resp.FinishReason,
		PromptTokens: resp.Usage.PromptTokens,
		OutputTokens: resp.Usage.CompletionTokens,
	}
}

func floatPtr(f float64) *float64 { return &f }
