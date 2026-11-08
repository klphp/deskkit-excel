/**
 * 中文词条。
 *
 * 只保留界面上真正用到的键 —— 之前为「合并表单 / 清洗表单」写的一大堆
 * 词条随界面一起删了。界面砍到「选输入、选输出、说要干什么」之后，
 * 需要解释的东西也少了大半。
 */
export const zhCN = {
  "app.name": "DeskKit Excel",
  "app.tagline": "Excel 批量处理",

  "nav.settings": "设置",
  "nav.about": "关于",

  "titlebar.settings": "设置",
  "titlebar.minimize": "最小化",
  "titlebar.maximize": "最大化",
  "titlebar.restore": "还原",
  "titlebar.close": "关闭",

  // ── 选择区 ──

  // ── 对话 ──
  "ai.emptyTitle": "你想对这些表格做什么？",
  "ai.emptyHint": "直接说就行，比如「合并成一张，按工号去重」「把日期统一成 2026-01-02」。我会先告诉你会发生什么，你同意后再动手。",
  "ai.askMerge": "把所有表合并成一张",
  "ai.askClean": "清掉空行和重复行",
  "ai.askSummary": "按部门汇总金额",
  "ai.askLook": "先看看这些表有什么问题",
  "ai.askNoDir": "先帮我选个文件夹吧",
  "ai.placeholder": "告诉我要做什么…",
  "ai.placeholderNoDir": "先选择输入文件夹",
  "ai.send": "发送",
  "ai.modelSettings": "模型设置",
  "ai.needsKey": "需要密钥",
  "ai.notRunning": "未运行",
  "ai.cancel": "取消",
  "ai.disabled": "AI 未启用",
  "ai.stepConnect": "接入",
  "ai.stepModel": "选择模型",
  "ai.connect": "保存并测试",
  "ai.connected": "连接正常",
  "ai.alreadyConnected": "已接入",
  "ai.refreshModels": "刷新模型",
  "ai.refreshHint": "点「刷新模型」向服务商要一次列表",
  "ai.modelsCount": "共 {n} 个模型",
  "ai.noModels": "对方没有返回模型列表，手动填一个模型名",
  "ai.modelPlaceholder": "模型名，如 deepseek-chat",
  "ai.useThisModel": "用这个模型",
  "ai.baseURL": "接口地址",
  "ai.configureFirst": "还没接入模型 —— 点这里接入",
  "ai.askBrowse": "先选一个文件夹",
  "ai.askWhat": "这工具能干什么？",
  "ai.askWhatThen": "把 Excel 表放进文件夹后，我可以帮你合并、清洗、汇总，或者按你的要求做别的处理。你先选个文件夹试试？",
  "ai.pickInput": "选择要处理的文件夹",
  "ai.pickOutput": "选择输出位置",
  "ai.useSample": "用示例数据试用一下",
  "ai.outputTo": "输出到",
  "ai.pickFeature": "选择功能",
  "ai.featureNone": "普通对话",
  "ai.featureNoneHint": "不处理文件，就是聊天 / 问问题",
  "ai.featureMore": "更多功能（视频处理、PDF 转换…）之后加。",
  "ai.placeholderFeature": "告诉我要对这个表做什么…",
  "ai.willWriteTo": "结果写到",
  "ai.packBundled": "这个功能的组件已随应用装好，直接可用。",
  "ai.packDownload": "首次使用需要下载功能组件。这台机器上还没装：",
  "ai.useFeature": "开始使用",
  "ai.downloadFeature": "下载组件",
  "ai.thinking": "正在处理…",

  // ── 模型配置 ──
  "ai.disabledHint": "配置文件里关闭了 AI。批量处理本身不依赖 AI，但需要用一句话驱动它时得先打开。",
  "ai.provider": "服务商",
  "ai.model": "模型",
  "ai.ready": "可用",
  "ai.notReady": "未就绪",
  "ai.keySaved": "已保存（留空则不修改）",
  "ai.testFailed": "连接失败",
  "ai.testOk": "连接正常",

  // ── 设置 / 关于 ──
  "settings.appearance": "外观",
  "settings.appearanceHint": "默认浅色。长时间看表格，深色底配白字太刺眼。",
  "settings.theme.system": "跟随系统",
  "settings.theme.light": "浅色",
  "settings.theme.dark": "深色",
  "settings.language": "界面语言",
  "settings.languageHint": "切换后立即生效。",
  "settings.update": "更新",
  "settings.updateIdle": "尚未检查",
  "settings.updateNotConfigured": "未配置更新源",
  "settings.checkUpdate": "检查更新",
  "settings.checking": "检查中…",
  "settings.upToDate": "已是最新版本",
  "settings.updateAvailable": "有新版本",
  "settings.downloadAndInstall": "下载并安装",
  "settings.goToDownload": "前往下载页",
  "settings.processing": "处理中…",
  "settings.downloadHint": "下载完成后会自动重启。",
  "settings.checkFailed": "检查失败",

  "about.title": "关于",
  "about.platform": "平台",
};

export default zhCN;
