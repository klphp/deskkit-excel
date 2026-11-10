<script setup lang="ts">
/**
 * 关于页：标志、名字、版本，右上角检查更新。
 *
 * 刻意不列平台 / 架构 / 框架 / 样式 —— 用户不需要知道这个应用是用什么写的，
 * 那些只对开发者有意义，摆在界面上既占地方也显得可疑。
 */
import { computed, onMounted, ref } from "vue";
import { Loader2, Download, AlertCircle, CheckCircle2, RefreshCw, ExternalLink } from "@lucide/vue";
import { useI18n } from "../i18n";
import logoURL from "../assets/logo.svg";

type AppInfo = {
  name: string;
  version: string;
  platform: string;
  arch: string;
  serverURL: string;
  /** 标题栏元素显隐，来自 config/windows.yaml 主窗口的配置 */
  showTitle: boolean;
  showSettingsButton: boolean;
  i18n: {
    enabled: boolean;
    locale: string;
    fallback: string;
    /** Go 的 slice 映射到 TS 是可为 null 的 */
    locales: string[] | null;
  };
};

type UpdateResult = {
  available: boolean;
  version: string;
  notes: string;
  releaseUrl: string;
  autoInstall: boolean;
  /** macOS 上给出的是 .dmg 直链：点了直接下载，不走发布页 */
  downloadUrl?: string;
};

const props = defineProps<{ info: AppInfo | null }>();
const { t } = useI18n();

/** 更新源是否在 config/app.yaml 里配好了。没配也能点，点了给一句明确提示。 */
const updateConfigured = ref(false);

// 按钮文案要跟着「点了会发生什么」走：
//   Windows 会自动装 → "Download and install"
//   macOS 拿到的是 .dmg 直链 → "Download .dmg"，点了浏览器就开始下
//   只有拿不到直链时才退回发布页 → "Open download page"
// （踩过：macOS 已经给出 dmg 直链，按钮却还写着发布页，用户以为会跳网页。）
const installLabel = computed(() => {
  if (result.value?.autoInstall) return t("about.downloadAndInstall");
  if ((result.value?.downloadUrl ?? "").toLowerCase().endsWith(".dmg")) return t("about.downloadDMG");
  return t("about.goToDownload");
});
/** 手动下载页：检查失败时给用户的出口 */
const releasePage = ref("");
const checking = ref(false);
const installing = ref(false);
/** idle | uptodate | available | error */
const state = ref<"idle" | "uptodate" | "available" | "error">("idle");
const result = ref<UpdateResult | null>(null);
const errorMsg = ref("");

/**
 * 自更新服务的方法。
 *
 * ★ 这里必须用模块级函数（`export function Enabled()`），不能用 `new UpdateService()`：
 * Wails 生成的绑定是一个个具名函数，没有类。写成 `new ...` 会在构造时抛
 * TypeError，而外面那句 catch 会把它吞掉，界面就永远显示「没配置更新源」——
 * 后端日志明明写着「自更新已启用」。这个 bug 就是这么来的。
 */
let updateSvc: typeof import("../../bindings/github.com/klphp/deskkit-excel/internal/services/updateservice") | null = null;

async function loadUpdateService() {
  if (!updateSvc) {
    updateSvc = await import(
      "../../bindings/github.com/klphp/deskkit-excel/internal/services/updateservice"
    );
  }
  return updateSvc;
}

onMounted(async () => {
  try {
    const svc = await loadUpdateService();
    updateConfigured.value = await svc.Enabled();
    releasePage.value = (await svc.ReleasePage?.()) || "";
  } catch (e) {
    // 不再静默：绑定层出问题时，至少控制台能看见，而不是让用户对着
    // 「没配置更新源」猜哪里错了。
    console.warn("[about] 读取自更新状态失败", e);
  }
});

/** 把后端给的短代码翻成一句人话；未知错误也绝不把原始报文甩给用户。 */
function friendlyError(e: unknown): string {
  const raw = String((e as any)?.message ?? e ?? "");
  if (raw.includes("rate_limited")) return t("about.updateRateLimited");
  if (raw.includes("no_release")) return t("about.updateNoRelease");
  if (raw.includes("network")) return t("about.updateNetwork");
  return t("about.updateCheckFailed");
}

