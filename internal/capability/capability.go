// Package capability 能力包的类型定义。
//
// 这是一个**叶子包**：它只定义类型，不 import 任何工具包。
// 这样每个工具包（excel / fs / 将来的 video、pdf）都可以 import 它来
// 声明「我是一个能力包」，而汇总方（packs）再 import 各个工具包 ——
// 不会出现「工具包 import 汇总包」的循环。
//
// 这就是用户说的工厂模式的落点：新增一个第三方工具 = 新加一个
// internal/tools/<名字> 包，里面提供 Register（挂工具）和 Pack（自我描述），
// 再到 internal/packs 里加一行。别的文件一个字都不用改。
package capability

import (
	"os/exec"

	"github.com/klphp/deskkit-excel/internal/ai"
)

// Pack 一个能力包（一类功能）。
type Pack struct {
	// ID 配置里开关它用的名字，也是界面上「功能选择」的标识。
	ID string
	// Name 给人看的名字。
	Name string
	// Description 一句话说明，会写进模型的系统提示。
	Description string
	// Requires 依赖的外部命令（ffmpeg / pandoc…）。纯 Go 实现留空。
	Requires []string

	// Register 把本工具包的工具挂进 AI 的工具表。
	Register func(*ai.Registry) error
}

// Missing 返回本机缺失的外部命令。
//
// 提前算出来，是为了在界面上说「首次使用需要下载组件」，
// 而不是等用户点了才弹一个 exec: executable file not found。
func (p Pack) Missing() []string {
	var miss []string
	for _, cmd := range p.Requires {
		if _, err := exec.LookPath(cmd); err != nil {
			miss = append(miss, cmd)
		}
	}
	return miss
}

// Bundled 功能组件是否随应用一起装好（纯 Go 实现 = 是）。
func (p Pack) Bundled() bool {
	return len(p.Requires) == 0
}
