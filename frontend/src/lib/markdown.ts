/**
 * Markdown 渲染。
 *
 * 为什么需要：模型的回答几乎总是 markdown —— 列表、表格、代码块。
 * 纯文本显示的结果是「| 部门 | 金额 |」这样一排竖线，表格完全没法看，
 * 而表格恰好是这个工具最常见的输出。
 *
 * ★ 必须消毒：模型输出是不可信内容（它可能被用户的表格内容或网页内容
 *   影响）。v-html 直接塞进 DOM 等于把 XSS 入口交给模型，所以用
 *   DOMPurify 过一遍，只留安全的标签。
 */
import { marked } from "marked";
import DOMPurify from "dompurify";

// GFM：表格、任务列表、删除线都要（表格是刚需）
// breaks：模型经常用单换行表示换行，不按这个来会把段落挤成一行
marked.setOptions({
  gfm: true,
  breaks: true,
});

/** 把 markdown 渲染成可安全插入的 HTML。 */
export function renderMarkdown(src: string): string {
  if (!src) return "";
  const html = marked.parse(src, { async: false }) as string;
  return DOMPurify.sanitize(html, {
    // 允许 a 的 target 以便外链新窗口；其余走默认白名单
    ADD_ATTR: ["target", "rel"],
  });
}

/**
 * 内容看起来是不是 markdown？
 *
 * 用途：只有真正的 markdown 才走渲染。纯文本（比如用户自己的输入）
 * 走渲染反而会出错 —— 比如内容里恰好有 `#` 或 `*` 会被当成语法。
 */
export function looksLikeMarkdown(src: string): boolean {
  if (!src) return false;
  const patterns = [
    /^#{1,6}\s/m, // 标题
    /```/, // 代码块
    /^\s*[-*+]\s/m, // 无序列表
    /^\s*\d+\.\s/m, // 有序列表
    /^\s*\|.*\|\s*$/m, // 表格
    /`[^`]+`/, // 行内代码
    /\*\*[^*]+\*\*/, // 粗体
    /^\s*>\s/m, // 引用
  ];
  return patterns.some((re) => re.test(src));
}
