<script setup lang="ts">
/**
 * AI 对话面板。
 *
 * 两种状态共用一块区域：
 *   · 还没配好模型 → 就地显示配置表单（不要让用户跑去设置页找）
 *   · 配好了 → 对话区 + 输入框
 *
 * 流式增量靠 ai:delta 事件累积；最终以返回值落历史 —— 事件可能在
 * 断线时缺尾，返回值才是完整的。
 */
import { computed, nextTick, onMounted, onUnmounted, ref } from "vue";
import { Bot, CheckCircle2, CornerDownLeft, Loader2, Send, Sparkles, X } from "@lucide/vue";
import { useI18n } from "../i18n";
import {
  AIService,
  chatInput,
  errorText,
  onDelta,
  onTool,
  withWorkspace,
  type ChatTurn,
  type ProviderInfo,
  type TestResult,
  type ToolEvent,
} from "../lib/ai";

const props = defineProps<{ dir?: string }>();
const { t } = useI18n();

/** 工具调用步骤：让用户看到「它在做什么」，而不是干等一句结论。 */
type Step = { tool: string; detail: string; done: boolean; ok: boolean };
const steps = ref<Step[]>([]);

const enabled = ref(false);
const providers = ref<ProviderInfo[]>([]);
const provider = ref("");
const model = ref("");
const apiKey = ref("");
const busy = ref(false);
const testing = ref(false);
const testResult = ref<TestResult | null>(null);
const failure = ref("");

const turns = ref<ChatTurn[]>([]);
const draft = ref("");
const streaming = ref("");
const scroller = ref<HTMLElement | null>(null);
const inputEl = ref<HTMLTextAreaElement | null>(null);

let offDelta: (() => void) | null = null;
let offTool: (() => void) | null = null;

/** 有任一可用服务商才算配好（本地模型不需要密钥）。 */
const ready = computed(() => providers.value.some((p) => p.ready));
const current = computed(() => providers.value.find((p) => p.name === provider.value));

async function refresh() {
  enabled.value = await AIService.Enabled();
  if (!enabled.value) return;
  providers.value = (await AIService.Providers()) ?? [];

  const st = await AIService.Settings();
  // 用户设置过的优先，其次第一个可用的
  const preferred =
    providers.value.find((p) => p.name === st.provider && p.ready) ??
    providers.value.find((p) => p.ready) ??
    providers.value[0];
  if (preferred) {
    provider.value = preferred.name;
    model.value = st.model || preferred.model;
  }
}

function pickProvider(name: string) {
  provider.value = name;
  const p = providers.value.find((x) => x.name === name);
  model.value = p?.model ?? "";
  testResult.value = null;
  failure.value = "";
}

async function saveAndTest() {
  failure.value = "";
  testResult.value = null;
  testing.value = true;
  try {
    await AIService.SaveSettings({
      provider: provider.value,
      baseURL: current.value?.baseURL ?? "",
      model: model.value,
      apiKey: apiKey.value,
      clearKey: false,
      disableLocalProbe: false,
    });
    apiKey.value = "";
    testResult.value = await AIService.Test(provider.value);
    await refresh();
  } catch (err) {
    failure.value = errorText(err);
  } finally {
    testing.value = false;
  }
}

async function scrollToBottom() {
  await nextTick();
  const el = scroller.value;
  if (el) el.scrollTop = el.scrollHeight;
}

