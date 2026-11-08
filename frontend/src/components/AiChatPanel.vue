<script setup lang="ts">
/**
 * 对话即界面。
 *
 * 这一版把上一版的「顶部输入/输出栏」也去掉了：那一栏让整个应用看起来
 * 像一张设置表，而用户要做的事情其实就是说话。现在整窗只有一列对话，
 * 文件选择降级成输入框旁边的附件按钮 —— 选了会显示成一个小标签，
 * 不选也能先聊天（这时候模型会问你）。
 *
 * 能力的组合在 Go 侧的工具循环里，这里只负责：把当前上下文交给模型、
 * 把流式文字和工具步骤显示出来、没接入模型时把设置打开。
 */
import { computed, nextTick, onMounted, onUnmounted, ref } from "vue";
import { ArrowUp, Check, FolderOpen, Layers, Loader2, Settings2, Wrench, X } from "@lucide/vue";
import { useI18n } from "../i18n";
import { useWorkspace } from "../composables/workspace";
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

const { t } = useI18n();
const ws = useWorkspace();

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
/** 功能选择下拉。 */
const showFeatures = ref(false);
/** 首次选某个功能时的组件提示。 */
const pendingPack = ref<{ id: string; name: string; bundled: boolean; missing: string[] } | null>(null);

/** 接入与选模型是两步 —— 和 opencode / Hermes 的顺序一致。 */
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
const isCustom = computed(() => provider.value === "custom");
const ready = computed(() => providers.value.some((p) => p.ready));

const suggestions = computed(() =>
  ws.paths.value.length > 0
    ? [t("ai.askClean"), t("ai.askSummary"), t("ai.askLook")]
    : [t("ai.askBrowse"), t("ai.askWhat")],
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
  if (text === t("ai.askBrowse")) {
    ws.pickPaths(t("ai.pickInput"));
    return;
  }
  if (text === t("ai.askWhat")) {
    draft.value = t("ai.askWhatThen");
    inputEl.value?.focus();
    return;
  }
  draft.value = text;
  inputEl.value?.focus();
}

const featureName = computed(() => {
  const p = ws.packs.value.find((x) => x.id === ws.feature.value);
  return p?.name ?? ws.feature.value;
});

/**
 * 选功能。
 *
 * 第一次选某个功能时先给一个组件提示：功能组件是不是已经在本机。
 * 纯 Go 实现的能力（表格）随应用装好，直接可用；将来接视频 / PDF 这类
 * 需要 ffmpeg / pandoc 的包，会在这里显示要下载什么 —— 与其让用户
 * 点了才看到「exec: ffmpeg not found」，不如提前说清楚。
 */
function chooseFeature(id: string) {
  showFeatures.value = false;
  if (!id) {
    ws.setFeature("");
    return;
  }
  const pack = ws.packs.value.find((x) => x.id === id);
  if (!pack) return;
  const seenKey = `deskkit-feature-seen-${id}`;
  if (localStorage.getItem(seenKey)) {
    ws.setFeature(id);
    return;
  }
  pendingPack.value = {
    id: pack.id,
    name: pack.name,
    bundled: Boolean(pack.bundled),
    missing: pack.missing ?? [],
  };
}

