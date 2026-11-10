package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LocalEndpoint 一个探测结果。
//
// Error 不为空表示这个地址没服务；保留它而不是直接丢掉，是为了让设置页
// 能显示「试过但没连上」，而不是让用户以为程序没去探测。
type LocalEndpoint struct {
	Endpoint string   `json:"endpoint"`
	Kind     string   `json:"kind"` // ollama | openai
	Models   []string `json:"models,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// ProbeLocal 并发探测本地模型服务。
//
// 只 GET 两个公开索引接口，不加载模型、不触发推理：
//
//	/api/tags   Ollama 原生
//	/v1/models  LM Studio / llama.cpp server / 其它 OpenAI 兼容实现
//
// 探测超时默认 400ms 是刻意压短：它发生在打开设置页时，不能让界面等。
// 本地服务没起来时连接会立刻被拒绝（几百微秒），400ms 只用来兜住
// 「端口开着但不响应」这种半死状态。
func ProbeLocal(ctx context.Context, endpoints []string, timeout time.Duration) []LocalEndpoint {
	if timeout <= 0 {
		timeout = 400 * time.Millisecond
	}

	results := make([]LocalEndpoint, len(endpoints))
	var wg sync.WaitGroup
	for i, ep := range endpoints {
		wg.Add(1)
		go func(i int, ep string) {
			defer wg.Done()
			results[i] = probeOne(ctx, ep, timeout)
		}(i, strings.TrimRight(strings.TrimSpace(ep), "/"))
	}
	wg.Wait()
	return results
}

func probeOne(ctx context.Context, endpoint string, timeout time.Duration) LocalEndpoint {
	out := LocalEndpoint{Endpoint: endpoint}
	if endpoint == "" {
		out.Error = "Address is empty"
		return out
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client := &http.Client{Transport: &http.Transport{ForceAttemptHTTP2: false}}

	// Ollama 原生接口优先：它能直接给出模型名，且不依赖 /v1 兼容层。
	if models, err := listModels(ctx, client, endpoint+"/api/tags"); err == nil {
		out.Kind = "ollama"
		out.Models = models
		return out
	}
	// 再试 OpenAI 兼容形态。LM Studio 默认 1234 端口、llama.cpp server 都有。
	if models, err := listModels(ctx, client, endpoint+"/v1/models"); err == nil {
		out.Kind = "openai"
		out.Models = models
		return out
	}

	out.Error = "No response from /api/tags or /v1/models"
	return out
}

func listModels(ctx context.Context, client *http.Client, url string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errStatus(resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	var wire struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, err
	}

	var out []string
	for _, d := range wire.Data {
		if d.ID != "" {
			out = append(out, d.ID)
		}
	}
	for _, m := range wire.Models {
		name := m.Name
		if name == "" {
			name = m.Model
		}
		if name != "" {
			out = append(out, name)
		}
	}
	if len(out) == 0 {
		// 服务在，但一个模型都没拉过 —— 这仍然是「可用」的，
		// 只是没模型可选，交给上层提示去 ollama pull。
		return nil, errNoModels
	}
	return out, nil
}

type simpleErr string

func (e simpleErr) Error() string { return string(e) }

func errStatus(code int) error { return simpleErr("HTTP " + strconv.Itoa(code)) }

const errNoModels = simpleErr("Service is up but has no available models")
