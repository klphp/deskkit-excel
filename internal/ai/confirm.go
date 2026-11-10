package ai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// 危险操作的确认令牌。
//
// 规则：
//   · 模型第一次调用危险工具（没带令牌）→ 不执行，生成一次性令牌，
//     把「要做什么、影响哪些东西」交给界面，由用户点确认。
//   · 用户点了确认之后，模型带着同一个令牌再调一次 → 校验通过才执行。
//   · 令牌绑定「工具名 + 参数」，换参数就失效；用一次就作废；5 分钟过期。
//
// 这样即使用户说「别问了直接删」，模型手里的调用也执行不了 ——
// 真正按下按钮的人必须是用户本人。

// ConfirmTTL 令牌有效期。用户可能切出去看文件、回来再点，5 分钟够用又不算长。
const ConfirmTTL = 5 * time.Minute

// ConfirmRequest 交给界面渲染的确认请求。
type ConfirmRequest struct {
	Token string `json:"token"`
	Tool  string `json:"tool"`
	Title string `json:"title"`
	// Items 受影响的对象，一行一个（完整路径），界面用 markdown 列表渲染。
	Items []string `json:"items"`
	// Note 风险提示，例如「永久删除不可恢复，建议先备份」。
	Note string `json:"note"`
}

type pending struct {
	tool    string
	argsKey string
	req     ConfirmRequest
	expires time.Time
}

// Confirms 待确认操作表。整个进程一份。
type Confirms struct {
	mu   sync.Mutex
	pend map[string]pending
}

// ConfirmPrefix 危险工具「需要用户确认」的返回标记。
// 和 AwaitPrefix 同一套机制：循环层看到标记就停下，把回合交给界面。
const ConfirmPrefix = "\x00need_confirm\x00"

var defaultConfirms = NewConfirms()

// DefaultConfirms 进程内共用的待确认表。工具实现在里面登记，循环层不管细节。
func DefaultConfirms() *Confirms { return defaultConfirms }

// NewConfirms 建一个空表。
func NewConfirms() *Confirms {
	return &Confirms{pend: make(map[string]pending)}
}

// argsKey 把参数规范化成一个稳定的指纹（键排序，避免同义参数换顺序就失效）。
func argsKey(args string) string {
	var v any
	if err := json.Unmarshal([]byte(args), &v); err != nil {
		sum := sha256.Sum256([]byte(args))
		return hex.EncodeToString(sum[:8])
	}
	var sb strings.Builder
	stableWrite(&sb, v)
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:8])
}

func stableWrite(sb *strings.Builder, v any) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			if k == "confirmToken" { // 令牌本身不参与指纹
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		sb.WriteString("{")
		for _, k := range keys {
			sb.WriteString(k)
			sb.WriteString(":")
			stableWrite(sb, t[k])
			sb.WriteString(",")
		}
		sb.WriteString("}")
	case []any:
		sb.WriteString("[")
		for _, item := range t {
			stableWrite(sb, item)
			sb.WriteString(",")
		}
		sb.WriteString("]")
	default:
		fmt.Fprintf(sb, "%v", t)
	}
}

// Issue 登记一次待确认操作，返回令牌。
func (c *Confirms) Issue(tool, args string, req ConfirmRequest) string {
	key := argsKey(args)
	// 令牌本身也带指纹，用户看到的短码和参数绑定
	sum := sha256.Sum256([]byte(tool + "|" + key + "|" + fmt.Sprint(time.Now().UnixNano())))
	token := hex.EncodeToString(sum[:6]) // 12 位十六进制，手抄也不难
	req.Token = token
	req.Tool = tool

	c.mu.Lock()
	defer c.mu.Unlock()
	// 顺手清掉过期的，避免表无限增长
	now := time.Now()
	for k, v := range c.pend {
		if now.After(v.expires) {
			delete(c.pend, k)
		}
	}
	c.pend[token] = pending{tool: tool, argsKey: key, req: req, expires: now.Add(ConfirmTTL)}
	return token
}

// Consume 校验并作废令牌。成功返回 true。
func (c *Confirms) Consume(token, tool, args string) bool {
	if token == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.pend[token]
	if !ok {
		return false
	}
	delete(c.pend, token) // 一次性：无论校验成败都作废
	if time.Now().After(p.expires) {
		return false
	}
	return p.tool == tool && p.argsKey == argsKey(args)
}

// ── 会话上下文与指令保存钩子 ──
//
// 工具层需要知道「当前是哪个会话」，才能把用户说的「结果存到 D:\报表\」
// 记到这轮对话名下。但工具函数的签名只有 (ctx, args)，所以走 context 传值；
// 落盘动作由启动流程注入（packs 不该知道会话库长什么样）。

type sessionCtxKey struct{}

// WithSessionID 把会话 id 放进 context。
func WithSessionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, sessionCtxKey{}, id)
}

// SessionIDFrom 从 context 取会话 id（没有则空串）。
func SessionIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(sessionCtxKey{}).(string); ok {
		return v
	}
	return ""
}

// instructionSaver 由启动流程注入：把指令写到当前会话。
var (
	saverMu sync.RWMutex
	saverFn func(sessionID, text string) error
)

// SetInstructionSaver 注入保存实现（main 里接到 SessionService）。
func SetInstructionSaver(fn func(sessionID, text string) error) {
	saverMu.Lock()
	defer saverMu.Unlock()
	saverFn = fn
}

// SaveSessionInstructions 保存当前会话的指令。
func SaveSessionInstructions(ctx context.Context, text string) error {
	saverMu.RLock()
	fn := saverFn
	saverMu.RUnlock()
	if fn == nil {
		return fmt.Errorf("Instruction storage is not ready")
	}
	id := SessionIDFrom(ctx)
	if id == "" {
		return fmt.Errorf("Cannot get the current session id")
	}
	return fn(id, text)
}
