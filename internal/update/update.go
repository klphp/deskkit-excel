// Package update 封装 Wails v3 内置的 app.Updater，按平台给出不同的更新体验。
//
// 职责边界很窄：它不做下载、不做验签、不做替换 —— 那些都在 Wails 的
// updater 里。这里只负责三件事：
//
//  1. 按配置选 provider（github / endpoint）
//  2. 决定「查到了怎么办」：Windows 自动装，macOS 交给用户自己下
//  3. 把结果翻译成前端能用的最小结构
//
// 为什么 macOS 不用自动装：
//
//	Wails 的替换流程只认 .zip（内含 .app），不支持 .dmg / .pkg。而 dmg 是
//	macOS 的分发惯例，用户也更信 dmg。加上 macOS 的 Gatekeeper 要求签名
//	端到端保持，自动替换换出来的二进制容易被拦。让用户自己下 dmg 拖进
//	Applications，反而更省事、更符合那边用户的习惯。
package update

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/endpoint"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"

	"github.com/klphp/excel-batch-tool/internal/config"
)

// ErrNetwork 是「网络不通」的统一出口。
//
// 为什么要自己分类：Wails 的 updater 没有定义网络错误类型，net/http 的
// *url.Error / *net.OpError / *net.DNSError 会原样冒到调用方。直接把那些
// 原始错误丢给用户，看到的是
//
//	Get "https://api.github.com/...": dial tcp: lookup api.github.com: no such host
//
// 这既看不懂也不知道该做什么。统一换成一句能照着做的话。
var ErrNetwork = errors.New("网络连接失败，请检查网络后重试")

// isNetworkError 判断错误是不是出在网络上（而不是配置、签名、磁盘）。
//
// 这几种都要认出：DNS 解析失败（改过 hosts / 断网 / 被投毒）、拨号失败
// （防火墙 / 无路由）、超时、以及 net/http 会把它们包一层的 *url.Error。
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}
	var (
		dnsErr *net.DNSError
		netErr net.Error
		urlErr *url.Error
		opErr  *net.OpError
	)
	if errors.As(err, &dnsErr) || errors.As(err, &opErr) {
		return true
	}
	if errors.As(err, &netErr) {
		return true
	}
	// url.Error 通常已经在上面被拆开了，但有些路径只有它
	if errors.As(err, &urlErr) {
		return true
	}
	return false
}

// Result 是交给前端的最小信息集。
//
// 不把 updater.Release 整个丢给前端：它带 Metadata（含 API URL、asset id）
// 和 Verification（digest 原始字节），传过去既没用又会泄漏发布细节。
type Result struct {
	Available  bool   `json:"available"`
	Version    string `json:"version"`
	Notes      string `json:"notes"`
	ReleaseURL string `json:"releaseUrl"`
	// AutoInstall 表示当前平台支持「点一下就自动装好」。
	// Windows 为 true（rename-aside 原子替换 + helper 重启）；
	// macOS 为 false，前端应把按钮文案改成「前往下载」。
	AutoInstall bool `json:"autoInstall"`

	// ArtifactSize 是安装包字节数，来自 release 元数据，用于推算下载超时。
	ArtifactSize int64 `json:"artifactSize"`
}

// Manager 持有 Updater 配置和最近一次 Check 的结果。
type Manager struct {
	app            *application.App
	cfg            config.Update
	currentVersion string
	fallbackURL    string

	mu   sync.Mutex
	last *Result
}

// New 构造 Manager。currentVersion 应与打包版本号一致（cfg.App.Version），
// 因为 updater 拿它和 release tag 比对 —— 注意 tag 上的 v 前缀由 provider
// 自己剥掉，这里不要带 v。
func New(app *application.App, cfg config.Update, currentVersion, fallbackURL string) *Manager {
	return &Manager{
		app:            app,
		cfg:            cfg,
		currentVersion: currentVersion,
		fallbackURL:    fallbackURL,
	}
}

