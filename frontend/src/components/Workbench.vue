<script setup lang="ts">
/**
 * Excel 工作台。
 *
 * 全部走真实后端：选目录 → 扫文件（excelize 读表头与行数）→ 预览合并 →
 * 执行合并。没有占位数据，也没有「即将上线」的假按钮。
 *
 * 一个必须知道的限制：**拖入文件拿不到完整路径**（WebView 的 File 对象
 * 出于安全不给路径），所以处理文件夹只能走原生目录选择框。
 */
import { computed, ref } from "vue";
import {
  AlertTriangle,
  CheckCircle2,
  FileSpreadsheet,
  FolderOpen,
  Layers,
  Loader2,
  Table2,
  Wand2,
} from "@lucide/vue";
import { useI18n } from "../i18n";
import AiChatPanel from "./AiChatPanel.vue";
import { humanSize } from "../lib/ai";
import * as Tools from "../../bindings/github.com/klphp/excel-batch-tool/internal/services/toolservice";
import {
  CleanOptions,
  MergeOptions,
  type CleanResult,
  type MergeResult,
  type ScanResult,
} from "../../bindings/github.com/klphp/excel-batch-tool/internal/tools/excel/models";

const { t } = useI18n();
const panel = ref<InstanceType<typeof AiChatPanel> | null>(null);

const dir = ref("");
const scan = ref<ScanResult | null>(null);
const busyScan = ref(false);
const busyPreview = ref(false);
const busyRun = ref(false);
const preview = ref<MergeResult | null>(null);
const done = ref<MergeResult | null>(null);
const failure = ref("");
const dedupText = ref("");

// ── 清洗 ──
const cleanDropBlank = ref(true);
const cleanTrim = ref(true);
const cleanDropDup = ref(true);
const cleanDates = ref(true);
const cleanDateFmt = ref("YYYY-MM-DD");
const cleanNumbers = ref(true);
const cleanDedupText = ref("");
const cleanPreview = ref<CleanResult | null>(null);
const cleanDone = ref<CleanResult | null>(null);
const busyCleanPreview = ref(false);
const busyCleanRun = ref(false);

const files = computed(() => scan.value?.files ?? []);
const dedupKey = computed(() =>
  dedupText.value
    .split(/[,，]/)
    .map((s) => s.trim())
    .filter(Boolean),
);

async function pickFolder() {
  failure.value = "";
  try {
    const chosen = await Tools.PickFolder(t("workbench.pickTitle"));
    if (!chosen) return; // 用户取消
    dir.value = chosen;
    await rescan();
  } catch (err) {
    failure.value = String(err);
  }
}

