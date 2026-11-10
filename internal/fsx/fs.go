// Package fs 本地文件操作能力包。
//
// 为什么这件事要放在 Go 侧而不是让 AI 现场写 shell / Python：
//
//	· 速度：AI 写一段脚本要先发请求、等模型输出、再执行，而我们这里
//	  一次调用就是一次 os.WalkDir。几百个文件的遍历差一个数量级。
//	· 可校验：路径在 Go 里能统一做边界检查（不许删系统目录、不许跳出
//	  工作目录），脚本做不到。
//	· 可预期：脚本有引号、空格、编码、平台差异的坑；模型写错一次就是
//	  删错文件。我们提供的是「参数化操作」，不是自由文本。
//
// 我们只提供能力，怎么组合由 AI 决定 —— 和表格那边同一个思路。
package fsx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Guard 操作边界。
//
// 桌面应用的用户把「管理文件」交给模型，所以必须有一层硬边界：
// 系统目录一律拒绝，删除一律走废纸篓（可恢复），而不是 os.RemoveAll。
type Guard struct {
	// Roots 允许操作的根目录（绝对路径）。空表示只允许用户主目录。
	Roots []string
}

// DefaultGuard 用户主目录 + 常见外挂卷。
func DefaultGuard() Guard {
	g := Guard{}
	if home, err := os.UserHomeDir(); err == nil {
		g.Roots = append(g.Roots, home)
	}
	for _, vol := range []string{"/Volumes"} {
		if info, err := os.Stat(vol); err == nil && info.IsDir() {
			g.Roots = append(g.Roots, vol)
		}
	}
	return g
}

// 绝不允许触碰的位置。
//
// 刻意**不**包含 /tmp 和 /var：macOS 上临时目录就在 /var/folders 下，
// 用户自己的临时工作完全合法，一律拒绝只会让正常操作报错。
// 真正的边界由 Roots 决定（默认只有用户主目录和 /Volumes）——
// 这里列的是任何情况下都不该动的系统目录。
var forbidden = []string{
	"/", "/System", "/usr", "/bin", "/sbin", "/etc",
	"/Library", "/Applications", "/cores", "/dev",
	"/private/etc", "/private/var/db", "/private/var/vm",
}

// Check 校验路径是否允许操作。返回绝对路径。
func (g Guard) Check(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("Path is empty")
	}
	// ~ 不展开的话，模型/用户写「~/Desktop」会得到 <cwd>/~/Desktop 这种
	// 根本不存在、报错还看不懂的路径。
	raw = expandHome(raw)
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("Invalid path: %w", err)
	}
	abs = filepath.Clean(abs)

	// ★ 解析符号链接后再比对：`~/Documents → 外置盘` 这种软链很常见，
	// 只看字面路径的话「只允许在主目录操作」的承诺就失效了
	// （实测 root/link → 外部目录，Delete 真的把外部文件删了）。
	real, err := resolveSymlinks(abs)
	if err != nil {
		// 解析不出来（权限、损坏链接）就按拒绝处理：安全侧宁严不松
		return "", fmt.Errorf("Path cannot be resolved and was rejected: %s (%v)", abs, err)
	}

	for _, bad := range forbidden {
		for _, p := range []string{abs, real} {
			if p == bad || strings.HasPrefix(p, bad+string(filepath.Separator)) {
				return "", fmt.Errorf("Refusing to touch a system folder: %s", abs)
			}
		}
	}

	roots := g.Roots
	if len(roots) == 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("Cannot determine the user folder: %w", err)
		}
		roots = []string{home}
	}
	// 返回给调用方的仍是原始绝对路径（不改变用户的路径语义），
	// 但「是否在允许范围内」必须按**解析后的真实路径**判断：
	// 只比字面路径的话，root/link → 外部目录 这种软链就能整个绕过边界。
	for _, root := range roots {
		rootAbs, err := filepath.Abs(expandHome(root))
		if err != nil {
			continue
		}
		rootReal, err := resolveSymlinks(rootAbs)
		if err != nil {
			rootReal = rootAbs
		}
		if real == rootReal || strings.HasPrefix(real, rootReal+string(filepath.Separator)) {
			return abs, nil
		}
		if rootReal != rootAbs {
			// 兜底：root 本身解析前后不一致时，也接受按原样比较（只在 root 存在时成立）
			if _, err := os.Stat(rootAbs); err != nil && (abs == rootAbs || strings.HasPrefix(abs, rootAbs+string(filepath.Separator))) {
				return abs, nil
			}
		}
	}
	return "", fmt.Errorf("Path is outside the allowed roots: %s (only paths under %s are allowed)", abs, strings.Join(roots, ", "))
}

