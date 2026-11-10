// 本地模拟「GitHub Releases API」的服务，用来调自更新。
//
//	go run ./tools/mock-updater --version 9.9.9 --dir /tmp/sim --port 8790
//
// 它伪装成 api.github.com 的返回结构，所以被调代码走的是**生产同一条路径**：
//
//	GET /repos/{owner}/{repo}/releases/latest
//	GET /{owner}/{repo}/releases/latest/download/{asset}      ← 校验和
//	GET /dl/{file}                                            ← 资产本体
//
// 客户端只需把配置里的 update.baseURL 指到 http://127.0.0.1:8790，
// 于是验证一轮从「构建 → 发版 → 点按钮」变成「起服务 → 一条命令」。
//
// 只用于开发，不进发布物。
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// asset 必须和真实 GitHub 的资产对象一致。
// 踩过一次：只给 browser_download_url，Wails 下载时用的是 url 字段
// （GitHub API 的资产地址），缺了它就报 "release metadata missing asset URL"，
// 而检查更新那一步一切正常 —— 模拟源不够真会掩盖问题。
type asset struct {
	ID                 int    `json:"id"`
	Name               string `json:"name"`
	URL                string `json:"url"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	ContentType        string `json:"content_type"`
}

type release struct {
	HTMLURL     string  `json:"html_url"`
	TagName     string  `json:"tag_name"`
	Name        string  `json:"name"`
	Draft       bool    `json:"draft"`
	Prerelease  bool    `json:"prerelease"`
	Body        string  `json:"body"`
	PublishedAt string  `json:"published_at"`
	Assets      []asset `json:"assets"`
}

func main() {
	var (
		version   = flag.String("version", "9.9.9", "要模拟的最新版本号（不带 v 也行）")
		dir       = flag.String("dir", filepath.Join(os.TempDir(), "deskkit-sim"), "资产与清单目录")
		port      = flag.Int("port", 8790, "监听端口")
		note      = flag.String("notes", "local simulation build", "release notes")
		bind      = flag.String("bind", "0.0.0.0", "监听地址（容器里必须是 0.0.0.0，127.0.0.1 只在本机回环可见）")
		publicURL = flag.String("public-url", "", "资产下载地址前缀（容器里要填 http://<局域网IP>:<端口>）")
		appName   = flag.String("app", "deskkit", "产物文件名前缀（应用名）")
		plats     = flag.String("platforms", "windows/amd64,darwin/amd64,darwin/arm64",
			"要模拟产物的平台/架构，逗号分隔（容器里 GOOS 是 linux，不能靠 runtime 猜）")
	)
	flag.Parse()

	tag := *version
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	ver := strings.TrimPrefix(tag, "v")

	if err := os.MkdirAll(*dir, 0o755); err != nil {
		log.Fatalf("建目录失败: %v", err)
	}

	// 每个平台一个真的 zip：内容随意，但文件名与校验和必须对得上
	base := strings.TrimRight(*publicURL, "/")
	if base == "" {
		base = fmt.Sprintf("http://127.0.0.1:%d", *port)
	}

	type built struct {
		Name string
		Sum  string
		Size int64
		Ext  string
	}
	var assets []built
	var sums []string
	for _, spec := range strings.Split(*plats, ",") {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			continue
		}
		parts := strings.Split(spec, "/")
		if len(parts) != 2 {
			log.Fatalf("--platforms 里 %q 格式应为 平台/架构", spec)
		}
		// 每个平台出两种包装，和真实发布保持一致：
		//   .zip  给自更新（Wails updater 只认 zip/tar.gz）
		//   .dmg  给 macOS 用户手动下载安装
		exts := []string{".zip"}
		if parts[0] == "darwin" {
			exts = append(exts, ".dmg")
		}
		for _, ext := range exts {
			name := fmt.Sprintf("%s-%s-%s-%s%s", *appName, ver, parts[0], parts[1], ext)
			path := filepath.Join(*dir, name)
			// 目录里已有同名文件就直接用它 —— 这是做「真升级演练」的方式：
			// 放一个真的安装包进去（比如当前的 Windows zip），模拟源照它发，
			// 客户端会真的下载、校验、解压、替换，而不是拿到一个假文本。
			if _, statErr := os.Stat(path); statErr != nil {
				payload := []byte("mock artifact " + name + " generated " + time.Now().Format(time.RFC3339))
				if err := os.WriteFile(path, payload, 0o644); err != nil {
					log.Fatalf("写资产失败: %v", err)
				}
			}
			sum, size := fileSHA256(path)
			assets = append(assets, built{Name: name, Sum: sum, Size: size, Ext: ext})
			sums = append(sums, sum+"  "+name)
		}
	}
	if len(assets) == 0 {
		log.Fatal("没有可用资产，检查 --platforms")
	}
	sumsBody := strings.Join(sums, "\n") + "\n"

	ghAssets := make([]asset, 0, len(assets))
	for _, a := range assets {
		ghAssets = append(ghAssets, asset{
			ID:                 len(ghAssets) + 1,
			Name:               a.Name,
			URL:                base + "/dl/" + a.Name,
			BrowserDownloadURL: base + "/dl/" + a.Name,
			Size:               a.Size,
			ContentType:        contentType(a.Ext),
		})
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Logger())

	// ① GitHub 风格：最新 release
	r.GET("/repos/:owner/:repo/releases/latest", func(c *gin.Context) {
		c.JSON(http.StatusOK, release{
			HTMLURL:     base + "/releases/tag/" + tag,
			TagName:     tag,
			Name:        "DeskKit Excel " + tag,
			Body:        *note,
			PublishedAt: time.Now().UTC().Format(time.RFC3339),
			Assets:      ghAssets,
		})
	})

	// ② GitHub 风格：兄弟资产（校验和）
	r.GET("/:owner/:repo/releases/latest/download/:asset", func(c *gin.Context) {
		if c.Param("asset") != "SHA256SUMS" {
			c.String(http.StatusNotFound, "only SHA256SUMS is simulated")
			return
		}
		c.String(http.StatusOK, sumsBody)
	})

	// ③ 资产本体
	r.GET("/dl/:file", func(c *gin.Context) {
		p := filepath.Join(*dir, filepath.Base(c.Param("file")))
		if _, err := os.Stat(p); err != nil {
			c.String(http.StatusNotFound, "no such file")
			return
		}
		c.File(p)
	})

	// ④ 人看的首页，方便确认服务活着
	r.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, "mock updater\n  version: %s\n  assets:\n    %s\n  base:    %s\n",
			tag, strings.Join(func() []string {
				out := make([]string, 0, len(assets))
				for _, a := range assets {
					out = append(out, fmt.Sprintf("%s  sha256=%s", a.Name, a.Sum))
				}
				return out
			}(), "\n    "), base)
	})

	log.Printf("mock updater: %s @ %s（%d 个平台资产）", tag, base, len(assets))
	log.Printf("客户端配置： update.baseURL: %q , update.repository: \"klphp/deskkit-excel\"", base)
	addr := fmt.Sprintf("%s:%d", *bind, *port)
	log.Printf("listening on %s", addr)
	if err := r.Run(addr); err != nil {
		log.Fatal(err)
	}
}

// contentType 让模拟源的响应头与真实发布一致：
// 下载 dmg 时浏览器按 application/x-apple-diskimage 处理。
func contentType(ext string) string {
	if ext == ".dmg" {
		return "application/x-apple-diskimage"
	}
	return "application/zip"
}

func fileSHA256(path string) (string, int64) {
	f, err := os.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		log.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil)), n
}
