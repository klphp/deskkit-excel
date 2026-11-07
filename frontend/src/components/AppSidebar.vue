<script setup lang="ts">
/**
 * 侧边栏。桌面应用的常见骨架：左侧固定导航，右侧内容区自己滚动。
 *
 * 导航项是模板占位，实际项目按需替换。
 * 底部不放版本号 —— 应用名和版本统一在标题栏显示，只留一处。
 */
import { Home, Settings, FileText, Info } from "@lucide/vue";

defineProps<{ active: string }>();
import { useI18n } from "../i18n";

const { t } = useI18n();

const emit = defineEmits<{ (e: "select", id: string): void }>();

const items = [
  // label 存 key，模板里再过 t() —— 语言切换时能直接响应，
  // 不需要重建数组。
  { id: "home", labelKey: "nav.home", icon: Home },
  { id: "docs", labelKey: "nav.docs", icon: FileText },
  { id: "settings", labelKey: "nav.settings", icon: Settings },
  { id: "about", labelKey: "nav.about", icon: Info },
];
</script>

<template>
  <aside
    class="flex w-[var(--sidebar-width)] shrink-0 flex-col border-r border-border bg-muted/30"
  >
    <nav class="flex-1 space-y-1 p-2">
      <button
        v-for="item in items"
        :key="item.id"
        type="button"
        class="flex w-full items-center gap-2.5 rounded-md px-3 py-2 text-sm transition-colors"
        :class="
          active === item.id
            ? 'bg-accent font-medium text-accent-foreground'
            : 'text-muted-foreground hover:bg-accent/60 hover:text-accent-foreground'
        "
        @click="emit('select', item.id)"
      >
        <component :is="item.icon" :size="15" :stroke-width="2" />
        <span>{{ t(item.labelKey) }}</span>
      </button>
    </nav>
  </aside>
</template>
