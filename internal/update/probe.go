package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/klphp/deskkit-excel/internal/config"
)

// Probe 是给「命令行自检」用的纯 Go 实现：不发 GUI、不安装，只回答四个问题：
//
//	① 远端最新版本是多少？
//	② 比当前版本新吗？
//	③ 会挑哪个资产文件？
//	④ 校验和对得上吗？
//
// 为什么要它：自更新这条链路的验证过去必须「改代码 → CI 构建 → 发 Release →
// 开窗口点按钮」。实测一个下午只跑通了三四轮。有了 Probe，同一轮验证变成
// 一条命令，几秒出结果；CI 里也能跑。
//
// 它与 Wails updater 的判断口径保持一致（同一个 /releases/latest、同样的
// tag 比较、同样的「文件名含平台+架构」选包规则），但**不覆盖**下载安装与
// 重启替换那一段——那段仍需真机点一次。
type ProbeResult struct {
	Provider  string `json:"provider"`
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
	Available bool   `json:"available"`
	Asset     string `json:"asset,omitempty"`
	AssetURL  string `json:"assetUrl,omitempty"`
	Checksum  string `json:"checksum,omitempty"` // ok | missing | mismatch | skipped
	Err       string `json:"error,omitempty"`
}

// Probe 查询更新源。platform/arch 传 runtime.GOOS / runtime.GOARCH。
func Probe(ctx context.Context, cfg config.Update, current, platform, arch string) ProbeResult {
	res := ProbeResult{Provider: cfg.Provider, Current: current, Checksum: "skipped"}

	if !cfg.Enabled {
		res.Err = "update disabled in config"
		return res
	}

	var latest, assetURL, assetName, wantDigest string
	switch cfg.Provider {
	case "github":
		if cfg.Repository == "" {
			res.Err = "repository is empty"
			return res
		}
		latest, assetName, assetURL, wantDigest, res.Err = probeGitHub(ctx, cfg, platform, arch)
	case "endpoint":
		if cfg.ManifestURL == "" {
			res.Err = "manifestUrl is empty"
			return res
		}
		latest, assetName, assetURL, wantDigest, res.Err = probeEndpoint(ctx, cfg, platform, arch)
	default:
		res.Err = fmt.Sprintf("unknown provider %q", cfg.Provider)
		return res
	}
	if res.Err != "" {
		return res
	}

	res.Latest = latest
	res.Asset = assetName
	res.AssetURL = assetURL
	res.Available = versionNewer(latest, current)

	if assetURL == "" {
		res.Checksum = "missing"
		return res
	}
	// 校验和：github 用兄弟资产 SHA256SUMS，endpoint 用清单里的 digest
	if cfg.ChecksumAsset != "" && cfg.Provider == "github" {
		want, err := fetchSiblingChecksum(ctx, cfg, assetName)
		if err != nil || want == "" {
			res.Checksum = "missing"
			return res
		}
		wantDigest = want
	}
	if wantDigest == "" {
		res.Checksum = "missing"
		return res
	}
	got, err := downloadDigest(ctx, assetURL)
	if err != nil {
		res.Checksum = "skipped"
		res.Err = "download failed: " + err.Error()
		return res
	}
	if strings.EqualFold(got, wantDigest) {
		res.Checksum = "ok"
	} else {
		res.Checksum = fmt.Sprintf("mismatch (want %s, got %s)", wantDigest, got)
	}
	return res
}

