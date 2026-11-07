<script setup lang="ts">
/**
 * Excel 工作台。
 *
 * 布局：左边「文件 + 场景」，右边 AI 面板。
 *
 * 现状（按实现诚实标注，不画假按钮）：
 *   · 拖入文件并按类型统计 —— 真的，浏览器 File API 本地读
 *   · 场景卡片 —— 真的，把指令填进 AI 输入框
 *   · AI 对话 —— 真的，走 internal/ai 的流式接口
 *   · 「开始处理」—— 还没实现（工具层接入后才有），所以这里不放这个按钮
 */
import { computed, ref } from "vue";
import { FileSpreadsheet, FolderOpen, Layers, Sparkles, Table2, Wand2 } from "@lucide/vue";
import { useI18n } from "../i18n";
import AiChatPanel from "./AiChatPanel.vue";
import { humanSize } from "../lib/ai";

const { t } = useI18n();

type Picked = { name: string; size: number };

const files = ref<Picked[]>([]);
const dragging = ref(false);
const panel = ref<InstanceType<typeof AiChatPanel> | null>(null);

const totalSize = computed(() => files.value.reduce((sum, f) => sum + f.size, 0));
const shown = computed(() => files.value.slice(0, 8));

/** Excel 相关后缀才算有效输入，其它文件忽略并计数。 */
const EXCEL_EXT = [".xlsx", ".xls", ".xlsm", ".csv"];
const ignored = ref(0);

function accept(fileList: FileList | null) {
  if (!fileList) return;
  const next: Picked[] = [];
  let skip = 0;
  for (const f of Array.from(fileList)) {
    const lower = f.name.toLowerCase();
    if (EXCEL_EXT.some((ext) => lower.endsWith(ext))) {
      next.push({ name: f.name, size: f.size });
    } else {
      skip += 1;
    }
  }
  files.value = next.slice(0, 500); // 只用于展示，别把几千个名字塞进 DOM
  ignored.value = skip;
}

function onDrop(event: DragEvent) {
  dragging.value = false;
  accept(event.dataTransfer?.files ?? null);
}

function onPick(event: Event) {
  accept((event.target as HTMLInputElement).files);
}

/** 场景卡片：把指令填进 AI 输入框，由用户确认后发送。 */
const scenarios = [
  { key: "merge", icon: Layers },
  { key: "clean", icon: Wand2 },
  { key: "summary", icon: Table2 },
] as const;

const scenarioPrompt: Record<string, string> = {
  merge: "把这些文件合并成一张总表，表头以第一个文件为准，按部门汇总。",
  clean: "清洗这批表格：去掉空行和重复行，日期统一成 YYYY-MM-DD，数字列去掉文本格式。",
  summary: "按月份和部门做分组汇总，输出一行一条的汇总表。",
};

function useScenario(key: string) {
  panel.value?.prefill(scenarioPrompt[key] ?? "");
}
</script>

<template>
  <div class="flex h-full min-h-0">
    <!-- 左：文件与场景 -->
    <section class="flex min-w-0 flex-1 flex-col overflow-y-auto">
      <div class="space-y-5 p-6">
        <header class="space-y-1">
          <h1 class="text-lg font-semibold">{{ t("workbench.title") }}</h1>
          <p class="text-sm text-muted-foreground">{{ t("workbench.subtitle") }}</p>
        </header>

        <!-- 拖入区 -->
        <div
          class="rounded-lg border-2 border-dashed px-8 py-12 text-center transition-colors"
          :class="dragging ? 'border-primary bg-primary/5' : 'border-border bg-card/40'"
          @dragover.prevent="dragging = true"
          @dragleave.prevent="dragging = false"
          @drop.prevent="onDrop"
        >
          <FolderOpen :size="28" class="mx-auto mb-3 text-muted-foreground" />
          <p class="text-sm font-medium">{{ t("workbench.dropTitle") }}</p>
          <p class="mt-1 text-xs text-muted-foreground">{{ t("workbench.dropHint") }}</p>

          <label
            class="mt-4 inline-flex cursor-pointer items-center gap-2 rounded-md border border-input px-3 py-1.5 text-sm hover:bg-accent"
          >
            <FileSpreadsheet :size="14" />
            {{ t("workbench.pick") }}
            <input type="file" multiple class="hidden" @change="onPick" />
          </label>
        </div>

        <!-- 已选文件 -->
        <div v-if="files.length" class="rounded-lg border border-border bg-card p-4">
          <div class="mb-3 flex items-center justify-between">
            <p class="text-sm font-medium">
              {{ t("workbench.filesCount").replace("{n}", String(files.length)) }}
              <span class="ml-2 font-normal text-muted-foreground">{{ humanSize(totalSize) }}</span>
            </p>
            <button
              type="button"
              class="text-xs text-muted-foreground hover:text-foreground"
              @click="files = []; ignored = 0"
            >
              {{ t("workbench.clear") }}
            </button>
          </div>
          <ul class="space-y-1">
            <li
              v-for="f in shown"
              :key="f.name"
              class="flex items-center justify-between gap-3 text-xs text-muted-foreground"
            >
              <span class="truncate font-mono">{{ f.name }}</span>
              <span class="shrink-0">{{ humanSize(f.size) }}</span>
            </li>
          </ul>
          <p v-if="files.length > shown.length" class="mt-2 text-[11px] text-muted-foreground">
            {{ t("workbench.more").replace("{n}", String(files.length - shown.length)) }}
          </p>
          <p v-if="ignored" class="mt-2 text-[11px] text-muted-foreground">
            {{ t("workbench.ignored").replace("{n}", String(ignored)) }}
          </p>
        </div>

        <!-- 场景 -->
        <div class="space-y-3">
          <h2 class="flex items-center gap-2 text-sm font-semibold">
            <Sparkles :size="14" class="text-muted-foreground" />
            {{ t("workbench.scenarios") }}
          </h2>
          <div class="grid gap-3 sm:grid-cols-3">
            <button
              v-for="s in scenarios"
              :key="s.key"
              type="button"
              class="rounded-lg border border-border bg-card p-4 text-left transition-colors hover:border-primary/40 hover:bg-accent/40"
              @click="useScenario(s.key)"
            >
              <component :is="s.icon" :size="16" class="mb-2 text-muted-foreground" />
              <p class="text-sm font-medium">{{ t(`workbench.${s.key}.title`) }}</p>
              <p class="mt-1 text-xs leading-relaxed text-muted-foreground">
                {{ t(`workbench.${s.key}.desc`) }}
              </p>
            </button>
          </div>
          <p class="text-[11px] leading-relaxed text-muted-foreground">{{ t("workbench.scenarioHint") }}</p>
        </div>

        <!-- 空状态：说明接下来会发生什么，而不是留一大片空白 -->
        <div v-if="!files.length" class="rounded-lg border border-border bg-card/40 p-4">
          <p class="text-xs leading-relaxed text-muted-foreground">{{ t("workbench.next") }}</p>
        </div>
      </div>
    </section>

    <!-- 右：AI -->
    <aside class="w-[360px] shrink-0 border-l border-border">
      <AiChatPanel ref="panel" />
    </aside>
  </div>
</template>
