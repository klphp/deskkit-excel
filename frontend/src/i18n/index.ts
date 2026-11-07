/**
 * 轻量多语言。
 *
 * 为什么不用 vue-i18n：模板默认关闭多语言，而 vue-i18n 打包要 20KB+，
 * 为了一个可能永远不开的功能常驻在包里不划算。这里只做了两件事 ——
 * 查表 + 响应式语言状态，够用且零依赖。
 *
 * 默认关闭时（config/app.yaml 的 i18n.enabled = false）：
 *   只加载 defaultLocale 的词条，语言切换入口不显示，行为和没有 i18n 一样。
 *
 * 开启后：
 *   设置窗口出现语言选择，切换立即生效并写入 localStorage，重启保持。
 *
 * 词条加载是动态 import 的（Vite 会按语言切 chunk），所以加语言不会
 * 让首屏变大。
 */
import { reactive, ref } from "vue";
import { zhCN } from "./locales/zh-CN";

const STORAGE_KEY = "deskkit-locale";

/** 已加载的词条表。用 reactive 是为了动态加载完成后模板能立刻刷新。 */
const messages = reactive<Record<string, Record<string, string>>>({
  // 中文始终预加载：它既是默认语言，也是兜底语言
  "zh-CN": zhCN,
});

const locale = ref("zh-CN");
const enabled = ref(false);
const available = ref<string[]>(["zh-CN"]);
let fallback = "zh-CN";

/** 语言文件按需加载。加新语言就在这里加一行。 */
const loaders: Record<string, () => Promise<Record<string, string>>> = {
  "zh-CN": async () => zhCN,
  "en-US": async () => (await import("./locales/en-US")).enUS,
};

async function loadLocale(l: string): Promise<void> {
  if (messages[l]) return;
  const loader = loaders[l];
  if (!loader) return;
  try {
    messages[l] = await loader();
  } catch (e) {
    // 语言文件缺失不该让界面挂掉 —— 查表时会回退到兜底语言
    console.warn(`[i18n] 加载语言包失败: ${l}`, e);
  }
}

export interface I18nConfig {
  enabled: boolean;
  locale: string;
  fallback: string;
  /** Go 的 slice 映射到 TS 是 string[] | null，别写成 string[] */
  locales: string[] | null;
}

/**
 * 初始化。由 App.vue 在拿到 AppService.GetInfo() 之后调用。
 *
 * 可以重复调用（多窗口、热重载），内部幂等。
 */
export async function initI18n(cfg: I18nConfig | undefined): Promise<void> {
  if (!cfg) return;

  enabled.value = cfg.enabled;
  fallback = cfg.fallback || cfg.locale || "zh-CN";
  available.value = cfg.locales?.length ? cfg.locales : ["zh-CN"];

  let target = cfg.locale || "zh-CN";

  // 只有开启时才尊重用户之前的选择；关着就固定用默认语言
  if (cfg.enabled) {
    const saved = localStorage.getItem(STORAGE_KEY);
    if (saved && available.value.includes(saved)) {
      target = saved;
    }
  }

  if (!available.value.includes(target)) {
    target = cfg.locale || "zh-CN";
  }

  await loadLocale(target);
  locale.value = target;

  document.documentElement.lang = target;
}

/** 切换语言。未启用多语言时不做任何事。 */
export async function setLocale(l: string): Promise<void> {
  if (!enabled.value) return;
  if (!available.value.includes(l)) return;
  await loadLocale(l);
  locale.value = l;
  localStorage.setItem(STORAGE_KEY, l);
  document.documentElement.lang = l;
}

/**
 * 取词条。
 *
 * 查找顺序：当前语言 → 兜底语言 → 中文 → key 本身。
 * 最后那一步是有意的：漏翻时页面上出现 key（比如 settings.update）比
 * 出现空白更容易定位问题。
 */
export function t(key: string): string {
  return (
    messages[locale.value]?.[key] ??
    messages[fallback]?.[key] ??
    messages["zh-CN"]?.[key] ??
    key
  );
}

/** 组件里用这个拿到响应式的 t。 */
export function useI18n() {
  return {
    t,
    locale,
    enabled,
    available,
    setLocale,
  };
}
