<script setup lang="ts">
/**
 * 主区对话 + 模型设置弹窗。
 *
 * 模型接入的流程参照 opencode / Hermes 那类工具，就两步：
 *   ① 接入：选服务商（或「自定义」自己填地址）+ 填 API Key → 保存并测试
 *   ② 刷新模型：接上之后再向对方 /v1/models 要一次列表，从中选一个
 *
 * 为什么不在配置里写死模型清单：各家模型更新很快，写死的清单很快就旧了，
 * 而且清单里总有些用户根本不会选的条目。先接入拿真实列表，用户看到的就是
 * 「这个 key 到底能用哪些」。
 *
 * 也不再把「未就绪」当主界面的状态 —— 平时你看到的就是对话，
 * 只有点齿轮才进设置。
 */
import { computed, nextTick, onMounted, onUnmounted, ref } from "vue";
import { ArrowUp, Check, Loader2, RefreshCw, Settings2, Wrench, X } from "@lucide/vue";
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
const baseURL = ref("");
const busy = ref(false);
const testing = ref(false);
const testResult = ref<TestResult | null>(null);
const failure = ref("");
const showSettings = ref(false);

/** 已接入（能拿到客户端）与模型清单分开对待 —— 这是两步流程的核心。 */
const connected = ref(false);
const models = ref<string[]>([]);
const loadingModels = ref(false);
const modelsError = ref("");

const turns = ref<ChatTurn[]>([]);
const draft = ref("");
const streaming = ref("");
const scroller = ref<HTMLElement | null>(null);
const inputEl = ref<HTMLTextAreaElement | null>(null);

let offDelta: (() => void) | null = null;
let offTool: (() => void) | null = null;

const current = computed(() => providers.value.find((p) => p.name === provider.value));
/** 自定义接入才需要用户填地址；预设服务商的地址是已知的。 */
const isCustom = computed(() => provider.value === "custom");
const ready = computed(() => providers.value.some((p) => p.ready));

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
    providers.value.find((p) => p.name === st.provider) ??
    providers.value.find((p) => p.ready) ??
    providers.value[0];
  if (preferred) {
    provider.value = preferred.name;
    model.value = st.model || preferred.model;
    baseURL.value = st.baseURL || "";
    connected.value = preferred.ready;
  }
}

function pickProvider(name: string) {
  provider.value = name;
  const p = providers.value.find((x) => x.name === name);
  model.value = p?.model ?? "";
  baseURL.value = "";
  models.value = p?.models ?? [];
  modelsError.value = "";
  testResult.value = null;
  failure.value = "";
  connected.value = Boolean(p?.ready);
}

/** 接入：保存 + 测试。成功后就能拉模型了。 */
async function connect() {
  failure.value = "";
  testResult.value = null;
  modelsError.value = "";
  testing.value = true;
  try {
    await AIService.SaveSettings({
      provider: provider.value,
      baseURL: isCustom.value ? baseURL.value.trim() : (current.value?.baseURL ?? ""),
      model: model.value,
      apiKey: apiKey.value,
      clearKey: false,
      disableLocalProbe: false,
    });
    apiKey.value = "";
    testResult.value = await AIService.Test(provider.value);
    if (testResult.value?.ok) {
      connected.value = true;
      await refresh();
      await loadModels();
    }
  } catch (err) {
    failure.value = errorText(err);
  } finally {
    testing.value = false;
  }
}

/** 刷新模型：真正向服务商要一次 /v1/models。 */
async function loadModels() {
  loadingModels.value = true;
  modelsError.value = "";
  try {
    const list = (await AIService.Models(provider.value)) ?? [];
    models.value = list;
    if (list.length === 0) {
      modelsError.value = t("ai.noModels");
    } else if (!model.value || !list.includes(model.value)) {
      model.value = list[0];
    }
  } catch (err) {
    modelsError.value = errorText(err);
  } finally {
    loadingModels.value = false;
  }
}

