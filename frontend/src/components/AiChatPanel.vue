<script setup lang="ts">
/**
 * 对话即界面。
 *
 * 这一版把上一版的「顶部输入/输出栏」也去掉了：那一栏让整个应用看起来
 * 像一张设置表，而用户要做的事情其实就是说话。现在整窗只有一列对话，
 * 文件选择降级成输入框旁边的附件按钮 —— 选了会显示成一个小标签，
 * 不选也能先聊天（这时候模型会问你）。
 *
 * 能力的组合在 Go 侧的工具循环里，这里只负责：把当前上下文交给模型、
 * 把流式文字和工具步骤显示出来、没接入模型时把设置打开。
 */
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import { ArrowDown, ArrowUp, Check, ChevronDown, Copy, FileSpreadsheet, FileText, Film, FolderOpen, Layers, Loader2, Maximize2, Minimize2, Plus, Settings2, ThumbsDown, ThumbsUp, Wrench, X } from "@lucide/vue";
import { useI18n } from "../i18n";
import { renderMarkdown } from "../lib/markdown";
import { useWorkspace } from "../composables/workspace";
import AttachButton from "./AttachButton.vue";
import * as Tools from "../../bindings/github.com/klphp/excel-batch-tool/internal/services/toolservice";
import * as Prefs from "../../bindings/github.com/klphp/excel-batch-tool/internal/services/prefsservice";
import * as Sessions from "../../bindings/github.com/klphp/excel-batch-tool/internal/services/sessionservice";
import { initSessions, useSessions } from "../composables/sessions";
import {
  AIService,
  chatInput,
  errorText,
  onDelta,
  onTool,
  onAsk,
  onConfirm,
  type AskPayload,
  type ConfirmPayload,
  withWorkspace,
  type ChatTurn,
  type ProviderInfo,
  type TestResult,
  type ToolEvent,
} from "../lib/ai";

const { t } = useI18n();
const ws = useWorkspace();
const sess = useSessions();

type Step = { tool: string; detail: string; done: boolean; ok: boolean };

/**
 * 内部工具：不在界面上摆出来。
 *
 * describe_capability 是模型自查「这个工具怎么用」，remember_instructions 是
 * 记用户偏好 —— 两个都不动用户的数据。用户关心的是「它动了什么文件」，
 * 不是它内部查了什么；摆出来只会让每轮对话多一条看不懂的步骤。
 *
 * 真出问题时（比如查询失败）这条被隐藏也不影响排查：错误会体现在模型的
 * 回答里，日志里也有原始事件。
 */
const HIDDEN_TOOLS = new Set(["describe_capability", "remember_instructions"]);
const steps = ref<Step[]>([]);

/** 与 sessions.ts 的每页条数保持一致：分页标记就打在每页的分界处。 */
const MSG_PAGE = 20;

/** 分页标记上的文字：这条消息的时间。 */
function pageMarkLabel(at?: number): string {
  if (!at) return "更早的消息";
  const d = new Date(at * 1000);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getMonth() + 1}月${d.getDate()}日 ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

const enabled = ref(false);
const providers = ref<ProviderInfo[]>([]);
const provider = ref("");
const model = ref("");
const apiKey = ref("");
/**
 * 已生效的服务商与模型 —— 和弹窗里的草稿分开。
 *
 * 之前只有一个 model ref，下拉一改就参加发送，于是「没点保存直接关闭，
 * 模型已经换了」。草稿归草稿，生效归生效：只有点「保存」（或「保存并测试」）
 * 才把草稿写进这里，也才写进设置文件。
 */
const activeProvider = ref("");
const activeModel = ref("");
const baseURL = ref("");
const busy = ref(false);
const testing = ref(false);
const testResult = ref<TestResult | null>(null);
const failure = ref("");
const showSettings = ref(false);
/** 功能选择下拉。 */
/** 首次选某个功能时的组件提示。 */
const pendingPack = ref<{ id: string; name: string; bundled: boolean; missing: string[] } | null>(null);

/** 接入与选模型是两步 —— 和 opencode / Hermes 的顺序一致。 */
const connected = ref(false);
const models = ref<string[]>([]);
const loadingModels = ref(false);
const modelsError = ref("");
/**
 * 模型选择默认是下拉 —— 服务商有什么模型，就列什么模型。
 * 文本框只作为兜底：有些自建网关的模型名不在任何清单里，
 * 但那是例外，不该让所有人都面对一个空输入框猜名字。
 */
const customModel = ref(false);
const CUSTOM = "__custom__";
const modelChoice = computed({
  get: () => (customModel.value ? CUSTOM : model.value),
  set: (v: string) => {
    if (v === CUSTOM) {
      customModel.value = true;
      model.value = "";
      return;
    }
    customModel.value = false;
    model.value = v;
  },
});

/**
 * 消息挂在「当前会话」上，而不是组件自己的 ref ——
 * 左边栏切换会话时，这里自然就换成那一轮的历史。
 * 用 computed 取而不是复制：切回来还是同一份数据，不会丢。
 */
const turns = sess.turns;
const draft = ref("");
const streaming = ref("");
const scroller = ref<HTMLElement | null>(null);
const inputEl = ref<HTMLTextAreaElement | null>(null);
/** 覆盖层的输入框（和折叠态是两个元素，各自有 ref）。 */
const expandedInput = ref<HTMLTextAreaElement | null>(null);

/**
 * 赞/踩：纯手感，不做任何事。
 *
 * 不落库、不上传、不统计、不显示数量 —— 就三个图标，点了有反应。
 * 存内存即可：切走会话回来归零无所谓，本来也不代表什么。
 */
const reactions = ref<Record<number, 1 | -1 | 0>>({});
/** 刚复制过的那条（显示一瞬间的 ✓）。 */
const copiedAt = ref(-1);

/**
 * 模型正在等用户点选的问题。
 *
 * 为什么不用弹窗：这个问题属于对话流程的一部分，答案也要留在对话里
 * （用户以后回看能知道自己当时选了什么）。弹窗会把它从上下文里摘出去。
 */
const pendingAsk = ref<AskPayload | null>(null);

/** 待用户确认的危险操作。 */
const pendingConfirm = ref<ConfirmPayload | null>(null);

/**
 * 用户自定义指令。发消息前读一次（读一个本地 JSON，成本可忽略），
 * 这样用户改了设置不用重启应用就生效。
 */
