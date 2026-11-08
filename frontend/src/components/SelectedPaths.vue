<script setup lang="ts">
/**
 * 已选路径：一个可点的胶囊（「6 项」）+ 弹出列表。
 *
 * 两种做法之间选了「胶囊 + 弹出层」而不是常驻列表：
 *   常驻列表一选六七个文件就占掉一百多像素，输入框会被顶上去；
 *   而用户多数时候只想确认「我选的是这几个」，不需要一直摊开。
 *
 * 但弹出层里必须能**逐项删除**：框选时选进错的文件很常见，
 * 只有「全部清空」等于让他重选一遍。
 *
 * 抽成组件是因为折叠态和全屏态各出现一次 —— 两份实现必然走偏。
 */
import { computed, ref } from "vue";
import { ChevronDown, FileText, FolderOpen, X } from "@lucide/vue";
import { useI18n } from "../i18n";
import { useWorkspace } from "../composables/workspace";

const { t } = useI18n();
const ws = useWorkspace();

const open = ref(false);

type Row = { path: string; name: string; isDir: boolean };

/**
 * 拆成「名字 + 路径」。目录没有扩展名、文件有 —— 用这个区分文件和目录，
 * 在有六七个路径要辨认时比统一图标有用。
 */
const rows = computed<Row[]>(() =>
  ws.paths.value.map((p) => {
    const trimmed = p.replace(/[/\\]+$/, "");
    const parts = trimmed.split(/[/\\]/);
    const name = parts[parts.length - 1] || trimmed;
    const hasExt = /\.[a-z0-9]{1,8}$/i.test(name);
    return { path: p, name, isDir: !hasExt };
  }),
);
</script>

<template>
  <div v-if="rows.length" class="relative mb-2 flex items-center gap-1.5">
    <div class="relative">
      <button
        type="button"
        class="inline-flex max-w-[420px] items-center gap-1.5 rounded-full bg-muted px-2.5 py-1 text-[13px] transition-colors hover:bg-accent"
        :class="open ? 'bg-accent' : ''"
        @click="open = !open"
      >
        <FolderOpen :size="12" class="shrink-0 text-muted-foreground" />
        <span class="shrink-0 font-medium">
          {{ rows.length === 1 ? rows[0].name : t("ai.itemsCount", { n: rows.length }) }}
        </span>
        <span v-if="rows.length === 1" class="min-w-0 truncate text-muted-foreground">{{ rows[0].path }}</span>
        <ChevronDown :size="12" class="shrink-0 text-muted-foreground" />
      </button>

      <!-- 遮罩与列表是兄弟节点：把列表塞进 fixed 遮罩里，absolute 就会
           相对视口定位，弹到屏幕外面去（加号那次的坑） -->
      <div v-if="open" class="fixed inset-0 z-[60]" @click="open = false" />
      <div
        v-if="open"
        class="absolute bottom-full left-0 z-[70] mb-2 max-h-[240px] w-[440px] overflow-y-auto rounded-xl border border-border bg-background p-1.5 shadow-xl"
      >
        <div
          v-for="row in rows"
          :key="row.path"
          class="flex items-center gap-1.5 rounded-lg px-2 py-1 text-[13px] hover:bg-accent"
        >
          <FolderOpen v-if="row.isDir" :size="13" class="shrink-0 text-primary" />
          <FileText v-else :size="13" class="shrink-0 text-muted-foreground" />
          <span class="shrink-0 font-medium">{{ row.name }}</span>
          <span class="min-w-0 flex-1 truncate text-[12px] text-muted-foreground" :title="row.path">
            {{ row.path }}
          </span>
          <button
            type="button"
            class="shrink-0 rounded p-0.5 text-muted-foreground transition-colors hover:bg-background hover:text-foreground"
            :title="t('ai.removePath')"
            @click="ws.removePath(row.path)"
          >
            <X :size="13" />
          </button>
        </div>

        <div class="mt-1 border-t border-border pt-1">
          <button
            type="button"
            class="w-full rounded-lg px-2 py-1.5 text-left text-[12.5px] text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            @click="ws.clearPaths(); open = false"
          >
            {{ t("ai.clearPaths") }}
          </button>
        </div>
      </div>
    </div>

    <!-- 胶囊右侧的叉：不清空全部，只收起列表的话太隐晦，这里保留一个整体清空入口 -->
    <button
      type="button"
      class="rounded p-0.5 text-muted-foreground transition-colors hover:text-foreground"
      :title="t('ai.clearPaths')"
      @click="ws.clearPaths()"
    >
      <X :size="13" />
    </button>
  </div>
</template>
