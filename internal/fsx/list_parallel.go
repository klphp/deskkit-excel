package fsx

// 递归遍历的并行版。
//
// 为什么值得单独写一遍：fs_list 递归一棵 1.4 万文件的项目目录时，串行 WalkDir
// 的时间几乎全花在等 syscall 返回上（每个目录一次 getdents、每个条目一次
// stat）。这些等待天然可以重叠 —— 并行把 24 个核的 IO 队列喂满，实测在
// 大目录上快 3~6 倍（数据见 logs/2026-11-09-mcp-fs.log）。
//
// 做法是最朴素的「工作队列 + 条件变量」：
//   · 待处理目录放在栈里，worker 抢到就 ReadDir，遇到子目录放回栈；
//   · pending 为 0 且没有在飞任务时，worker 自然退出，不需要额外的关闭信号；
//   · 命中数够了、或调用方取消 → stopped/cancel，worker 在下一个检查点退出。
//
// 不跟随符号链接（和串行版一致）：一个循环链接就能把整次遍历走死。

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// listParallel 并行遍历。返回前按路径排序，保证同样的目录树得到同样的顺序。
func (g Guard) listParallel(ctx context.Context, root string, opt ListOptions, workers int) (ListResult, error) {
	max := opt.MaxEntries
	if max <= 0 {
		max = 2000
	}
	// 收集上限：并行拿到的顺序本来就不确定，多收几倍再排序截断，
	// 结果比「谁先读到算谁」稳定得多。4 倍足够覆盖排序偏差。
	buffer := max * 4
	if buffer < max {
		buffer = max
	}

	cctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu       sync.Mutex
		cond     = sync.NewCond(&mu)
		pending  = []string{root}
		inflight int
		stopped  bool
		entries  []Entry
		dirs     int
		files    int
		total    int64
		hitLimit bool
	)

	// pop 取一个待处理目录。返回 false 表示活儿干完了（或已中止）。
	pop := func() (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		for {
			if stopped || cctx.Err() != nil {
				return "", false
			}
			if n := len(pending); n > 0 {
				dir := pending[n-1]
				pending = pending[:n-1]
				inflight++
				return dir, true
			}
			if inflight == 0 {
				return "", false
			}
			cond.Wait()
		}
	}
	done := func() {
		mu.Lock()
		inflight--
		mu.Unlock()
		cond.Broadcast()
	}
	push := func(dir string) {
		mu.Lock()
		pending = append(pending, dir)
		mu.Unlock()
		cond.Broadcast()
	}

	process := func(dir string) {
		defer done()
		des, err := os.ReadDir(dir)
		if err != nil {
			return // 没权限的目录跳过，不中断整次遍历
		}
		for _, d := range des {
			if cctx.Err() != nil {
				return
			}
			name := d.Name()
			path := filepath.Join(dir, name)
			isDir := d.IsDir()
			if isDir {
				// 和串行版一致：不递归时不进子目录，也不把子目录当作条目返回；
				// 隐藏目录/node_modules 同理整个跳过。
				if !opt.Recursive || shouldSkipDir(name) {
					continue
				}
			} else if opt.Pattern != "" {
				if ok, _ := filepath.Match(opt.Pattern, name); !ok {
					continue
				}
			}
			fi, e := d.Info()
			if e != nil {
				continue
			}
			mu.Lock()
			if len(entries) >= buffer {
				hitLimit = true
				stopped = true
				mu.Unlock()
				cancel()
				return
			}
			entry := Entry{
				Path:    path,
				Name:    name,
				Dir:     isDir,
				Size:    fi.Size(),
				ModTime: fi.ModTime().Format(time.RFC3339),
			}
			if !isDir {
				entry.Ext = lowerExt(path)
			}
			entries = append(entries, entry)
			mu.Unlock()
			if isDir {
				push(path)
			}
		}
	}

	// worker 数不超过并行度上限；只在递归时有意义。
	w := resolveWorkers(workers, 0)
	if w < 1 {
		w = 1
	}
	var wg sync.WaitGroup
	for i := 0; i < w; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				dir, ok := pop()
				if !ok {
					return
				}
				process(dir)
			}
		}()
	}
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return ListResult{Root: root}, err
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	if len(entries) > max {
		entries = entries[:max]
		hitLimit = true
	}
	// 计数必须按「最终返回的条目」重算：并行版先多收再截断，
	// 用收集阶段的计数器会给出比 entries 更少的行数不一致。
	res := ListResult{Root: root, Entries: entries, Truncated: hitLimit}
	for _, e := range entries {
		if e.Dir {
			dirs++
		} else {
			files++
			total += e.Size
		}
	}
	res.Dirs = dirs
	res.Files = files
	res.TotalSize = total
	return res, nil
}

func lowerExt(path string) string {
	return toLowerASCII(filepath.Ext(path))
}

// toLowerASCII 只处理 ASCII 后缀 —— 文件后缀本来就只有 ASCII，
// 用 strings.ToLower 也行，但这里避免额外 import 的重量级路径。
func toLowerASCII(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}