async function send() {
  const text = draft.value.trim();
  if (!text || busy.value) return;

  turns.value.push({ role: "user", content: text });
  draft.value = "";
  failure.value = "";
  busy.value = true;
  streaming.value = "";
  scrollToBottom();

  steps.value = [];

  offDelta = onDelta((chunk) => {
    streaming.value += chunk;
    scrollToBottom();
  });

  // 工具步骤：调用时先占一行（转圈），结果回来时补上
  offTool = onTool((ev: ToolEvent) => {
    if (ev.type === "tool_call") {
      // 上一轮的完整话先落进历史，中间推理就不会被吞掉
      if (streaming.value.trim()) {
        turns.value.push({ role: "assistant", content: streaming.value });
        streaming.value = "";
      }
      steps.value.push({ tool: ev.tool || "?", detail: ev.args || "", done: false, ok: true });
    } else if (ev.type === "tool_result") {
      const last = [...steps.value].reverse().find((s) => s.tool === ev.tool && !s.done);
      if (last) {
        last.done = true;
        last.ok = !ev.error;
        last.detail = ev.error || ev.result || last.detail;
      }
    }
    scrollToBottom();
  });

  try {
    const history = withWorkspace(turns.value, props.dir);
    const result = await AIService.ChatAgent(chatInput(provider.value, model.value, history));
    if (streaming.value.trim()) {
      turns.value.push({ role: "assistant", content: streaming.value });
    } else if (result.content && !result.content.trim().startsWith("{")) {
      const lastAssistant = [...turns.value].reverse().find((x) => x.role === "assistant");
      if (lastAssistant?.content !== result.content) {
        turns.value.push({ role: "assistant", content: result.content });
      }
    }
  } catch (err) {
    failure.value = errorText(err);
  } finally {
    offDelta?.();
    offTool?.();
    offDelta = null;
    offTool = null;
    busy.value = false;
    streaming.value = "";
    scrollToBottom();
  }
}

/** 供工作台把场景指令填进输入框。 */
function prefill(text: string) {
  draft.value = text;
  inputEl.value?.focus();
}
defineExpose({ prefill });

onMounted(() => {
  refresh().catch((err) => (failure.value = errorText(err)));
});
onUnmounted(() => {
  offDelta?.();
  offTool?.();
});
</script>

