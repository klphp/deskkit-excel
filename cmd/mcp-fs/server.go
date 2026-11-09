package main

// MCP stdio 协议层：换行分隔的 JSON-RPC 2.0。
//
// 实现范围（够 opencode / Claude Desktop 用）：
//   requests : initialize / tools/list / tools/call / ping
//   notify   : notifications/initialized（以及其它 notifications/*）→ 不回包
//   unknown  : JSON-RPC error -32601（不是「静默忽略」—— 静默会让客户端一直等）
//
// 关键取舍：**工具执行失败不是协议错误**。文件不存在、路径越界这些都是
// 正常的业务失败，要包成 isError:true 的 content 回给模型，让它自己换路径重试；
// 只有「你发的 JSON 不是请求」才算协议错误。把两者混在一起是 MCP 实现里
// 最常见的问题（模型永远看不到「文件不存在」，只看到连接报错）。
//
// 并发：读循环把每个请求丢进有界协程池，响应按 id 回写（JSON-RPC 允许乱序）。
// 这样一个慢的 fs_search 不会把 ping 堵住；EOF 时等在飞的请求都回完再退出。

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/klphp/excel-batch-tool/internal/ai"
	"github.com/klphp/excel-batch-tool/internal/tools/fs"
)

// protocolVersion 按 MCP 2024-11-05 规范回应。客户端提的版本如果不认识，
// 也必须回这个（规范要求服务端回自己支持的版本）。
const protocolVersion = "2024-11-05"

// JSON-RPC 错误码。
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// mcpTool 对外暴露的工具描述。
type mcpTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type callResult struct {
	Content []textContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type server struct {
	reg   *ai.Registry
	guard fs.Guard

	// writeMu 串行化 stdout 写入：并发回的包不能互相插进对方中间。
	writeMu sync.Mutex
	out     io.Writer
}

func newServer(reg *ai.Registry, guard fs.Guard) *server {
	return &server{reg: reg, guard: guard}
}

// serve 读 stdin 直到 EOF。
func (s *server) serve(stdin io.Reader, stdout io.Writer) error {
	s.out = stdout
	sc := bufio.NewScanner(stdin)
	// 一行可能很长（批量 paths、大文件内容进参数），给 8MB 上限；
	// 默认的 64KB 会在真实使用中直接报 "token too long"。
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	workers := fs.Parallelism()
	if workers > 8 {
		workers = 8 // 请求是逐个来的，协程太多只是空转
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup

	enc := json.NewEncoder(stdout)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		buf := append([]byte(nil), line...)
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			resp, ok := s.handle(buf)
			if !ok {
				return
			}
			s.writeMu.Lock()
			defer s.writeMu.Unlock()
			if err := enc.Encode(resp); err != nil {
				fmt.Fprintf(io.Discard, "%v", err)
			}
		}()
	}
	// EOF：先把在飞的请求回完再退出，否则最后一个 tools/call 的响应会丢。
	wg.Wait()
	return sc.Err()
}

// handle 处理一行请求，返回响应和一个「要不要回包」的标记。
// 通知（没有 id）不回包。
func (s *server) handle(line []byte) (rpcResponse, bool) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return errorResponse(nil, codeParseError, "收到的不是合法 JSON: "+err.Error()), true
	}
	isNotification := len(req.ID) == 0 || string(req.ID) == "null"

	switch req.Method {
	case "initialize":
		return okResponse(req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]any{
				"tools": map[string]any{"listChanged": false},
			},
			"serverInfo": map[string]any{
				"name":    "deskkit-mcp-fs",
				"version": serverVersion,
			},
		}), !isNotification

	case "notifications/initialized", "initialized":
		// 客户端确认握手完成，不需要回应。
		return rpcResponse{}, false

	case "ping":
		return okResponse(req.ID, map[string]any{}), !isNotification

	case "tools/list":
		return okResponse(req.ID, map[string]any{"tools": s.tools()}), !isNotification

	case "tools/call":
		if isNotification {
			return rpcResponse{}, false
		}
		return s.handleCall(req)

	default:
		if isNotification || len(req.Method) > 0 && req.Method[0] == 'n' && hasPrefix(req.Method, "notifications/") {
			// 未知通知：按规范静默忽略（客户端可能在发我们不认识的进度通知）
			return rpcResponse{}, false
		}
		return errorResponse(req.ID, codeMethodNotFound, "不支持的方法: "+req.Method), true
	}
}

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }

func (s *server) tools() []mcpTool {
	defs := s.reg.Definitions()
	out := make([]mcpTool, 0, len(defs))
	for _, d := range defs {
		out = append(out, mcpTool{
			Name:        d.Function.Name,
			Description: d.Function.Description,
			InputSchema: d.Function.Parameters,
		})
	}
	return out
}

// handleCall 执行一次工具调用。
func (s *server) handleCall(req rpcRequest) (rpcResponse, bool) {
	var in struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &in); err != nil {
		return errorResponse(req.ID, codeInvalidParams, "tools/call 参数解析失败: "+err.Error()), true
	}
	if in.Name == "" {
		return errorResponse(req.ID, codeInvalidParams, "tools/call 缺少工具名 name"), true
	}
	args := in.Arguments
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}

	// 用 context.Background：stdio 上难以做「单次调用取消」，但工具内部的
	// 并行扫描仍然受并发上限约束，不会无限跑下去。
	out, err := s.reg.Execute(context.Background(), in.Name, string(args))
	if err != nil {
		// ★ 业务失败 → isError，不是协议错误。
		return okResponse(req.ID, callResult{
			Content: []textContent{{Type: "text", Text: err.Error()}},
			IsError: true,
		}), true
	}

	// fs_delete 的确认令牌语义保留：第一次调用返回 token，模型原样带上再调一次
	// 才真删。丢掉 token 就重新走一遍确认（不会静默删除）。
	if payload, ok := trimConfirmPrefix(out); ok {
		return okResponse(req.ID, callResult{
			Content: []textContent{{
				Type: "text",
				Text: "需要用户确认后才能执行。请把下面的 token 原样作为 confirmToken 再次调用 " +
					in.Name + "（用户没有明确同意就不要继续）：\n" + payload,
			}},
		}), true
	}

	return okResponse(req.ID, callResult{Content: []textContent{{Type: "text", Text: out}}}), true
}

func trimConfirmPrefix(out string) (string, bool) {
	const prefix = ai.ConfirmPrefix
	if len(out) >= len(prefix) && out[:len(prefix)] == prefix {
		return out[len(prefix):], true
	}
	return "", false
}

func okResponse(id json.RawMessage, result any) rpcResponse {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func errorResponse(id json.RawMessage, code int, msg string) rpcResponse {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}
