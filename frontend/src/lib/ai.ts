/**
 * AI 前端封装。
 *
 * 为什么不直接在组件里 import bindings：
 *   · bindings 的路径里带模块全名，换模块名要改一堆组件
 *   · 流式增量走的是事件（ai:delta），订阅/退订容易漏，集中一处好查
 *   · 组件只关心「一段历史 + 一次调用」，不该知道 ChatInput 的构造细节
 */
import { Events } from "@wailsio/runtime";
import * as AIService from "../../bindings/github.com/klphp/excel-batch-tool/internal/services/aiservice";
import { ChatInput } from "../../bindings/github.com/klphp/excel-batch-tool/internal/services/models";
import type { AISettings, TestResult } from "../../bindings/github.com/klphp/excel-batch-tool/internal/services/models";
import { Message } from "../../bindings/github.com/klphp/excel-batch-tool/internal/ai/models";
import type { ProviderInfo } from "../../bindings/github.com/klphp/excel-batch-tool/internal/ai/models";

export { AIService };
export type { AISettings, ProviderInfo, TestResult };

/** 一轮对话。只保留纯文本 —— 工具调用步骤走事件，不进历史。 */
export type ChatTurn = {
  role: "user" | "assistant" | "system";
  content: string;
  /** 库里的消息 id。分页加载时用它当稳定 key / 滚动锚点。 */
  id?: number;
  /** 消息时间（unix 秒），用于分页标记上的时间。 */
  at?: number;
};

/** 工具循环推过来的步骤事件（对应 Go 侧 ai.Event）。 */
export type ToolEvent = {
  type: "tool_call" | "tool_result";
  tool?: string;
  args?: string;
  result?: string;
  error?: string;
  round?: number;
};

/** 转成 Go 侧的消息结构。 */
export function toMessages(turns: ChatTurn[]): Message[] {
  return turns.map((turn): Message => ({ role: turn.role, content: turn.content }));
}

/**
 * 把当前上下文塞成 system 提示。
 *
 * 模型要调工具就必须知道绝对路径，也要知道输出该往哪写 —— 这些事情
 * 界面上已经选好了，不该让用户每次在话里重复一遍。
 */
export type Workspace = {
  /** 用户自己写的指令（设置 → 自定义指令），优先级高于我们的默认规则。 */
  instructions?: string;
  /** 用户选中的东西：可能是单个文件、多个文件、或一个文件夹。 */
  paths?: string[];
  /** 选中的功能包（空 = 普通对话）。 */
  feature?: string;
  /** 该功能包的用法说明（由工具定义自动生成）。 */
  usage?: string;
};

/**
 * 把当前上下文塞成 system 提示。
 *
 * 模型要调工具就必须知道绝对路径，也要明确「结果该写到哪、原文件不能动」——
 * 这两件事界面上已经定了，不该让用户每次在话里重复一遍。
 */
/**
 * 把当前上下文塞成 system 提示。
 *
 * 只放模型猜不到的东西：它是谁、用哪种语言、路径在哪、输出规则、不确定怎么办。
 * 13 个工具的清单和参数不进这里 —— 那些已经在 tools 参数里，
 * 再推一遍每轮白烧 token，而且工具改名后提示词必然落后。
 * 需要细节时让模型自己调 describe_capability 来拿。
 */