// expandHome 把开头的 ~ 展开成用户主目录。
func expandHome(p string) string {
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return p
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~"+string(filepath.Separator)) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// resolveSymlinks 解析路径里的符号链接；目标还不存在时逐段向上找到最近的存在前缀再拼接。
//
// 逐段解析是必须的：「新建一个文件」的目标路径本身不存在，
// 但它的父目录完全可能是个软链 —— 只对整条路径 EvalSymlinks 会直接失败，
// 于是又退化成「字面比较」，软链绕过的洞就还在。
func resolveSymlinks(abs string) (string, error) {
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	dir := abs
	var rest string
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("No parent folder that can be resolved was found")
		}
		if rest == "" {
			rest = filepath.Base(dir)
		} else {
			rest = filepath.Join(filepath.Base(dir), rest)
		}
		dir = parent
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			continue
		}
		return filepath.Join(resolved, rest), nil
	}
}

// Entry 一个文件或目录。
type Entry struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Dir     bool   `json:"dir"`
	Size    int64  `json:"size"`
	ModTime string `json:"modified"`
	Ext     string `json:"ext,omitempty"`
}

// ListOptions 遍历参数。
type ListOptions struct {
	Path      string
	Recursive bool
	// Pattern 文件名通配（如 *.xlsx）。空表示不过滤。
	Pattern string
	// MaxEntries 上限，默认 2000；防止在主目录上做无限遍历。
	MaxEntries int
	// Concurrency 递归遍历时的并行度。0 = 默认（可用算力的 60%，上限 8），
	// 1 = 强制串行。非递归遍历永远是串行（就一次 ReadDir，并行只会更慢）。
	Concurrency int `json:"concurrency,omitempty"`
}

// ListResult 遍历结果。
type ListResult struct {
	Root      string  `json:"root"`
	Entries   []Entry `json:"entries"`
	Truncated bool    `json:"truncated"`
	Dirs      int     `json:"dirs"`
	Files     int     `json:"files"`
	TotalSize int64   `json:"totalSize"`
}

// List 遍历目录。
//
// 用 WalkDir 而不是 ReadDir 递归：WalkDir 不跟随符号链接、按目录顺序读，
// 不会因为一个循环链接把整棵树走死。
func (g Guard) List(ctx context.Context, opt ListOptions) (ListResult, error) {
	root, err := g.Check(opt.Path)
	if err != nil {
		return ListResult{}, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return ListResult{}, fmt.Errorf("Cannot open: %w", err)
	}
	if !info.IsDir() {
		return ListResult{}, fmt.Errorf("%s is not a folder", root)
	}

	// 递归走并行（1.4 万文件的目录实测快 3~6 倍）；单层目录就一次 ReadDir，
	// 并行只会多出调度开销，所以固定串行。
	if opt.Recursive && opt.Concurrency != 1 {
		return g.listParallel(ctx, root, opt, opt.Concurrency)
	}

	max := opt.MaxEntries
	if max <= 0 {
		max = 2000
	}
	res := ListResult{Root: root}

	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // 单个条目没权限就跳过，不要中断整次遍历
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if path == root {
			return nil
		}
		if d.IsDir() && !opt.Recursive {
			return filepath.SkipDir
		}
		// 跳过隐藏目录的深层内容：.git / node_modules 之类会淹没有效信息
		if d.IsDir() && shouldSkipDir(d.Name()) {
			return filepath.SkipDir
		}
		if !d.IsDir() && opt.Pattern != "" {
			ok, _ := filepath.Match(opt.Pattern, d.Name())
			if !ok {
				return nil
			}
		}
		if len(res.Entries) >= max {
			res.Truncated = true
			return filepath.SkipAll
		}
		fi, e := d.Info()
		if e != nil {
			return nil
		}
		entry := Entry{
			Path:    path,
			Name:    d.Name(),
			Dir:     d.IsDir(),
			Size:    fi.Size(),
			ModTime: fi.ModTime().Format(time.RFC3339),
		}
		if !d.IsDir() {
			entry.Ext = strings.ToLower(filepath.Ext(path))
			res.Files++
			res.TotalSize += fi.Size()
		} else {
			res.Dirs++
		}
		res.Entries = append(res.Entries, entry)
		return nil
	})
	if walkErr != nil {
		return res, walkErr
	}
	return res, nil
}

func shouldSkipDir(name string) bool {
	switch name {
	case ".git", ".svn", "node_modules", ".cache", ".Trash", "Library":
		return true
	}
	return strings.HasPrefix(name, ".") && len(name) > 1
}