let instructions = "";
async function loadInstructions() {
  // 会话级优先：这个会话可能在做完全不同的任务（报表 vs 视频）
  try {
    const id = sess.currentId.value;
    if (id) {
      const own = (await Sessions.SessionInstructions(id)) ?? "";
      if (own.trim()) {
        instructions = own;
        return;
      }
    }
  } catch {
    /* 会话库不可用就退到全局 */
  }
  try {
    instructions = (await Prefs.GetInstructions()) ?? "";
  } catch {
    instructions = "";
  }
}
let offConfirm: (() => void) | null = null;

function react(idx: number, kind: 1 | -1) {
  // 再点一次取消，像真的按钮
  reactions.value[idx] = reactions.value[idx] === kind ? 0 : kind;
}

async function copyTurn(idx: number, text: string) {
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    return;
  }
  copiedAt.value = idx;
  setTimeout(() => {
    if (copiedAt.value === idx) copiedAt.value = -1;
  }, 1400);
}

let offDelta: (() => void) | null = null;
let offTool: (() => void) | null = null;
let offAsk: (() => void) | null = null;

const current = computed(() => providers.value.find((p) => p.name === provider.value));

/** 底部状态栏显示的「正在用的服务商名」，跟生效值走，不跟弹窗草稿走。 */
const activeLabel = computed(
  () =>
    providers.value.find((p) => p.name === activeProvider.value)?.label ||
    activeProvider.value ||
    provider.value,
);
const isCustom = computed(() => provider.value === "custom");
const ready = computed(() => providers.value.some((p) => p.ready));

/**
 * 首屏能力清单。
 *
 * 视频与文档两组现在还没接（v1 只做表格），但不藏起来 —— 用户有权知道
 * 这个工具打算做什么；同时必须标清「即将支持」，不能让人以为点了就能用。
 */
const intro = computed(() => [
  {
    id: "excel",
    icon: FileSpreadsheet,
    name: t("intro.excel.name"),
    available: true,
    badge: "",
    items: [t("intro.excel.i1"), t("intro.excel.i2"), t("intro.excel.i3"), t("intro.excel.i4")],
  },
  {
    id: "video",
    icon: Film,
    name: t("intro.video.name"),
    available: false,
    badge: t("intro.soon"),
    items: [t("intro.video.i1"), t("intro.video.i2"), t("intro.video.i3"), t("intro.video.i4")],
  },
  {
    id: "docs",
    icon: FileText,
    name: t("intro.docs.name"),
    available: false,
    badge: t("intro.soon"),
    items: [t("intro.docs.i1"), t("intro.docs.i2"), t("intro.docs.i3")],
  },
]);

/** 生效的模型名：设置里存过就用存的，否则用服务商的默认模型。 */
function ActiveModelOrDefault(st: { model?: string }): string {
  const preferred = providers.value.find((p) => p.name === (st as any).provider);
  return st.model || preferred?.model || "";
}

async function refresh() {
  enabled.value = await AIService.Enabled();
  if (!enabled.value) return;
  providers.value = (await AIService.Providers()) ?? [];

  const st = await AIService.Settings();
  const preferred =
    providers.value.find((p) => p.name === st.provider) ??
    providers.value.find((p) => p.ready) ??
    providers.value[0];
  if (preferred) {
    provider.value = preferred.name;
    model.value = ActiveModelOrDefault(st);
    activeProvider.value = preferred.name;
    activeModel.value = ActiveModelOrDefault(st);
    baseURL.value = st.baseURL || "";
    connected.value = preferred.ready;
    // ★ 候选清单必须在这里就填上：下拉没有 options，当前值匹配不到任何一项，
    //   结果就是一个空白框 —— 用户会以为模型没选。
    models.value = preferred.models ?? [];
    customModel.value = !!model.value && !models.value.includes(model.value) && models.value.length > 0;
    // 已接上的服务商顺手要一次真实清单（覆盖候选）
    if (preferred.ready) void loadModels();
  }
}

function pickProvider(name: string) {
  provider.value = name;
  const p = providers.value.find((x) => x.name === name);
  model.value = p?.model ?? "";
  baseURL.value = "";
  models.value = p?.models ?? [];
  customModel.value = !model.value && models.value.length === 0;
  modelsError.value = "";
  testResult.value = null;
  failure.value = "";
  connected.value = Boolean(p?.ready);
  // 已接上的服务商直接拉真实清单，用户看到的就是「它到底有什么模型」
  if (p?.ready) void loadModels();
}

async function connect() {
  failure.value = "";
  testResult.value = null;
  modelsError.value = "";
  testing.value = true;
  try {
    await AIService.SaveSettings({
      provider: provider.value,
      baseURL: isCustom.value ? baseURL.value.trim() : (current.value?.baseURL ?? ""),
      model: model.value,
      apiKey: apiKey.value,
      clearKey: false,
      disableLocalProbe: false,
    });
    apiKey.value = "";
    // 接入这一步本身就是一次显式保存：服务商和密钥定下来了，模型也一起定
    activeProvider.value = provider.value;
    activeModel.value = model.value;
    testResult.value = await AIService.Test(provider.value);
    if (testResult.value?.ok) {
      connected.value = true;
      await refresh();
      await loadModels();
    }
  } catch (err) {
    failure.value = errorText(err);
  } finally {
    testing.value = false;
  }
}

async function loadModels() {
  loadingModels.value = true;
  modelsError.value = "";
  try {
    const list = (await AIService.Models(provider.value)) ?? [];
    if (list.length === 0) {
      modelsError.value = t("ai.noModels");
      return;
    }
    models.value = list;
    customModel.value = false;
    if (!model.value || !list.includes(model.value)) {
      model.value = list[0];
    }
  } catch (err) {
    modelsError.value = errorText(err);
  } finally {
    loadingModels.value = false;
  }
}

async function saveModel() {
  failure.value = "";
  try {
    await AIService.SaveSettings({
      provider: provider.value,
      baseURL: isCustom.value ? baseURL.value.trim() : (current.value?.baseURL ?? ""),
      model: model.value,
      apiKey: "",
      clearKey: false,
      disableLocalProbe: false,
    });
    await refresh();
    showSettings.value = false;
  } catch (err) {
    failure.value = errorText(err);
  }
}

/** 用户点了某个选项：当成一条用户消息发回去，卡片收起。 */
async function answerAsk(choice: string) {
  const ask = pendingAsk.value;
  pendingAsk.value = null;
  if (!ask) return;
  draft.value = choice;
  await send();
}

