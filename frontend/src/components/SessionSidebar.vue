<script setup lang="ts">
/**
 * 左侧会话栏。
 *
 * 之前砍掉过侧边栏（当时只有三个页面，导航纯占地方）。现在它有意义了：
 * 对话是有历史的，用户需要能新开一轮、回看上一轮。
 *
 * 宽度窄（232px）而不是 280：这一栏是索引，不是主体 —— 主体永远是右边
 * 那次对话。
 */
import { computed, onMounted } from "vue";
import { Plus, Trash2 } from "@lucide/vue";
import { useI18n } from "../i18n";
import { initSessions, useSessions } from "../composables/sessions";

const { t } = useI18n();
const sess = useSessions();

onMounted(() => initSessions());

/** 今天 / 更早 分组，避免一长串时间戳。 */
const groups = computed(() => {
  const now = new Date();
  const startOfToday = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  const today: typeof sess.ordered.value = [];
  const earlier: typeof sess.ordered.value = [];
  for (const s of sess.ordered.value) {
    (s.updatedAt >= startOfToday ? today : earlier).push(s);
  }
  return [
    { label: t("session.today"), items: today },
    { label: t("session.earlier"), items: earlier },
  ].filter((g) => g.items.length > 0);
});

function timeOf(ts: number): string {
  const d = new Date(ts);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

function onNew() {
  sess.create();
}

function onSelect(id: string) {
  sess.select(id);
}

function onRemove(id: string, ev: MouseEvent) {
  ev.stopPropagation();
  sess.remove(id);
}
</script>

<template>
  <aside class="flex w-[232px] shrink-0 flex-col border-r border-border bg-muted/30">
    <div class="p-2.5">
      <button
        type="button"
        class="flex w-full items-center gap-2 rounded-lg border border-border bg-background px-2.5 py-2 text-[14px] font-medium transition-colors hover:bg-accent"
        @click="onNew"
      >
        <Plus :size="14" />
        {{ t("session.new") }}
      </button>
    </div>

    <div class="min-h-0 flex-1 overflow-y-auto px-2 pb-3">
      <p v-if="sess.ordered.value.length === 0" class="px-1.5 py-2 text-[13px] text-muted-foreground">
        {{ t("session.empty") }}
      </p>

      <template v-for="g in groups" :key="g.label">
        <p class="px-1.5 pb-1 pt-2.5 text-[12px] font-medium uppercase tracking-wide text-muted-foreground">
          {{ g.label }}
        </p>
        <div
          v-for="s in g.items"
          :key="s.id"
          class="group mb-0.5 flex items-center gap-2 rounded-md px-2 py-2 text-[14px] transition-colors"
          :class="
            s.id === sess.currentId.value
              ? 'bg-accent text-accent-foreground'
              : 'text-muted-foreground hover:bg-accent/60'
          "
          role="button"
          tabindex="0"
          @click="onSelect(s.id)"
          @keydown.enter="onSelect(s.id)"
        >
          <span class="min-w-0 flex-1 truncate">{{ s.title || t("session.untitled") }}</span>
          <span class="shrink-0 text-[12px] text-muted-foreground group-hover:hidden">{{ timeOf(s.updatedAt) }}</span>
          <button
            type="button"
            class="hidden shrink-0 rounded p-0.5 text-muted-foreground hover:text-destructive group-hover:block"
            :title="t('session.delete')"
            @click="onRemove(s.id, $event)"
          >
            <Trash2 :size="12" />
          </button>
        </div>
      </template>
    </div>
  </aside>
</template>
