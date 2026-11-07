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
  return turns.map((turn) => new Message({ role: turn.role, content: turn.content }));
}

/** 把当前目录作为 system 提示塞进去：模型要调用工具就必须知道绝对路径。 */
export function withWorkspace(turns: ChatTurn[], dir?: string): ChatTurn[] {
  if (!dir) return turns;
  return [
    { role: "system", content: `当前工作目录：${dir}。调用工具时直接使用这个路径。` },
    ...turns,
  ];
}

/** 组一次请求。provider 为空时后端会用默认值。 */
export function chatInput(provider: string, model: string, turns: ChatTurn[]): ChatInput {
  return new ChatInput({
    provider,
    model,
    messages: toMessages(turns),
  });
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
