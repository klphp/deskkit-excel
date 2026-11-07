package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProbeLocalOllama(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.Write([]byte(`{"models":[{"name":"qwen2.5:7b"},{"name":"llama3.2:3b"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	out := ProbeLocal(context.Background(), []string{srv.URL}, time.Second)
	if len(out) != 1 {
		t.Fatalf("期望 1 个结果，实际 %d", len(out))
	}
	if out[0].Kind != "ollama" {
		t.Fatalf("应识别为 ollama，实际 %q（err=%s）", out[0].Kind, out[0].Error)
	}
	if len(out[0].Models) != 2 || out[0].Models[0] != "qwen2.5:7b" {
		t.Fatalf("模型列表不对: %+v", out[0].Models)
	}
}

func TestProbeLocalOpenAICompatible(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Write([]byte(`{"data":[{"id":"local-model"}]}`))
			return
		}
		http.NotFound(w, r) // /api/tags 不存在 → 退回 OpenAI 兼容形态
	}))
	defer srv.Close()

	out := ProbeLocal(context.Background(), []string{srv.URL}, time.Second)
	if out[0].Kind != "openai" {
		t.Fatalf("应识别为 openai 兼容，实际 %q（err=%s）", out[0].Kind, out[0].Error)
	}
	if len(out[0].Models) != 1 || out[0].Models[0] != "local-model" {
		t.Fatalf("模型列表不对: %+v", out[0].Models)
	}
}

// 没服务时不能阻塞设置页：超时要短、要返回 Error 而不是死等。
func TestProbeLocalDeadEndpoint(t *testing.T) {
	start := time.Now()
	out := ProbeLocal(context.Background(), []string{"http://127.0.0.1:1"}, 200*time.Millisecond)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("探测太慢: %v", elapsed)
	}
	if out[0].Error == "" {
		t.Fatal("死地址应当有 Error")
	}
	if out[0].Kind != "" {
		t.Fatalf("死地址不该识别出 kind: %q", out[0].Kind)
	}
}

// 服务在但没拉模型：算「不报错但不可用」，提示去 pull。
func TestProbeLocalNoModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"models":[]}`))
	}))
	defer srv.Close()

	out := ProbeLocal(context.Background(), []string{srv.URL}, time.Second)
	if len(out[0].Models) != 0 {
		t.Fatalf("不该有模型: %+v", out[0].Models)
	}
	if out[0].Error == "" {
		t.Fatal("没有模型时应当给出提示性 Error")
	}
}
