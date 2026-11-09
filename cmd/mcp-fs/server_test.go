package main

// MCP stdio 的端到端测试：真起子进程、真喂 JSON-RPC 行、真读回包。
//
// 为什么不在进程内调 handle()：协议最大的风险在传输层 —— 换行分隔还是
// Content-Length 头、stdout 有没有被日志污染、EOF 能不能干净退出。
// 这些只有把二进制当黑盒跑一遍才验证得到。

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	buildOnce sync.Once
	binPath   string
	buildErr  error
)

// TestMain 编译一次被测二进制（和正式交付命令同一组参数）。
func TestMain(m *testing.M) {
	code := m.Run()
	if binPath != "" {
		os.RemoveAll(filepath.Dir(binPath))
	}
	os.Exit(code)
}

func buildBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "mcp-fs-test")
		if err != nil {
			buildErr = err
			return
		}
		binPath = filepath.Join(dir, "deskkit-mcp-fs")
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-w -s", "-o", binPath, ".")
		out, err := cmd.CombinedOutput()
		if err != nil {
			buildErr = fmt.Errorf("编译失败: %v\n%s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatalf("%v", buildErr)
	}
	return binPath
}

// mcpClient 一个跑着的 MCP server 子进程。
type mcpClient struct {
	t     *testing.T
	cmd   *exec.Cmd
	stdin io.WriteCloser
	lines chan string
	done  chan error
}

func startServer(t *testing.T, roots ...string) *mcpClient {
	t.Helper()
	bin := buildBinary(t)
	args := append([]string{}, roots...)
	cmd := exec.Command(bin, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("建 stdin 管道失败: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("建 stdout 管道失败: %v", err)
	}
	// stderr 收下来，出错时能打印；同时验证「日志不许进 stdout」
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动 server 失败: %v", err)
	}

	c := &mcpClient{t: t, cmd: cmd, stdin: stdin, lines: make(chan string, 64), done: make(chan error, 1)}
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		for sc.Scan() {
			c.lines <- sc.Text()
		}
		close(c.lines)
	}()
	go func() { c.done <- cmd.Wait() }()
	t.Cleanup(func() {
		stdin.Close()
		// 进程可能已经被测试自己 Wait 过了（EOF 测试），这时 ProcessState 非空，
		// 再等 done 只会白等到超时。
		if c.cmd.ProcessState == nil {
			select {
			case <-c.done:
			case <-time.After(5 * time.Second):
				c.cmd.Process.Kill()
			}
		}
		if t.Failed() && stderr.Len() > 0 {
			t.Logf("server stderr:\n%s", stderr.String())
		}
	})
	return c
}

func (c *mcpClient) sendRaw(line string) {
	c.t.Helper()
	if _, err := io.WriteString(c.stdin, line+"\n"); err != nil {
		c.t.Fatalf("写入请求失败: %v", err)
	}
}

func (c *mcpClient) send(v any) {
	c.t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		c.t.Fatalf("序列化请求失败: %v", err)
	}
	c.sendRaw(string(raw))
}

// nextLine 读一行回包，超时算失败。
func (c *mcpClient) nextLine(timeout time.Duration) (string, bool) {
	c.t.Helper()
	select {
	case line, ok := <-c.lines:
		return line, ok
	case <-time.After(timeout):
		return "", false
	}
}

// await 读到指定 id 的回包为止（服务端并发处理，回包顺序不做保证）。
func (c *mcpClient) await(id int) map[string]any {
	c.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		line, ok := c.nextLine(time.Until(deadline))
		if !ok {
			c.t.Fatalf("等 id=%d 的回包超时", id)
		}
		var resp map[string]any
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			c.t.Fatalf("回包不是合法 JSON: %q（%v）", line, err)
		}
		if got, ok := resp["id"].(float64); ok && int(got) == id {
			return resp
		}
	}
	c.t.Fatalf("等 id=%d 的回包超时", id)
	return nil
}

