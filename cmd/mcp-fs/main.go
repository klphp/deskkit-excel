// Command mcp-fs 把 internal/tools/fs 的文件能力包成一个 MCP stdio server。
//
// 为什么用 Go 重写一份：opencode / Claude Desktop 里原来用 Node 版
// @modelcontextprotocol/server-filesystem。实测 node 单进程常驻 57MB、启动
// 270ms，用户一开十几个会话就是 GB 级内存。Go 版是单文件静态二进制，
// 冷启动到能应答 initialize 只要几毫秒，常驻内存是 Node 的十分之一量级
// （实测数据见 logs/2026-11-09-mcp-fs.log）。
//
// 传输层是 MCP stdio：**换行分隔的 JSON-RPC 2.0**，不是 Content-Length 头。
// 这点最容易搞错：写错一个字节客户端就连不上，而且没有任何错误提示。
//
// 作用域：命令行给一个或多个允许目录（和 Node 版一致），不给就默认用户主目录。
// 所有工具动作仍然走 fs.Guard —— 系统目录、符号链接逃逸、越界路径一律拒绝，
// 删除默认进废纸篓并且要确认令牌。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/klphp/excel-batch-tool/internal/tools/fs"
)

const serverVersion = "1.0.0"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fset := flag.NewFlagSet("mcp-fs", flag.ContinueOnError)
	fset.SetOutput(os.Stderr)
	var roots multiFlag
	fset.Var(&roots, "root", "允许操作的目录（可重复）；不给则用位置参数；都没有则默认用户主目录")
	showVersion := fset.Bool("version", false, "打印版本后退出")
	if err := fset.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Println("mcp-fs " + serverVersion)
		return 0
	}
	// 位置参数也当根目录：`mcp-fs /path/a /path/b` 与 Node 版用法一致，
	// opencode 的配置里可以直接照抄。
	roots = append(roots, fset.Args()...)

	guard, err := buildGuard(roots)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcp-fs:", err)
		return 1
	}

	// 和桌面应用共用同一份能力目录（fs.Guard.Catalog()）：语义（含删除确认、
	// 编码处理、路径边界）完全一致，但这里只依赖 fs 包 + 标准库。
	srv := newServer(guard)
	// 日志只能写 stderr：stdout 是协议通道，多打一个字节客户端就崩。
	fmt.Fprintf(os.Stderr, "mcp-fs %s ready (roots: %v)\n", serverVersion, guard.Roots)
	if err := srv.serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "mcp-fs: 读 stdin 失败:", err)
		return 1
	}
	return 0
}

// multiFlag 可重复的 -root。
type multiFlag []string

func (m *multiFlag) String() string { return fmt.Sprint([]string(*m)) }

func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

// buildGuard 把命令行给的目录变成边界。目录必须存在 —— 打错一个字母就变成
// 「什么都不能做」的空 server，还不如启动时报错。
func buildGuard(roots []string) (fs.Guard, error) {
	if len(roots) == 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			return fs.Guard{}, fmt.Errorf("拿不到用户主目录: %w", err)
		}
		roots = []string{home}
	}
	out := make([]string, 0, len(roots))
	for _, raw := range roots {
		abs, err := filepath.Abs(raw)
		if err != nil {
			return fs.Guard{}, fmt.Errorf("路径不合法 %q: %w", raw, err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return fs.Guard{}, fmt.Errorf("目录不存在或读不到: %s（%v）", abs, err)
		}
		if !info.IsDir() {
			return fs.Guard{}, fmt.Errorf("%s 不是目录", abs)
		}
		out = append(out, abs)
	}
	return fs.Guard{Roots: out}, nil
}
