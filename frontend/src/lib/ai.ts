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
  dir?: string;
  outDir?: string;
  summary?: string;
  columns?: string[];
};

export function withWorkspace(turns: ChatTurn[], ws: Workspace): ChatTurn[] {
  if (!ws.dir) {
    return [
      {
        role: "system",
        content: "用户还没选择要处理的文件夹。先请他选择一个目录（界面上的「输入」按钮），再动手。",
      },
      ...turns,
    ];
  }

  const lines = [
    `当前输入目录：${ws.dir}`,
    ws.outDir ? `输出目录：${ws.outDir}` : "",
    ws.summary ? `文件情况：${ws.summary}` : "",
    ws.columns?.length ? `这些表的列名（并集）：${ws.columns.join("、")}` : "",
    "",
    "调用工具时直接使用上面的绝对路径，不要让用户再提供一次。",
    "开始动手前先用预览类工具（preview_*）看清会发生什么，把关键数字告诉用户，",
    "得到同意后再用执行类工具（run_*）落盘。",
  ].filter(Boolean);

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
