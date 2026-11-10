package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/klphp/deskkit-excel/internal/config"
)

// 本地模拟更新源：一个 manifest + 一个真的 zip，全程不碰 GitHub。
// 这就是以后调更新的主路径 —— 改动一次跑一次，几秒出结果。
func TestProbeEndpointLocal(t *testing.T) {
	artifact := []byte("fake zip payload")
	digest := sha256Hex(artifact)

	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/artifact.zip", func(w http.ResponseWriter, r *http.Request) {
		w.Write(artifact)
	})
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		m := map[string]any{
			"schemaVersion": 1,
			"version":       "9.9.9",
			"artifacts": []map[string]any{{
				"url":        srv.URL + "/artifact.zip",
				"filename":   "deskkit-9.9.9-" + runtime.GOOS + "-" + runtime.GOARCH + ".zip",
				"platform":   runtime.GOOS,
				"arch":       runtime.GOARCH,
				"digestAlgo": "sha256",
				"digest":     digest,
			}},
		}
		json.NewEncoder(w).Encode(m)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	cfg := config.Update{Enabled: true, Provider: "endpoint", ManifestURL: srv.URL + "/manifest.json"}
	res := Probe(context.Background(), cfg, "0.1.3", runtime.GOOS, runtime.GOARCH)

	if res.Err != "" {
		t.Fatalf("不该报错: %s", res.Err)
	}
	if res.Latest != "9.9.9" {
		t.Errorf("latest = %q，期望 9.9.9", res.Latest)
	}
	if !res.Available {
		t.Error("9.9.9 应该被认为比 0.1.3 新")
	}
	if !strings.Contains(res.Asset, runtime.GOOS) || !strings.Contains(res.Asset, runtime.GOARCH) {
		t.Errorf("选中的资产名不对: %q", res.Asset)
	}
	if res.Checksum != "ok" {
		t.Errorf("校验和应为 ok，得到 %q", res.Checksum)
	}
}

// 版本号相同 → 不提示更新（这是最常见的误报来源）
func TestProbeEndpointSameVersionNotAvailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"version": "0.1.3", "artifacts": []any{}})
	}))
	defer srv.Close()

	res := Probe(context.Background(),
		config.Update{Enabled: true, Provider: "endpoint", ManifestURL: srv.URL},
		"0.1.3", runtime.GOOS, runtime.GOARCH)
	if res.Available {
		t.Error("同版本不该提示有更新")
	}
	if res.Checksum != "skipped" && res.Checksum != "missing" {
		t.Errorf("没有资产时校验状态应为 skipped/missing，得到 %q", res.Checksum)
	}
}

// 校验和对不上必须能发现 —— 这正是「发布时忘了更新 SHA256SUMS」会出的问题
func TestProbeEndpointDetectsBadDigest(t *testing.T) {
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/a.zip", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("payload")) })
	mux.HandleFunc("/m.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"version": "2.0.0",
			"artifacts": []map[string]any{{
				"url": srv.URL + "/a.zip", "filename": "deskkit-2.0.0.zip",
				"platform": runtime.GOOS, "arch": runtime.GOARCH,
				"digestAlgo": "sha256", "digest": strings.Repeat("0", 64),
			}},
		})
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	res := Probe(context.Background(),
		config.Update{Enabled: true, Provider: "endpoint", ManifestURL: srv.URL + "/m.json"},
		"1.0.0", runtime.GOOS, runtime.GOARCH)
	if !strings.HasPrefix(res.Checksum, "mismatch") {
		t.Errorf("坏的校验和应被识别为 mismatch，得到 %q", res.Checksum)
	}
}

func TestVersionNewer(t *testing.T) {
	cases := []struct {
		remote, current string
		want            bool
	}{
		{"v0.1.4", "0.1.3", true},
		{"0.1.3", "0.1.3", false},
		{"0.1.2", "0.1.3", false},
		{"1.0.0", "0.9.9", true},
		{"v0.2.0", "v0.1.9", true},
		{"0.2.0-rc1", "0.2.0", false}, // 预发布不比正式版新
		{"0.2.0", "0.2.0-rc1", true},  // 正式版比预发布新
		{"", "0.1.3", false},          // 解析不出来就当没有更新，不要误报
		{"garbage", "0.1.3", false},
	}
	for _, c := range cases {
		if got := versionNewer(c.remote, c.current); got != c.want {
			t.Errorf("versionNewer(%q, %q) = %v，期望 %v", c.remote, c.current, got, c.want)
		}
	}
}

// 资产挑选：必须同时含平台与架构，且跳过校验和/签名/安装器
func TestPickAsset(t *testing.T) {
	assets := []ghAsset{
		{Name: "SHA256SUMS"},
		{Name: "deskkit-1.0.0-windows-amd64.zip.asc"},
		{Name: "deskkit-1.0.0-installer.exe"},
		{Name: "deskkit-1.0.0-darwin-arm64.zip"},
		{Name: "deskkit-1.0.0-windows-amd64.zip"},
	}
	if idx := pickAsset(assets, "windows", "amd64"); idx < 0 || assets[idx].Name != "deskkit-1.0.0-windows-amd64.zip" {
		t.Errorf("windows/amd64 挑错了: idx=%d", idx)
	}
	if idx := pickAsset(assets, "darwin", "arm64"); idx < 0 || assets[idx].Name != "deskkit-1.0.0-darwin-arm64.zip" {
		t.Errorf("darwin/arm64 挑错了: idx=%d", idx)
	}
	if idx := pickAsset(assets, "linux", "amd64"); idx != -1 {
		t.Errorf("没有匹配资产时应返回 -1，得到 %d", idx)
	}
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
