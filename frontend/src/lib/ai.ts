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
export type ChatTurn = { role: "user" | "assistant" | "system"; content: string };

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
  /** 用户选中的东西：可能是单个文件、多个文件、或一个文件夹。 */
  paths?: string[];
  /** 后端算好的输出路径：同目录、同名、带时间戳。 */
  outputHint?: string;
  summary?: string;
  columns?: string[];
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
export function withWorkspace(turns: ChatTurn[], ws: Workspace): ChatTurn[] {
  const lines: string[] = [];

  if (ws.paths && ws.paths.length > 0) {
    lines.push(`用户选中的路径：${ws.paths.join("、")}`);
    if (ws.summary) lines.push(`文件情况：${ws.summary}`);
    if (ws.columns?.length) lines.push(`这些表的列名（并集）：${ws.columns.join("、")}`);
    if (ws.outputHint) {
      lines.push(`输出路径：${ws.outputHint}`);
      lines.push("这是后端已经算好的路径（原目录、原文件名加时间戳）。原文件永远不要修改。");
    }
    lines.push("");
    lines.push("路径已经给了，不要再问用户要。选中文件夹时用 scan_excel_folder 看有哪些表；");
    lines.push("选中单个文件时用 inspect_file。改数据一律输出新文件，原文件不动；");
    lines.push("输出位置默认与原文件同目录、原名加时间戳，用户另有要求时按他说的。");
  } else {
    lines.push("用户还没有选中任何路径。如果他要处理文件，提醒他先点输入框左边的文件夹按钮；闲聊就正常聊。");
  }

  if (ws.feature) {
    lines.push(`当前选中的功能：${ws.feature}`);
    if (ws.usage) {
      lines.push("");
      lines.push("以下是这个功能的接口用法（工具名、参数、调用示例）：");
      lines.push(ws.usage);
    }
  } else {
    lines.push("用户没有选择功能，这是普通对话：不要主动去调用文件处理工具，除非他明确要求。");
  }

  return [{ role: "system", content: lines.join("\n") }, ...turns];
}

/** 组一次请求。provider 为空时后端会用默认值。 */
export function chatInput(provider: string, model: string, turns: ChatTurn[]): ChatInput {
  return {
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
