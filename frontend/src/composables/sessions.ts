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

const summaries = ref<Summary[]>([]);
const currentId = ref("");
/** 当前会话的消息。 */
const turns = ref<ChatTurn[]>([]);
const ready = ref(false);
const error = ref("");

function newID(): string {
  return `s${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;
}

function toTurn(role: string, content: string): ChatTurn {
  return { role: role === "assistant" ? "assistant" : "user", content };
}

/** 从 Go 侧重读列表。 */
async function reload() {
  try {
    const list = (await Sessions.List()) ?? [];
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
    const s = (await Sessions.Load(id)) as DbSession;
    turns.value = (s.messages ?? []).map((m) => toTurn(m.role, m.content));
  } catch (err) {
    error.value = String(err);
    turns.value = [];
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

export function useSessions() {
  return {
    summaries,
    ordered,
    current,
    currentId,
    turns,
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