async function checkUpdate() {
  if (checking.value) return;
  if (!updateConfigured.value) {
    // 对用户只说一句。怎么改配置是开发者的事（config/app.yaml 的 update 段），
    // 写在界面上只是噪音。
    state.value = "error";
    errorMsg.value = t("about.updateNotConfigured");
    return;
  }
  checking.value = true;
  errorMsg.value = "";
  try {
    const res = await (await loadUpdateService()).Check();
    if (!res) {
      // 后端没有返回结果（例如更新未启用）：等价于"没有新版本"
      state.value = "uptodate";
      return;
    }
    result.value = res as UpdateResult;
    state.value = res.available ? "available" : "uptodate";
  } catch (e: any) {
    state.value = "error";
    errorMsg.value = friendlyError(e);
  } finally {
    checking.value = false;
  }
}

async function installUpdate() {
  if (installing.value) return;
  installing.value = true;
  try {
    const svc = await loadUpdateService();
    if (result.value?.autoInstall) {
      await svc.Install();
    } else if (result.value?.releaseUrl) {
      // 不能自动装就打开下载页，不让用户自己去找
      window.open(result.value.releaseUrl, "_blank");
    }
  } catch (e: any) {
    state.value = "error";
    errorMsg.value = friendlyError(e);
  } finally {
    installing.value = false;
  }
}
</script>

<template>
  <div class="mx-auto max-w-md p-6">
    <div class="relative rounded-lg border border-border bg-card px-6 py-7 text-center">
      <!-- 圆角交给 SVG 自己画，外面不再套色块 -->
      <img :src="logoURL" alt="" class="mx-auto mb-3 h-14 w-14" />
      <h2 class="text-base font-semibold">{{ props.info?.name ?? t("app.name") }}</h2>
      <p class="mt-1 font-mono text-xs text-muted-foreground">
        v{{ props.info?.version ?? "0.1.0" }}
      </p>

      <!-- 检查更新：整宽按钮，放在版本号下面。不做成右上角小图标 ——
           它不是高频操作，挤在 logo 旁边只会让卡片看起来不安分。 -->
      <button
        type="button"
        class="mt-4 flex w-full items-center justify-center gap-2 rounded-md border border-border px-3 py-2 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground disabled:opacity-50"
        :disabled="checking"
        @click="checkUpdate"
      >
        <Loader2 v-if="checking" :size="13" class="animate-spin" />
        <RefreshCw v-else :size="13" />
        {{ checking ? t("about.checking") : t("about.checkUpdate") }}
      </button>

      <!-- 有新版本 -->
      <div
        v-if="state === 'available' && result"
        class="mt-4 space-y-2 border-t border-border pt-4 text-left"
      >
        <p class="flex items-center gap-1.5 text-[13px]">
          <Download :size="13" class="text-primary" />
          <span>
            {{ t("about.updateAvailable") }}
            <strong class="font-mono">v{{ result.version }}</strong>
          </span>
        </p>
        <pre
          v-if="result.notes"
          class="max-h-32 overflow-y-auto whitespace-pre-wrap rounded-md bg-muted p-2.5 font-sans text-[12px] text-muted-foreground"
          >{{ result.notes }}</pre
        >
        <button
          type="button"
          class="flex w-full items-center justify-center gap-2 rounded-md bg-primary px-3 py-2 text-[13px] font-medium text-primary-foreground transition-opacity hover:opacity-90 disabled:opacity-50"
          :disabled="installing"
          @click="installUpdate"
        >
          <Loader2 v-if="installing" :size="13" class="animate-spin" />
          <Download v-else :size="13" />
          {{ installLabel }}
        </button>
      </div>

      <!-- 已是最新 -->
      <p
        v-else-if="state === 'uptodate'"
        class="mt-4 flex items-center justify-center gap-1.5 border-t border-border pt-4 text-[13px] text-muted-foreground"
      >
        <CheckCircle2 :size="13" class="text-primary" />
        {{ t("about.upToDate") }}
      </p>

      <!-- 出错 -->
      <p
        v-else-if="state === 'error'"
        class="mt-4 flex items-start gap-1.5 border-t border-border pt-4 text-left text-[13px] text-destructive"
      >
        <AlertCircle :size="13" class="mt-0.5 shrink-0" />
        <span class="select-text">{{ errorMsg || t("about.checkFailed") }}</span>
      </p>

      <!-- 失败时给一个手动出口：只说「失败」用户不知道下一步做什么 -->
      <a
        v-if="state === 'error' && releasePage"
        :href="releasePage"
        target="_blank"
        rel="noopener"
        class="mt-2 inline-flex items-center gap-1 text-[12px] text-primary hover:underline"
      >
        {{ t("about.openReleases") }}
        <ExternalLink :size="11" />
      </a>
    </div>
  </div>
</template>
