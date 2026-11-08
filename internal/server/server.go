// Package server 内嵌的本地 HTTP 服务。
//
// # 为什么 Wails 应用还要跑 HTTP
//
// bindings 只能 Go ↔ 前端直连，**外部程序调不到**。这些场景需要 HTTP：
//
//	· 命令行工具 / 脚本触发应用的操作
//	· 浏览器书签、其它进程、甚至同一台机器上的另一个应用
//	· 流式响应（SSE）、文件下载、大响应体
//	· 想把应用同时当「本地 API 服务器」用时，配置一行就开
//
// # 端口绝不写死
//
// 监听地址默认 127.0.0.1:0 —— 端口 0 让系统分配空闲端口。写死端口的话
// 同时开两个本应用（或模板 + 实际项目）第二个直接 bind 失败。
// 实际端口在启动后通过 Addr() 拿到，写进日志，也可以暴露给前端。
//
// # 只监听 127.0.0.1
//
// 不要改成 0.0.0.0。那等于把应用的本地能力（文件读写、配置修改、
// 可能的系统命令）暴露给同一局域网的任何设备。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/klphp/excel-batch-tool/internal/capability"
	"github.com/klphp/excel-batch-tool/internal/packs"

	"github.com/klphp/excel-batch-tool/internal/config"
)

// Server 本地服务。
type Server struct {
	cfg  config.Server
	http *http.Server
	ln   net.Listener
	addr string // 实际监听地址（含系统分配的端口）
}

// New 创建服务并注册路由。
func New(cfg config.Server) *Server {
	if cfg.LogLevel != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())

	// 请求日志走 slog，和 Wails 自己的日志统一格式
	r.Use(func(c *gin.Context) {
		start := time.Now()
		c.Next()
		slog.Debug("local http",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"latency_ms", float64(time.Since(start).Microseconds())/1000,
		)
	})

	if cfg.EnableCORS {
		r.Use(corsForLocalhost)
	}

	s := &Server{cfg: cfg}
	s.registerRoutes(r)

	s.http = &http.Server{
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s
}

// Start 启动监听。返回后 Addr() 就是实际地址。
func (s *Server) Start() error {
	if !s.cfg.Enabled {
		return nil
	}

	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("本地服务监听 %s 失败: %w", s.cfg.Addr, err)
	}
	s.ln = ln
	s.addr = ln.Addr().String()

	go func() {
		if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("本地服务异常退出", "err", err)
		}
	}()

	slog.Info("本地服务已启动", "addr", s.addr)
	return nil
}

// Addr 实际监听地址（如 127.0.0.1:53421）。未启动时为空。
func (s *Server) Addr() string { return s.addr }

// URL 完整基地址，前端拼接口用。
func (s *Server) URL() string {
	if s.addr == "" {
		return ""
	}
	return "http://" + s.addr
}

// Stop 优雅关闭。
func (s *Server) Stop(ctx context.Context) error {
	if s.http == nil || s.ln == nil {
		return nil
	}
	return s.http.Shutdown(ctx)
}