// Setup 构造 provider 并初始化 app.Updater。必须在 application.New 之后调用。
// 未启用时直接返回，不留任何副作用。
func (m *Manager) Setup() error {
	if !m.cfg.Enabled {
		return nil
	}

	var (
		prov updater.Provider
		err  error
	)
	switch m.cfg.Provider {
	case "github", "":
		if m.cfg.Repository == "" {
			return fmt.Errorf("update: provider=github 但 repository 为空")
		}
		prov, err = github.New(github.Config{
			Repository:    m.cfg.Repository,
			ChecksumAsset: m.cfg.ChecksumAsset,
			Prerelease:    m.cfg.Prerelease,
		})
	case "endpoint":
		if m.cfg.ManifestURL == "" {
			return fmt.Errorf("update: provider=endpoint 但 manifestUrl 为空")
		}
		prov, err = endpoint.New(endpoint.Config{URL: m.cfg.ManifestURL})
	default:
		return fmt.Errorf("update: 未知 provider %q（只支持 github / endpoint）", m.cfg.Provider)
	}
	if err != nil {
		return fmt.Errorf("update: 构造 provider 失败: %w", err)
	}

	ucfg := updater.Config{
		CurrentVersion: m.currentVersion,
		Providers:      []updater.Provider{prov},
	}
	// 只有配了公钥才验签。留空时 Wails 允许只带 digest 的 release 安装，
	// 但任何带 Signature 的 release 会被拒 —— 这是它的规定行为，够用。
	if m.cfg.PublicKey != "" {
		ucfg.PublicKey = []byte(m.cfg.PublicKey)
	}

	if err := m.app.Updater.Init(ucfg); err != nil {
		return fmt.Errorf("update: 初始化 Updater 失败: %w", err)
	}

	slog.Info("自更新已启用",
		"provider", m.cfg.Provider,
		"current", m.currentVersion,
		"验签", m.cfg.PublicKey != "")
	return nil
}

// Enabled 报告自更新是否启用。
func (m *Manager) Enabled() bool { return m.cfg.Enabled }

// Check 只检查，不下载、不安装。这是「用户自己决定」的关键 ——
// 前端拿到结果后可以只显示一个提示条，用户点了才走 Install。
func (m *Manager) Check(ctx context.Context) (*Result, error) {
	if !m.cfg.Enabled {
		return &Result{}, nil
	}

	rel, err := m.app.Updater.Check(ctx)
	if err != nil {
		// 网络不通就到此为止：不上报「无更新」、不提示「已是最新」，
		// 那种结论在断网时是错的，会让用户以为版本检查成功了。
		if isNetworkError(err) {
			slog.Debug("检查更新时网络不通", "err", err)
			return nil, ErrNetwork
		}
		return nil, err
	}
	if rel == nil {
		return &Result{Available: false}, nil
	}

	res := &Result{
		Available:    true,
		Version:      rel.Version,
		Notes:        rel.Notes,
		ReleaseURL:   m.releaseURL(rel),
		AutoInstall:  runtime.GOOS == "windows",
		ArtifactSize: rel.Artifact.Size,
	}

	m.mu.Lock()
	m.last = res
	m.mu.Unlock()

	return res, nil
}

// Last 返回最近一次成功检查的结果，没有则返回 nil。
func (m *Manager) Last() *Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.last
}

// Install 执行更新。
//
// Windows：让 Wails 下载 + 校验 + 原子替换，然后重启。用户看到的是
// 「更新并重启」一个动作，之后不再有 SmartScreen 警告 —— 因为替换出来的
// 文件是 Go 写的，不带浏览器下载才有的 MOTW 标记。
//
// 其它平台：打开 release 页面让用户自己下。原因见包注释。
// 下载超时的推算依据。
//
// 不写死时长的理由：包大小和用户网速都是未知量。7 MB 的包给 5 分钟够用，
// 但一个带模型/字体/媒体资源的 50 MB 包按同样预算必然超时。
//
// 30 KB/s 这个下限来自实测：国内直连 GitHub 的速率在 151~431 KB/s 之间波动，
// 取 30 KB/s 意味着即使掉到常态速度的 1/5 也能把包下完。
const (
	minDownloadSpeed     = 30 * 1024 // 推定超时时假设的最慢速度
	downloadTimeoutSlack = 1.5       // 再留的余量，吸收波动与 TCP 重传
	minDownloadTimeout   = 2 * time.Minute
	maxDownloadTimeout   = 40 * time.Minute
	fallbackDownloadTime = 5 * time.Minute // 拿不到包大小时的兜底
)

// downloadTimeout 按安装包大小推算一个宽容的下载超时。
//
// size 来自 release 元数据的 Artifact.Size；拿不到（老 release、endpoint
// provider 没给）就退回固定值。
func downloadTimeout(size int64) time.Duration {
	if size <= 0 {
		return fallbackDownloadTime
	}
	base := time.Duration(float64(size)/float64(minDownloadSpeed)) * time.Second
	d := time.Duration(float64(base) * downloadTimeoutSlack)
	if d < minDownloadTimeout {
		d = minDownloadTimeout
	}
	if d > maxDownloadTimeout {
		d = maxDownloadTimeout
	}
	return d
}

