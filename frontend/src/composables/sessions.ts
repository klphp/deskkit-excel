/**
 * 会话管理（存在 Go 侧的加密 SQLite 里）。
 *
 * 之前用 localStorage，有三个问题：容量约 5MB、清一次 WebView 数据就全没了、
 * 没法备份也没法排查。历史属于用户资料，必须落盘、可带走、并且加密。
 *
 * 现在的落点：
 *   Go: internal/session（SQLCipher 加密库，密钥在系统钥匙串，用户无感）
 *   前端：只保证「界面状态」和库里一致，不做任何持久化
 *
 * 一次性的迁移：老版本把历史写在 localStorage 的 deskkit-sessions-v1，
 * 启动时读出来导进库，导入成功后删掉那个 key。
 */
import { computed, ref } from "vue";
import { ChatTurn } from "../lib/ai";
import * as Sessions from "../../bindings/github.com/klphp/excel-batch-tool/internal/services/sessionservice";
import type { Session as DbSession } from "../../bindings/github.com/klphp/excel-batch-tool/internal/session/models";

const LEGACY_KEY = "deskkit-sessions-v1";

/** 会话摘要（列表用）。 */
export type Summary = {
  id: string;
  title: string;
  updatedAt: number;
  count: number;
};

/**
 * 分页参数。
 *
 * 会话是只增不删的：用一年就是几千条。一次全查出来，前端要渲染几千个节点，
 * 进设置页再退回来都得顿一下 —— 而用户真正看的永远是最近那几十条。
 *
 * 消息同理：聊了几百轮的会话，打开时只需要最后一屏。
 */
const SESS_PAGE = 30;
/**
 * 聊天区每页消息条数。
 *
 * 20 条大约是一屏半：首屏打开就是最新的一屏，往上翻一格就能再取一页 ——
 * 分页是能被用户看见的。设成 40 时反而「一次翻太多」，用户分不清自己在哪。
 */
const MSG_PAGE = 20;
/**
 * 加载更早消息时，「加载中」至少有这么多毫秒可见。
 *
 * 本地读库是毫秒级的，不设下限的话提示会一闪而过，用户根本不知道
 * 发生了什么，只觉得内容突然跳了一下。这里不是造假延迟，而是让
 * 反馈「看得见」——低于人眼可感知时间（约 300ms）的状态变化等于没做。
 */
const LOADING_MIN_MS = 450;

const summaries = ref<Summary[]>([]);
const currentId = ref("");
/** 当前会话的消息。 */
const turns = ref<ChatTurn[]>([]);
/** 会话列表还有没有更多（侧栏滚到底时再去取）。 */
const hasMoreSessions = ref(false);
/** 当前会话还有没有更早的消息（聊天区滚到顶时再去取）。 */
const hasMoreTurns = ref(false);
/** 已加载的最早一条消息 id，作为「加载更早」的游标。 */
let oldestMsgID = 0;
/** 正在加载更早的消息（界面上要显示出来）。 */
const loadingOlder = ref(false);
const ready = ref(false);
const error = ref("");

