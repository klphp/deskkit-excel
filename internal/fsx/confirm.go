package fsx

// 危险操作的确认令牌（文件删除用）。
//
// 为什么这份逻辑放在 fs 而不是 ai：MCP server 要能独立成一个模块
// （go.mod = github.com/klphp/deskmcp），只能依赖「文件能力核心 + 第三方库」。
// 令牌机制是删除语义的一部分，必须跟着能力走；而 ai 包带着对话循环、
// 会话存储这些桌面端才有的东西，搬不动也不需要搬。
//
// 规则不变：
//   · 第一次调用（没带令牌）→ 不执行，生成一次性令牌，把「要删什么」交给界面；
//   · 用户点确认后，带着同一个令牌再调一次 → 校验通过才执行；
//   · 令牌绑定「工具名 + 参数」、用一次作废、5 分钟过期。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ConfirmTTL 令牌有效期。
const ConfirmTTL = 5 * time.Minute

// ConfirmPrefix 危险工具「需要用户确认」的返回标记。
//
// 这个字符串必须和 ai.ConfirmPrefix 完全一致：界面层（对话循环）靠它识别
// 「这轮不是在回答，是在等用户点确认」。fsai 包里有一条测试专门盯着两者相等，
// 谁改了都会红。
const ConfirmPrefix = "\x00need_confirm\x00"

// ConfirmRequest 交给界面 / MCP 客户端渲染的确认请求。
type ConfirmRequest struct {
	Token string `json:"token"`
	Tool  string `json:"tool"`
	Title string `json:"title"`
	// Items 受影响的对象，一行一个（完整路径）。
	Items []string `json:"items"`
	// Note 风险提示，例如「永久删除不可恢复」。
	Note string `json:"note"`
}

type pendingConfirm struct {
	tool    string
	argsKey string
	req     ConfirmRequest
	expires time.Time
}

// Confirms 待确认操作表。
type Confirms struct {
	mu   sync.Mutex
	pend map[string]pendingConfirm
}

var defaultConfirms = NewConfirms()

// DefaultConfirms 进程内共用的待确认表。
func DefaultConfirms() *Confirms { return defaultConfirms }

// NewConfirms 建一个空表。
func NewConfirms() *Confirms {
	return &Confirms{pend: make(map[string]pendingConfirm)}
}

// Issue 登记一次待确认操作，返回令牌。
func (c *Confirms) Issue(tool, args string, req ConfirmRequest) string {
	key := confirmArgsKey(args)
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d", tool, key, time.Now().UnixNano())))
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
	c.pend[token] = pendingConfirm{tool: tool, argsKey: key, req: req, expires: now.Add(ConfirmTTL)}
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
	return p.tool == tool && p.argsKey == confirmArgsKey(args)
}

// confirmArgsKey 把参数规范化成稳定指纹（键排序；confirmToken 本身不参与）。
func confirmArgsKey(args string) string {
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
			if k == "confirmToken" {
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