func (m *Manager) Install(ctx context.Context) error {
	if !m.cfg.Enabled {
		return fmt.Errorf("update: 自更新未启用")
	}

	// 下载预算按包大小推算，不写死。
	//
	// 踩过的坑：调用方图省事，Check 和 Install 共用一个 20 秒的 ctx ——
	// 查一次 GitHub API 20 秒够用，但 Install 要下载整个安装包。实测同一
	// 网络下 7 MB 的包：macOS 16 秒、Windows 34 秒、最慢一次 47 秒。于是
	// 每次都在下载途中被取消，报 context deadline exceeded。
	size := int64(0)
	if r := m.Last(); r != nil {
		size = r.ArtifactSize
	}
	budget := downloadTimeout(size)

	// WithoutCancel 摘掉上层取消信号（保留 ctx 里的值），超时只由 budget 决定。
	installCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), budget)
	defer cancel()

	slog.Info("下载预算", "artifactSizeBytes", size, "timeoutSec", int(budget.Seconds()))

	if runtime.GOOS == "windows" {
		slog.Info("开始自动更新", "currentVersion", m.currentVersion)

		if err := m.app.Updater.DownloadAndInstall(installCtx); err != nil {
			// 下载失败必须在这里断掉：后面的 Restart 会把应用关掉再拉起来，
			// 而磁盘上根本没有可用的新二进制 —— 用户会看到应用退出后
			// 原地重启，什么也没更新，还以为已经升上去了。
			if isNetworkError(err) {
				// 超时和断网要分开说：前者是网速慢（GitHub 在国内不稳），
				// 后者是真连不通，给用户下一步动作完全不同。
				msg := err.Error()
				if errors.Is(err, context.DeadlineExceeded) || strings.Contains(msg, "deadline exceeded") {
					slog.Warn("下载更新包超时", "err", err)
					return fmt.Errorf("下载超时。安装包通常 5~10 MB，网络较慢时可稍后重试，或手动到发布页下载：%s", m.fallbackURL)
				}
				slog.Warn("下载更新包失败：网络不通", "err", err)
				return ErrNetwork
			}
			slog.Warn("自动安装失败", "err", err, "errType", fmt.Sprintf("%T", err))
			return err
		}

		slog.Info("下载与替换完成，准备重启")
		if err := m.app.Updater.Restart(ctx); err != nil {
			slog.Warn("重启失败", "err", err)
			return fmt.Errorf("更新包已就位但重启失败，请手动关闭程序后再打开：%w", err)
		}
		slog.Info("重启指令已发出")
		return nil
	}

	// macOS / Linux：走浏览器。优先用刚查到的 release 页面，
	// 没有就退回配置里的固定地址。
	url := m.fallbackURL
	if r := m.Last(); r != nil && r.ReleaseURL != "" {
		url = r.ReleaseURL
	}
	if url == "" {
		return fmt.Errorf("update: 没有可用的下载地址（检查配置里是否填了 repository 或 manifestUrl）")
	}
	return m.app.Browser.OpenURL(url)
}

// StartPeriodic 在后台按间隔检查，发现新版本时回调。
//
// 自己起 goroutine 而不用 updater.Config.CheckInterval：那个选项内部走的是
// CheckAndInstall，会直接把窗口弹出来并自动安装，违背「不强制」的定位。
func (m *Manager) StartPeriodic(ctx context.Context, interval time.Duration, onFound func(*Result)) {
	if !m.cfg.Enabled || interval <= 0 {
		return
	}

	go func() {
		// 等界面和本地服务稳定下来，避免启动瞬间抢资源
		select {
		case <-ctx.Done():
			return
		case <-time.After(8 * time.Second):
		}

		for {
			res, err := m.Check(ctx)
			switch {
			case err != nil:
				// 网络不通、GitHub 限速都是常态，降级成 debug 日志，
				// 别在用户日志里刷错误
				slog.Debug("检查更新失败", "err", err)
			case res.Available:
				slog.Info("发现新版本", "version", res.Version)
				if onFound != nil {
					onFound(res)
				}
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(interval):
			}
		}
	}()
}

// releaseURL 从 Release 的 metadata 里取发布页地址。
// github provider 会塞 github.release.htmlURL；endpoint 没有这个约定，
// 取不到就退回配置的 fallback。
func (m *Manager) releaseURL(rel *updater.Release) string {
	if rel != nil && rel.Metadata != nil {
		if u, ok := rel.Metadata["github.release.htmlURL"].(string); ok && u != "" {
			return u
		}
	}
	return m.fallbackURL
}
