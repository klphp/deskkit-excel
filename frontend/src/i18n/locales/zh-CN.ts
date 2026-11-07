/**
 * 中文词条。
 *
 * 这里同时承担两个角色：
 *   1. 默认语言（i18n 关闭时就用它）
 *   2. 兜底语言（某个语言缺词条时回退到这里）
 *
 * 所以**新增文案必须先加在这里**，其它语言可以后补 —— 漏了只会显示中文，
 * 不会显示 key 名。
 */
export const zhCN: Record<string, string> = {
  // ── 通用 ──
  "app.name": "DeskKit",

  // ── 侧边导航 ──
  "nav.home": "首页",
  "nav.docs": "文档",
  "nav.settings": "设置",
  "nav.about": "关于",

  // ── 标题栏 ──
  "titlebar.settings": "设置",
  "titlebar.minimize": "最小化",
  "titlebar.maximize": "最大化",
  "titlebar.restore": "还原",
  "titlebar.close": "关闭",

  // ── 设置 · 外观 ──
  "settings.appearance": "外观",
  "settings.appearanceHint": "跟随系统时，系统切换深浅色会实时生效。",
  "settings.theme.system": "跟随系统",
  "settings.theme.light": "亮色",
  "settings.theme.dark": "暗色",

  // ── 设置 · 更新 ──
  "settings.update": "软件更新",
  "settings.updateIdle": "检查是否有新版本可用。",
  "settings.updateNotConfigured": "更新源尚未配置。",
  "settings.checkUpdate": "检查更新",
  "settings.checking": "检查中…",
  "settings.upToDate": "已是最新版本",
  "settings.updateAvailable": "发现新版本",
  "settings.downloadAndInstall": "下载并安装",
  "settings.goToDownload": "前往下载",
  "settings.processing": "处理中…",
  "settings.downloadHint": "当前平台不支持应用内替换，会在浏览器里打开下载页面。",
  "settings.updateNotConfiguredMsg":
    "未配置更新源。在 config/app.yaml 的 update 段填上 repository（或 manifestUrl），并把 enabled 设为 true。",
  "settings.checkFailed": "检查失败",

  // ── 设置 · 语言 ──
  "settings.language": "语言",
  "settings.languageHint": "切换后立即生效，重启后保持。",

  // ── 关于 ──
  "about.title": "关于",
  "about.version": "版本",
  "about.platform": "平台",
  "about.localServer": "本地接口",
};
