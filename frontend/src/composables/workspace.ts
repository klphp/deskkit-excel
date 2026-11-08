/**
 * 工作区状态（选哪批文件、输出到哪）。
 *
 * 抽成 composable 是因为它已经不属于「某个页面的顶部栏」了 —— 现在
 * 整窗就是一个对话，文件选择变成输入框旁的附件按钮，状态得能被对话区
 * 和输入区同时读到。
 *
 * 状态只有一份（模块级 ref），多个组件读同一份，不会各自算一遍扫描结果。
 */
import { computed, ref } from "vue";
import * as Tools from "../../bindings/github.com/klphp/excel-batch-tool/internal/services/toolservice";
import type { ScanResult } from "../../bindings/github.com/klphp/excel-batch-tool/internal/tools/excel/models";

const dir = ref("");
const outDir = ref("");
const scan = ref<ScanResult | null>(null);
const busyScan = ref(false);
const failure = ref("");

/** 可用文件（读不了的不算进统计）。 */
const usable = computed(() => (scan.value?.files ?? []).filter((f) => !f.error));
const broken = computed(() => (scan.value?.files ?? []).filter((f) => f.error));
const totalRows = computed(() => usable.value.reduce((sum, f) => sum + f.rows, 0));

/** 文件夹名：路径太长，对话里只显示最后一段。 */
const dirName = computed(() => {
  if (!dir.value) return "";
  const parts = dir.value.split("/").filter(Boolean);
  return parts[parts.length - 1] ?? dir.value;
});

/** 列名并集：让模型（和用户）一眼看到这批表有哪些列。 */
const columns = computed(() => {
  const seen = new Set<string>();
  for (const f of usable.value) for (const h of f.headers ?? []) if (h) seen.add(h);
  return [...seen].slice(0, 30);
});

/** 给模型的一句话现状。 */
const summary = computed(() => {
  if (!dir.value || !scan.value) return "";
  if (usable.value.length === 0) return "目录里没有可读的表格";
  return `${usable.value.length} 个文件 · ${totalRows.value} 行`;
});

/** 输出目录：没单独选就是输入目录下的 deskkit-out。 */
const effectiveOutDir = computed(() => outDir.value || (dir.value ? `${dir.value}/deskkit-out` : ""));

async function rescan() {
  if (!dir.value) return;
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

async function pickInput(title: string) {
  failure.value = "";
  try {
    const picked = await Tools.PickFolder(title);
    if (!picked) return;
    dir.value = picked;
    outDir.value = "";
    await rescan();
  } catch (err) {
    failure.value = String(err);
  }
}

async function pickOutput(title: string) {
  try {
    const picked = await Tools.PickFolder(title);
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

function clearInput() {
  dir.value = "";
  outDir.value = "";
  scan.value = null;
  failure.value = "";
}

export function useWorkspace() {
  return {
    dir,
    outDir,
    effectiveOutDir,
    dirName,
    scan,
    usable,
    broken,
    totalRows,
    columns,
    summary,
    busyScan,
    failure,
    rescan,
    pickInput,
    pickOutput,
    useSample,
    clearInput,
  };
}