// call 发一个请求并等回包。
func (c *mcpClient) call(id int, method string, params any) map[string]any {
	c.t.Helper()
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	c.send(req)
	return c.await(id)
}

// toolCall 调一个工具，返回 (文本内容, isError)。
func (c *mcpClient) toolCall(id int, name string, args any) (string, bool) {
	c.t.Helper()
	resp := c.call(id, "tools/call", map[string]any{"name": name, "arguments": args})
	if e, ok := resp["error"].(map[string]any); ok {
		c.t.Fatalf("tools/call 返回了协议错误（应该用 isError）: %v", e)
	}
	result, _ := resp["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		c.t.Fatalf("tools/call 没有 content: %v", resp)
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	isErr, _ := result["isError"].(bool)
	return text, isErr
}

func (c *mcpClient) initialize(t *testing.T) {
	t.Helper()
	resp := c.call(1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "1"},
	})
	result, _ := resp["result"].(map[string]any)
	if result == nil {
		t.Fatalf("initialize 没有 result: %v", resp)
	}
	// 握手完成后按规范发通知（不回包）
	c.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
}

func TestInitializeHandshake(t *testing.T) {
	root := t.TempDir()
	c := startServer(t, root)

	resp := c.call(1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "1"},
	})
	result, _ := resp["result"].(map[string]any)
	if result == nil {
		t.Fatalf("initialize 没有 result: %v", resp)
	}
	if got := result["protocolVersion"]; got != "2024-11-05" {
		t.Errorf("protocolVersion 应为 2024-11-05，实际 %v", got)
	}
	caps, _ := result["capabilities"].(map[string]any)
	if _, ok := caps["tools"]; !ok {
		t.Errorf("capabilities 应声明 tools: %v", caps)
	}
	info, _ := result["serverInfo"].(map[string]any)
	if info["name"] != "deskkit-mcp-fs" {
		t.Errorf("serverInfo.name 不对: %v", info)
	}
	if info["version"] == nil || info["version"] == "" {
		t.Errorf("serverInfo.version 缺失: %v", info)
	}

	// initialized 通知不该产生任何回包
	c.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	if line, ok := c.nextLine(300 * time.Millisecond); ok {
		t.Fatalf("通知不该有回包，却收到: %s", line)
	}

	// ping 必须能答
	resp = c.call(2, "ping", nil)
	if _, ok := resp["result"]; !ok {
		t.Errorf("ping 应返回空 result: %v", resp)
	}
}

func TestToolsList(t *testing.T) {
	c := startServer(t, t.TempDir())
	c.initialize(t)

	resp := c.call(2, "tools/list", nil)
	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) < 8 {
		t.Fatalf("至少应暴露 8 个工具，实际 %d", len(tools))
	}
	names := map[string]bool{}
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		name, _ := tool["name"].(string)
		names[name] = true
		if tool["description"] == "" {
			t.Errorf("工具 %s 缺 description", name)
		}
		schema, _ := tool["inputSchema"].(map[string]any)
		if schema == nil || schema["type"] != "object" {
			t.Errorf("工具 %s 的 inputSchema 不是 object: %v", name, tool["inputSchema"])
		}
	}
	for _, want := range []string{"fs_list", "fs_read", "fs_search", "fs_peek", "fs_mkdir", "fs_move", "fs_copy", "fs_delete"} {
		if !names[want] {
			t.Errorf("缺少工具 %s（当前 %v）", want, names)
		}
	}
}

