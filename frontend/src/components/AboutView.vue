<script setup lang="ts">
/**
 * 关于页：标志、名字、版本，右上角检查更新。
 *
 * 刻意不列平台 / 架构 / 框架 / 样式 —— 用户不需要知道这个应用是用什么写的，
 * 那些只对开发者有意义，摆在界面上既占地方也显得可疑。
 */
import { onMounted, ref } from "vue";
import { Loader2, Download, AlertCircle, CheckCircle2, RefreshCw } from "@lucide/vue";
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
};

const props = defineProps<{ info: AppInfo | null }>();
const { t } = useI18n();

/** 更新源是否在 config/app.yaml 里配好了。没配也能点，点了给一句明确提示。 */
const updateConfigured = ref(false);
const checking = ref(false);
const installing = ref(false);
/** idle | uptodate | available | error */
const state = ref<"idle" | "uptodate" | "available" | "error">("idle");
const result = ref<UpdateResult | null>(null);
const errorMsg = ref("");

async function getUpdateService(): Promise<any> {
  const mod: any = await import(
    "../../bindings/github.com/klphp/excel-batch-tool/internal/services"
  );
  return new mod.UpdateService();
}

onMounted(async () => {
  try {
    updateConfigured.value = await (await getUpdateService()).Enabled();
  } catch {
    /* bindings 未生成 */
  }
});

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
    const res: UpdateResult = await (await getUpdateService()).Check();
    result.value = res;
    state.value = res.available ? "available" : "uptodate";
  } catch (e: any) {
    state.value = "error";
    errorMsg.value = String(e?.message ?? e);
  } finally {
    checking.value = false;
  }
}

async function installUpdate() {
  if (installing.value) return;
  installing.value = true;
  try {
    const svc = await getUpdateService();
    if (result.value?.autoInstall) {
      await svc.Install();
    } else if (result.value?.releaseUrl) {
      // 不能自动装就打开下载页，不让用户自己去找
      window.open(result.value.releaseUrl, "_blank");
    }
  } catch (e: any) {
    state.value = "error";
    errorMsg.value = String(e?.message ?? e);
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
          {{ result.autoInstall ? t("about.downloadAndInstall") : t("about.goToDownload") }}
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
    </div>
  </div>
</template>