function confirmFeature() {
  if (!pendingPack.value) return;
  localStorage.setItem(`deskkit-feature-seen-${pendingPack.value.id}`, "1");
  ws.setFeature(pendingPack.value.id);
  pendingPack.value = null;
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
      paths: ws.paths.value,
      outputHint: ws.outputHint.value,
      summary: ws.summary.value,
      columns: ws.columns.value,
      feature: ws.feature.value,
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
  ws.loadPacks().catch(() => {});
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
      <div class="mx-auto max-w-[760px] px-6 py-8">
        <div v-if="turns.length === 0 && !streaming" class="pt-20 text-center">
          <h2 class="text-[22px] font-semibold tracking-tight">{{ t("ai.emptyTitle") }}</h2>
          <p class="mx-auto mt-2.5 max-w-[480px] text-[13.5px] leading-relaxed text-muted-foreground">
            {{ t("ai.emptyHint") }}
          </p>
          <div class="mt-7 flex flex-wrap justify-center gap-2">
            <button
              v-for="s in suggestions"
              :key="s"
              type="button"
              class="rounded-full border border-border bg-card px-4 py-1.5 text-[13px] transition-colors hover:bg-accent"
              @click="useSuggestion(s)"
            >
              {{ s }}
            </button>
          </div>
        </div>

        <div v-for="(turn, i) in turns" :key="i" class="mb-5">
          <div v-if="turn.role === 'user'" class="flex justify-end">
            <div class="max-w-[85%] whitespace-pre-wrap rounded-2xl rounded-br-sm bg-primary px-4 py-2.5 text-[13.5px] leading-relaxed text-primary-foreground">
              {{ turn.content }}
            </div>
          </div>
          <div v-else-if="turn.role === 'assistant'" class="whitespace-pre-wrap text-[13.5px] leading-[1.8]">
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

        <div v-if="streaming" class="whitespace-pre-wrap text-[13.5px] leading-[1.8]">{{ streaming }}</div>
        <div v-if="busy && !streaming" class="flex items-center gap-2 text-[13px] text-muted-foreground">
          <Loader2 :size="13" class="animate-spin" />{{ t("ai.thinking") }}
        </div>
        <p v-if="failure || ws.failure.value" class="mt-3 text-[13px] text-destructive">
          {{ failure || ws.failure.value }}
        </p>
      </div>
    </div>

    <!-- ── 输入区：对话的一部分，不是工具栏 ── -->
    <div class="shrink-0 px-6 pb-5">
      <div class="mx-auto max-w-[760px]">
        <!-- 已选文件：一个小标签。第二行小字说明结果会写到哪 -->
        <div v-if="ws.paths.value.length > 0" class="mb-2 flex flex-wrap items-center gap-x-3 gap-y-1">
          <span class="inline-flex max-w-full items-center gap-1.5 rounded-full bg-muted px-2.5 py-1 text-[12.5px]">
            <FolderOpen :size="12" class="shrink-0 text-muted-foreground" />
            <button type="button" class="truncate font-medium hover:underline" :title="ws.paths.value.join('\n')" @click="ws.pickPaths(t('ai.pickInput'))">
              {{ ws.selectionLabel.value }}
            </button>
            <span class="shrink-0 text-muted-foreground">{{ ws.summary.value }}</span>
            <button type="button" class="shrink-0 text-muted-foreground hover:text-foreground" @click="ws.clearPaths()">
              <X :size="12" />
            </button>
          </span>
          <button
            v-if="ws.feature.value"
            type="button"
            class="inline-flex items-center gap-1 rounded-full border border-border px-2.5 py-1 text-[12.5px] text-muted-foreground hover:bg-accent"
            @click="showFeatures = true"
          >
            <Layers :size="12" />{{ featureName }}
          </button>
          <span v-if="ws.outputHint.value" class="truncate text-[12px] text-muted-foreground" :title="ws.outputHint.value">
            {{ t("ai.willWriteTo") }} {{ ws.outputHint.value.split("/").pop() }}
          </span>
        </div>

        <div class="flex items-end gap-1.5 rounded-xl border border-input bg-background px-2 py-2 focus-within:ring-1 focus-within:ring-ring">
          <!-- ① 选文件 -->
          <button
            type="button"
            class="mb-0.5 grid size-8 shrink-0 place-items-center rounded-lg text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground disabled:opacity-50"
            :title="t('ai.pickInput')"
            :disabled="ws.busy.value"
            @click="ws.pickPaths(t('ai.pickInput'))"
          >
            <Loader2 v-if="ws.busy.value" :size="15" class="animate-spin" />
            <FolderOpen v-else :size="15" />
          </button>

          <!-- ② 功能选择。不选就是普通对话。 -->
          <div class="relative mb-0.5 shrink-0">
            <button
              type="button"
              class="grid size-8 place-items-center rounded-lg transition-colors hover:bg-accent"
              :class="ws.feature.value ? 'bg-accent text-accent-foreground' : 'text-muted-foreground hover:text-accent-foreground'"
              :title="t('ai.pickFeature')"
              @click="showFeatures = !showFeatures"
            >
              <Layers :size="15" />
            </button>

            <div
              v-if="showFeatures"
              class="absolute bottom-10 left-0 z-40 w-[300px] rounded-xl border border-border bg-background p-1.5 shadow-xl"
            >
              <button
                type="button"
                class="flex w-full flex-col items-start gap-0.5 rounded-lg px-2.5 py-2 text-left hover:bg-accent"
                :class="!ws.feature.value ? 'bg-accent/60' : ''"
                @click="chooseFeature('')"
              >
                <span class="text-[13px] font-medium">{{ t("ai.featureNone") }}</span>
                <span class="text-[11.5px] text-muted-foreground">{{ t("ai.featureNoneHint") }}</span>
              </button>
              <button
                v-for="p in ws.packs.value"
                :key="p.id"
                type="button"
                class="flex w-full flex-col items-start gap-0.5 rounded-lg px-2.5 py-2 text-left hover:bg-accent"
                :class="ws.feature.value === p.id ? 'bg-accent/60' : ''"
                @click="chooseFeature(p.id)"
              >
                <span class="text-[13px] font-medium">{{ p.name }}</span>
                <span class="text-[11.5px] leading-relaxed text-muted-foreground">{{ p.description }}</span>
              </button>
              <p class="px-2.5 pb-1 pt-2 text-[11px] text-muted-foreground">{{ t("ai.featureMore") }}</p>
            </div>
          </div>

          <textarea
            ref="inputEl"
            v-model="draft"
            rows="1"
            :placeholder="ws.feature.value ? t('ai.placeholderFeature') : t('ai.placeholder')"
            class="max-h-40 min-h-[26px] flex-1 resize-none bg-transparent px-1 py-1 text-[13.5px] leading-relaxed outline-none placeholder:text-muted-foreground"
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

        <div class="mt-1.5 flex items-center justify-between px-1 text-[11.5px] text-muted-foreground">
          <span v-if="ready">{{ current?.label || provider }} · {{ model }}</span>
          <button v-else type="button" class="text-primary hover:underline" @click="openSettings">
            {{ t("ai.configureFirst") }}
          </button>
          <span class="font-mono">{{ ws.columns.value.length ? ws.columns.value.slice(0, 6).join(" · ") : "" }}</span>
        </div>
      </div>
    </div>

    <!-- ── 首次使用某功能的组件提示 ── -->
    <div
      v-if="pendingPack"
      class="fixed inset-0 z-50 grid place-items-center bg-black/40 p-6"
      @click.self="pendingPack = null"
    >
      <div class="w-full max-w-[460px] rounded-xl border border-border bg-background p-5 shadow-xl">
        <div class="flex items-start gap-2">
          <Layers :size="16" class="mt-0.5 shrink-0 text-primary" />
          <div>
            <h3 class="text-[14.5px] font-semibold">{{ pendingPack.name }}</h3>
            <p class="mt-1.5 text-[13px] leading-relaxed text-muted-foreground">
              {{ pendingPack.bundled ? t("ai.packBundled") : t("ai.packDownload") }}
            </p>
            <ul v-if="pendingPack.missing.length" class="mt-2 list-inside list-disc text-[12.5px] text-amber-600">
              <li v-for="m in pendingPack.missing" :key="m">{{ m }}</li>
            </ul>
          </div>
        </div>
        <div class="mt-4 flex justify-end gap-2">
          <button type="button" class="h-9 rounded-md border border-input px-3.5 text-[13.5px] hover:bg-accent" @click="pendingPack = null">
            {{ t("ai.cancel") }}
          </button>
          <button type="button" class="h-9 rounded-md bg-primary px-4 text-[13.5px] font-medium text-primary-foreground" @click="confirmFeature">
            {{ pendingPack.bundled ? t("ai.useFeature") : t("ai.downloadFeature") }}
          </button>
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
          <button type="button" class="grid size-7 place-items-center rounded-md text-muted-foreground hover:bg-accent" @click="showSettings = false">
            <X :size="15" />
          </button>
        </div>

        <div class="max-h-[70vh] space-y-5 overflow-y-auto px-5 py-4">
          <p v-if="!enabled" class="rounded-md bg-muted/50 p-3 text-[13px] leading-relaxed text-muted-foreground">
            {{ t("ai.disabledHint") }}
          </p>

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
                <span class="truncate text-[13px] font-medium">{{ p.label || p.name }}</span>
                <span class="flex items-center gap-1 text-[11.5px] text-muted-foreground">
                  <Check v-if="p.ready" :size="11" class="text-emerald-600" />
                  {{ p.ready ? t("ai.ready") : p.local ? t("ai.notRunning") : t("ai.needsKey") }}
                </span>
              </button>
            </div>

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
              <span v-if="testResult" class="flex items-center gap-1 text-[13px]" :class="testResult.ok ? 'text-emerald-600' : 'text-destructive'">
                <Check v-if="testResult.ok" :size="13" />
                <X v-else :size="13" />
                {{ testResult.ok ? t("ai.connected") : testResult.error || testResult.hint || t("ai.testFailed") }}
              </span>
              <span v-else-if="connected && ready" class="text-[13px] text-emerald-600">{{ t("ai.alreadyConnected") }}</span>
            </div>
          </section>

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
                <Loader2 v-if="loadingModels" :size="11" class="animate-spin" />
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
          <button type="button" class="h-9 rounded-md border border-input px-3.5 text-[13.5px] hover:bg-accent" @click="showSettings = false">
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
