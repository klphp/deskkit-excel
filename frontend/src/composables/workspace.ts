/**
 * 工作区状态：当前打开的是哪个文件、用的是哪个功能。
 *
 * 第一版只做单文件（不做批量）：用户选一个表，说一句要干什么，
 * 结果写回同一个目录、同名加时间戳。原因很实在 ——
 * 批量要先回答「哪些文件、按什么顺序、输出到哪」，那是另一套交互和
 * 另一套错误处理；单文件跑顺了再谈批量。
 *
 * 功能选择的默认值是「普通对话」：不选功能时就是个纯 AI 对话，
 * 不假设用户一定要处理文件。
 */
import { computed, ref } from "vue";
import * as Tools from "../../bindings/github.com/klphp/excel-batch-tool/internal/services/toolservice";
import type { PackInfo } from "../../bindings/github.com/klphp/excel-batch-tool/internal/services/models";
import type { SingleInfo } from "../../bindings/github.com/klphp/excel-batch-tool/internal/tools/excel/models";

/** 当前功能。空字符串 = 普通对话。 */
const feature = ref("");
/** 用户选的东西：可能是 1 个文件，也可能是多个文件 / 一个文件夹。 */
const paths = ref<string[]>([]);
const info = ref<SingleInfo | null>(null);
const packs = ref<PackInfo[]>([]);
const busy = ref(false);
/** 当前功能的用法说明（markdown），给模型当提示词。 */
const usage = ref("");
const failure = ref("");

/** 选择摘要：一个就显示名字，多个显示「3 项」。 */
const selectionLabel = computed(() => {
  const list = paths.value;
  if (list.length === 0) return "";
  if (list.length === 1) return list[0].split("/").pop() ?? list[0];
  return `${list.length} 项`;
});

/** 表结构摘要：几张表、多少列多少行。 */
const summary = computed(() => {
  const tables = info.value?.tables ?? [];
  if (tables.length === 0) return "";
  const rows = tables.reduce((n, t) => n + t.rows, 0);
  return `${tables.length} 张表 · ${rows} 行`;
});

/** 列名并集，给模型和界面做参考。 */
const columns = computed(() => {
  const seen = new Set<string>();
  for (const t of info.value?.tables ?? []) for (const c of t.columns ?? []) if (c) seen.add(c);
  return [...seen].slice(0, 30);
});

/** 输出会写到哪（后端算好的，界面照抄，不自己拼路径）。 */
const outputHint = computed(() => info.value?.outputHint ?? "");

async function loadPacks() {
  try {
    packs.value = (await Tools.Packs()) ?? [];
  } catch (err) {
    failure.value = String(err);
  }
}

async function pickPaths(title: string) {
  failure.value = "";
  busy.value = true;
  try {
    const picked = (await Tools.PickPaths(title)) ?? [];
    if (picked.length === 0) return;
    paths.value = picked;
    // 只选了一个文件时顺手读它、显示「几张表几行」，让用户心里有数。
    // 选了文件夹或多个文件就不猜了 —— 结构由 AI 用工具自己去看。
    if (picked.length === 1 && /\.xlsx?$/i.test(picked[0]) === false && /\.xlsm$/i.test(picked[0])) {
      info.value = await Tools.InspectFile(picked[0]);
    } else if (picked.length === 1 && /\.(xlsx|xlsm)$/i.test(picked[0])) {
      info.value = await Tools.InspectFile(picked[0]);
    } else {
      info.value = null;
    }
  } catch (err) {
    failure.value = String(err);
    paths.value = [];
    info.value = null;
  } finally {
    busy.value = false;
  }
}

function clearPaths() {
  paths.value = [];
  info.value = null;
  failure.value = "";
}

/**
 * 切功能。
 *
 * 切过去的同时把那份用法说明取回来：模型据此知道有哪些工具、
 * 每个工具收什么参数，不用靠猜。用法由工具定义自动生成，
 * 和 /api/v1/capabilities/:id/usage 是同一份内容。
 */
async function setFeature(id: string) {
  feature.value = id;
  usage.value = "";
  if (!id) return;
  try {
    usage.value = (await Tools.CapabilityUsage(id)) ?? "";
  } catch (err) {
    failure.value = String(err);
  }
}

export function useWorkspace() {
  return {
    feature,
    packs,
    paths,
    selectionLabel,
    info,
    summary,
    columns,
    outputHint,
    busy,
    failure,
    usage,
    loadPacks,
    pickPaths,
    clearPaths,
    setFeature,
  };
}