/**
 * 用户点了确认或取消。
 *
 * 确认时把令牌原样发回去 —— 令牌绑定「工具 + 参数」且一次性，
 * 所以模型即使被别的说法带着走，也做不了用户没点过的操作。
 */
async function answerConfirm(ok: boolean) {
  const c = pendingConfirm.value;
  pendingConfirm.value = null;
  if (!c) return;
  draft.value = ok ? `确认执行（令牌 ${c.token}）` : "取消，不要执行";
  await send();
}

/**
 * 点正文里的路径 → 在文件管理器里定位到它。
 *
 * 用事件委托而不是给每个路径挂 onClick：正文是 v-html 渲染的，
 * 里面挂不了 Vue 事件；而且一段回答里可能有好几个路径。
 */
async function onContentClick(e: MouseEvent) {
  const el = (e.target as HTMLElement | null)?.closest?.("[data-reveal-path]") as HTMLElement | null;
  if (!el) return;
  const path = el.dataset.revealPath;
  if (!path) return;
  try {
    await Tools.RevealPath(path);
  } catch {
    /* 文件可能已被移走；不弹错，用户自己会看出来 */
  }
}

/**
 * 滚到底部。
 *
 * 不能只 nextTick 一次：消息是异步从库里读出来的，nextTick 时 DOM 可能还没
 * 长出来，这时 scrollHeight 等于视口高度，scrollTop 设成它等于停在顶部 ——
 * 表现就是「打开长会话看到中间某一段」。
 * 所以要等两帧 + 内容高度稳定后再滚。
 */
async function scrollToBottom() {
  await nextTick();
  const el = scroller.value;
  if (!el) return;
  el.scrollTop = el.scrollHeight;
  await new Promise((r) => requestAnimationFrame(() => r(null)));
  el.scrollTop = el.scrollHeight;
  await new Promise((r) => requestAnimationFrame(() => r(null)));
  el.scrollTop = el.scrollHeight;
  atBottom = true;
  reachedBottomOnce = true;
  showJumpDown.value = false;
}

/**
 * 只有在「已经站到底部过」之后才响应「滚到顶部加载更早」。
 *
 * 否则会出现连锁：内容还没渲染 → scrollTop=0 → 自动加载更早 → 位置补偿 →
 * 又接近顶部 → 再加载…… 一下子把所有历史都拉出来，正好抵消了分页的意义。
 */
let atBottom = false;
/** 首屏是否已经落到底过（决定要不要允许「往上翻加载更早」）。 */
let reachedBottomOnce = false;
/** 离开底部后显示「回到底部」按钮。 */
const showJumpDown = ref(false);

/**
 * 滚动时同时做两件事：记录是否离开底部（控制按钮显隐）、
 * 靠近顶部时加载更早的消息。
 */
function onScrollerScroll() {
  const el = scroller.value;
  if (el) {
    const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
    showJumpDown.value = distance > 240;
  }
  void onScroll();
}

/** 回到底部。 */
async function jumpToBottom() {
  await scrollToBottom();
  showJumpDown.value = false;
}

/**
 * 滚到顶部附近时加载更早的消息。
 *
 * 关键是要「保住用户正在看的位置」：新内容插在上面会把整体往下推，
 * 所以补一个 scrollTop 偏移，否则内容会突然乱跳。
 */
async function onScroll() {
  const el = scroller.value;
  if (!el || sess.loadingOlder.value) return;
  if (!reachedBottomOnce) return;              // 首屏还没落到底，先别翻历史
  if (el.scrollHeight <= el.clientHeight + 40) return; // 本来就没得滚
  if (el.scrollTop > 120) return;
  if (!sess.hasMoreTurns.value) return;

  // 记「锚点」而不是记高度差：高度差算法假设内容只从上面长出来、且没人动过
  // scrollTop，但浏览器自己的滚动锚定、加载提示条的插入/移除都会让这个假设失效
  // —— 表现就是加载完画面往前蹿了几条（用户看到的「41 条跳到 51 条」）。
  // 记下当前第一条可见消息的位置，加载完把它放回原处，才是准的。
  const anchor = captureAnchor(el);
  const prevHeight = el.scrollHeight;
  try {
    const added = await sess.loadOlderTurns();
    if (added > 0) {
      await nextTick();
      restoreAnchor(el, anchor, el.scrollHeight - prevHeight);
    }
  } catch {
    /* 失败时 hasMoreTurns 会被置位，用户再点一次即可 */
  }
}

type Anchor = { key: string; offset: number };

/** 记下第一条「还看得见」的消息，以及它相对视口顶部的偏移。 */
function captureAnchor(el: HTMLElement): Anchor | null {
  const top = el.getBoundingClientRect().top;
  for (const node of Array.from(el.querySelectorAll<HTMLElement>("[data-turn-key]"))) {
    const r = node.getBoundingClientRect();
    if (r.bottom > top + 1) {
      return { key: node.dataset.turnKey ?? "", offset: r.top - top };
    }
  }
  return null;
}

/** 把同一条消息放回原来的位置。找不到就退回高度差补偿。 */
function restoreAnchor(el: HTMLElement, anchor: Anchor | null, fallbackDelta: number) {
  if (!anchor) {
    el.scrollTop += fallbackDelta;
    return;
  }
  const node = el.querySelector<HTMLElement>(`[data-turn-key="${CSS.escape(anchor.key)}"]`);
  if (!node) {
    el.scrollTop += fallbackDelta;
    return;
  }
  const top = el.getBoundingClientRect().top;
  el.scrollTop += node.getBoundingClientRect().top - top - anchor.offset;
}

/**
 * 能力包的名字/描述。
 *
 * ★ 这两个字段来自 Go 侧（capability.Pack），是写死的中文 —— 切语言时
 *   不会跟着变，用户会看到「界面英文、功能名中文」。所以这里按 id 查词条，
 *   查不到（新加的能力包还没翻译）再退回 Go 给的值，至少不是空白。
 */
function packName(pk?: { id: string; name: string } | null): string {
  if (!pk) return "";
  const key = `pack.${pk.id}.name`;
  const translated = t(key);
  return translated === key ? pk.name : translated;
}

function packDesc(pk: { id: string; description: string }): string {
  const key = `pack.${pk.id}.desc`;
  const translated = t(key);
  return translated === key ? pk.description : translated;
}