/** 选完模型要落盘，否则下次启动还是旧的（或空的）。 */
async function saveModel() {
  failure.value = "";
  try {
    await AIService.SaveSettings({
      provider: provider.value,
      baseURL: isCustom.value ? baseURL.value.trim() : (current.value?.baseURL ?? ""),
      model: model.value,
      apiKey: "",
      clearKey: false,
      disableLocalProbe: false,
    });
    await refresh();
    showSettings.value = false;
  } catch (err) {
    failure.value = errorText(err);
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

function openSettings() {
  testResult.value = null;
  failure.value = "";
  modelsError.value = "";
  showSettings.value = true;
}

async function send() {
  const text = draft.value.trim();
  if (!text || busy.value) return;

  // 没接入就先接入 —— 与其报一句「未配置」，不如直接把设置打开
  if (!enabled.value || !ready.value) {
    openSettings();
    return;
  }

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
    <!-- ── 对话 ── -->
    <div ref="scroller" class="min-h-0 flex-1 overflow-y-auto">
      <div class="mx-auto max-w-[820px] px-6 py-6">
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

        <div v-if="streaming" class="whitespace-pre-wrap text-[13.5px] leading-[1.75]">{{ streaming }}</div>
        <div v-if="busy && !streaming" class="flex items-center gap-2 text-[13px] text-muted-foreground">
          <Loader2 :size="13" class="animate-spin" />{{ t("ai.thinking") }}
        </div>
        <p v-if="failure" class="mt-3 text-[13px] text-destructive">{{ failure }}</p>
      </div>
    </div>

    <!-- ── 输入 ── -->
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
            class="mb-0.5 grid size-8 shrink-0 place-items-center rounded-lg text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
            :title="t('ai.modelSettings')"
            @click="openSettings"
          >
            <Settings2 :size="15" />
          </button>
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
          <span v-if="ready">{{ current?.label || provider }} · {{ model }}</span>
          <button v-else type="button" class="text-primary hover:underline" @click="openSettings">
            {{ t("ai.configureFirst") }}
          </button>
          <span v-if="!enabled">{{ t("ai.disabled") }}</span>
          <span v-else></span>
        </div>
      </div>
    </div>

    <!-- ── 模型设置 ── -->
    <div
      v-if="showSettings"
      class="fixed inset-0 z-50 grid place-items-center bg-black/40 p-6"
      @click.self="showSettings = false"
    >
      <div class="w-full max-w-[600px] rounded-xl border border-border bg-background shadow-xl">
        <div class="flex items-center justify-between border-b border-border px-5 py-3">
          <h3 class="text-[14.5px] font-semibold">{{ t("ai.modelSettings") }}</h3>
          <button
            type="button"
            class="grid size-7 place-items-center rounded-md text-muted-foreground hover:bg-accent"
            @click="showSettings = false"
          >
            <X :size="15" />
          </button>
        </div>

        <div class="max-h-[70vh] space-y-5 overflow-y-auto px-5 py-4">
          <p v-if="!enabled" class="rounded-md bg-muted/50 p-3 text-[13px] leading-relaxed text-muted-foreground">
            {{ t("ai.disabledHint") }}
          </p>

          <!-- ① 接入 -->
          <section>
            <p class="mb-2 flex items-center gap-2 text-[13px] font-medium">
              <span class="grid size-5 place-items-center rounded-full bg-primary text-[11px] text-primary-foreground">1</span>
              {{ t("ai.stepConnect") }}
            </p>
            <div class="grid grid-cols-3 gap-2">
              <button
                v-for="p in providers"
                :key="p.name"
                type="button"
                class="flex flex-col items-start gap-1 rounded-lg border px-3 py-2 text-left transition-colors"
                :class="provider === p.name ? 'border-primary bg-primary/5' : 'border-border hover:bg-accent'"
                @click="pickProvider(p.name)"
              >
                <span class="text-[13px] font-medium">{{ p.label || p.name }}</span>
                <span class="flex items-center gap-1 text-[11.5px] text-muted-foreground">
                  <Check v-if="p.ready" :size="11" class="text-emerald-600" />
                  {{ p.ready ? t("ai.ready") : p.local ? t("ai.notRunning") : t("ai.needsKey") }}
                </span>
              </button>
            </div>

            <!-- 自定义才需要地址 -->
            <label v-if="isCustom" class="mt-3 block">
              <span class="text-[13px] text-muted-foreground">{{ t("ai.baseURL") }}</span>
              <input
                v-model="baseURL"
                class="mt-1 w-full rounded-md border border-input bg-background px-2.5 py-1.5 font-mono text-[13px]"
                placeholder="https://example.com/v1"
              />
            </label>

            <label v-if="!current?.local" class="mt-3 block">
              <span class="text-[13px] text-muted-foreground">API Key</span>
              <input
                v-model="apiKey"
                type="password"
                autocomplete="off"
                class="mt-1 w-full rounded-md border border-input bg-background px-2.5 py-1.5 font-mono text-[13px]"
                :placeholder="current?.hasKey ? t('ai.keySaved') : 'sk-...'"
                @keydown.enter.prevent="connect"
              />
            </label>

            <div class="mt-3 flex items-center gap-2">
              <button
                type="button"
                class="inline-flex h-9 items-center gap-2 rounded-md bg-primary px-4 text-[13.5px] font-medium text-primary-foreground disabled:opacity-50"
                :disabled="testing || !provider"
                @click="connect"
              >
                <Loader2 v-if="testing" :size="14" class="animate-spin" />
                {{ t("ai.connect") }}
              </button>
              <span
                v-if="testResult"
                class="flex items-center gap-1 text-[13px]"
                :class="testResult.ok ? 'text-emerald-600' : 'text-destructive'"
              >
                <Check v-if="testResult.ok" :size="13" />
                <X v-else :size="13" />
                {{ testResult.ok ? t("ai.connected") : testResult.error || testResult.hint || t("ai.testFailed") }}
              </span>
              <span v-else-if="connected && ready" class="text-[13px] text-emerald-600">{{ t("ai.alreadyConnected") }}</span>
            </div>
          </section>

          <!-- ② 刷新模型：接上之后才点得动 -->
          <section :class="connected || ready ? '' : 'opacity-50'">
            <p class="mb-2 flex items-center gap-2 text-[13px] font-medium">
              <span class="grid size-5 place-items-center rounded-full bg-primary text-[11px] text-primary-foreground">2</span>
              {{ t("ai.stepModel") }}
              <button
                type="button"
                class="ml-1 inline-flex items-center gap-1 rounded-md border border-input px-2 py-0.5 text-[12px] transition-colors hover:bg-accent disabled:opacity-50"
                :disabled="loadingModels || !(connected || ready)"
                @click="loadModels"
              >
                <RefreshCw :size="11" :class="loadingModels ? 'animate-spin' : ''" />
                {{ t("ai.refreshModels") }}
              </button>
            </p>

            <select
              v-if="models.length"
              v-model="model"
              class="w-full rounded-md border border-input bg-background px-2.5 py-1.5 font-mono text-[13px]"
            >
              <option v-for="m in models" :key="m" :value="m">{{ m }}</option>
            </select>
            <input
              v-else
              v-model="model"
              class="w-full rounded-md border border-input bg-background px-2.5 py-1.5 font-mono text-[13px]"
              :placeholder="t('ai.modelPlaceholder')"
            />
            <p v-if="modelsError" class="mt-1.5 text-[12.5px] text-destructive">{{ modelsError }}</p>
            <p v-else class="mt-1.5 text-[12.5px] text-muted-foreground">
              {{ models.length ? t("ai.modelsCount").replace("{n}", String(models.length)) : t("ai.refreshHint") }}
            </p>
          </section>

          <p v-if="failure" class="text-[13px] text-destructive">{{ failure }}</p>
        </div>

        <div class="flex items-center justify-end gap-2 border-t border-border px-5 py-3">
          <button
            type="button"
            class="h-9 rounded-md border border-input px-3.5 text-[13.5px] hover:bg-accent"
            @click="showSettings = false"
          >
            {{ t("ai.cancel") }}
          </button>
          <button
            type="button"
            class="h-9 rounded-md bg-primary px-4 text-[13.5px] font-medium text-primary-foreground disabled:opacity-50"
            :disabled="!model || !(connected || ready)"
            @click="saveModel"
          >
            {{ t("ai.useThisModel") }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
