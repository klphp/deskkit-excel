<script setup lang="ts">
/**
 * 设置视图。只放两件事：主题、更新。
 *
 * 刻意保持窄：设置项越多，真正要改的东西越难找。开发辅助信息（本地接口
 * 地址、怎么加 shadcn 组件）不属于用户设置，已经挪走。
 */
import { computed, onMounted, ref } from "vue";
import { Check, Loader2, Download, AlertCircle, CheckCircle2, Languages } from "@lucide/vue";
import { setTheme, useTheme, type ThemeMode } from "../composables/useTheme";
import { useI18n } from "../i18n";

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

const props = defineProps<{ info: AppInfo | null }>();

const { mode } = useTheme();
const { t, locale, enabled: i18nEnabled, available, setLocale } = useI18n();

// ── 语言 ──
// 只在 i18n 启用且配了多种语言时才显示这一块
const showLanguage = computed(
  () => i18nEnabled.value && (available.value?.length ?? 0) > 1,
);

/** 语言代码 → 显示名。加新语言在这里补一行，没写的就直接显示代码。 */
const localeNames: Record<string, string> = {
  "zh-CN": "简体中文",
  "en-US": "English",
  "ja-JP": "日本語",
  "zh-TW": "繁體中文",
};

// labelKey 而不是 label：语言切换时标签要跟着变，存 key 才能响应式
const themeOptions: { value: ThemeMode; labelKey: string }[] = [
  { value: "system", labelKey: "settings.theme.system" },
  { value: "light", labelKey: "settings.theme.light" },
  { value: "dark", labelKey: "settings.theme.dark" },
];

// ── 更新 ──
type UpdateResult = {
  available: boolean;
  version: string;
  notes: string;
  releaseUrl: string;
  autoInstall: boolean;
};

/** 更新源是否在 config/app.yaml 里配好了。
 *
 *  未配置时不隐藏这一块 —— 按钮该看见就看见，点了给明确提示，比整块
 *  消失让人怀疑「这功能到底有没有」要好。 */
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
  return mod.UpdateService;
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
    state.value = "error";
    errorMsg.value =
      "未配置更新源。在 config/app.yaml 的 update 段填上 repository（或 manifestUrl），并把 enabled 设为 true。";
    return;
  }

  checking.value = true;
  errorMsg.value = "";
  try {
    const svc = await getUpdateService();
    const res: UpdateResult = await svc.Check();
    result.value = res;
    state.value = res.available ? "available" : "uptodate";
  } catch (e: any) {
    // Go 侧已经把网络错误统一成中文提示（「网络连接失败，请检查网络后重试」），
    // 其它错误原样显示 —— 都比吞掉强
    state.value = "error";
    errorMsg.value = String(e?.message ?? e);
  } finally {
    checking.value = false;
  }
}

async function installUpdate() {
  if (installing.value) return;
  installing.value = true;
  errorMsg.value = "";
  try {
    await (await getUpdateService()).Install();
    // Windows 上 Install 成功后应用会重启，走不到这里；
    // 其它平台是打开了下载页，停留在这个界面即可。
  } catch (e: any) {
    state.value = "error";
    errorMsg.value = String(e?.message ?? e);
  } finally {
    installing.value = false;
  }
}
</script>