<template>
  <div class="flex h-full flex-col">
    <header class="flex h-12 shrink-0 items-center gap-2 border-b border-border px-4">
      <Sparkles :size="15" class="text-muted-foreground" />
      <h2 class="text-sm font-semibold">{{ t("ai.title") }}</h2>
      <span
        v-if="enabled"
        class="ml-auto inline-flex items-center gap-1.5 text-xs"
        :class="ready ? 'text-muted-foreground' : 'text-destructive'"
      >
        <span
          class="size-1.5 rounded-full"
          :class="ready ? 'bg-emerald-500' : 'bg-destructive'"
        />
        {{ ready ? t("ai.ready") : t("ai.notConfigured") }}
      </span>
    </header>

    <!-- 未启用 AI：解释清楚，不假装能用 -->
    <div v-if="!enabled" class="flex flex-1 flex-col items-center justify-center gap-2 p-6">
      <p class="max-w-[240px] text-center text-xs leading-relaxed text-muted-foreground">
        {{ t("ai.disabled") }}
      </p>
      <!-- 调用失败也要显示出来：绑定层报错时如果只显示「未启用」，
           会被误判成配置问题，实际是前后端接口没对上 -->
      <p v-if="failure" class="max-w-[240px] break-words text-center text-[11px] text-destructive">
        {{ failure }}
      </p>
    </div>

    <!-- 启用了但还没配好：就地配置 -->
    <div v-else-if="!ready" class="flex-1 space-y-3 overflow-y-auto p-4">
      <p class="text-xs leading-relaxed text-muted-foreground">{{ t("ai.configureHint") }}</p>

      <label class="block space-y-1">
        <span class="text-xs text-muted-foreground">{{ t("ai.provider") }}</span>
        <select
          class="w-full rounded-md border border-input bg-background px-2 py-1.5 text-sm"
          :value="provider"
          @change="pickProvider(($event.target as HTMLSelectElement).value)"
        >
          <option v-for="p in providers" :key="p.name" :value="p.name">
            {{ p.label }}{{ p.local ? ` · ${t("ai.local")}` : "" }}
          </option>
        </select>
      </label>

      <label class="block space-y-1">
        <span class="text-xs text-muted-foreground">{{ t("ai.model") }}</span>
        <input
          v-model="model"
          class="w-full rounded-md border border-input bg-background px-2 py-1.5 text-sm font-mono"
          :placeholder="current?.model || 'gpt-4o-mini'"
        />
      </label>

      <label v-if="!current?.local" class="block space-y-1">
        <span class="text-xs text-muted-foreground">{{ t("ai.apiKey") }}</span>
        <input
          v-model="apiKey"
          type="password"
          class="w-full rounded-md border border-input bg-background px-2 py-1.5 text-sm font-mono"
          :placeholder="t('ai.apiKeyPlaceholder')"
          autocomplete="off"
        />
      </label>

      <button
        type="button"
        class="inline-flex w-full items-center justify-center gap-2 rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground disabled:opacity-50"
        :disabled="testing"
        @click="saveAndTest"
      >
        <Loader2 v-if="testing" :size="14" class="animate-spin" />
        {{ testing ? t("ai.testing") : t("ai.saveAndTest") }}
      </button>

      <p v-if="testResult" class="text-xs" :class="testResult.ok ? 'text-emerald-600' : 'text-destructive'">
        {{ testResult.ok ? `${t("ai.testOk")} · ${testResult.latencyMs}ms` : testResult.hint || testResult.error }}
      </p>
      <p v-if="failure" class="text-xs text-destructive">{{ failure }}</p>

      <p class="text-[11px] leading-relaxed text-muted-foreground">{{ t("ai.localHint") }}</p>
    </div>

    <!-- 已配好：对话 -->
    <template v-else>
      <div ref="scroller" class="min-h-0 flex-1 space-y-3 overflow-y-auto p-4">
        <p v-if="turns.length === 0 && !streaming" class="text-xs leading-relaxed text-muted-foreground">
          {{ t("ai.emptyHint") }}
        </p>

        <div v-for="(turn, i) in turns" :key="i" class="flex gap-2">
          <div
            class="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full"
            :class="turn.role === 'user' ? 'bg-accent' : 'bg-primary/10'"
          >
            <Bot v-if="turn.role === 'assistant'" :size="13" />
            <span v-else class="text-[10px] font-semibold">{{ t("ai.you") }}</span>
          </div>
          <p class="whitespace-pre-wrap break-words text-sm leading-relaxed">{{ turn.content }}</p>
        </div>

        <div v-if="streaming" class="flex gap-2">
          <div class="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full bg-primary/10">
            <Bot :size="13" />
          </div>
          <p class="whitespace-pre-wrap break-words text-sm leading-relaxed">{{ streaming }}</p>
        </div>

        <ul v-if="steps.length" class="space-y-1 rounded-md bg-muted/40 p-2">
          <li
            v-for="(s, i) in steps"
            :key="i"
            class="flex items-start gap-1.5 text-[11px] leading-relaxed"
            :class="s.ok ? 'text-muted-foreground' : 'text-destructive'"
          >
            <Loader2 v-if="!s.done" :size="10" class="mt-0.5 shrink-0 animate-spin" />
            <CheckCircle2 v-else :size="10" class="mt-0.5 shrink-0" />
            <span class="min-w-0 break-words">
              <span class="font-mono">{{ s.tool }}</span>
              <span v-if="s.detail" class="ml-1 opacity-70">{{ s.detail.slice(0, 120) }}</span>
            </span>
          </li>
        </ul>

        <p v-if="busy && !streaming" class="flex items-center gap-2 text-xs text-muted-foreground">
          <Loader2 :size="12" class="animate-spin" />{{ t("ai.thinking") }}
        </p>
        <p v-if="failure" class="text-xs text-destructive">{{ failure }}</p>
      </div>

      <div class="shrink-0 border-t border-border p-3">
        <div class="rounded-md border border-input bg-background focus-within:ring-1 focus-within:ring-ring">
          <textarea
            ref="inputEl"
            v-model="draft"
            rows="2"
            class="w-full resize-none bg-transparent px-3 py-2 text-sm outline-none"
            :placeholder="t('ai.inputPlaceholder')"
            @keydown.enter.exact.prevent="send"
          />
          <div class="flex items-center justify-between px-2 pb-2">
            <span class="flex items-center gap-1 text-[10px] text-muted-foreground">
              <CornerDownLeft :size="10" />{{ t("ai.enterToSend") }}
            </span>
            <button
              type="button"
              class="inline-flex items-center gap-1 rounded-md bg-primary px-2.5 py-1 text-xs font-medium text-primary-foreground disabled:opacity-50"
              :disabled="busy || !draft.trim()"
              @click="send"
            >
              <Send :size="12" />{{ t("ai.send") }}
            </button>
          </div>
        </div>
        <button
          v-if="turns.length"
          type="button"
          class="mt-2 inline-flex items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground"
          @click="turns = []; failure = ''"
        >
          <X :size="10" />{{ t("ai.clear") }}
        </button>
      </div>
    </template>
  </div>
</template>