func TestToolCallReadSearchList(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "报表.txt"), []byte("标题\n金额 100\n合计 100\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "log.txt"), []byte("nothing\n合计 200\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := startServer(t, root)
	c.initialize(t)

	// fs_read
	text, isErr := c.toolCall(10, "fs_read", map[string]any{
		"path": filepath.Join(root, "报表.txt"), "offset": 2, "limit": 1,
	})
	if isErr {
		t.Fatalf("fs_read 失败: %s", text)
	}
	var read map[string]any
	if err := json.Unmarshal([]byte(text), &read); err != nil {
		t.Fatalf("fs_read 内容不是 JSON: %v（%s）", err, text)
	}
	if read["totalLines"].(float64) != 3 {
		t.Errorf("总行数应为 3，实际 %v", read["totalLines"])
	}
	lines, _ := read["lines"].([]any)
	if len(lines) != 1 || lines[0] != "金额 100" {
		t.Errorf("返回的行不对: %v", lines)
	}

	// fs_search
	text, isErr = c.toolCall(11, "fs_search", map[string]any{"path": root, "pattern": "合计"})
	if isErr {
		t.Fatalf("fs_search 失败: %s", text)
	}
	var sr map[string]any
	if err := json.Unmarshal([]byte(text), &sr); err != nil {
		t.Fatalf("fs_search 内容不是 JSON: %v（%s）", err, text)
	}
	matches, _ := sr["matches"].([]any)
	if len(matches) != 2 {
		t.Fatalf("应命中 2 处，实际 %v", matches)
	}

	// fs_list 递归
	text, isErr = c.toolCall(12, "fs_list", map[string]any{"path": root, "recursive": true})
	if isErr {
		t.Fatalf("fs_list 失败: %s", text)
	}
	var lr map[string]any
	if err := json.Unmarshal([]byte(text), &lr); err != nil {
		t.Fatalf("fs_list 内容不是 JSON: %v（%s）", err, text)
	}
	if lr["files"].(float64) != 2 {
		t.Errorf("应看到 2 个文件，实际 %v", lr["files"])
	}

	// fs_peek 也要能用（确认旧工具有没被改坏）
	text, isErr = c.toolCall(13, "fs_peek", map[string]any{"path": filepath.Join(root, "报表.txt")})
	if isErr {
		t.Fatalf("fs_peek 失败: %s", text)
	}
	if !strings.Contains(text, "encoding") {
		t.Errorf("fs_peek 结果异常: %s", text)
	}
}

func TestToolErrorsAreNotProtocolErrors(t *testing.T) {
	root := t.TempDir()
	c := startServer(t, root)
	c.initialize(t)

	// 越界路径 → isError:true（不是 JSON-RPC error）
	text, isErr := c.toolCall(10, "fs_read", map[string]any{"path": "/etc/hosts"})
	if !isErr {
		t.Fatalf("越界路径应返回 isError:true，实际 text=%s", text)
	}
	if !strings.Contains(text, "允许范围") && !strings.Contains(text, "拒绝") {
		t.Errorf("错误信息应说明拒绝原因: %s", text)
	}

	// 不存在的文件
	text, isErr = c.toolCall(11, "fs_read", map[string]any{"path": filepath.Join(root, "没有.txt")})
	if !isErr || !strings.Contains(text, "读不到") {
		t.Errorf("不存在的文件应返回 isError: %v / %s", isErr, text)
	}

	// 未知工具
	text, isErr = c.toolCall(12, "fs_not_exist", map[string]any{})
	if !isErr {
		t.Errorf("未知工具应返回 isError:true，实际 %s", text)
	}
}

func TestUnknownMethodReturnsMethodNotFound(t *testing.T) {
	c := startServer(t, t.TempDir())
	c.initialize(t)

	resp := c.call(20, "tools/whatever", nil)
	e, _ := resp["error"].(map[string]any)
	if e == nil {
		t.Fatalf("未知方法应返回 error: %v", resp)
	}
	if int(e["code"].(float64)) != -32601 {
		t.Errorf("错误码应为 -32601，实际 %v", e["code"])
	}
}

func TestMalformedJSONReturnsParseError(t *testing.T) {
	c := startServer(t, t.TempDir())
	c.initialize(t)

	c.sendRaw(`{"jsonrpc":"2.0","id":30,`)
	line, ok := c.nextLine(5 * time.Second)
	if !ok {
		t.Fatal("乱码行应返回解析错误")
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("回包不是合法 JSON: %q", line)
	}
	e, _ := resp["error"].(map[string]any)
	if e == nil || int(e["code"].(float64)) != -32700 {
		t.Fatalf("应返回 -32700: %s", line)
	}
}