function newID(): string {
  return `s${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;
}

function toTurn(role: string, content: string, id?: number, at?: number): ChatTurn {
  return { role: role === "assistant" ? "assistant" : "user", content, id, at };
}

/** 从 Go 侧重读列表。 */
async function reload() {
  try {
    const page = await Sessions.ListPage(SESS_PAGE, 0);
    const list = page?.items ?? [];
    hasMoreSessions.value = !!page?.hasMore;
    summaries.value = list.map((s) => ({
      id: s.id,
      title: s.title,
      updatedAt: s.updatedAt,
      count: s.count,
    }));
  } catch (err) {
    error.value = String(err);
  }
}

/** 切换会话：读完整消息。 */
async function select(id: string) {
  if (!summaries.value.some((s) => s.id === id)) return;
  try {
    currentId.value = id;
    // 只取最后一屏：打开会话时用户要看的是最后在聊什么
    const page = await Sessions.MessagesPage(id, MSG_PAGE, 0);
    const msgs = page?.messages ?? [];
    turns.value = msgs.map((m) => toTurn(m.role, m.content, m.id, m.createdAt));
    hasMoreTurns.value = !!page?.hasMore;
    oldestMsgID = msgs.length > 0 ? msgs[0].id : 0;
  } catch (err) {
    error.value = String(err);
    turns.value = [];
    hasMoreTurns.value = false;
    oldestMsgID = 0;
  }
}

/**
 * 往上翻：加载更早的一页消息。
 *
 * 返回新加载的条数，调用方据此决定要不要把滚动位置往下补 ——
 * 不补的话内容会突然整体下移，用户正在看的那条就跳走了。
 */
async function loadOlderTurns(): Promise<number> {
  if (!hasMoreTurns.value || loadingOlder.value || !currentId.value || oldestMsgID === 0) return 0;
  loadingOlder.value = true;
  const startedAt = Date.now();
  try {
    const page = await Sessions.MessagesPage(currentId.value, MSG_PAGE, oldestMsgID);
    const msgs = page?.messages ?? [];
    if (msgs.length === 0) {
      hasMoreTurns.value = false;
      return 0;
    }
    turns.value = [...msgs.map((m) => toTurn(m.role, m.content, m.id, m.createdAt)), ...turns.value];
    hasMoreTurns.value = !!page?.hasMore;
    oldestMsgID = msgs[0].id;
    return msgs.length;
  } catch (err) {
    error.value = String(err);
    return 0;
  } finally {
    // 补足最小可见时长
    const spent = Date.now() - startedAt;
    if (spent < LOADING_MIN_MS) {
      await new Promise((r) => setTimeout(r, LOADING_MIN_MS - spent));
    }
    loadingOlder.value = false;
  }
}

/** 侧栏滚到底：再取一页会话。 */
async function loadMoreSessions(): Promise<number> {
  if (!hasMoreSessions.value) return 0;
  try {
    const page = await Sessions.ListPage(SESS_PAGE, summaries.value.length);
    const items = page?.items ?? [];
    const exist = new Set(summaries.value.map((s) => s.id));
    let added = 0;
    for (const it of items) {
      if (exist.has(it.id)) continue;
      summaries.value.push({
        id: it.id,
        title: it.title,
        updatedAt: it.updatedAt,
        count: it.count,
      });
      added++;
    }
    hasMoreSessions.value = !!page?.hasMore;
    return added;
  } catch (err) {
    error.value = String(err);
    return 0;
  }
}

/** 新建会话（先落库，再切过去）。 */
async function create(): Promise<void> {
  const id = newID();
  try {
    await Sessions.Upsert(id, "");
  } catch (err) {
    error.value = String(err);
  }
  currentId.value = id;
  turns.value = [];
  await reload();
}

/** 追加一条消息：先更新界面，再落库（落库失败不影响继续聊）。 */
async function appendTurn(role: "user" | "assistant", content: string) {
  if (!content.trim()) return;
  turns.value = [...turns.value, { role, content }];
  const id = currentId.value;
  if (!id) return;
  try {
    await Sessions.Append(id, role, content);
    await reload();
  } catch (err) {
    error.value = String(err);
  }
}

/** 第一条用户消息决定标题。 */
async function ensureTitle() {
  const id = currentId.value;
  const cur = summaries.value.find((s) => s.id === id);
  if (!id || cur?.title) return;
  const first = turns.value.find((t) => t.role === "user");
  if (!first) return;
  const text = first.content.replace(/\s+/g, " ").trim();
  const title = text.length > 24 ? `${text.slice(0, 24)}…` : text;
  try {
    await Sessions.Rename(id, title);
    await reload();
  } catch (err) {
    error.value = String(err);
  }
}

/** 删除会话。 */
async function remove(id: string) {
  try {
    await Sessions.Delete(id);
  } catch (err) {
    error.value = String(err);
  }
  const wasCurrent = currentId.value === id;
  await reload();
  if (wasCurrent) {
    if (summaries.value.length > 0) {
      await select(summaries.value[0].id);
    } else {
      await create();
    }
  }
}

/** 改名。 */
async function rename(id: string, title: string) {
  try {
    await Sessions.Rename(id, title);
    await reload();
  } catch (err) {
    error.value = String(err);
  }
}

/**
 * 把旧版 localStorage 的历史导进库。
 *
 * 只在首次运行（库里没有会话）时做，导入成功后删掉旧 key ——
 * 留着会让下次启动又导一遍。
 */
async function migrateLegacy() {
  let raw: string | null = null;
  try {
    raw = localStorage.getItem(LEGACY_KEY);
  } catch {
    return;
  }
  if (!raw) return;

  try {
    const parsed = JSON.parse(raw) as Array<{
      id: string;
      title: string;
      createdAt: number;
      updatedAt: number;
      turns: ChatTurn[];
    }>;
    if (!Array.isArray(parsed) || parsed.length === 0) {
      localStorage.removeItem(LEGACY_KEY);
      return;
    }
    const items = parsed
      .filter((s) => s && s.id && Array.isArray(s.turns))
      .map((s) => ({
        id: s.id,
        title: s.title ?? "",
        createdAt: s.createdAt ?? 0,
        updatedAt: s.updatedAt ?? 0,
        messages: s.turns.map((t) => ({
          role: t.role,
          content: t.content,
          createdAt: 0,
        })),
      })) as DbSession[];

    const n = await Sessions.Import(items);
    if (n >= 0) localStorage.removeItem(LEGACY_KEY);
  } catch {
    // 旧数据格式不对就放弃迁移，别让应用卡在这儿
    localStorage.removeItem(LEGACY_KEY);
  }
}

const ordered = computed(() =>
  [...summaries.value].sort((a, b) => b.updatedAt - a.updatedAt),
);
const current = computed(() => summaries.value.find((s) => s.id === currentId.value));

let started = false;
/** 初始化一次（模块级，多个组件共用）。 */
export async function initSessions() {
  if (started) return;
  started = true;

  try {
    ready.value = await Sessions.Ready();
  } catch {
    ready.value = false;
  }
  if (!ready.value) {
    error.value = "会话存储不可用";
    return;
  }

  await migrateLegacy();
  await reload();

  if (summaries.value.length > 0) {
    await select(summaries.value[0].id);
  } else {
    await create();
  }
}

/**
 * 外部（比如设置里清空历史）改动库之后，让界面重新对一次。
 *
 * 没有会话了就新建一个空会话 —— 主界面必须始终有一个「当前会话」，
 * 否则输入框发出去没地方落。
 */
export async function refreshSessions(): Promise<void> {
  if (!ready.value) return;
  await reload();
  if (summaries.value.length === 0) {
    await create();
    return;
  }
  const stillThere = summaries.value.some((s) => s.id === currentId.value);
  await select(stillThere ? currentId.value : summaries.value[0].id);
}

export function useSessions() {
  return {
    summaries,
    ordered,
    current,
    currentId,
    turns,
    hasMoreSessions,
    hasMoreTurns,
    loadingOlder,
    loadOlderTurns,
    loadMoreSessions,
    ready,
    error,
    create,
    select,
    remove,
    rename,
    appendTurn,
    ensureTitle,
  };
}
