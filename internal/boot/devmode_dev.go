//go:build dev

package boot

// devBuild 标记这是 wails3 dev 起的调试构建。
//
// 单实例锁标识会因此加 .dev 后缀：否则调试实例会把装好的正式版抢走
// （或反过来），表现是「双击图标没反应」。
const devBuild = true