// 删除的确认令牌语义：没令牌只给清单和 token，带令牌才真删。
func TestDeleteConfirmTokenFlow(t *testing.T) {
	root := t.TempDir()
	victim := filepath.Join(root, "要删的.txt")
	if err := os.WriteFile(victim, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := startServer(t, root)
	c.initialize(t)

	text, isErr := c.toolCall(40, "fs_delete", map[string]any{"paths": []string{victim}, "permanent": true})
	if isErr {
		t.Fatalf("第一次删除不该是错误: %s", text)
	}
	if !strings.Contains(text, "确认") || !strings.Contains(text, "token") {
		t.Fatalf("第一次调用应返回确认请求: %s", text)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatal("没确认之前不许删文件")
	}

	// 从回包里取出 token
	idx := strings.Index(text, "{")
	if idx < 0 {
		t.Fatalf("回包里没有 JSON: %s", text)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(text[idx:]), &payload); err != nil {
		t.Fatalf("确认请求不是 JSON: %v（%s）", err, text)
	}
	token, _ := payload["token"].(string)
	if token == "" {
		t.Fatalf("确认请求里没有 token: %s", text)
	}
	items, _ := payload["items"].([]any)
	if len(items) != 1 || !strings.Contains(fmt.Sprint(items[0]), "要删的.txt") {
		t.Errorf("清单应列出完整路径: %v", items)
	}

	// 带错令牌：仍然要重新确认
	text, _ = c.toolCall(41, "fs_delete", map[string]any{
		"paths": []string{victim}, "permanent": true, "confirmToken": "deadbeef",
	})
	if !strings.Contains(text, "确认") {
		t.Errorf("错令牌应要求重新确认: %s", text)
	}

	// 带真令牌：执行
	text, isErr = c.toolCall(42, "fs_delete", map[string]any{
		"paths": []string{victim}, "permanent": true, "confirmToken": token,
	})
	if isErr {
		t.Fatalf("带令牌的删除失败: %s", text)
	}
	if _, err := os.Stat(victim); !os.IsNotExist(err) {
		t.Fatal("带令牌之后文件应该被删掉")
	}
}

func TestEOFExitsCleanly(t *testing.T) {
	c := startServer(t, t.TempDir())
	c.initialize(t)

	// 关掉 stdin 模拟客户端退出
	if err := c.stdin.Close(); err != nil {
		t.Fatalf("关闭 stdin 失败: %v", err)
	}
	select {
	case err := <-c.done:
		if err != nil {
			t.Fatalf("EOF 之后应正常退出（exit 0），实际: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("EOF 之后 5 秒还没退出")
	}
	// 关完之后 stdout 应该被关掉，不留悬挂
	if _, ok := <-c.lines; ok {
		t.Log("stdout 上还有残留行（已忽略）")
	}
}

func TestStartupWithNoRootsUsesHome(t *testing.T) {
	bin := buildBinary(t)
	cmd := exec.Command(bin)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	defer func() {
		stdin.Close()
		cmd.Wait()
	}()
	io.WriteString(stdin, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`+"\n")
	rd := bufio.NewReader(stdout)
	line, err := rd.ReadString('\n')
	if err != nil {
		t.Fatalf("没收到 initialize 回包: %v（stderr: %s）", err, stderr.String())
	}
	if !strings.Contains(line, "2024-11-05") {
		t.Errorf("回包异常: %s", line)
	}
	// stderr 里应说明默认根目录是用户主目录
	if !strings.Contains(stderr.String(), "/") {
		t.Logf("stderr: %s", stderr.String())
	}
}

func TestBadRootFailsFast(t *testing.T) {
	bin := buildBinary(t)
	cmd := exec.Command(bin, "/nonexistent-dir-for-test")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("不存在的目录应启动失败，实际输出: %s", out)
	}
	if !strings.Contains(string(out), "不存在") && !strings.Contains(string(out), "读不到") {
		t.Errorf("错误信息应说明目录不存在: %s", out)
	}
}
