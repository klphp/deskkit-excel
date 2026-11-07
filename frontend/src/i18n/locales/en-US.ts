/**
 * English strings.
 *
 * 骨架示例：key 和 zh-CN.ts 一一对应。缺的会自动回退到中文，
 * 不会显示 key 名，所以可以分批翻译。
 */
export const enUS: Record<string, string> = {
  "app.name": "DeskKit",

  "nav.home": "Home",
  "nav.docs": "Docs",
  "nav.settings": "Settings",
  "nav.about": "About",

  "titlebar.settings": "Settings",
  "titlebar.minimize": "Minimize",
  "titlebar.maximize": "Maximize",
  "titlebar.restore": "Restore",
  "titlebar.close": "Close",

  "settings.appearance": "Appearance",
  "settings.appearanceHint":
    "When following the system, dark/light switches apply immediately.",
  "settings.theme.system": "System",
  "settings.theme.light": "Light",
  "settings.theme.dark": "Dark",

  "settings.update": "Updates",
  "settings.updateIdle": "Check for a newer version.",
  "settings.updateNotConfigured": "Update source is not configured.",
  "settings.checkUpdate": "Check for updates",
  "settings.checking": "Checking…",
  "settings.upToDate": "You're up to date",
  "settings.updateAvailable": "New version available",
  "settings.downloadAndInstall": "Download and install",
  "settings.goToDownload": "Go to download",
  "settings.processing": "Working…",
  "settings.downloadHint":
    "In-app replacement isn't supported on this platform; the download page will open in your browser.",
  "settings.updateNotConfiguredMsg":
    "No update source configured. Set update.repository (or manifestUrl) and enabled: true in config/app.yaml.",
  "settings.checkFailed": "Check failed",

  "settings.language": "Language",
  "settings.languageHint": "Applies immediately and persists across restarts.",

  "about.title": "About",
  "about.version": "Version",
  "about.platform": "Platform",
  "about.localServer": "Local server",
};
