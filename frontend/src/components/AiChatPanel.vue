<script setup lang="ts">
/**
 * 主区对话。
 *
 * 这是应用真正的界面：用户用一句人话说要做什么，模型自己在 Go 侧
 * 的工具循环里挑工具、跑预览、再执行。前端只做三件事 ——
 *   ① 把现状（目录 / 输出位置 / 有哪些列）交给模型
 *   ② 把流式文字和工具步骤如实显示出来
 *   ③ 没配模型时就地让用户配上（不让他跑去设置页找）
 *
 * 流式增量靠 ai:delta 累积；最终以返回值落历史 —— 事件可能在
 * 断线时缺尾，返回值才是完整的。
 */
import { computed, nextTick, onMounted, onUnmounted, ref } from "vue";
import { ArrowUp, Check, Loader2, Sparkles, Wrench, X } from "@lucide/vue";
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

const props = defineProps<{
  dir?: string;
  outDir?: string;
  summary?: string;
  columns?: string[];
}>();

const { t } = useI18n();

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
const showConfig = ref(false);

const turns = ref<ChatTurn[]>([]);
const draft = ref("");
const streaming = ref("");
const scroller = ref<HTMLElement | null>(null);
const inputEl = ref<HTMLTextAreaElement | null>(null);

let offDelta: (() => void) | null = null;
let offTool: (() => void) | null = null;

const ready = computed(() => providers.value.some((p) => p.ready));
const current = computed(() => providers.value.find((p) => p.name === provider.value));
const configured = computed(() => enabled.value && ready.value && !showConfig.value);

/** 起手建议：点一下填进输入框，用户还能改。 */
const suggestions = computed(() =>
  props.dir
    ? [t("ai.askMerge"), t("ai.askClean"), t("ai.askSummary"), t("ai.askLook")]
    : [t("ai.askNoDir")],
);