async function rescan() {
  if (!dir.value) return;
  busyScan.value = true;
  preview.value = null;
  done.value = null;
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

function mergeOptions(dryRun: boolean): MergeOptions {
  return new MergeOptions({
    dir: dir.value,
    dedupKey: dedupKey.value,
    dryRun,
  });
}

async function doPreview() {
  busyPreview.value = true;
  done.value = null;
  failure.value = "";
  try {
    preview.value = await Tools.Merge(mergeOptions(true));
  } catch (err) {
    failure.value = String(err);
  } finally {
    busyPreview.value = false;
  }
}

async function doMerge() {
  busyRun.value = true;
  failure.value = "";
  try {
    done.value = await Tools.Merge(mergeOptions(false));
    preview.value = null;
  } catch (err) {
    failure.value = String(err);
  } finally {
    busyRun.value = false;
  }
}

function cleanOptions(dryRun: boolean): CleanOptions {
  return new CleanOptions({
    dir: dir.value,
    dropBlankRows: cleanDropBlank.value,
    dropDuplicates: cleanDropDup.value,
    dedupKey: cleanDedupText.value.split(/[,，]/).map((x) => x.trim()).filter(Boolean),
    trimSpaces: cleanTrim.value,
    normalizeDates: cleanDates.value,
    dateFormat: cleanDateFmt.value,
    numbersAsText: cleanNumbers.value,
    dryRun,
  });
}

async function doCleanPreview() {
  busyCleanPreview.value = true;
  cleanDone.value = null;
  failure.value = "";
  try {
    cleanPreview.value = await Tools.Clean(cleanOptions(true));
  } catch (err) {
    failure.value = String(err);
  } finally {
    busyCleanPreview.value = false;
  }
}

async function doCleanRun() {
  busyCleanRun.value = true;
  failure.value = "";
  try {
    cleanDone.value = await Tools.Clean(cleanOptions(false));
    cleanPreview.value = null;
  } catch (err) {
    failure.value = String(err);
  } finally {
    busyCleanRun.value = false;
  }
}

/** 场景卡片：把带路径的指令填进 AI 输入框，由用户确认后发送。 */
const scenarios = [
  { key: "merge", icon: Layers },
  { key: "clean", icon: Wand2 },
  { key: "summary", icon: Table2 },
] as const;

function useScenario(key: string) {
  const target = dir.value ? `目录 ${dir.value}` : "我选的这批表格";
  const prompts: Record<string, string> = {
    merge: `${target} 里的表表头不完全一样。先扫描看看情况，再预览合并结果，然后合并成一张总表。`,
    clean: `${target} 里的数据比较脏。先扫描看情况，告诉我有哪些问题（空行、重复、日期格式），再给方案。`,
    summary: `${target} 里的表需要按部门汇总。先扫描，再预览合并，如需分组统计请用预览结果告诉我行数与列名。`,
  };
  panel.value?.prefill(prompts[key] ?? "");
}
</script>

<template>
  <div class="flex h-full min-h-0">
    <!-- 左：文件与操作 -->
    <section class="flex min-w-0 flex-1 flex-col overflow-y-auto">
      <div class="space-y-4 p-6">
        <header class="space-y-1">
          <h1 class="text-lg font-semibold">{{ t("workbench.title") }}</h1>
          <p class="text-sm text-muted-foreground">{{ t("workbench.subtitle") }}</p>
        </header>

        <!-- 目录 -->
        <div class="rounded-lg border border-border bg-card p-4">
          <div class="flex items-center gap-3">
            <button
              type="button"
              class="inline-flex shrink-0 items-center gap-2 rounded-md border border-input px-3 py-1.5 text-sm hover:bg-accent"
              :disabled="busyScan"
              @click="pickFolder"
            >
              <FolderOpen :size="14" />
              {{ t("workbench.pick") }}
            </button>
            <p class="min-w-0 flex-1 truncate font-mono text-xs" :class="dir ? '' : 'text-muted-foreground'">
              {{ dir || t("workbench.noDir") }}
            </p>
            <button
              v-if="dir"
              type="button"
              class="shrink-0 text-xs text-muted-foreground hover:text-foreground"
              :disabled="busyScan"
              @click="rescan"
            >
              {{ t("workbench.rescan") }}
            </button>
          </div>

          <p v-if="busyScan" class="mt-3 flex items-center gap-2 text-xs text-muted-foreground">
            <Loader2 :size="12" class="animate-spin" />{{ t("workbench.scanning") }}
          </p>

          <div v-else-if="scan" class="mt-3 space-y-1 text-xs text-muted-foreground">
            <p>
              {{ t("workbench.summary").replace("{files}", String(files.length))
                 .replace("{rows}", String(scan.totalRows))
                 .replace("{size}", humanSize(scan.totalSize)) }}
            </p>
            <p v-if="scan.ignored">{{ t("workbench.ignoredFiles").replace("{n}", String(scan.ignored)) }}</p>
            <p v-if="scan.unsupported?.length">
              {{ t("workbench.unsupported").replace("{files}", (scan.unsupported || []).join("、")) }}
            </p>
            <p v-if="scan.truncated" class="text-amber-600">{{ t("workbench.truncated") }}</p>
          </div>
        </div>

        <p v-if="failure" class="rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive">{{ failure }}</p>

        <!-- 文件清单 -->
        <div v-if="files.length" class="overflow-hidden rounded-lg border border-border">
          <table class="w-full text-left text-xs">
            <thead class="bg-muted/40 text-muted-foreground">
              <tr>
                <th class="px-3 py-2 font-medium">{{ t("workbench.colFile") }}</th>
                <th class="px-3 py-2 font-medium">{{ t("workbench.colHeaders") }}</th>
                <th class="px-3 py-2 text-right font-medium">{{ t("workbench.colRows") }}</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="f in files.slice(0, 100)"
                :key="f.path"
                class="border-t border-border/60"
                :class="f.error ? 'bg-destructive/5' : ''"
              >
                <td class="max-w-[220px] truncate px-3 py-1.5 font-mono">{{ f.name }}</td>
                <td class="max-w-[280px] truncate px-3 py-1.5 text-muted-foreground">
                  <span v-if="f.error" class="text-destructive">{{ f.error }}</span>
                  <span v-else>{{ f.headers.join(" · ") || "—" }}</span>
                </td>
                <td class="px-3 py-1.5 text-right tabular-nums">{{ f.error ? "—" : f.rows }}</td>
              </tr>
            </tbody>
          </table>
          <p v-if="files.length > 100" class="border-t border-border/60 px-3 py-1.5 text-[11px] text-muted-foreground">
            {{ t("workbench.moreRows").replace("{n}", String(files.length - 100)) }}
          </p>
        </div>

        <!-- 合并 -->
        <div v-if="files.length" class="rounded-lg border border-border bg-card p-4">
          <h2 class="flex items-center gap-2 text-sm font-semibold">
            <FileSpreadsheet :size="14" class="text-muted-foreground" />
            {{ t("workbench.mergeTitle") }}
          </h2>
          <p class="mt-1 text-xs leading-relaxed text-muted-foreground">{{ t("workbench.mergeHint") }}</p>

          <label class="mt-3 flex items-center gap-2">
            <span class="shrink-0 text-xs text-muted-foreground">{{ t("workbench.dedup") }}</span>
            <input
              v-model="dedupText"
              class="min-w-0 flex-1 rounded-md border border-input bg-background px-2 py-1 text-xs"
              :placeholder="t('workbench.dedupPlaceholder')"
            />
          </label>

          <div class="mt-3 flex gap-2">
            <button
              type="button"
              class="inline-flex items-center gap-2 rounded-md border border-input px-3 py-1.5 text-sm hover:bg-accent disabled:opacity-50"
              :disabled="busyPreview || busyRun"
              @click="doPreview"
            >
              <Loader2 v-if="busyPreview" :size="14" class="animate-spin" />
              {{ t("workbench.preview") }}
            </button>
            <button
              type="button"
              class="inline-flex items-center gap-2 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground disabled:opacity-50"
              :disabled="busyPreview || busyRun"
              @click="doMerge"
            >
              <Loader2 v-if="busyRun" :size="14" class="animate-spin" />
              {{ t("workbench.run") }}
            </button>
          </div>

          <!-- 预览结果 -->
          <div v-if="preview" class="mt-3 space-y-1 rounded-md bg-muted/40 p-3 text-xs">
            <p class="flex items-center gap-1.5 font-medium">
              <AlertTriangle :size="12" class="text-amber-600" />{{ t("workbench.previewTitle") }}
            </p>
            <p class="text-muted-foreground">
              {{ t("workbench.previewBody")
                  .replace("{files}", String(preview.files))
                  .replace("{rowsIn}", String(preview.rowsIn))
                  .replace("{rowsOut}", String(preview.rowsOut))
                  .replace("{dup}", String(preview.duplicates)) }}
            </p>
            <p v-if="preview.columns.length" class="text-muted-foreground">
              {{ t("workbench.previewCols").replace("{cols}", preview.columns.join(" · ")) }}
            </p>
            <ul v-if="preview.warnings?.length" class="list-inside list-disc text-amber-600">
              <li v-for="w in preview.warnings.slice(0, 5)" :key="w">{{ w }}</li>
            </ul>
            <ul v-if="preview.skipped?.length" class="list-inside list-disc text-destructive">
              <li v-for="s in preview.skipped.slice(0, 5)" :key="s.name">{{ s.name }}：{{ s.reason }}</li>
            </ul>
            <p class="text-[11px] text-muted-foreground">{{ t("workbench.previewNoWrite") }}</p>
          </div>

          <!-- 执行结果 -->
          <div v-if="done" class="mt-3 space-y-1 rounded-md bg-emerald-500/10 p-3 text-xs">
            <p class="flex items-center gap-1.5 font-medium text-emerald-700">
              <CheckCircle2 :size="12" />{{ t("workbench.doneTitle") }}
            </p>
            <p class="text-muted-foreground">
              {{ t("workbench.doneBody")
                  .replace("{rows}", String(done.rowsOut))
                  .replace("{ms}", String(done.elapsedMs)) }}
            </p>
            <p class="select-text break-all font-mono text-[11px]">{{ done.outputPath }}</p>
          </div>
        </div>

        <!-- 清洗 -->
        <div v-if="files.length" class="rounded-lg border border-border bg-card p-4">
          <h2 class="flex items-center gap-2 text-sm font-semibold">
            <Wand2 :size="14" class="text-muted-foreground" />
            {{ t("workbench.cleanTitle") }}
          </h2>
          <p class="mt-1 text-xs leading-relaxed text-muted-foreground">{{ t("workbench.cleanHint") }}</p>

          <div class="mt-3 grid gap-2 sm:grid-cols-2">
            <label class="flex items-center gap-2 text-xs">
              <input v-model="cleanDropBlank" type="checkbox" class="accent-primary" />
              {{ t("workbench.cleanBlank") }}
            </label>
            <label class="flex items-center gap-2 text-xs">
              <input v-model="cleanTrim" type="checkbox" class="accent-primary" />
              {{ t("workbench.cleanTrim") }}
            </label>
            <label class="flex items-center gap-2 text-xs">
              <input v-model="cleanDropDup" type="checkbox" class="accent-primary" />
              {{ t("workbench.cleanDup") }}
            </label>
            <label class="flex items-center gap-2 text-xs">
              <input v-model="cleanNumbers" type="checkbox" class="accent-primary" />
              {{ t("workbench.cleanNumbers") }}
            </label>
            <label class="flex items-center gap-2 text-xs sm:col-span-2">
              <input v-model="cleanDates" type="checkbox" class="accent-primary" />
              {{ t("workbench.cleanDates") }}
              <select
                v-model="cleanDateFmt"
                class="ml-1 rounded-md border border-input bg-background px-1.5 py-0.5 text-xs"
                :disabled="!cleanDates"
              >
                <option value="YYYY-MM-DD">YYYY-MM-DD</option>
                <option value="YYYY/MM/DD">YYYY/MM/DD</option>
              </select>
            </label>
          </div>

          <label v-if="cleanDropDup" class="mt-2 flex items-center gap-2">
            <span class="shrink-0 text-xs text-muted-foreground">{{ t("workbench.dedup") }}</span>
            <input
              v-model="cleanDedupText"
              class="min-w-0 flex-1 rounded-md border border-input bg-background px-2 py-1 text-xs"
              :placeholder="t('workbench.cleanDedupPlaceholder')"
            />
          </label>

          <div class="mt-3 flex gap-2">
            <button
              type="button"
              class="inline-flex items-center gap-2 rounded-md border border-input px-3 py-1.5 text-sm hover:bg-accent disabled:opacity-50"
              :disabled="busyCleanPreview || busyCleanRun"
              @click="doCleanPreview"
            >
              <Loader2 v-if="busyCleanPreview" :size="14" class="animate-spin" />
              {{ t("workbench.cleanPreviewBtn") }}
            </button>
            <button
              type="button"
              class="inline-flex items-center gap-2 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground disabled:opacity-50"
              :disabled="busyCleanPreview || busyCleanRun"
              @click="doCleanRun"
            >
              <Loader2 v-if="busyCleanRun" :size="14" class="animate-spin" />
              {{ t("workbench.cleanRunBtn") }}
            </button>
          </div>

          <div v-if="cleanPreview" class="mt-3 space-y-1 rounded-md bg-muted/40 p-3 text-xs">
            <p class="flex items-center gap-1.5 font-medium">
              <AlertTriangle :size="12" class="text-amber-600" />{{ t("workbench.cleanPreviewTitle") }}
            </p>
            <p class="text-muted-foreground">
              {{ t("workbench.cleanPreviewBody")
                  .replace("{files}", String(cleanPreview.files))
                  .replace("{rowsIn}", String(cleanPreview.rowsIn))
                  .replace("{rowsOut}", String(cleanPreview.rowsOut))
                  .replace("{blank}", String(cleanPreview.blankRows))
                  .replace("{dup}", String(cleanPreview.duplicateRows)) }}
            </p>
            <p class="text-muted-foreground">
              {{ t("workbench.cleanFixes")
                  .replace("{trim}", String(cleanPreview.trimmedCells))
                  .replace("{dates}", String(cleanPreview.dateFixed))
                  .replace("{nums}", String(cleanPreview.numberFixed)) }}
            </p>
            <ul v-if="cleanPreview.warnings?.length" class="list-inside list-disc text-amber-600">
              <li v-for="w in cleanPreview.warnings.slice(0, 5)" :key="w">{{ w }}</li>
            </ul>
            <p class="text-[11px] text-muted-foreground">{{ t("workbench.previewNoWrite") }}</p>
          </div>

          <div v-if="cleanDone" class="mt-3 space-y-1 rounded-md bg-emerald-500/10 p-3 text-xs">
            <p class="flex items-center gap-1.5 font-medium text-emerald-700">
              <CheckCircle2 :size="12" />{{ t("workbench.cleanDoneTitle") }}
            </p>
            <p class="text-muted-foreground">
              {{ t("workbench.cleanDoneBody")
                  .replace("{files}", String(cleanDone.files))
                  .replace("{rows}", String(cleanDone.rowsOut))
                  .replace("{ms}", String(cleanDone.elapsedMs)) }}
            </p>
            <p class="select-text break-all font-mono text-[11px]">{{ cleanDone.outputDir }}</p>
          </div>
        </div>

        <!-- 场景 -->
        <div class="space-y-3">
          <h2 class="text-sm font-semibold">{{ t("workbench.scenarios") }}</h2>
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
      </div>
    </section>

    <!-- 右：AI -->
    <aside class="w-[360px] shrink-0 border-l border-border">
      <AiChatPanel ref="panel" :dir="dir" />
    </aside>
  </div>
</template>