export function withWorkspace(turns: ChatTurn[], ws: Workspace): ChatTurn[] {
  const lines: string[] = [];

  // ── 身份与说话方式 ──
  lines.push("你是 DeskKit Excel，一个专注于表格处理的本地工具：用户用一句话说明要做什么，你调用本地工具完成，文件不出这台机器。");
  lines.push("语言：除非用户明确指定用哪种语言，否则一律用用户提问时使用的语言回答。");
  lines.push("口吻：直接给结果，不寒暄，不复述用户的需求。");
  // 用户说「你好」时模型很容易回一大段功能清单，还是凭印象写的、往往是旧的。
  // 寒暄就一句；问能力必须先查，不许凭印象罗列。
  lines.push("用户只是打招呼：回一句就好，别罗列功能。");
  lines.push("用户问你能做什么：先调 describe_capability 拿到真实清单再回答，不要凭印象列。" +
    "只讲和表格相关的实际能力，不要编造工具没有的功能。");

  // ── 能力与自由度 ──
  // 我们的工具集是「加速器」，不是「笼子」：它快、稳、本地、结果可预期，
  // 所以应当优先用。但工具覆盖不到的事情，不该让模型直接说做不到。
  lines.push("能力：需要确认某个工具怎么用、参数是什么时，调用 describe_capability 查，不要凭猜。");
  lines.push("优先用提供的本地工具完成：它们比你自己写脚本更快、更稳，结果也可预期。");
  lines.push("这些工具覆盖不到时，你可以用你认为可行的其他办法完成任务（包括告诉用户该用哪个系统功能、该运行哪条命令），不要因为工具缺一项就说做不到。");

  // ── 工作区 ──
  if (ws.paths && ws.paths.length > 0) {
    lines.push("");
    lines.push(`用户选中的路径：${ws.paths.join("、")}`);
    lines.push("路径已经给了，不要再问用户要。选中的可能是文件、文件夹或混合，不要假设文件类型。");
    lines.push("选中文件夹时用 scan_excel_folder 看里面有哪些表；选中单个文件时用 inspect_file。");
    lines.push("改数据一律输出新文件，原文件绝不修改。默认原目录、原名加时间戳；用户另有要求时按他说的。");
  } else {
    lines.push("");
    // 界面上的真实入口是输入框左下角的「+」，点开才有「选择文件 / 选择文件夹」。
    // 以前这里写成「文件夹按钮」，模型照着说，用户找不到按钮。
    lines.push("用户还没有选中任何路径。他要处理文件时，告诉他点输入框左下角的「+」，"
      + "在弹出的菜单里选「选择文件」或「选择文件夹」；不要说成文件夹图标，也不要让他粘贴路径。闲聊就正常聊。");
  }

  // ── 功能包 ──
  if (ws.feature) {
    lines.push("");
    lines.push(`当前选中的功能：${ws.feature}`);
    if (ws.usage) {
      lines.push("以下是这个功能的接口用法（工具名、参数、调用示例）：");
      lines.push(ws.usage);
    }
  } else {
    lines.push("用户没有选择功能，这是普通对话：不要主动调用文件处理工具，除非他明确要求。");
  }

  // ── 危险操作 ──
  // 模型最危险的不是「做错」，是「不打招呼就做」。删文件、覆盖、移动整个目录
  // 这类动作必须先摊开影响面，让用户有机会喊停。
  lines.push("");
  lines.push("危险操作（删除、覆盖、移动整个目录、批量改名）必须先说清再动手，一步都不许省：");
  lines.push("1. 明确列出会被影响的每一项：该操作会删除 A、B、C 共 3 项。不要用「这些文件」这种含糊说法。");
  lines.push("2. 加一句风险提示：为避免误操作，建议先备份。");
  lines.push("3. 问一句确认：确定继续吗？然后停下来等用户回答，不要自己替用户答。");
  lines.push("4. 用户明确同意之后才调用工具；他改口、犹豫或没回答就都不要动。");

  // ── 用户自定义指令 ──
  // 放在前面：这是用户的真实习惯（保存到哪、命名怎么起、要不要问），
  // 我们给十个目录选择器也猜不到。冲突时以用户的为准 —— 唯一例外是
  // 「绝不修改原文件」这条底线，那是产品承诺，不能被指令覆盖。
  if (ws.instructions && ws.instructions.trim()) {
    lines.push("");
    lines.push("用户自定义要求（优先遵守，与下面的默认规则冲突时以它为准）：");
    lines.push(ws.instructions.trim());
  }

  // ── 工作目录 ──
  // 用户的东西该存哪我们猜不到。别自己编一个目录，也别每次处理都问一遍：
  // 第一次问清、记到这个会话上，之后照做。
  if (!ws.instructions || !ws.instructions.trim()) {
    lines.push("");
    lines.push("如果这个会话里还不知道用户的工作目录，开始处理前先用 ask_user 问一次：" +
      "下载/来源文件放在哪、处理结果输出到哪。给出「用原目录」这类常见选项，让用户点或自己写。" +
      "拿到答案后调用 remember_instructions 记到这个会话，之后不要再重复问。");
  }

  // ── 出问题怎么办 ──
  // 这一段是给模型的「故障处理顺序」。没有它，模型遇到错误最常见的三种反应是：
  // 原样重试、猜一个路径、编一个结果 —— 三种都比报错更糟。
  lines.push("");
  lines.push("遇到问题按这个顺序处理：");
  lines.push("1. 工具报错先看错误本身说了什么，照实告诉用户，不要原样重试同一个调用。");
  lines.push("2. 缺信息（该处理哪个文件、按哪列去重）就一次问清一个问题，不要连问一串。");
  lines.push("3. 文件读不出来、格式不认识、被别的程序占用，说清是哪种情况，并给出用户能做的下一步。");
  lines.push("4. 真的做不到才说做不到 —— 但要先想一遍有没有别的路子；无论如何不编造结果、不虚构文件路径。");

  return [{ role: "system", content: lines.join("\n") }, ...turns];
}