async function refresh() {
  enabled.value = await AIService.Enabled();
  if (!enabled.value) return;
  providers.value = (await AIService.Providers()) ?? [];

  const st = await AIService.Settings();
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
    if (testResult.value?.ok) showConfig.value = false;
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

function useSuggestion(text: string) {
  draft.value = text;
  inputEl.value?.focus();
}

async function send() {
  const text = draft.value.trim();
  if (!text || busy.value) return;

  turns.value.push({ role: "user", content: text });
  draft.value = "";
  failure.value = "";
  busy.value = true;
  streaming.value = "";
  steps.value = [];
  scrollToBottom();

  offDelta = onDelta((chunk) => {
    streaming.value += chunk;
    scrollToBottom();
  });

  offTool = onTool((ev: ToolEvent) => {
    if (ev.type === "tool_call") {
      // 上一段完整的话先落进历史，中间推理不会被吞掉
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
    const history = withWorkspace(turns.value, {
      dir: props.dir,
      outDir: props.outDir,
      summary: props.summary,
      columns: props.columns,
    });
    const result = await AIService.ChatAgent(chatInput(provider.value, model.value, history));
    if (streaming.value.trim()) {
      turns.value.push({ role: "assistant", content: streaming.value });
    } else if (result.content && !result.content.trim().startsWith("{")) {
      const last = [...turns.value].reverse().find((x) => x.role === "assistant");
      if (last?.content !== result.content) {
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

/** Enter 发送，Shift+Enter 换行 —— 聊天框的通用习惯。 */
function onKeydown(e: KeyboardEvent) {
  if (e.key === "Enter" && !e.shiftKey && !e.isComposing) {
    e.preventDefault();
    send();
  }
}

onMounted(() => {
  refresh().catch((err) => (failure.value = errorText(err)));
});
onUnmounted(() => {
  offDelta?.();
  offTool?.();
});
</script>

<template>
  <div class="flex h-full min-h-0 flex-col">
    <!-- ── 模型状态 / 配置 ── -->
    <div v-if="!configured" class="min-h-0 flex-1 overflow-y-auto">
      <div class="mx-auto max-w-[560px] px-6 py-10">
        <div v-if="!enabled" class="rounded-lg border border-border bg-card p-5">
          <p class="text-[14px] font-medium">{{ t("ai.disabledTitle") }}</p>
          <p class="mt-1.5 text-[13px] leading-relaxed text-muted-foreground">{{ t("ai.disabledHint") }}</p>
        </div>

        <div v-else class="rounded-lg border border-border bg-card p-5">
          <div class="flex items-start gap-2">
            <Sparkles :size="16" class="mt-0.5 shrink-0 text-primary" />
            <div>
              <p class="text-[14px] font-medium">{{ t("ai.setupTitle") }}</p>
              <p class="mt-1 text-[13px] leading-relaxed text-muted-foreground">{{ t("ai.setupHint") }}</p>
            </div>
          </div>

          <div class="mt-4 space-y-3">
            <label class="block">
              <span class="text-[13px] text-muted-foreground">{{ t("ai.provider") }}</span>
              <select
                :value="provider"
                class="mt-1 w-full rounded-md border border-input bg-background px-2.5 py-1.5 text-[13px]"
                @change="pickProvider(($event.target as HTMLSelectElement).value)"
              >
                <option v-for="p in providers" :key="p.name" :value="p.name">
                  {{ p.label }} — {{ p.ready ? t("ai.ready") : t("ai.notReady") }}
                </option>
              </select>
            </label>

            <label class="block">
              <span class="text-[13px] text-muted-foreground">{{ t("ai.model") }}</span>
              <input
                v-model="model"
                class="mt-1 w-full rounded-md border border-input bg-background px-2.5 py-1.5 text-[13px]"
                :placeholder="current?.model ?? ''"
              />
            </label>

            <label v-if="!current?.local" class="block">
              <span class="text-[13px] text-muted-foreground">API Key</span>
              <input
                v-model="apiKey"
                type="password"
                autocomplete="off"
                class="mt-1 w-full rounded-md border border-input bg-background px-2.5 py-1.5 text-[13px]"
                :placeholder="current?.hasKey ? t('ai.keySaved') : 'sk-...'"
              />
            </label>
          </div>

          <div class="mt-4 flex items-center gap-2">
            <button
              type="button"
              class="inline-flex h-9 items-center gap-2 rounded-md bg-primary px-4 text-[13.5px] font-medium text-primary-foreground disabled:opacity-50"
              :disabled="testing"
              @click="saveAndTest"
            >
              <Loader2 v-if="testing" :size="14" class="animate-spin" />
              {{ t("ai.saveAndTest") }}
            </button>
            <button
              v-if="ready"
              type="button"
              class="inline-flex h-9 items-center gap-2 rounded-md border border-input px-3.5 text-[13.5px] hover:bg-accent"
              @click="showConfig = false"
            >
              {{ t("ai.useReady") }}
            </button>
          </div>

          <p v-if="testResult" class="mt-3 flex items-start gap-1.5 text-[13px]"
             :class="testResult.ok ? 'text-emerald-600' : 'text-destructive'">
            <Check v-if="testResult.ok" :size="14" class="mt-0.5 shrink-0" />
            <X v-else :size="14" class="mt-0.5 shrink-0" />
            <span>{{ testResult.ok ? t("ai.testOk") : (testResult.error || testResult.hint || t("ai.testFailed")) }}</span>
          </p>
          <p v-if="failure" class="mt-2 text-[13px] text-destructive">{{ failure }}</p>
        </div>
      </div>
    </div>

    <!-- ── 对话 ── -->
    <template v-else>
      <div ref="scroller" class="min-h-0 flex-1 overflow-y-auto">
        <div class="mx-auto max-w-[820px] px-6 py-6">
          <!-- 开场：只有一个问题，而不是一堆表单 -->
          <div v-if="turns.length === 0 && !streaming" class="pt-16 text-center">
            <h2 class="text-[19px] font-semibold tracking-tight">{{ t("ai.emptyTitle") }}</h2>
            <p class="mt-2 text-[13.5px] leading-relaxed text-muted-foreground">{{ t("ai.emptyHint") }}</p>
            <div class="mt-6 flex flex-wrap justify-center gap-2">
              <button
                v-for="s in suggestions"
                :key="s"
                type="button"
                class="rounded-full border border-border bg-card px-3.5 py-1.5 text-[13px] transition-colors hover:bg-accent"
                @click="useSuggestion(s)"
              >
                {{ s }}
              </button>
            </div>
          </div>

          <!-- 历史 -->
          <div v-for="(turn, i) in turns" :key="i" class="mb-4">
            <div v-if="turn.role === 'user'" class="flex justify-end">
              <div class="max-w-[85%] whitespace-pre-wrap rounded-2xl rounded-br-sm bg-primary px-4 py-2.5 text-[13.5px] leading-relaxed text-primary-foreground">
                {{ turn.content }}
              </div>
            </div>
            <div v-else-if="turn.role === 'assistant'" class="whitespace-pre-wrap text-[13.5px] leading-[1.75]">
              {{ turn.content }}
            </div>
          </div>

          <!-- 工具步骤：让人看见它在干什么，而不是干等 -->
          <div v-if="steps.length" class="mb-3 space-y-1">
            <div
              v-for="(s, i) in steps"
              :key="i"
              class="flex items-start gap-2 text-[12.5px] text-muted-foreground"
            >
              <Wrench :size="12" class="mt-0.5 shrink-0" />
              <span class="font-mono">{{ s.tool }}</span>
              <Loader2 v-if="!s.done" :size="12" class="mt-0.5 animate-spin" />
              <Check v-else-if="s.ok" :size="12" class="mt-0.5 text-emerald-600" />
              <X v-else :size="12" class="mt-0.5 text-destructive" />
              <span class="min-w-0 truncate">{{ s.detail }}</span>
            </div>
          </div>

          <!-- 流式 -->
          <div v-if="streaming" class="whitespace-pre-wrap text-[13.5px] leading-[1.75]">{{ streaming }}</div>
          <div v-if="busy && !streaming" class="flex items-center gap-2 text-[13px] text-muted-foreground">
            <Loader2 :size="13" class="animate-spin" />{{ t("ai.thinking") }}
          </div>
          <p v-if="failure" class="mt-3 text-[13px] text-destructive">{{ failure }}</p>
        </div>
      </div>

      <!-- 输入 -->
      <div class="shrink-0 border-t border-border px-6 py-3">
        <div class="mx-auto max-w-[820px]">
          <div class="flex items-end gap-2 rounded-xl border border-input bg-background px-3 py-2 focus-within:ring-1 focus-within:ring-ring">
            <textarea
              ref="inputEl"
              v-model="draft"
              rows="1"
              :placeholder="dir ? t('ai.placeholder') : t('ai.placeholderNoDir')"
              class="max-h-32 min-h-[24px] flex-1 resize-none bg-transparent text-[13.5px] leading-relaxed outline-none placeholder:text-muted-foreground"
              @keydown="onKeydown"
            />
            <button
              type="button"
              class="mb-0.5 inline-flex size-8 shrink-0 items-center justify-center rounded-lg bg-primary text-primary-foreground disabled:opacity-40"
              :disabled="!draft.trim() || busy"
              :title="t('ai.send')"
              @click="send"
            >
              <ArrowUp :size="15" />
            </button>
          </div>
          <div class="mt-1.5 flex items-center justify-between px-1 text-[12px] text-muted-foreground">
            <span>{{ provider }} · {{ model }}</span>
            <button type="button" class="hover:text-foreground" @click="showConfig = true">{{ t("ai.changeModel") }}</button>
          </div>
        </div>
      </div>
    </template>
  </div>
</template>
