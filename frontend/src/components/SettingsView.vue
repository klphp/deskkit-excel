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
import { onMounted, ref } from "vue";
import * as Sessions from "../../bindings/github.com/klphp/excel-batch-tool/internal/services/sessionservice";
import { refreshSessions } from "../composables/sessions";
import * as Prefs from "../../bindings/github.com/klphp/excel-batch-tool/internal/services/prefsservice";
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

// ── 会话历史保留 ──
// 只在 Go 侧存一份（会话库的 meta 表），启动时后台按它清理。
// 0 = 永久。3/7/15 天太短了 —— 半年前的一次排查记录常常还有用，
// 而会话库本身不大，没必要为省几 MB 让用户丢上下文。
const retentionOptions = [30, 90, 180, 365, 0];
const retention = ref(365);

/** 天数 → 界面文字。不显示「天」以外的单位换算解释。 */
function retentionLabel(days: number): string {
  if (days === 0) return t("settings.history.forever");
  if (days < 90) return `${days} ${t("settings.history.days")}`;
  if (days < 365) return `${days / 30} ${t("settings.history.months")}`;
  return `1 ${t("settings.history.year")}`;
}

onMounted(async () => {
  try {
    retention.value = await Sessions.Retention();
  } catch {
    /* 会话库不可用时保持默认 */
  }
  try {
    instructions.value = (await Prefs.GetInstructions()) ?? "";
  } catch {
    /* 偏好文件不可用时留空 */
  }
});

// ── 自定义指令 ──
// 我们不知道用户的东西该存哪、文件该怎么命名，所以留一句话给他自己写。
const instructions = ref("");
const saved = ref(false);

async function saveInstructions() {
  try {
    await Prefs.SetInstructions(instructions.value);
    saved.value = true;
    setTimeout(() => (saved.value = false), 1500);
  } catch {
    /* 保存失败就不亮"已保存" */
  }
}

/** 清空确认：不可恢复，所以要点第二次才真删。 */
const confirmingClear = ref(false);
const clearing = ref(false);

async function doClearAll() {
  clearing.value = true;
  try {
    await Sessions.ClearAll();
    await refreshSessions();
    confirmingClear.value = false;
  } catch {
    /* 失败就留在确认态，用户能看到没成功 */
  } finally {
    clearing.value = false;
  }
}

async function setRetention(days: number) {
  retention.value = days;
  try {
    await Sessions.SetRetention(days);
  } catch {
    /* 失败就保持界面值，下次打开会读回真实值 */
  }
}

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
  <div class="space-y-2 p-3">
    <!-- ── 自定义指令 ──
         放最上面：这是最"属于用户"的一项，别让他往下翻。
         目录选择器我们给不出他真正想要的，一句话可以。 -->
    <div class="rounded-lg border border-border bg-card px-3 py-2.5">
      <div class="mb-1.5 flex items-center justify-between">
        <h2 class="text-[12.5px] font-semibold">{{ t("settings.instructions.title") }}</h2>
        <button
          type="button"
          class="text-[12px] text-muted-foreground transition-colors hover:text-foreground disabled:opacity-50"
          @click="saveInstructions"
        >
          {{ saved ? t("settings.instructions.saved") : t("settings.instructions.save") }}
        </button>
      </div>
      <textarea
        v-model="instructions"
        rows="3"
        class="w-full resize-y rounded-md border border-input bg-background px-2.5 py-1.5 text-[12.5px] leading-relaxed outline-none focus:border-ring"
        :placeholder="t('settings.instructions.ph')"
        maxlength="2000"
        @blur="saveInstructions"
      />
    </div>

    <!-- ── 外观 ── -->
    <div class="rounded-lg border border-border bg-card px-3 py-2.5">
      <h2 class="mb-1.5 text-[12.5px] font-semibold">{{ t("settings.appearance") }}</h2>
      <div class="flex gap-2">
        <button
          v-for="opt in themeOptions"
          :key="opt.value"
          type="button"
          class="h-7 flex-1 rounded-md border text-[12px] transition-colors"
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
    <div class="rounded-lg border border-border bg-card px-3 py-2.5">
      <h2 class="mb-1.5 text-[12.5px] font-semibold">{{ t("settings.font.title") }}</h2>
      <div class="flex items-center gap-2">
        <button
          v-for="s in fontSizes"
          :key="s"
          type="button"
          class="h-7 min-w-[46px] rounded-md border text-[12px] transition-colors"
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

    <!-- ── 会话历史 ──
         按「最后一次使用时间」清理，启动时在后台做，不挡窗口。 -->
    <div class="rounded-lg border border-border bg-card px-3 py-2.5">
      <h2 class="mb-1.5 text-[12.5px] font-semibold">{{ t("settings.history.title") }}</h2>
      <div class="flex flex-wrap items-center gap-1">
        <button
          v-for="d in retentionOptions"
          :key="d"
          type="button"
          class="h-7 min-w-[46px] rounded-md border text-[12px] transition-colors"
          :class="
            retention === d
              ? 'border-primary bg-primary text-primary-foreground'
              : 'border-border text-muted-foreground hover:bg-accent hover:text-accent-foreground'
          "
          @click="setRetention(d)"
        >
          {{ retentionLabel(d) }}
        </button>

      </div>

      <!-- 清空单独一行：五档按钮已经占满一行，挤进去只会换行显得乱。
           点一次问一次，第二次才真删。 -->
      <div class="mt-1.5 flex items-center justify-end gap-1.5 border-t border-border pt-1.5">
        <template v-if="!confirmingClear">
          <button
            type="button"
            class="text-[12px] text-muted-foreground transition-colors hover:text-destructive"
            @click="confirmingClear = true"
          >
            {{ t("settings.history.clear") }}
          </button>
        </template>
        <template v-else>
          <span class="text-[12px] text-destructive">{{ t("settings.history.clearConfirm") }}</span>
          <button
            type="button"
            class="h-6 rounded-md bg-destructive px-2 text-[12px] font-medium text-destructive-foreground disabled:opacity-50"
            :disabled="clearing"
            @click="doClearAll"
          >
            {{ clearing ? t("settings.history.clearing") : t("settings.history.clearYes") }}
          </button>
          <button
            type="button"
            class="h-6 rounded-md border border-border px-2 text-[12px] text-muted-foreground hover:bg-accent"
            @click="confirmingClear = false"
          >
            {{ t("settings.history.clearNo") }}
          </button>
        </template>
      </div>
    </div>

    <!-- ── 语言 ──
         只有 i18n 启用、且配了两种以上语言时才出现。默认配置下这一块
         完全不存在，界面和没有多语言时一样。 -->
    <div v-if="showLanguage" class="rounded-lg border border-border bg-card px-3 py-2.5">
      <h2 class="mb-1.5 text-[12.5px] font-semibold">{{ t("settings.language") }}</h2>
      <div class="flex flex-wrap gap-1">
        <button
          v-for="l in available"
          :key="l"
          type="button"
          class="flex h-7 items-center gap-1 rounded-md border px-2 text-[12px] transition-colors"
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
