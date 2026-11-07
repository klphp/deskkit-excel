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
  "nav.workbench": "工作台",
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

  // ── 工作台 ──
  "workbench.title": "把表格交给它",
  "workbench.subtitle": "拖入文件夹，用人话下指令，批量处理 —— 数据不出本机。",
  "workbench.dropTitle": "把存放 Excel 的文件夹拖到这里",
  "workbench.dropHint": "支持 xlsx / xls / xlsm / csv，可多选",
  "workbench.pick": "选择文件",
  "workbench.filesCount": "已选 {n} 个文件",
  "workbench.clear": "清空",
  "workbench.more": "另有 {n} 个未显示",
  "workbench.ignored": "已忽略 {n} 个非表格文件",
  "workbench.scenarios": "常用场景",
  "workbench.merge.title": "合并报表",
  "workbench.merge.desc": "多个结构不一的表合成一张总表",
  "workbench.clean.title": "清洗脏数据",
  "workbench.clean.desc": "去空行、去重、日期与数字格式统一",
  "workbench.summary.title": "分组汇总",
  "workbench.summary.desc": "按维度分组汇总，输出汇总表",
  "workbench.scenarioHint": "点一下会把指令填进右侧输入框，改完再发。",

  // ── AI 面板 ──
  "ai.title": "AI 助手",
  "ai.ready": "已就绪",
  "ai.notConfigured": "未配置",
  "ai.disabled": "AI 未启用。在 config/app.yaml 里打开 ai.enabled 后可用自然语言下指令。",
  "ai.configureHint": "接一个模型即可用人话下指令。批处理功能本身不依赖 AI。",
  "ai.provider": "服务商",
  "ai.model": "模型",
  "ai.apiKey": "API Key",
  "ai.apiKeyPlaceholder": "sk-…（只存本机）",
  "ai.saveAndTest": "保存并测试",
  "ai.testing": "测试中…",
  "ai.testOk": "连接正常",
  "ai.local": "本地",
  "ai.localHint": "本地模型（Ollama / LM Studio）不需要密钥，数据不出本机。",
  "ai.emptyHint": "试试：「把这些表合并成一张，按部门汇总」。",
  "ai.inputPlaceholder": "说说要做什么…",
  "ai.enterToSend": "Enter 发送，Shift+Enter 换行",
  "ai.send": "发送",
  "ai.thinking": "思考中…",
  "ai.you": "你",
  "ai.clear": "清空对话",
  "workbench.next": "拖入文件后，右侧的 AI 会先说明它打算怎么做（改哪些列、输出什么文件），你确认后再执行 —— 原文件不会被覆盖。",

  // ── 工作台（接后端后新增）──
  "workbench.pickTitle": "选择存放 Excel 的文件夹",
  "workbench.noDir": "还没选择文件夹",
  "workbench.rescan": "重新扫描",
  "workbench.scanning": "正在扫描…",
  "workbench.summary": "{files} 个表格 · {rows} 行数据 · {size}",
  "workbench.ignoredFiles": "另有 {n} 个非表格文件被忽略",
  "workbench.unsupported": "不支持的文件（xls 老格式请另存为 xlsx）：{files}",
  "workbench.truncated": "文件太多，只处理了前 2000 个",
  "workbench.colFile": "文件",
  "workbench.colHeaders": "表头",
  "workbench.colRows": "行数",
  "workbench.moreRows": "另有 {n} 个文件未列出",
  "workbench.mergeTitle": "合并成一张总表",
  "workbench.mergeHint": "按列名对齐，不要求列序一致；缺的列留空并提示。输出到 deskkit-out 子目录，不覆盖原文件。",
  "workbench.dedup": "去重键",
  "workbench.dedupPlaceholder": "按哪些列去重（逗号分隔，可留空）",
  "workbench.preview": "预览",
  "workbench.run": "执行合并",
  "workbench.previewTitle": "预览：将要做的事",
  "workbench.previewBody": "合并 {files} 个文件：输入 {rowsIn} 行 → 输出 {rowsOut} 行，重复 {dup} 行",
  "workbench.previewCols": "输出列：{cols}",
  "workbench.previewNoWrite": "预览不写任何文件。确认无误再点「执行合并」。",
  "workbench.doneTitle": "合并完成",
  "workbench.doneBody": "共 {rows} 行，用时 {ms} 毫秒",

  // ── 清洗 ──
  "workbench.cleanTitle": "清洗这批表格",
  "workbench.cleanHint": "逐文件处理：每个输入生成一个 -clean.xlsx，原文件不动。",
  "workbench.cleanBlank": "删除空行",
  "workbench.cleanTrim": "去掉首尾空白",
  "workbench.cleanDup": "删除重复行",
  "workbench.cleanNumbers": "文本数字转数值（保留前导零）",
  "workbench.cleanDates": "统一日期格式",
  "workbench.cleanDedupPlaceholder": "按哪些列判断重复（留空=整行完全相同）",
  "workbench.cleanPreviewBtn": "预览清洗",
  "workbench.cleanRunBtn": "执行清洗",
  "workbench.cleanPreviewTitle": "预览：清洗会做什么",
  "workbench.cleanPreviewBody": "{files} 个文件：{rowsIn} 行 → {rowsOut} 行，删除空行 {blank}、重复行 {dup}",
  "workbench.cleanFixes": "将修正：空白单元格 {trim}、日期 {dates}、数字 {nums}",
  "workbench.cleanDoneTitle": "清洗完成",
  "workbench.cleanDoneBody": "{files} 个文件，共 {rows} 行，用时 {ms} 毫秒",
};