// registerRoutes 注册路由。
//
// 这一层只放「外部也需要调」的接口。纯前端用的数据走 bindings 更快
// （不经过 HTTP 序列化）。
func (s *Server) registerRoutes(r *gin.Engine) {
	// 健康检查：外部探活、脚本判断应用是否在跑
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	api := r.Group("/api/v1")
	{
		// ── 能力包接口 ──
		//
		// 这两条不只是给界面用的：外部 agent（MCP 客户端、脚本）也能拿到
		// 「本机有哪些能力、每个工具收什么参数」。用法说明由工具定义自动
		// 生成，所以永远不会和实现脱节。
		api.GET("/capabilities", func(c *gin.Context) {
			docs, err := capability.BuildAllUsage(packs.Enabled(), nil)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			// markdown 在清单里没意义（太长），列表只回摘要
			type item struct {
				ID          string   `json:"id"`
				Name        string   `json:"name"`
				Description string   `json:"description"`
				Bundled     bool     `json:"bundled"`
				Requires    []string `json:"requires,omitempty"`
				Missing     []string `json:"missing,omitempty"`
				ToolCount   int      `json:"toolCount"`
				Tools       []string `json:"tools"`
			}
			items := make([]item, 0, len(docs))
			for _, d := range docs {
				names := make([]string, 0, len(d.Tools))
				for _, t := range d.Tools {
					names = append(names, t.Name)
				}
				items = append(items, item{
					ID: d.ID, Name: d.Name, Description: d.Description,
					Bundled: d.Bundled, Requires: d.Requires, Missing: d.Missing,
					ToolCount: len(d.Tools), Tools: names,
				})
			}
			c.JSON(http.StatusOK, gin.H{"capabilities": items})
		})

		// 全部工具的机器可读清单：外部 agent 拿这一份就够写调用了。
		// 结构和 OpenAI 的 tools 定义一致，直接能塞进请求体。
		api.GET("/tools.json", func(c *gin.Context) {
			docs, err := capability.BuildAllUsage(packs.Enabled(), nil)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			type toolOut struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				Parameters  json.RawMessage `json:"parameters,omitempty"`
				Example     string          `json:"example,omitempty"`
			}
			capsOut := make([]gin.H, 0, len(docs))
			for _, d := range docs {
				tools := make([]toolOut, 0, len(d.Tools))
				for _, t := range d.Tools {
					tools = append(tools, toolOut{Name: t.Name, Description: t.Description, Parameters: t.Parameters, Example: t.Example})
				}
				capsOut = append(capsOut, gin.H{
					"id": d.ID, "name": d.Name, "description": d.Description,
					"bundled": d.Bundled, "requires": d.Requires, "missing": d.Missing,
					"tools": tools,
				})
			}
			c.JSON(http.StatusOK, gin.H{"app": "deskkit", "capabilities": capsOut})
		})

		// 全部用法合并成一份 markdown：一次请求就能把整本说明书塞进提示词
		api.GET("/usage", func(c *gin.Context) {
			docs, err := capability.BuildAllUsage(packs.Enabled(), nil)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			var b strings.Builder
			b.WriteString("# DeskKit 能力说明\n\n")
			b.WriteString("本机可用的文件/表格处理能力，全部在本机执行，数据不上传。\n\n")
			for _, d := range docs {
				b.WriteString(d.Markdown)
				b.WriteString("\n")
			}
			c.String(http.StatusOK, b.String())
		})

		// 用法说明：给模型当提示词，也给开发者当文档
		api.GET("/capabilities/:id/usage", func(c *gin.Context) {
			id := c.Param("id")
			for _, p := range packs.Enabled() {
				if p.ID != id {
					continue
				}
				doc, err := capability.BuildUsage(p)
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
					return
				}
				if c.Query("format") == "md" {
					c.String(http.StatusOK, doc.Markdown)
					return
				}
				c.JSON(http.StatusOK, doc)
				return
			}
			c.JSON(http.StatusNotFound, gin.H{"error": "没有这个能力包: " + id})
		})

		api.GET("/ping", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"pong": true, "time": time.Now().Format(time.RFC3339)})
		})
	}
}

// corsForLocalhost 只允许本地来源的跨域请求。
//
// 不写 Access-Control-Allow-Origin: * —— 开了 CORS 就已经扩大了暴露面，
// 再允许任意来源等于任何网页都能读本应用的数据。
func corsForLocalhost(c *gin.Context) {
	origin := c.GetHeader("Origin")
	if origin != "" && (isLocalOrigin(origin)) {
		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
	}
	if c.Request.Method == http.MethodOptions {
		c.AbortWithStatus(http.StatusNoContent)
		return
	}
	c.Next()
}

func isLocalOrigin(origin string) bool {
	for _, p := range []string{
		"http://localhost", "http://127.0.0.1", "http://[::1]",
		"https://localhost", "https://127.0.0.1",
		"wails://", // Wails 自己的协议
	} {
		if len(origin) >= len(p) && origin[:len(p)] == p {
			return true
		}
	}
	return false
}