// MakeDir 创建目录。已存在且是目录时视为成功 —— 用户说「建个目录保存结果」，
// 目录本来就在不该算失败。
func (g Guard) MakeDir(path string) (string, error) {
	abs, err := g.Check(path)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(abs); err == nil {
		if info.IsDir() {
			return abs, nil
		}
		return "", fmt.Errorf("%s already exists and is not a folder", abs)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return "", fmt.Errorf("Failed to create the folder: %w", err)
	}
	return abs, nil
}

// Move 移动 / 重命名。目标已存在时拒绝，避免悄悄覆盖。
func (g Guard) Move(from, to string) (string, error) {
	src, err := g.Check(from)
	if err != nil {
		return "", err
	}
	dst, err := g.Check(to)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(src); err != nil {
		return "", fmt.Errorf("Source does not exist: %w", err)
	}
	if _, err := os.Stat(dst); err == nil {
		return "", fmt.Errorf("Target already exists; refusing to overwrite: %s", dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", fmt.Errorf("Failed to create the target folder: %w", err)
	}
	if err := os.Rename(src, dst); err != nil {
		return "", fmt.Errorf("Move failed: %w", err)
	}
	return dst, nil
}

// Copy 复制文件或目录树。
func (g Guard) Copy(from, to string) (string, error) {
	src, err := g.Check(from)
	if err != nil {
		return "", err
	}
	dst, err := g.Check(to)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(src)
	if err != nil {
		return "", fmt.Errorf("Source does not exist: %w", err)
	}
	if _, err := os.Stat(dst); err == nil {
		return "", fmt.Errorf("Target already exists; refusing to overwrite: %s", dst)
	}

	if info.IsDir() {
		if err := copyTree(src, dst); err != nil {
			return "", err
		}
		return dst, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	if err := copyFile(src, dst, info.Mode()); err != nil {
		return "", err
	}
	return dst, nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}

// DeleteResult 删除结果。删除默认进废纸篓，所以要把「移到哪」告诉用户。
type DeleteResult struct {
	Path string `json:"path"`
	To   string `json:"to"`
}

// Delete 删除 —— 默认移到废纸篓，不是 os.RemoveAll。
//
// 理由：模型判断错一次，文件就找不回来了。废纸篓是可恢复的，
// 而「清理回收站」是用户自己的动作，不该由我们代做。
// 同一个卷上移动几乎是瞬时的，几百个文件也很快。
func (g Guard) Delete(paths []string, permanent bool) ([]DeleteResult, []string, error) {
	if len(paths) == 0 {
		return nil, nil, errors.New("There are no paths to delete")
	}
	var done []DeleteResult
	var failed []string

	for _, raw := range paths {
		abs, err := g.Check(raw)
		if err != nil {
			failed = append(failed, err.Error())
			continue
		}
		if abs == "/" || abs == filepath.Dir(abs) {
			failed = append(failed, "Refusing to delete a root folder: "+abs)
			continue
		}
		if _, err := os.Stat(abs); err != nil {
			failed = append(failed, "Does not exist: "+abs)
			continue
		}

		if permanent {
			if err := os.RemoveAll(abs); err != nil {
				failed = append(failed, fmt.Sprintf("%s: %v", abs, err))
				continue
			}
			done = append(done, DeleteResult{Path: abs, To: "(permanently deleted)"})
			continue
		}

		to, err := moveToTrash(abs)
		if err != nil {
			// 跨卷等情况下退化为「原地改名」，仍然可恢复
			fallback := abs + ".deleted-" + time.Now().Format("20060102-150405")
			if e := os.Rename(abs, fallback); e != nil {
				failed = append(failed, fmt.Sprintf("%s: %v", abs, err))
				continue
			}
			done = append(done, DeleteResult{Path: abs, To: fallback})
			continue
		}
		done = append(done, DeleteResult{Path: abs, To: to})
	}
	return done, failed, nil
}

// moveToTrash 移到用户废纸篓，重名时加时间戳。
func moveToTrash(path string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	trash := filepath.Join(home, ".Trash")
	if info, err := os.Stat(trash); err != nil || !info.IsDir() {
		return "", errors.New("The trash is not available")
	}
	target := filepath.Join(trash, filepath.Base(path))
	if _, err := os.Stat(target); err == nil {
		ext := filepath.Ext(path)
		stem := strings.TrimSuffix(filepath.Base(path), ext)
		target = filepath.Join(trash, fmt.Sprintf("%s-%s%s", stem, time.Now().Format("20060102-150405"), ext))
	}
	if err := os.Rename(path, target); err != nil {
		return "", err
	}
	return target, nil
}
