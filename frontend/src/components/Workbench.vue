<script setup lang="ts">
/**
 * 工作台 —— 只剩三件事：选输入、选输出、说要干什么。
 *
 * 为什么不做「合并表单 / 清洗表单 / 汇总表单」：
 * 每个表单都在替用户决定「你要怎么做」。可表格处理有无数种做法，
 * 我们猜不完；而用户自己知道他要什么。所以界面只提供上下文
 * （哪批文件、输出到哪），具体做什么交给 AI 现场判断、现场调工具。
 *
 * 想加新能力时：在 internal/tools/excel 里加一个工具并注册到 AI，
 * 界面上什么都不用改 —— 这是这块界面刻意保持这么小的原因。
 */
import { computed, ref } from "vue";
import { FolderOpen, Loader2, Sparkles, TriangleAlert } from "@lucide/vue";
import { useI18n } from "../i18n";
import * as Tools from "../../bindings/github.com/klphp/excel-batch-tool/internal/services/toolservice";
import type { ScanResult } from "../../bindings/github.com/klphp/excel-batch-tool/internal/tools/excel/models";
import AiChatPanel from "./AiChatPanel.vue";

const { t } = useI18n();

const dir = ref("");
const outDir = ref("");
const scan = ref<ScanResult | null>(null);
const busyScan = ref(false);
const failure = ref("");

/** 可用文件（读不了的不算进统计）。 */
const usable = computed(() => (scan.value?.files ?? []).filter((f) => !f.error));
const totalRows = computed(() => usable.value.reduce((sum, f) => sum + f.rows, 0));
const broken = computed(() => (scan.value?.files ?? []).filter((f) => f.error));

/** 给 AI 的一句话现状，比一屏表格行有用。 */
const summary = computed(() => {
  if (!dir.value || !scan.value) return "";
  if (usable.value.length === 0) return t("workbench.noFiles");
  return `${usable.value.length} ${t("workbench.filesUnit")} · ${totalRows.value} ${t("workbench.rowsUnit")}`;
});

/** 表头合集：让 AI 和你都一眼看到这批表有哪些列。 */
const columns = computed(() => {
  const seen = new Set<string>();
  for (const f of usable.value) for (const h of f.headers ?? []) if (h) seen.add(h);
  return [...seen].slice(0, 30);
});

async function rescan() {
  busyScan.value = true;
  failure.value = "";
  try {
    scan.value = await Tools.Scan(dir.value);
  } catch (err) {
    failure.value = String(err);
    scan.value = null;
  } finally {
    busyScan.value = false;
  }
}

async function pickInput() {
  failure.value = "";
  try {
    const picked = await Tools.PickFolder(t("workbench.pickInput"));
    if (!picked) return;
    dir.value = picked;
    outDir.value = "";
    await rescan();
  } catch (err) {
    failure.value = String(err);
  }
}

async function pickOutput() {
  try {
    const picked = await Tools.PickFolder(t("workbench.pickOutput"));
    if (picked) outDir.value = picked;
  } catch (err) {
    failure.value = String(err);
  }
}

/** 示例数据：手上没有表格也能立刻开始。 */
async function useSample() {
  busyScan.value = true;
  failure.value = "";
  try {
    const sample = await Tools.MakeSample();
    dir.value = sample.dir;
    outDir.value = "";
    await rescan();
  } catch (err) {
    failure.value = String(err);
  } finally {
    busyScan.value = false;
  }
}
</script>

<template>
  <div class="flex h-full min-h-0 flex-col">
    <!-- ── 选择区：输入 / 输出 ── -->
    <div class="shrink-0 border-b border-border bg-muted/40 px-6 py-3.5">
      <div class="mx-auto flex max-w-[820px] flex-col gap-2.5">
        <!-- 输入 -->
        <div class="flex items-center gap-3">
          <span class="w-9 shrink-0 text-[13px] font-medium text-muted-foreground">{{ t("workbench.input") }}</span>
          <div class="flex min-w-0 flex-1 items-center gap-2.5">
            <span v-if="dir" class="min-w-0 truncate font-mono text-[13px]" :title="dir">{{ dir }}</span>
            <span v-else class="truncate text-[13px] text-muted-foreground">{{ t("workbench.noInput") }}</span>
            <span v-if="summary" class="shrink-0 text-[13px] text-muted-foreground">{{ summary }}</span>
          </div>
          <button
            type="button"
            class="inline-flex h-8 shrink-0 items-center gap-1.5 rounded-md border border-input bg-background px-3 text-[13px] transition-colors hover:bg-accent disabled:opacity-50"
            :disabled="busyScan"
            @click="pickInput"
          >
            <Loader2 v-if="busyScan" :size="13" class="animate-spin" />
            <FolderOpen v-else :size="13" />
            {{ dir ? t("workbench.changeInput") : t("workbench.pickInput") }}
          </button>
          <button
            v-if="!dir"
            type="button"
            class="inline-flex h-8 shrink-0 items-center gap-1.5 rounded-md bg-primary px-3 text-[13px] font-medium text-primary-foreground disabled:opacity-50"
            :disabled="busyScan"
            @click="useSample"
          >
            <Sparkles :size="13" />
            {{ t("workbench.useSample") }}
          </button>
        </div>

        <!-- 输出 -->
        <div class="flex items-center gap-3">
          <span class="w-9 shrink-0 text-[13px] font-medium text-muted-foreground">{{ t("workbench.output") }}</span>
          <span class="min-w-0 flex-1 truncate font-mono text-[13px] text-muted-foreground">
            {{ outDir || (dir ? `${dir}/deskkit-out` : t("workbench.outputDefault")) }}
          </span>
          <button
            type="button"
            class="inline-flex h-8 shrink-0 items-center gap-1.5 rounded-md border border-input bg-background px-3 text-[13px] transition-colors hover:bg-accent disabled:opacity-50"
            :disabled="!dir"
            @click="pickOutput"
          >
            <FolderOpen :size="13" />
            {{ t("workbench.changeOutput") }}
          </button>
        </div>

        <!-- 列名：有了这一行，问 AI 时不用再描述表里有什么 -->
        <p v-if="columns.length" class="pl-12 text-[12.5px] leading-relaxed text-muted-foreground">
          {{ t("workbench.columns") }}：{{ columns.join(" · ") }}
        </p>
        <p v-if="broken.length" class="flex items-start gap-1.5 pl-12 text-[12.5px] text-amber-600">
          <TriangleAlert :size="13" class="mt-0.5 shrink-0" />
          <span>{{ t("workbench.broken") }}：{{ broken.map((f) => f.name).join("、") }}</span>
        </p>
        <p v-if="failure" class="pl-12 text-[12.5px] text-destructive">{{ failure }}</p>
      </div>
    </div>

    <!-- ── 主区：对话 ── -->
    <AiChatPanel
      class="min-h-0 flex-1"
      :dir="dir"
      :out-dir="outDir || (dir ? `${dir}/deskkit-out` : '')"
      :summary="summary"
      :columns="columns"
    />
  </div>
</template>