func httpClient() *http.Client { return &http.Client{Timeout: 60 * time.Second} }

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}
type ghRelease struct {
	TagName    string    `json:"tag_name"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Assets     []ghAsset `json:"assets"`
}

func probeGitHub(ctx context.Context, cfg config.Update, platform, arch string) (latest, assetName, assetURL, digest, errMsg string) {
	apiBase := strings.TrimRight(cfg.BaseURL, "/")
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	endpoint := apiBase + "/repos/" + cfg.Repository + "/releases/latest"
	if cfg.Prerelease {
		endpoint = apiBase + "/repos/" + cfg.Repository + "/releases?per_page=10"
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "deskkit-update-probe")
	resp, err := httpClient().Do(req)
	if err != nil {
		errMsg = "network: " + err.Error()
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	switch {
	case resp.StatusCode == 403 || strings.Contains(string(body), "rate limit"):
		errMsg = "rate_limited"
		return
	case resp.StatusCode == 404:
		errMsg = "no_release"
		return
	case resp.StatusCode != 200:
		errMsg = fmt.Sprintf("github api %d", resp.StatusCode)
		return
	}

	var rel ghRelease
	if cfg.Prerelease {
		var list []ghRelease
		if e := json.Unmarshal(body, &list); e != nil || len(list) == 0 {
			errMsg = "no_release"
			return
		}
		rel = list[0]
	} else if e := json.Unmarshal(body, &rel); e != nil {
		errMsg = "decode failed: " + e.Error()
		return
	}

	latest = rel.TagName
	idx := pickAsset(rel.Assets, platform, arch)
	if idx >= 0 {
		assetName = rel.Assets[idx].Name
		assetURL = rel.Assets[idx].BrowserDownloadURL
	}
	return
}

// pickAsset 复刻 Wails DefaultAssetMatcher 的核心规则：
// 文件名（小写）同时含平台名与架构名，且跳过 .sig/.asc/installer 等附属文件。
func pickAsset(assets []ghAsset, platform, arch string) int {
	plat := strings.ToLower(platform)
	arch = strings.ToLower(arch)
	if plat == "darwin" {
		plat = "darwin" // 不用 macos 别名，产物名统一写 darwin
	}
	for i, a := range assets {
		name := strings.ToLower(a.Name)
		if strings.HasSuffix(name, ".sig") || strings.HasSuffix(name, ".asc") {
			continue
		}
		if strings.Contains(name, "-installer.") || strings.HasSuffix(name, ".sha256") || name == "sha256sums" {
			continue
		}
		if strings.Contains(name, plat) && strings.Contains(name, arch) {
			return i
		}
	}
	return -1
}

func fetchChecksumAt(ctx context.Context, url, assetName string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "deskkit-update-probe")
	resp, err := httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("checksum asset %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return parseChecksum(body, assetName), nil
}

func parseChecksum(body []byte, assetName string) string {
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.Contains(fields[len(fields)-1], assetName) {
			return fields[0]
		}
	}
	return ""
}

func fetchSiblingChecksum(ctx context.Context, cfg config.Update, assetName string) (string, error) {
	// 官方 GitHub 的校验和走 github.com/.../releases/latest/download/；
	// 模拟服务与 GitHub API 同源，所以用 baseURL 拼接。
	if b := strings.TrimRight(cfg.BaseURL, "/"); b != "" {
		return fetchChecksumAt(ctx, b+"/"+cfg.Repository+"/releases/latest/download/"+cfg.ChecksumAsset, assetName)
	}
	url := fmt.Sprintf("https://github.com/%s/releases/latest/download/%s", cfg.Repository, cfg.ChecksumAsset)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "deskkit-update-probe")
	resp, err := httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("checksum asset %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.Contains(fields[len(fields)-1], assetName) {
			return fields[0], nil
		}
	}
	return "", nil
}

type epArtifact struct {
	URL        string `json:"url"`
	Filename   string `json:"filename"`
	Platform   string `json:"platform"`
	Arch       string `json:"arch"`
	DigestAlgo string `json:"digestAlgo"`
	Digest     string `json:"digest"`
}
type epManifest struct {
	Version   string       `json:"version"`
	Artifacts []epArtifact `json:"artifacts"`
}

func probeEndpoint(ctx context.Context, cfg config.Update, platform, arch string) (latest, assetName, assetURL, digest, errMsg string) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, cfg.ManifestURL, nil)
	resp, err := httpClient().Do(req)
	if err != nil {
		errMsg = "network: " + err.Error()
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		errMsg = fmt.Sprintf("manifest %d", resp.StatusCode)
		return
	}
	var m epManifest
	if e := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&m); e != nil {
		errMsg = "manifest decode failed: " + e.Error()
		return
	}
	latest = m.Version
	for _, a := range m.Artifacts {
		if strings.EqualFold(a.Platform, platform) && strings.EqualFold(a.Arch, arch) {
			assetName, assetURL = a.Filename, a.URL
			if strings.EqualFold(a.DigestAlgo, "sha256") {
				digest = a.Digest
			}
			break
		}
	}
	return
}

func downloadDigest(ctx context.Context, url string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "deskkit-update-probe")
	resp, err := httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download %d", resp.StatusCode)
	}
	h := sha256.New()
	if _, err := io.Copy(h, resp.Body); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// versionNewer 判断 remote 是否比 current 新。只处理 x.y.z（可带 v 前缀），
// 带预发布后缀的一律视为比同版本正式版旧——与 semver 的直觉一致。
func versionNewer(remote, current string) bool {
	r, rok := parseVersion(remote)
	c, cok := parseVersion(current)
	if !rok || !cok {
		return false
	}
	for i := 0; i < 3; i++ {
		if r.num[i] != c.num[i] {
			return r.num[i] > c.num[i]
		}
	}
	// 数字相同：remote 有预发布后缀而 current 没有 → 不算更新
	if r.pre != c.pre {
		return !r.pre
	}
	return false
}

type version struct {
	num [3]int
	pre bool
}

func parseVersion(s string) (version, bool) {
	v := version{}
	s = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(s), "v"))
	if s == "" {
		return v, false
	}
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		v.pre = true
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return v, false
	}
	for i := 0; i < 3 && i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return v, false
		}
		v.num[i] = n
	}
	return v, true
}
