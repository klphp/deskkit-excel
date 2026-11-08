<script setup lang="ts">
/**
 * 设置视图：外观、会话区字号、界面语言。
 *
 * 刻意保持窄 —— 设置项越多，真正要改的东西越难找。
 * 检查更新在「关于」页，因为它和版本号是同一件事。
 */
import { computed } from "vue";
import { Languages } from "@lucide/vue";
import { setTheme, useTheme, type ThemeMode } from "../composables/useTheme";
import { useChatFont } from "../composables/useChatFont";
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

defineProps<{ info: AppInfo | null }>();

const { mode } = useTheme();
const { size: chatFont, setChatFont, sizes: fontSizes, labels: fontLabels } = useChatFont();
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

</script>

<template>
  <div class="mx-auto max-w-md space-y-4 p-6">
    <!-- ── 外观 ── -->
    <div class="rounded-lg border border-border bg-card p-6">
      <h2 class="mb-3 text-sm font-semibold">{{ t("settings.appearance") }}</h2>
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

    <!-- ── 会话区字号 ──
         只影响对话正文，界面其它部分（按钮、标签）不跟着放大：
         那些放大只会挤占空间，真正需要放大的是要读的内容。 -->
    <div class="rounded-lg border border-border bg-card p-6">
      <h2 class="mb-3 text-sm font-semibold">{{ t("settings.font.title") }}</h2>
      <div class="flex items-center gap-2">
        <button
          v-for="s in fontSizes"
          :key="s"
          type="button"
          class="h-9 min-w-[64px] rounded-md border text-[13px] transition-colors"
          :class="
            chatFont === s
              ? 'border-primary bg-primary text-primary-foreground'
              : 'border-border text-muted-foreground hover:bg-accent hover:text-accent-foreground'
          "
          @click="setChatFont(s)"
        >
          {{ t(fontLabels[s]) }}
        </button>
      </div>
    </div>

    <!-- ── 语言 ──
         只有 i18n 启用、且配了两种以上语言时才出现。默认配置下这一块
         完全不存在，界面和没有多语言时一样。 -->
    <div v-if="showLanguage" class="rounded-lg border border-border bg-card p-6">
      <h2 class="mb-3 text-sm font-semibold">{{ t("settings.language") }}</h2>
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

  </div>
</template>
