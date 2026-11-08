/**
 * 会话区字号。
 *
 * 为什么单独做成设置项：这张脸每天要盯很久。同一套字号，有人觉得刚好，
 * 有人眼睛累（尤其表格、代码块密集的时候）。界面其它部分不跟着变 ——
 * 按钮、标签放大反而占地方，只有「读的内容」需要放大。
 *
 * 实现用 CSS 变量 + em 继承：会话区容器改 --chat-fs，内部标题、列表、
 * 代码块都按 em 缩放，不用逐个元素改字号。
 */
import { computed, ref } from "vue";

const STORAGE_KEY = "deskkit-chat-font";

/**
 * 三档：小 12 / 中 16 / 大 20，默认中。
 *
 * 不做滑杆也不给一堆数字：字号是「感觉」不是「参数」，
 * 三档足够，选了就知道效果。中间档 16px 是正文读起来舒服的起点。
 */
export const FONT_SIZES = [12, 16, 20] as const;

/** 档位 → 词条 key。界面只显示大中小，不显示像素值。 */
export const FONT_LABELS: Record<number, string> = {
  12: "settings.font.small",
  16: "settings.font.medium",
  20: "settings.font.large",
};

const DEFAULT_SIZE = 16;

const size = ref<number>(DEFAULT_SIZE);

function apply(px: number) {
  document.documentElement.style.setProperty("--chat-fs", `${px}px`);
}

/** 启动时调一次，重复调没有副作用。 */
export function initChatFont() {
  const raw = Number(localStorage.getItem(STORAGE_KEY) ?? "");
  const px = (FONT_SIZES as readonly number[]).includes(raw) ? raw : DEFAULT_SIZE;
  size.value = px;
  apply(px);
}

export function setChatFont(px: number) {
  const v = (FONT_SIZES as readonly number[]).includes(px) ? px : DEFAULT_SIZE;
  size.value = v;
  apply(v);
  localStorage.setItem(STORAGE_KEY, String(v));
}

export function useChatFont() {
  return {
    size: computed(() => size.value),
    setChatFont,
    sizes: FONT_SIZES,
    labels: FONT_LABELS,
  };
}
