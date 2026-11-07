// Package api 外部 API 客户端。
//
// 统一处理认证、超时、重试、错误归一 —— 各业务代码不要自己拼 http.Client。
//
//	api := apiManager.Get("example")
//	var out SomeResponse
//	err := api.GetJSON(ctx, "/v1/things", &out)
//
// # 为什么要有这一层
//
// 每个对接点自己写 http.Get 的话，这些东西会散落在各处且不一致：
// 超时多少、失败重不重试、密钥从哪读、5xx 怎么判定、错误信息什么格式。
// 出问题时排查成本极高。集中一层之后改一处全局生效。
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/klphp/excel-batch-tool/internal/config"
)

// ErrNotFound 资源不存在（404）。
var ErrNotFound = errors.New("资源不存在")

// ErrUnauthorized 认证失败（401/403）。
var ErrUnauthorized = errors.New("认证失败，检查 API 密钥")

// Client 单个外部 API 的客户端。
type Client struct {
	name    string
	baseURL string
	apiKey  string
	retry   config.Retry
	http    *http.Client
}

// Manager 按名字管理多个 API 客户端。
type Manager struct {
	clients map[string]*Client
}

// NewManager 从配置创建。
func NewManager(cfg interface {
	APIKey(name string) string
}, apis map[string]config.API) *Manager {
	m := &Manager{clients: make(map[string]*Client)}

	for name, a := range apis {
		timeout := a.Timeout
		if timeout == 0 {
			timeout = 30 * time.Second
		}
		m.clients[name] = &Client{
			name:    name,
			baseURL: strings.TrimRight(a.BaseURL, "/"),
			apiKey:  cfg.APIKey(name),
			retry:   a.Retry,
			http:    &http.Client{Timeout: timeout},
		}
	}
	return m
}

// Get 取一个客户端。
func (m *Manager) Get(name string) (*Client, error) {
	c, ok := m.clients[name]
	if !ok {
		return nil, fmt.Errorf("API %q 没有配置（config/app.yaml 的 apis 下加一段）", name)
	}
	return c, nil
}

// GetJSON 发 GET 并把响应解到 out。
func (c *Client) GetJSON(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

// PostJSON 发 POST（body 会被 JSON 编码）。
func (c *Client) PostJSON(ctx context.Context, path string, body, out any) error {
	var buf []byte
	if body != nil {
		var err error
		buf, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("编码请求体失败: %w", err)
		}
	}
	return c.do(ctx, http.MethodPost, path, buf, out)
}

// do 执行请求，按配置重试。
//
// 只对「可能自己恢复」的失败重试：网络错误、5xx、429。
// 4xx（尤其 401/400）重试没意义，只会浪费配额和拖慢响应。
func (c *Client) do(ctx context.Context, method, path string, body []byte, out any) error {
	attempts := c.retry.MaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	backoff := c.retry.Backoff
	if backoff <= 0 {
		backoff = 500 * time.Millisecond
	}

	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff * time.Duration(i)):
			}
		}

		err, retryable := c.once(ctx, method, path, body, out)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retryable {
			return err
		}
	}
	return fmt.Errorf("%s 请求失败（重试 %d 次）: %w", c.name, attempts, lastErr)
}

// once 单次请求。第二个返回值表示是否值得重试。
func (c *Client) once(ctx context.Context, method, path string, body []byte, out any) (error, bool) {
	url := c.baseURL + path
	if !strings.HasPrefix(path, "/") {
		url = c.baseURL + "/" + path
	}

	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return fmt.Errorf("构造请求失败: %w", err), false
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// 网络层错误：可能是一过性的，值得重试
		return fmt.Errorf("请求 %s 失败: %w", url, err), true
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20)) // 上限 32MB，防超大响应打爆内存
	if err != nil {
		return fmt.Errorf("读响应失败: %w", err), true
	}

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound, false
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w（HTTP %d）", ErrUnauthorized, resp.StatusCode), false
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("%s 限流（HTTP 429）", c.name), true
	case resp.StatusCode >= 500:
		return fmt.Errorf("%s 服务端错误（HTTP %d）: %s", c.name, resp.StatusCode, snippet(data)), true
	case resp.StatusCode >= 400:
		return fmt.Errorf("%s 请求被拒（HTTP %d）: %s", c.name, resp.StatusCode, snippet(data)), false
	}

	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("解析响应失败: %w（原文: %s）", err, snippet(data)), false
		}
	}
	return nil, false
}

// snippet 截断响应体，避免把整页 HTML 错误打进日志。
func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