/**
 * 选功能。
 *
 * 第一次选某个功能时先给一个组件提示：功能组件是不是已经在本机。
 * 纯 Go 实现的能力（表格）随应用装好，直接可用；将来接视频 / PDF 这类
 * 需要 ffmpeg / pandoc 的包，会在这里显示要下载什么 —— 与其让用户
 * 点了才看到「exec: ffmpeg not found」，不如提前说清楚。
 */
function chooseFeature(id: string) {
  if (!id) {
    ws.setFeature("");
    return;
  }
  const pack = ws.packs.value.find((x) => x.id === id);
  if (!pack) return;
  const seenKey = `deskkit-feature-seen-${id}`;
  if (localStorage.getItem(seenKey)) {
    ws.setFeature(id);
    return;
  }
  pendingPack.value = {
    id: pack.id,
    name: pack.name,
    bundled: Boolean(pack.bundled),
    missing: pack.missing ?? [],
  };
}

function confirmFeature() {
  if (!pendingPack.value) return;
  localStorage.setItem(`deskkit-feature-seen-${pendingPack.value.id}`, "1");
  ws.setFeature(pendingPack.value.id);
  pendingPack.value = null;
}

function openSettings() {
  testResult.value = null;
  failure.value = "";
  modelsError.value = "";
  // 草稿重置为「已生效」的那一对：上次没保存就关掉的话，这次打开不该
  // 还显示那个被丢弃的选择。
  if (activeProvider.value) {
    provider.value = activeProvider.value;
    model.value = activeModel.value;
    models.value = providers.value.find((p) => p.name === activeProvider.value)?.models ?? [];
    customModel.value = !!model.value && models.value.length > 0 && !models.value.includes(model.value);
    connected.value = Boolean(providers.value.find((p) => p.name === activeProvider.value)?.ready);
  }
  showSettings.value = true;
}

async function send() {
  const text = draft.value.trim();
  if (!text || busy.value) return;
  if (!enabled.value || !ready.value) {
    openSettings();
    return;
  }

  // 发送后自动收起：内容的去处是对话区，输入框该回到安静的等待状态。
  // autogrow 无条件调一次 —— 不只是展开态需要，折叠态清空后也要缩回单行。
  if (expanded.value) expanded.value = false;
  await nextTick();
  autogrow();
  await loadInstructions();
  await sess.appendTurn("user", text);
  await sess.ensureTitle();
  draft.value = "";
  failure.value = "";
  busy.value = true;
  streaming.value = "";
  steps.value = [];
  scrollToBottom();

  offDelta = onDelta((chunk) => {
    streaming.value += chunk;
    scrollToBottom();
  });

  offConfirm = onConfirm((c) => {
    pendingConfirm.value = c;
    scrollToBottom();
  });

  offAsk = onAsk((ask) => {
    pendingAsk.value = ask;
    scrollToBottom();
  });

  offTool = onTool((ev: ToolEvent) => {
    if (ev.tool && HIDDEN_TOOLS.has(ev.tool)) return; // 内部调用不展示
    if (ev.type === "tool_call") {
      if (streaming.value.trim()) {
        // 落一条到历史：工具调用之间的中间话术也要留痕
        void sess.appendTurn("assistant", streaming.value);
        streaming.value = "";
      }
      steps.value.push({ tool: ev.tool || "?", detail: ev.args || "", done: false, ok: true });
    } else if (ev.type === "tool_result") {
      const last = [...steps.value].reverse().find((s) => s.tool === ev.tool && !s.done);
      if (last) {
        last.done = true;
        last.ok = !ev.error;
        last.detail = ev.error || ev.result || last.detail;
      }
    }
    scrollToBottom();
  });

  try {
    const history = withWorkspace(turns.value, {
      paths: ws.paths.value,
      feature: ws.feature.value,
      instructions,
    });
    // ★ 用 active*，不是弹窗里的草稿：改了模型没点保存就不该生效
    const sendProvider = activeProvider.value || provider.value;
    const sendModel = activeModel.value || model.value;
    const result = await AIService.ChatAgent(chatInput(sendProvider, sendModel, history, sess.currentId.value));
    if (streaming.value.trim()) {
      await sess.appendTurn("assistant", streaming.value);
    } else if (result.content && !result.content.trim().startsWith("{")) {
      const last = [...turns.value].reverse().find((x) => x.role === "assistant");
      if (last?.content !== result.content) {
        await sess.appendTurn("assistant", result.content);
      }
    }
  } catch (err) {
    failure.value = errorText(err);
  } finally {
    offDelta?.();
    offTool?.();
    offDelta = null;
    offTool = null;
    busy.value = false;
    streaming.value = "";
    scrollToBottom();
  }
}

/**
 * 输入框自动长高。
 *
 * 默认给三行高度（像 Gemini 那样一眼看出能写多行），内容多了往上长，
 * 到 200px 封顶后内部滚动 —— 否则一条长指令会把对话区挤没。
 */
/** 收起态最多长到这么高，再高就内部滚动 —— 不然把对话区挤没了。 */
const SINGLE_LINE_MAX = 200;
/** 展开态：铺满窗口的编辑视图。 */
const expanded = ref(false);

function autogrow() {
  const el = inputEl.value;
  if (!el) return;
  if (expanded.value) return; // 覆盖层里的编辑区由 CSS 撑满，不参与计算

  // 清空后不写死像素高度，直接交回 CSS（min-h 24px = 单行）——
  // 否则会留着一个「上次撑开的高度」，看着像没还原
  if (!draft.value) {
    el.style.height = "";
    return;
  }
  el.style.height = "auto";
  el.style.height = `${Math.min(el.scrollHeight, SINGLE_LINE_MAX)}px`;
}

/** 展开 / 收起。像 Gemini 输入框右上角那个角标的作用：写长指令时用大框。 */
function toggleExpand() {
  expanded.value = !expanded.value;
  nextTick(() => {
    autogrow();
    // 展开进大框、收起回小框，焦点都跟着走
    (expanded.value ? expandedInput.value : inputEl.value)?.focus();
  });
}

/** Esc 收起，符合「放大视图按 Esc 退出」的惯例。 */
function onEscape(e: KeyboardEvent) {
  if (e.key === "Escape" && expanded.value) {
    e.preventDefault();
    toggleExpand();
  }
}

