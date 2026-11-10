package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/klphp/deskkit-excel/internal/config"
)

// 用户明确的平台分工：
//   Windows —— 全自动：下载、校验、替换运行中的 exe、重启，一次点击搞定
//   macOS   —— 给他一个 .dmg 直链，浏览器下载、双击运行，后面用户自己弄
//
// 这几条测试守住 macOS 那一半：下载目标必须是 dmg，不能退回 zip（zip 是给
// 更新器用的，人手点开只会得到一个内部目录结构），也不能悄悄变成发布页
// 让用户在多个文件里挑。

func TestManualDownloadURLPrefersDMG(t *testing.T) {
	res := &Result{
		DownloadURL: "https://example.com/deskkit-0.2.0-darwin-amd64.dmg",
		ReleaseURL:  "https://example.com/releases/tag/v0.2.0",
	}
	got := manualDownloadURL(res, "https://example.com/fallback")
	if !strings.HasSuffix(got, ".dmg") {
		t.Fatalf("应优先用 dmg 直链，实际: %q", got)
	}
}

func TestManualDownloadURLFallsBackToReleasePage(t *testing.T) {
	res := &Result{ReleaseURL: "https://example.com/releases/tag/v0.2.0"}
	got := manualDownloadURL(res, "https://example.com/fallback")
	if got != res.ReleaseURL {
		t.Fatalf("没有 dmg 时应退到发布页，实际: %q", got)
	}
}

func TestManualDownloadURLFallsBackToConfig(t *testing.T) {
	if got := manualDownloadURL(&Result{}, "https://example.com/fb"); got != "https://example.com/fb" {
		t.Fatalf("都没有时应退到配置地址，实际: %q", got)
	}
	if got := manualDownloadURL(nil, "https://example.com/fb"); got != "https://example.com/fb" {
		t.Fatalf("结果为 nil 时也应退到配置地址，实际: %q", got)
	}
}

func TestPickDMG(t *testing.T) {
	assets := []ghAsset{
		{Name: "deskkit-0.2.0-windows-amd64.zip"},
		{Name: "deskkit-0.2.0-darwin-amd64.zip"},
		{Name: "deskkit-0.2.0-darwin-arm64.zip"},
		{Name: "deskkit-0.2.0-darwin-amd64.dmg"},
		{Name: "deskkit-0.2.0-darwin-arm64.dmg"},
	}

	cases := []struct{ arch, want string }{
		{"amd64", "deskkit-0.2.0-darwin-amd64.dmg"},
		{"arm64", "deskkit-0.2.0-darwin-arm64.dmg"},
	}
	for _, c := range cases {
		i := pickDMG(assets, "darwin", c.arch)
		if i < 0 {
			t.Fatalf("%s 没找到 dmg", c.arch)
		}
		if assets[i].Name != c.want {
			t.Fatalf("%s 挑错文件：%s（期望 %s）", c.arch, assets[i].Name, c.want)
		}
	}

	// 架构必须匹配：不能把 arm64 的 dmg 给 Intel 机器，装上跑不起来
	if i := pickDMG(assets, "darwin", "386"); i >= 0 {
		t.Fatalf("不该为不存在的架构挑出资产，却挑到 %s", assets[i].Name)
	}
	// 只有 zip 时必须明确给不出 dmg（让上层退到发布页），绝不能返回 zip
	zipOnly := []ghAsset{{Name: "deskkit-0.2.0-darwin-amd64.zip"}}
	if i := pickDMG(zipOnly, "darwin", "amd64"); i >= 0 {
		t.Fatalf("只有 zip 时不该返回任何资产，却返回 %s", zipOnly[i].Name)
	}
}

// DMGURL 走真实 HTTP 路径（模拟源）。这条覆盖「查最新 release → 挑 dmg → 拿直链」
// 整条链路，包括 BaseURL 指向内网模拟源的情况。
func TestDMGURLFromMockServer(t *testing.T) {
	srv := mockReleaseServer(t, `{"tag_name":"v9.9.9","assets":[
		{"name":"deskkit-9.9.9-windows-amd64.zip","browser_download_url":"http://x/w.zip","size":1},
		{"name":"deskkit-9.9.9-darwin-amd64.zip","browser_download_url":"http://x/m.zip","size":1},
		{"name":"deskkit-9.9.9-darwin-amd64.dmg","browser_download_url":"http://x/m.dmg","size":1}]}`)

	cfg := config.Update{Enabled: true, Provider: "github", Repository: "klphp/deskkit-excel", BaseURL: srv}
	got := DMGURL(context.Background(), cfg, "darwin", "amd64")
	if !strings.HasSuffix(got, ".dmg") {
		t.Fatalf("应拿到 dmg 直链，实际: %q", got)
	}
	// Windows 平台不该有 dmg 可挑
	if u := DMGURL(context.Background(), cfg, "windows", "amd64"); u != "" {
		t.Fatalf("windows 不该返回 dmg，实际: %q", u)
	}
}

// mockReleaseServer 起一个只回 /repos/o/r/releases/latest 的假 GitHub。
// body 就是 release JSON 原文。
func mockReleaseServer(t *testing.T, body string) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/klphp/deskkit-excel/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}
