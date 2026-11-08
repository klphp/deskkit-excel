package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/klphp/excel-batch-tool/internal/ai"
	"github.com/klphp/excel-batch-tool/internal/config"
)

func main() {
	appYAML, _ := os.ReadFile("config/app.yaml")
	winYAML, _ := os.ReadFile("config/windows.yaml")
	cfg, err := config.Parse(appYAML, winYAML)
	if err != nil {
		panic(err)
	}
	fmt.Println("① 配置解析出的候选模型（下拉用）：")
	names := make([]string, 0, len(cfg.AI.Providers))
	for n := range cfg.AI.Providers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		p := cfg.AI.Providers[n]
		fmt.Printf("   %-12s model=%-22s models=%v\n", n, p.Model, p.Models)
	}

	base, _ := os.UserConfigDir()
	dir := filepath.Join(base, "deskkit")
	m := ai.NewManager(cfg.AI, func(k string) string { return os.Getenv(k) }, dir)
	ctx := context.Background()

	fmt.Println("\n② 走真实接口拉 opencode 的模型清单：")
	list, aiErr := m.Models(ctx, "opencode")
	if aiErr != nil {
		fmt.Printf("   失败: %+v\n", *aiErr)
		return
	}
	fmt.Printf("   返回 %d 个：%v\n", len(list), list)
}
