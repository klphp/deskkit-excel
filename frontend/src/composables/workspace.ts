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

/**
 * 当前功能。空字符串 = 普通对话。
 *
 * 现在默认就是表格：这一版是单功能产品，让用户在「表格 / 视频 / 文档」里选
 * 只是把还没做的东西摆出来让人犹豫。等真有第二个能力包了，再谈怎么切换 ——
 * 那时应该是「装了哪包就用哪包」，而不是让用户承担选择成本。
 */
const feature = ref("excel");
/** 用户选的东西：可能是 1 个文件，也可能是多个文件 / 一个文件夹。 */
const paths = ref<string[]>([]);
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

// 不做类型探查：用户可能选表格，也可能选视频、PDF、一整个目录。
// 替他判断「这是什么文件、有几张表」纯属多余，判断错了反而误导，
// 而且我们根本不知道他接下来要说哪件事。选了什么交给 AI 去看。

async function loadPacks() {
  try {
    packs.value = (await Tools.Packs()) ?? [];
  } catch (err) {
    failure.value = String(err);
  }
}

/** 只选文件。 */
async function pickFiles(title: string) {
  busy.value = true;
  try {
    const picked = (await Tools.PickFiles(title)) ?? [];
    if (picked.length === 0) return;
    paths.value = picked;
  } catch (err) {
    failure.value = String(err);
  } finally {
    busy.value = false;
  }
}

/** 只选文件夹。 */
async function pickDirs(title: string) {
  busy.value = true;
  try {
    const picked = (await Tools.PickDirs(title)) ?? [];
    if (picked.length === 0) return;
    paths.value = picked;
  } catch (err) {
    failure.value = String(err);
  } finally {
    busy.value = false;
  }
}

async function pickPaths(title: string) {
  failure.value = "";
  busy.value = true;
  try {
    const picked = (await Tools.PickPaths(title)) ?? [];
    if (picked.length === 0) return;
    paths.value = picked;
  } catch (err) {
    failure.value = String(err);
    paths.value = [];
  } finally {
    busy.value = false;
  }
}

function clearPaths() {
  paths.value = [];
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
    busy,
    failure,
    usage,
    loadPacks,
    pickPaths,
    pickFiles,
    pickDirs,
    clearPaths,
    setFeature,
  };
}
