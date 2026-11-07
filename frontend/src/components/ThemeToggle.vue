<script setup lang="ts">
/**
 * 三态循环的亮暗切换按钮。逻辑在 composables/useTheme.ts，这里只画按钮。
 *
 * 初始化（读 localStorage、挂系统主题监听）不在这里做 —— 这个按钮可能
 * 不出现在某个窗口里，但主题仍然要正确初始化。initTheme() 统一在 App 挂载时调。
 */
import { computed } from "vue";
import { Sun, Moon, Monitor } from "@lucide/vue";
import { cycleTheme, useTheme } from "../composables/useTheme";
import { useI18n } from "../i18n";

const { mode } = useTheme();
const { t } = useI18n();

const label = computed(
  () =>
    ({
      system: t("settings.theme.system"),
      light: t("settings.theme.light"),
      dark: t("settings.theme.dark"),
    })[mode.value],
);
</script>

<template>
  <button
    type="button"
    class="grid h-8 w-8 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
    :title="label"
    :aria-label="label"
    @click="cycleTheme"
  >
    <Monitor v-if="mode === 'system'" :size="15" :stroke-width="1.75" />
    <Sun v-else-if="mode === 'light'" :size="15" :stroke-width="1.75" />
    <Moon v-else :size="15" :stroke-width="1.75" />
  </button>
</template>