/** 组一次请求。provider 为空时后端会用默认值。 */
export function chatInput(provider: string, model: string, turns: ChatTurn[], sessionId = ""): ChatInput {
  return {
    sessionId,
    provider,
    model,
    messages: toMessages(turns),
  };
}

/**
 * 订阅流式增量。返回退订函数 —— 必须调用，否则面板反复挂载会叠加监听，
 * 表现为「一次提问，文字重复出现好几次」。
 */
export function onDelta(handler: (text: string) => void): () => void {
  return Events.On("ai:delta", (ev) => {
    const payload = ev.data as { content?: string } | undefined;
    if (payload?.content) handler(payload.content);
  });
}

/** 订阅工具调用步骤。同样必须退订，否则监听叠加会重复渲染。 */
export function onTool(handler: (ev: ToolEvent) => void): () => void {
  return Events.On("ai:tool", (ev) => {
    const payload = ev.data as ToolEvent | undefined;
    if (payload?.type) handler(payload);
  });
}

/** 模型向用户提问：{question, options[], detail}。 */
export type AskPayload = {
  question: string;
  options: string[];
  detail?: string;
};

/**
 * 订阅「模型提问」事件。返回退订函数。
 *
 * 模型调 ask_user 之后本轮就结束了，所以这里必须由界面接住：
 * 渲染成按钮，用户点一下就把那一条当新消息发回去。
 */
export function onAsk(handler: (ask: AskPayload) => void): () => void {
  return Events.On("ai:ask", (ev) => {
    const p = ev.data as AskPayload | undefined;
    if (p?.question) handler({ question: p.question, options: p.options ?? [], detail: p.detail });
  });
}

/** 危险操作的确认请求：{token, title, items[], note}。 */
export type ConfirmPayload = {
  token: string;
  title: string;
  items: string[];
  note?: string;
};

/** 订阅「危险操作待确认」事件。返回退订函数。 */
export function onConfirm(handler: (c: ConfirmPayload) => void): () => void {
  return Events.On("ai:confirm", (ev) => {
    const p = ev.data as ConfirmPayload | undefined;
    if (p?.token) handler({ token: p.token, title: p.title ?? "", items: p.items ?? [], note: p.note });
  });
}

/** 从错误对象里挖出人话。Go 侧返回的是 ai.Error.Message，Hint 走事件。 */
export function errorText(err: unknown): string {
  if (typeof err === "string") return err;
  if (err instanceof Error) return err.message;
  return String(err);
}

/** 字节数转人话。 */
export function humanSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KB", "MB", "GB"];
  let value = bytes / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value.toFixed(value >= 10 ? 0 : 1)} ${units[unit]}`;
}
