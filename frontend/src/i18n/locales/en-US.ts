/**
 * English strings.
 *
 * 骨架示例：key 和 zh-CN.ts 一一对应。缺的会自动回退到中文，
 * 不会显示 key 名，所以可以分批翻译。
 */
export const enUS: Record<string, string> = {
  "app.name": "DeskKit",

  "nav.home": "Home",
  "nav.workbench": "Workbench",
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

  // ── Workbench ──
  "workbench.title": "Hand your spreadsheets over",
  "workbench.subtitle": "Drop a folder, describe what you want, and let it run — your data never leaves this machine.",
  "workbench.dropTitle": "Drop a folder of Excel files here",
  "workbench.dropHint": "xlsx / xls / xlsm / csv, multiple allowed",
  "workbench.pick": "Choose files",
  "workbench.filesCount": "{n} files selected",
  "workbench.clear": "Clear",
  "workbench.more": "{n} more not shown",
  "workbench.ignored": "{n} non-spreadsheet files ignored",
  "workbench.scenarios": "Common tasks",
  "workbench.merge.title": "Merge reports",
  "workbench.merge.desc": "Combine many inconsistent sheets into one table",
  "workbench.clean.title": "Clean messy data",
  "workbench.clean.desc": "Drop blank and duplicate rows, normalize dates and numbers",
  "workbench.summary.title": "Group and summarize",
  "workbench.summary.desc": "Aggregate by dimension and export a summary table",
  "workbench.scenarioHint": "Clicking fills the prompt on the right — edit it before sending.",

  // ── AI panel ──
  "ai.title": "AI assistant",
  "ai.ready": "Ready",
  "ai.notConfigured": "Not configured",
  "ai.disabled": "AI is disabled. Turn on ai.enabled in config/app.yaml to use natural-language commands.",
  "ai.configureHint": "Connect a model to use plain language. The batch features themselves do not need AI.",
  "ai.provider": "Provider",
  "ai.model": "Model",
  "ai.apiKey": "API key",
  "ai.apiKeyPlaceholder": "sk-… (stored locally only)",
  "ai.saveAndTest": "Save and test",
  "ai.testing": "Testing…",
  "ai.testOk": "Connection OK",
  "ai.local": "local",
  "ai.localHint": "Local models (Ollama / LM Studio) need no key and keep everything on this machine.",
  "ai.emptyHint": "Try: “merge these sheets into one table and summarize by department”.",
  "ai.inputPlaceholder": "What should it do?",
  "ai.enterToSend": "Enter to send, Shift+Enter for newline",
  "ai.send": "Send",
  "ai.thinking": "Thinking…",
  "ai.you": "You",
  "ai.clear": "Clear chat",
  "workbench.next": "Once files are in, the AI explains what it plans to do (which columns, which output file) before running anything — your originals are never overwritten.",
};
