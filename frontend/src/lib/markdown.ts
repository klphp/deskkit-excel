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

/**
 * 把正文里的绝对路径包成可点的小标签。
 *
 * 为什么：AI 处理完最常说的是「结果写到 /xxx/yyy.xlsx」。用户看到路径的第一反应
 * 是「在哪」，让他自己去文件管理器翻目录，是最容易劝退的一步。
 * 这里只做一件事：给路径挂上 data-reveal-path，点击由容器统一处理
 * （v-html 里挂不了 Vue 事件，所以用事件委托）。
 *
 * 只认「看起来像路径」的串：
 *   macOS   以 / 开头的绝对路径（如用户目录、外接卷） 等以 / 开头且有扩展名或两段以上
 *   Windows C:\... 或 C:/...
 * 末尾标点（句号、逗号、右括号）不算路径的一部分。
 */
export function linkifyPaths(html: string): string {
  return html.replace(PATH_RE, (match) => {
    const trimmed = match.replace(/[\s，。；：、）)】」"']+$/, "");
    if (!/\.\w{1,8}$/.test(trimmed)) return match; // 没有扩展名，多半不是文件
    const safe = trimmed.replace(/"/g, "&quot;");
    return `<button type="button" class="path-chip" data-reveal-path="${safe}">${trimmed}</button>${match.slice(trimmed.length)}`;
  });
}

// 路径模式：/开头的 POSIX 路径，或 C:\ 形式的 Windows 路径。
const PATH_RE = /(?:\/(?:Users|Volumes|private|tmp|home)\/[^\s"'<>|]+|[A-Za-z]:[\\/][^\s"'<>|]+)/g;

/** 把 markdown 渲染成可安全插入的 HTML。 */
export function renderMarkdown(src: string): string {
  if (!src) return "";
  const html = marked.parse(src, { async: false }) as string;
  // 先消毒再加交互标签：顺序反了会让 DOMPurify 把 data-* 和 class 洗掉的风险变大
  const cleaned = DOMPurify.sanitize(html, {
    // 允许 a 的 target 以便外链新窗口；其余走默认白名单
    ADD_ATTR: ["target", "rel"],
  });
  return linkifyPaths(cleaned);
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
