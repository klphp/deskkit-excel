package main

import (
	"embed"
	"log"

	"github.com/klphp/deskkit/internal/boot"
)

// 内核要用的配置（app.yaml + windows.yaml）。
//
// embed 必须写在根包：Go 的 //go:embed 不允许引用父目录
// （`//go:embed ../../config/*.yaml` 直接编译失败）。
//
//go:embed config/app.yaml config/windows.yaml
var configFS embed.FS

// 前端构建产物。用 all: 前缀是为了把 public/ 里的点文件和子目录一起带上。
//
//go:embed all:frontend/dist
var assets embed.FS

// appName 配置目录名与日志文件的依据。改名时只改这一处。
const appName = "deskkit"

func main() {
	// 装配全在 boot 里：每个工具（Excel / PDF / …）的 main.go 都长这样，
	// 只是 AppName、内嵌的配置和额外注册的服务不同。
	if err := boot.Run(boot.Options{
		AppName:  appName,
		ConfigFS: configFS,
		Assets:   assets,
	}); err != nil {
		log.Fatal(err)
	}
}