watch(draft, () => nextTick(autogrow));
onMounted(() => nextTick(autogrow));

function onKeydown(e: KeyboardEvent) {
  if (e.key === "Enter" && !e.shiftKey && !e.isComposing) {
    e.preventDefault();
    send();
  }
}

onMounted(() => {
  // 首次进入要停在最后一条消息：用户回来是要接着聊，不是从头读一遍。
  // initSessions 里会 select 最近那个会话，这里等它读完再滚。
  void initSessions().then(() => scrollToBottom());
  refresh().catch((err) => (failure.value = errorText(err)));
  ws.loadPacks().catch(() => {});
});

// 切换会话：内容换了，位置也要跟着回到最新
watch(
  () => sess.currentId.value,
  () => {
    atBottom = false;
    reachedBottomOnce = false;
    void scrollToBottom();
  },
);

// 消息是异步读出来的：条数变化后（首屏加载完 / 往上翻完）重新贴底。
// 往上翻的情况不该贴底，所以只在 turns 变长且还没到底时补一次。
watch(
  () => sess.turns.value.length,
  () => {
    if (!atBottom) void scrollToBottom();
  },
);
onUnmounted(() => {
  offDelta?.();
  offTool?.();
  offAsk?.();
  offConfirm?.();
});
</script>

<template>
  <div class="relative flex h-full min-h-0 flex-col">
    <!-- ── 对话 ── -->
    <!-- 滚动区外面套一层 relative：浮动按钮要相对「聊天记录区域」定位，
         而不是相对整列（那样会贴到输入框边上）。 -->
    <div class="relative min-h-0 flex-1">
      <div ref="scroller" class="h-full selectable overflow-y-auto" @click="onContentClick" @scroll.passive="onScrollerScroll">
      <div class="mx-auto max-w-[800px] px-6 py-8">
        <!-- 更早的消息：吸在顶部一直可见，点一下或往上滚都能加载。
             不显示这个的话，用户会以为会话只有这么长。 -->
        <!-- 只在真的在加载时显示一行转圈：平时的「这里还有更早的」提示没有信息量，
             用户往上滚自然会加载，不需要被告知。 -->
        <div v-if="sess.loadingOlder.value" class="sticky top-0 z-10 -mt-6 mb-4 flex justify-center pt-2">
          <div class="flex items-center gap-1.5 rounded-full border border-border bg-card px-3 py-1 text-[12px] text-muted-foreground">
            <Loader2 :size="12" class="animate-spin" />
            {{ t("chat.loadingOlder") }}
          </div>
        </div>

        <!-- 首屏 = 产品介绍。用户第一次打开不知道它能干什么，
             与其放一句「你想做什么」，不如把能力和边界直接摊开：
             现在能用的、还要等组件的，都写清楚。 -->
        <div v-if="turns.length === 0 && !streaming" class="flex min-h-full flex-col items-center justify-center py-8">
          <div class="w-full max-w-[680px]">
            <div class="mb-1.5 flex items-center gap-2">
              <FileSpreadsheet :size="18" class="text-primary" />
              <span class="text-[12.5px] font-medium uppercase tracking-wide text-muted-foreground">{{ t("intro.kicker") }}</span>
            </div>
            <h1 class="text-[26px] font-semibold leading-[1.3] tracking-tight">{{ t("intro.title") }}</h1>
            <p class="mt-3 max-w-[560px] text-[14.5px] leading-[1.75] text-muted-foreground">{{ t("intro.lead") }}</p>

            <!-- 能力清单：三组，逐条列它到底能干什么 -->
            <div class="mt-6 space-y-2.5">
              <div v-for="group in intro" :key="group.id" class="rounded-xl border border-border bg-card px-4 py-3.5">
                <div class="flex items-baseline gap-2">
                  <component :is="group.icon" :size="15" class="translate-y-[2px] text-primary" />
                  <span class="text-[14.5px] font-medium">{{ group.name }}</span>
                  <span
                    class="rounded-full px-1.5 py-0.5 text-[11.5px]"
                    :class="group.available ? 'bg-primary/12 text-primary' : 'bg-muted text-muted-foreground'"
                  >
                    {{ group.available ? t("intro.ready") : group.badge }}
                  </span>
                </div>
                <ul class="mt-2 space-y-1 pl-6 text-[13px] leading-[1.65] text-muted-foreground">
                  <li v-for="item in group.items" :key="item" class="list-outside list-disc marker:text-border">{{ item }}</li>
                </ul>
              </div>
            </div>

          </div>
        </div>

        <template v-for="(turn, i) in turns" :key="turn.id ?? i">
          <!-- 每 20 条一个分页标记：既是视觉上的「到哪儿了」，也是往上翻时的落点参考 -->
          <div v-if="i % MSG_PAGE === 0" class="my-4 flex items-center gap-2 text-[11.5px] text-muted-foreground/70">
            <span class="h-px flex-1 bg-border" />
            <span>{{ pageMarkLabel(turn.at) }}</span>
            <span class="h-px flex-1 bg-border" />
          </div>
          <div :data-turn-key="String(turn.id ?? i)" class="mb-5">
          <div v-if="turn.role === 'user'" class="flex justify-end">
            <div class="max-w-[85%] whitespace-pre-wrap rounded-2xl rounded-br-sm bg-primary px-4 py-2.5 text-[length:var(--chat-fs)] leading-[1.7] text-primary-foreground">
              {{ turn.content }}
            </div>
          </div>
          <!-- 模型回答按 markdown 渲染：表格、代码块、列表都是常态 -->
          <template v-else-if="turn.role === 'assistant'">
            <div class="md selectable text-[length:var(--chat-fs)] leading-[1.75]" v-html="renderMarkdown(turn.content)" />
            <!-- 底部三个图标：赞、踩、复制。前两个只是手感，不记录 -->
            <div class="mt-1.5 flex items-center gap-0.5">
              <button
                type="button"
                class="grid h-7 w-7 place-items-center rounded-md transition-[transform,background-color,color] duration-150 active:scale-[0.82]"
                :class="
                  reactions[i] === 1
                    ? 'bg-primary/12 text-primary'
                    : 'text-muted-foreground hover:bg-accent hover:text-foreground'
                "
                :aria-pressed="reactions[i] === 1"
                :title="t('chat.like')"
                @click="react(i, 1)"
              >
                <ThumbsUp :size="14" :class="reactions[i] === 1 ? 'fill-current' : ''" />
              </button>
              <button
                type="button"
                class="grid h-7 w-7 place-items-center rounded-md transition-[transform,background-color,color] duration-150 active:scale-[0.82]"
                :class="
                  reactions[i] === -1
                    ? 'bg-destructive/12 text-destructive'
                    : 'text-muted-foreground hover:bg-accent hover:text-foreground'
                "
                :aria-pressed="reactions[i] === -1"
                :title="t('chat.dislike')"
                @click="react(i, -1)"
              >
                <ThumbsDown :size="14" :class="reactions[i] === -1 ? 'fill-current' : ''" />
              </button>
              <button
                type="button"
                class="grid h-7 w-7 place-items-center rounded-md text-muted-foreground transition-[transform,background-color,color] duration-150 hover:bg-accent hover:text-foreground active:scale-[0.82]"
                :class="copiedAt === i ? 'text-primary' : ''"
                :title="copiedAt === i ? t('chat.copied') : t('chat.copy')"
                @click="copyTurn(i, turn.content)"
              >
                <Check v-if="copiedAt === i" :size="14" />
                <Copy v-else :size="14" />
              </button>
            </div>
          </template>
          </div>
        </template>

        <div v-if="steps.length" class="mb-3 space-y-1">
          <div
            v-for="(s, i) in steps"
            :key="i"
            class="flex items-start gap-2 text-[13px] text-muted-foreground"
          >
            <Wrench :size="12" class="mt-0.5 shrink-0" />
            <span class="font-mono">{{ s.tool }}</span>
            <Loader2 v-if="!s.done" :size="12" class="mt-0.5 animate-spin" />
            <Check v-else-if="s.ok" :size="12" class="mt-0.5 text-emerald-600" />
            <X v-else :size="12" class="mt-0.5 text-destructive" />
            <span class="min-w-0 truncate">{{ s.detail }}</span>
          </div>
        </div>

        <!-- ── 危险操作确认 ──
             清单一行一个完整路径：用户是靠路径判断「是不是这几个」的，
             只给「3 个文件」等于没给信息。 -->
        <div v-if="pendingConfirm" class="mt-1 mb-3 rounded-xl border border-destructive/40 bg-destructive/5 p-3.5">
          <p class="text-[length:var(--chat-fs)] font-medium">{{ pendingConfirm.title }}</p>
          <ul class="mt-2 space-y-0.5">
            <li
              v-for="item in pendingConfirm.items"
              :key="item"
              class="select-text break-all font-mono text-[12.5px] leading-[1.6] text-muted-foreground"
            >
              · {{ item }}
            </li>
          </ul>
          <p v-if="pendingConfirm.note" class="mt-2 text-[13px] text-destructive">{{ pendingConfirm.note }}</p>
          <div class="mt-2.5 flex flex-wrap gap-1.5">
            <button
              type="button"
              class="rounded-md bg-destructive px-3 py-1.5 text-[13px] font-medium text-destructive-foreground transition-transform active:scale-[0.97]"
              @click="answerConfirm(true)"
            >
              {{ t("chat.confirmYes") }}
            </button>
            <button
              type="button"
              class="rounded-md border border-border px-3 py-1.5 text-[13px] text-muted-foreground transition-colors hover:bg-accent"
              @click="answerConfirm(false)"
            >
              {{ t("chat.confirmNo") }}
            </button>
          </div>
        </div>

        <!-- ── 模型在等用户点选 ──
             渲染成按钮而不是让它自己打字回答：问题本身就是个小决定，
             点一下比敲一段话快，也不会答得含糊。 -->
        <div v-if="pendingAsk" class="mt-1 mb-3 rounded-xl border border-border bg-card p-3.5">
          <p class="text-[length:var(--chat-fs)] leading-[1.6]">{{ pendingAsk.question }}</p>
          <p v-if="pendingAsk.detail" class="mt-1.5 whitespace-pre-wrap text-[13px] leading-relaxed text-muted-foreground">
            {{ pendingAsk.detail }}
          </p>
          <div class="mt-2.5 flex flex-wrap gap-1.5">
            <button
              v-for="opt in pendingAsk.options"
              :key="opt"
              type="button"
              class="rounded-md border border-input bg-background px-3 py-1.5 text-[13px] transition-[transform,background-color,border-color] hover:border-primary/50 hover:bg-accent active:scale-[0.97]"
              @click="answerAsk(opt)"
            >
              {{ opt }}
            </button>
          </div>
        </div>

        <!-- 流式过程中也渲染：边出边成形，不会先看到一堆 *** -->
        <div v-if="streaming" class="md selectable text-[length:var(--chat-fs)] leading-[1.75]" v-html="renderMarkdown(streaming)" />
        <div v-if="busy && !streaming" class="flex items-center gap-2 text-[13px] text-muted-foreground">
          <Loader2 :size="13" class="animate-spin" />{{ t("ai.thinking") }}
        </div>
        <p v-if="failure || ws.failure.value" class="mt-3 text-[13px] text-destructive">
          {{ failure || ws.failure.value }}
        </p>
      </div>
    </div>


      <!-- 回到底部：翻了一段历史之后一路滚回去很累。
           浮动在聊天记录区底部，离开底部才出现，平时不占地方。
           用主色（暖琥珀）而不是灰底：它是「动作」，不是状态提示。 -->
      <button
        v-if="showJumpDown"
        type="button"
        class="absolute bottom-4 left-1/2 z-20 grid h-9 w-9 -translate-x-1/2 place-items-center rounded-full bg-primary text-primary-foreground shadow-lg shadow-primary/25 transition-[opacity,transform,background-color] duration-150 hover:bg-primary/90 active:scale-[0.92]"
        :title="t('chat.jumpToBottom')"
        :aria-label="t('chat.jumpToBottom')"
        @click="jumpToBottom"
      >
        <ArrowDown :size="16" />
      </button>
    </div>

    <!-- ── 输入区：对话的一部分，不是工具栏 ── -->
    <div class="shrink-0 px-6 pb-5">
      <div class="mx-auto max-w-[800px]">
        <!-- 已选文件：一个小标签。第二行小字说明结果会写到哪 -->
        <div v-if="ws.paths.value.length > 0" class="mb-2 flex flex-wrap items-center gap-x-3 gap-y-1">
          <span class="inline-flex max-w-full items-center gap-1.5 rounded-full bg-muted px-2.5 py-1 text-[13px]">
            <FolderOpen :size="12" class="shrink-0 text-muted-foreground" />
            <button type="button" class="truncate font-medium hover:underline" :title="ws.paths.value.join('\n')" @click="ws.pickPaths(t('ai.pickInput'))">
              {{ ws.selectionLabel.value }}
            </button>
            <button type="button" class="shrink-0 text-muted-foreground hover:text-foreground" @click="ws.clearPaths()">
              <X :size="12" />
            </button>
          </span>
        </div>

        <!-- 输入框按 Gemini 的分层：
             上方只有内容 + 右上角的缩放角标；
             文件、功能、模型、发送全部压在框底那一行。 -->
        <div
          class="relative rounded-[26px] border border-input bg-card px-3 pb-2 pt-3 transition-shadow focus-within:border-ring focus-within:ring-1 focus-within:ring-ring"
        >
          <!-- 右上角：只有放大 -->
          <button
            type="button"
            class="absolute right-3 top-2.5 grid size-6 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
            :title="t('ai.expand')"
            @click="toggleExpand"
          >
            <Maximize2 :size="13" />
          </button>

          <textarea
            ref="inputEl"
            v-model="draft"
            rows="1"
            :placeholder="ws.feature.value ? t('ai.placeholderFeature') : t('ai.placeholder')"
            class="max-h-[200px] min-h-[24px] w-full resize-none bg-transparent py-1 pr-8 text-[length:var(--chat-fs)] leading-[1.6] outline-none placeholder:text-muted-foreground"
            @input="autogrow"
            @keydown="onKeydown"
            @keydown.esc="onEscape"
          />

          <!-- 框底那一行：左边附件与功能，右边模型与发送 -->
          <div class="mt-1 flex items-center gap-1">
            <AttachButton />

            <div class="flex-1" />

            <button
              type="button"
              class="grid size-9 place-items-center rounded-full text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
              :title="t('ai.modelSettings')"
              @click="openSettings"
            >
              <Settings2 :size="17" />
            </button>

            <button
              type="button"
              class="inline-flex size-9 items-center justify-center rounded-full bg-primary text-primary-foreground transition-opacity disabled:opacity-35"
              :disabled="!draft.trim() || busy"
              :title="t('ai.send')"
              @click="send"
            >
              <ArrowUp :size="16" />
            </button>
          </div>
        </div>

        <div class="mt-1.5 flex items-center justify-between px-1 text-[11.5px] text-muted-foreground">
          <span v-if="ready" class="truncate">{{ activeLabel }} · {{ activeModel }}</span>
          <button v-else type="button" class="text-primary hover:underline" @click="openSettings">
            {{ t("ai.configureFirst") }}
          </button>
        </div>
      </div>
    </div>

    <!-- ── 首次使用某功能的组件提示 ── -->
    <div
      v-if="pendingPack"
      class="fixed inset-0 z-50 grid place-items-center bg-black/40 p-6"
      @click.self="pendingPack = null"
    >
      <div class="w-full max-w-[460px] rounded-xl border border-border bg-background p-5 shadow-xl">
        <div class="flex items-start gap-2">
          <Layers :size="16" class="mt-0.5 shrink-0 text-primary" />
          <div>
            <h3 class="text-[14.5px] font-semibold">{{ pendingPack.name }}</h3>
            <p class="mt-1.5 text-[13px] leading-relaxed text-muted-foreground">
              {{ pendingPack.bundled ? t("ai.packBundled") : t("ai.packDownload") }}
            </p>
            <ul v-if="pendingPack.missing.length" class="mt-2 list-inside list-disc text-[13px] text-amber-600">
              <li v-for="m in pendingPack.missing" :key="m">{{ m }}</li>
            </ul>
          </div>
        </div>
        <div class="mt-4 flex justify-end gap-2">
          <button type="button" class="h-9 rounded-md border border-input px-3.5 text-[14px] hover:bg-accent" @click="pendingPack = null">
            {{ t("ai.cancel") }}
          </button>
          <button type="button" class="h-9 rounded-md bg-primary px-4 text-[14px] font-medium text-primary-foreground" @click="confirmFeature">
            {{ pendingPack.bundled ? t("ai.useFeature") : t("ai.downloadFeature") }}
          </button>
        </div>
      </div>
    </div>

    <!-- 满屏编辑挂到 body：它要盖住整个窗口，不能被父级的 overflow 裁掉 -->
    <Teleport to="body">
    <!-- ── 满屏编辑：只覆盖会话区 ──
         定位基准是外层 relative 容器（= 标题栏以下、侧栏以右的那块），
         所以顶栏和左侧会话记录都不会被盖住。 -->
    <div v-if="expanded" class="absolute inset-0 z-40 flex flex-col bg-background">
        <div class="flex shrink-0 items-center justify-between border-b border-border px-5 py-2.5">
          <span class="text-[13px] text-muted-foreground">{{ t("ai.expandTitle") }}</span>
          <button
            type="button"
            class="inline-flex items-center gap-1.5 rounded-md border border-border px-2.5 py-1 text-[13px] transition-colors hover:bg-accent"
            @click="toggleExpand"
          >
            <Minimize2 :size="13" />
            {{ t("ai.collapse") }}
          </button>
        </div>

        <div class="mx-auto flex min-h-0 w-full max-w-[880px] flex-1 flex-col px-6 py-4">
          <textarea
            ref="expandedInput"
            v-model="draft"
            class="min-h-0 flex-1 resize-none bg-transparent text-[length:var(--chat-fs)] leading-[1.75] outline-none placeholder:text-muted-foreground"
            :placeholder="ws.feature.value ? t('ai.placeholderFeature') : t('ai.placeholder')"
            @keydown="onKeydown"
            @keydown.esc="onEscape"
          />
          <!-- 两个图标和折叠态一致（选文件 / 选功能），右边发送；
               另一个「收起」在顶部条右侧 -->
          <div class="flex shrink-0 items-center gap-1 pt-3">
            <AttachButton />

            <div class="flex-1" />

            <button
              type="button"
              class="inline-flex size-9 items-center justify-center rounded-full bg-primary text-primary-foreground transition-opacity disabled:opacity-35"
              :disabled="!draft.trim() || busy"
              :title="t('ai.send')"
              @click="send"
            >
              <ArrowUp :size="16" />
            </button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- ── 模型设置 ── -->
    <div
      v-if="showSettings"
      class="fixed inset-0 z-50 grid place-items-center bg-black/40 p-6"
      @click.self="showSettings = false"
    >
      <div class="w-full max-w-[600px] rounded-xl border border-border bg-background shadow-xl">
        <div class="flex items-center justify-between border-b border-border px-5 py-3">
          <h3 class="text-[14.5px] font-semibold">{{ t("ai.modelSettings") }}</h3>
          <button type="button" class="grid size-7 place-items-center rounded-md text-muted-foreground hover:bg-accent" @click="showSettings = false">
            <X :size="15" />
          </button>
        </div>

        <div class="max-h-[70vh] space-y-5 overflow-y-auto px-5 py-4">
          <p v-if="!enabled" class="rounded-md bg-muted/50 p-3 text-[13px] leading-relaxed text-muted-foreground">
            {{ t("ai.disabledHint") }}
          </p>

          <section>
            <p class="mb-2 flex items-center gap-2 text-[13px] font-medium">
              <span class="grid size-5 place-items-center rounded-full bg-primary text-[11px] text-primary-foreground">1</span>
              {{ t("ai.stepConnect") }}
            </p>
            <!-- 服务商也用下拉，和下面的模型选择长得一样。
                 九个格子摊在弹窗里像张仪表盘，实际每次只会选一个。 -->
            <div class="relative">
              <select
                :value="provider"
                class="h-9 w-full appearance-none rounded-md border border-input bg-background pl-2.5 pr-8 text-[13px]"
                @change="pickProvider(($event.target as HTMLSelectElement).value)"
              >
                <option v-for="p in providers" :key="p.name" :value="p.name">
                  {{ p.label || p.name }} · {{ p.ready ? t("ai.ready") : p.local ? t("ai.notRunning") : t("ai.needsKey") }}
                </option>
              </select>
              <ChevronDown
                :size="14"
                class="pointer-events-none absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground"
              />
            </div>

            <label v-if="isCustom" class="mt-3 block">
              <span class="text-[13px] text-muted-foreground">{{ t("ai.baseURL") }}</span>
              <input
                v-model="baseURL"
                class="mt-1 w-full rounded-md border border-input bg-background px-2.5 py-1.5 font-mono text-[13px]"
                placeholder="https://example.com/v1"
              />
            </label>

            <label v-if="!current?.local" class="mt-3 block">
              <span class="text-[13px] text-muted-foreground">API Key</span>
              <input
                v-model="apiKey"
                type="password"
                autocomplete="off"
                class="mt-1 w-full rounded-md border border-input bg-background px-2.5 py-1.5 font-mono text-[13px]"
                :placeholder="current?.hasKey ? t('ai.keySaved') : 'sk-...'"
                @keydown.enter.prevent="connect"
              />
            </label>

            <div class="mt-3 flex items-center gap-2">
              <button
                type="button"
                class="inline-flex h-9 items-center gap-2 rounded-md bg-primary px-4 text-[14px] font-medium text-primary-foreground disabled:opacity-50"
                :disabled="testing || !provider"
                @click="connect"
              >
                <Loader2 v-if="testing" :size="14" class="animate-spin" />
                {{ t("ai.connect") }}
              </button>
              <span v-if="testResult" class="flex items-center gap-1 text-[13px]" :class="testResult.ok ? 'text-emerald-600' : 'text-destructive'">
                <Check v-if="testResult.ok" :size="13" />
                <X v-else :size="13" />
                {{ testResult.ok ? t("ai.connected") : testResult.error || testResult.hint || t("ai.testFailed") }}
              </span>
              <span v-else-if="connected && ready" class="text-[13px] text-emerald-600">{{ t("ai.alreadyConnected") }}</span>
            </div>
          </section>

          <section :class="connected || ready ? '' : 'opacity-50'">
            <p class="mb-2 flex items-center gap-2 text-[13px] font-medium">
              <span class="grid size-5 place-items-center rounded-full bg-primary text-[11px] text-primary-foreground">2</span>
              {{ t("ai.stepModel") }}
            </p>

            <!-- 下拉 + 刷新并排：先看服务商有什么，不满意就再要一次 -->
            <div class="flex items-center gap-2">
              <div class="relative min-w-0 flex-1">
                <select
                  v-model="modelChoice"
                  class="h-9 w-full appearance-none rounded-md border border-input bg-background pl-2.5 pr-8 font-mono text-[13px]"
                >
                  <option v-if="models.length === 0" value="" disabled>{{ t("ai.pickModelHint") }}</option>
                  <option v-for="m in models" :key="m" :value="m">{{ m }}</option>
                  <option :value="CUSTOM">{{ t("ai.customModel") }}</option>
                </select>
                <ChevronDown
                  :size="14"
                  class="pointer-events-none absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground"
                />
              </div>
              <!-- 向服务商要一次模型列表。用主色：它是个动作，不是装饰 -->
              <button
                type="button"
                class="inline-flex h-9 shrink-0 items-center gap-1.5 rounded-md bg-primary px-3 text-[13px] font-medium text-primary-foreground transition-[transform,opacity] active:scale-[0.97] disabled:opacity-45"
                :disabled="loadingModels || !(connected || ready)"
                @click="loadModels"
              >
                <RefreshCw :size="13" :class="loadingModels ? 'animate-spin' : ''" />
                {{ t("ai.refreshModels") }}
              </button>
            </div>
            <input
              v-if="customModel"
              v-model="model"
              class="mt-2 h-9 w-full rounded-md border border-input bg-background px-2.5 font-mono text-[13px]"
              :placeholder="t('ai.modelPlaceholder')"
            />
            <p v-if="modelsError" class="mt-1.5 text-[13px] text-destructive">{{ modelsError }}</p>
          </section>

          <p v-if="failure" class="text-[13px] text-destructive">{{ failure }}</p>
        </div>

        <div class="flex items-center justify-end gap-2 border-t border-border px-5 py-3">
          <button type="button" class="h-9 rounded-md border border-input px-3.5 text-[14px] hover:bg-accent" @click="showSettings = false">
            {{ t("ai.cancel") }}
          </button>
          <button
            type="button"
            class="h-9 rounded-md bg-primary px-4 text-[14px] font-medium text-primary-foreground disabled:opacity-50"
            :disabled="!model || !(connected || ready)"
            @click="saveModel"
          >
            {{ t("ai.useThisModel") }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
