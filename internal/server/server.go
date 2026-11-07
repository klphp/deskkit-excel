// Package server 内嵌的本地 HTTP 服务。
//
// # 为什么 Wails 应用还要跑 HTTP
//
// bindings 只能 Go ↔ 前端直连，**外部程序调不到**。这些场景需要 HTTP：
//
//   · 命令行工具 / 脚本触发应用的操作
//   · 浏览器书签、其它进程、甚至同一台机器上的另一个应用
//   · 流式响应（SSE）、文件下载、大响应体
//   · 想把应用同时当「本地 API 服务器」用时，配置一行就开
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
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/klphp/deskkit/internal/config"
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
