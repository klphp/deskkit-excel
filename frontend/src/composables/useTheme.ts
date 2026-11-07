/**
 * 主题状态。
 *
 * 抽成 composable 是因为有两个地方要改它：标题栏的循环按钮、设置页的三选一。
 * 状态只有一份（模块级 ref），两处同时显示也不会打架 —— 在设置页点了「暗色」，
 * 标题栏的按钮图标跟着变。
 *
 * 用 .dark 类而不是 media query：用户可能只想让这个应用变暗，不想改整个系统。
 * 三态里的 system 是「跟随系统」，不是「跟随浏器默认」。
 */
import { computed, ref } from "vue";

export type ThemeMode = "system" | "light" | "dark";

const STORAGE_KEY = "deskkit-theme";

const mode = ref<ThemeMode>("system");

function apply(m: ThemeMode) {
  const root = document.documentElement;
  if (m === "system") {
    root.classList.toggle("dark", window.matchMedia("(prefers-color-scheme: dark)").matches);
  } else {
    root.classList.toggle("dark", m === "dark");
  }
}

/** 初始化。在 App 挂载时调一次即可，重复调没有副作用。 */
export function initTheme() {
  const saved = localStorage.getItem(STORAGE_KEY) as ThemeMode | null;
  // 没存过偏好时用浅色：数据类工具长时间盯着看，纯深色底 + 近白字
  // 对比度太高，反馈是「刺眼」。想要深色的用户可在设置里切换，
  // 选择会被记住（含 system 跟随系统）。
  mode.value = saved === "light" || saved === "dark" || saved === "system" ? saved : "light";
  apply(mode.value);

  // 只挂一次监听，避免重复注册
  if ((window as any).__wails3ThemeListener) return;
  (window as any).__wails3ThemeListener = true;

  // 跟随系统时，系统切换要实时生效
  window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
    if (mode.value === "system") apply("system");
  });

  // ★ 跨窗口同步。
  //
  // 每个 Wails 窗口是独立的 WebView，有各自的 JS 运行时 —— mode 这个 ref
  // 是两个互不相干的变量。localStorage 本身是共享的，但一个窗口写进去，
  // 另一个窗口的 ref 不会自己变，也不会重新 apply 类名。所以在设置窗口
  // 点「暗色」，只有设置窗口变暗，主窗口不动。
  //
  // storage 事件正好补上这个缺口：它只在「其它文档」改同一份 localStorage
  // 时触发，当前窗口自己写入时不触发 —— 正是需要的行为，不会形成回环。
  window.addEventListener("storage", (e) => {
    if (e.key !== STORAGE_KEY) return;
    const v = e.newValue;
    if (v === "light" || v === "dark" || v === "system") {
      mode.value = v;
      apply(v);
    }
  });
}

export function setTheme(m: ThemeMode) {
  mode.value = m;
  localStorage.setItem(STORAGE_KEY, m);
  apply(m);
}

/** 三态循环：跟随系统 → 亮 → 暗 → 跟随系统 */
export function cycleTheme() {
  const order: ThemeMode[] = ["system", "light", "dark"];
  setTheme(order[(order.indexOf(mode.value) + 1) % order.length]);
}

export function useTheme() {
  return {
    mode: computed(() => mode.value),
    setTheme,
    cycleTheme,
  };
}