<template>
  <div class="mx-auto max-w-md space-y-4 p-6">
    <!-- ── 外观 ── -->
    <div class="rounded-lg border border-border bg-card p-6">
      <h2 class="mb-1 text-sm font-semibold">{{ t("settings.appearance") }}</h2>
      <p class="mb-3 text-xs text-muted-foreground">
        {{ t("settings.appearanceHint") }}
      </p>
      <div class="flex gap-2">
        <button
          v-for="opt in themeOptions"
          :key="opt.value"
          type="button"
          class="flex-1 rounded-md border px-3 py-2 text-xs transition-colors"
          :class="
            mode === opt.value
              ? 'border-primary bg-primary text-primary-foreground'
              : 'border-border text-muted-foreground hover:bg-accent hover:text-accent-foreground'
          "
          @click="setTheme(opt.value)"
        >
          {{ t(opt.labelKey) }}
        </button>
      </div>
    </div>

    <!-- ── 语言 ──
         只有 i18n 启用、且配了两种以上语言时才出现。默认配置下这一块
         完全不存在，界面和没有多语言时一样。 -->
    <div v-if="showLanguage" class="rounded-lg border border-border bg-card p-6">
      <h2 class="mb-1 text-sm font-semibold">{{ t("settings.language") }}</h2>
      <p class="mb-3 text-xs text-muted-foreground">{{ t("settings.languageHint") }}</p>
      <div class="flex flex-wrap gap-2">
        <button
          v-for="l in available"
          :key="l"
          type="button"
          class="flex items-center gap-1.5 rounded-md border px-3 py-2 text-xs transition-colors"
          :class="
            locale === l
              ? 'border-primary bg-primary text-primary-foreground'
              : 'border-border text-muted-foreground hover:bg-accent hover:text-accent-foreground'
          "
          @click="setLocale(l)"
        >
          <Languages v-if="locale === l" :size="12" :stroke-width="1.75" />
          {{ localeNames[l] ?? l }}
        </button>
      </div>
    </div>

    <!-- ── 更新 ── -->
    <div class="rounded-lg border border-border bg-card p-6">
      <div class="mb-3 flex items-baseline justify-between">
        <h2 class="text-sm font-semibold">{{ t("settings.update") }}</h2>
        <span class="font-mono text-xs text-muted-foreground">v{{ props.info?.version ?? "—" }}</span>
      </div>

      <!-- 发现新版本 -->
      <div v-if="state === 'available' && result" class="space-y-3">
        <div class="flex items-center gap-2 text-xs">
          <Download :size="14" class="text-primary" />
          <span>
            {{ t("settings.updateAvailable") }}
            <strong class="font-mono">v{{ result.version }}</strong>
          </span>
        </div>
        <pre
          v-if="result.notes"
          class="max-h-40 overflow-y-auto whitespace-pre-wrap rounded-md bg-muted p-3 font-sans text-xs text-muted-foreground"
          >{{ result.notes }}</pre
        >
        <button
          type="button"
          class="flex w-full items-center justify-center gap-2 rounded-md bg-primary px-3 py-2 text-xs font-medium text-primary-foreground transition-opacity hover:opacity-90 disabled:opacity-50"
          :disabled="installing"
          @click="installUpdate"
        >
          <Loader2 v-if="installing" :size="13" class="animate-spin" />
          <Download v-else :size="13" />
          {{
            installing
              ? t("settings.processing")
              : result.autoInstall
                ? t("settings.downloadAndInstall")
                : t("settings.goToDownload")
          }}
        </button>
        <p v-if="!result.autoInstall" class="text-xs text-muted-foreground">
          {{ t("settings.downloadHint") }}
        </p>
      </div>

      <!-- 已是最新 -->
      <div
        v-else-if="state === 'uptodate'"
        class="flex items-center gap-2 text-xs text-muted-foreground"
      >
        <CheckCircle2 :size="14" class="text-primary" />
        <span>{{ t("settings.upToDate") }}</span>
      </div>

      <!-- 出错（含网络不通） -->
      <div
        v-else-if="state === 'error'"
        class="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-3 text-xs"
      >
        <AlertCircle :size="14" class="mt-0.5 shrink-0 text-destructive" />
        <span class="select-text">{{ errorMsg || t("settings.checkFailed") }}</span>
      </div>

      <p v-else class="mb-3 text-xs text-muted-foreground">
        {{
          updateConfigured
            ? t("settings.updateIdle")
            : t("settings.updateNotConfigured")
        }}
      </p>

      <button
        type="button"
        class="mt-3 flex w-full items-center justify-center gap-2 rounded-md border border-border px-3 py-2 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground disabled:opacity-50"
        :disabled="checking"
        @click="checkUpdate"
      >
        <Loader2 v-if="checking" :size="13" class="animate-spin" />
        <Check v-else :size="13" />
        {{ checking ? t("settings.checking") : t("settings.checkUpdate") }}
      </button>
    </div>

  </div>
</template>
