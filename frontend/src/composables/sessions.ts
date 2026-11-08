/**
 * 会话管理。
 *
 * 左边那栏要能「新开会话 / 切回上一个」，所以每次对话都得有身份和存档。
 *
 * 存在 localStorage 而不是后端：这些是界面状态（标题、消息、时间），
 * 桌面应用里 WebView 的数据就在本机，不牵涉任何上传；也不必为它
 * 新增一组 HTTP 接口。真要跨设备同步是另一个产品问题。
 *
 * 标题不手填：取第一条用户消息的前若干字。用户不需要再多做一个动作，
 * 事后也能一眼认出是哪次对话。
 */
import { computed, ref, watch } from "vue";
import type { ChatTurn } from "../lib/ai";

const STORAGE_KEY = "deskkit-sessions-v1";

export type Session = {
  id: string;
  /** 标题：第一条用户消息的前 24 个字 */
  title: string;
  createdAt: number;
  updatedAt: number;
  turns: ChatTurn[];
};

const sessions = ref<Session[]>([]);
const currentId = ref("");

function newID(): string {
  return `s${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;
}

function load() {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw) {
      const parsed = JSON.parse(raw) as Session[];
      if (Array.isArray(parsed)) sessions.value = parsed.filter((s) => s && s.id);
    }
  } catch {
    // 存档坏了就当没有，不要让应用起不来
    sessions.value = [];
  }
  if (sessions.value.length === 0) {
    create();
  } else {
    currentId.value = sessions.value[0].id;
  }
}

function persist() {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(sessions.value));
  } catch {
    // 存不下（隐私模式 / 配额满）不阻断使用
  }
}

/** 新开会话并切过去。 */
function create(): Session {
  const s: Session = {
    id: newID(),
    title: "",
    createdAt: Date.now(),
    updatedAt: Date.now(),
    turns: [],
  };
  sessions.value.unshift(s);
  currentId.value = s.id;
  persist();
  return s;
}

function select(id: string) {
  if (sessions.value.some((s) => s.id === id)) {
    currentId.value = id;
  }
}

function remove(id: string) {
  const idx = sessions.value.findIndex((s) => s.id === id);
  if (idx < 0) return;
  sessions.value.splice(idx, 1);
  if (currentId.value === id) {
    currentId.value = sessions.value[0]?.id ?? "";
    if (!currentId.value) create();
  }
  persist();
}

function rename(id: string, title: string) {
  const s = sessions.value.find((x) => x.id === id);
  if (s) {
    s.title = title;
    persist();
  }
}

/** 第一条用户消息决定标题。 */
function autoTitle(id: string) {
  const s = sessions.value.find((x) => x.id === id);
  if (!s || s.title) return;
  const first = s.turns.find((t) => t.role === "user");
  if (!first) return;
  const text = first.content.replace(/\s+/g, " ").trim();
  s.title = text.length > 24 ? `${text.slice(0, 24)}…` : text;
  persist();
}

const current = computed(() => sessions.value.find((s) => s.id === currentId.value));
/** 按最近更新排序的会话列表。 */
const ordered = computed(() =>
  [...sessions.value].sort((a, b) => b.updatedAt - a.updatedAt),
);

// 任何改动都落盘 + 更新排序时间
watch(
  sessions,
  () => {
    const cur = current.value;
    if (cur) cur.updatedAt = Date.now();
    persist();
  },
  { deep: true },
);

/** 加载一次即可（模块级，多个组件共用同一份）。 */
let loaded = false;
export function initSessions() {
  if (loaded) return;
  loaded = true;
  load();
}

export function useSessions() {
  return {
    sessions,
    ordered,
    current,
    currentId,
    create,
    select,
    remove,
    rename,
    autoTitle,
  };
}
