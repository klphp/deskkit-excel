<script setup lang="ts">
/**
 * 左侧会话栏。
 *
 * 交互参照 Gemini 的会话列表：hover 出现「⋯」，点开只有两项 ——
 * 重命名、删除。没有再多的动作：这是本地工具，分享/固定/笔记本那些
 * 对当前场景没有意义，多一项就多一分犹豫。
 *
 * 重命名做成行内编辑（不弹窗），删除做成行内二次确认（不弹窗）：
 * 弹窗会打断视线，而这两个动作都在这一行里就能说清楚。
 */
import { computed, nextTick, onMounted, ref } from "vue";
import { Plus, MoreVertical, Pencil, Trash2, Check, X } from "@lucide/vue";
import { useI18n } from "../i18n";
import { initSessions, useSessions } from "../composables/sessions";

const { t } = useI18n();
const sess = useSessions();

onMounted(() => {
  void initSessions();
});

/** 打开菜单的会话 id。 */
const menuFor = ref("");
/** 正在重命名的会话 id 与草稿。 */
const editingId = ref("");
const draftTitle = ref("");
const editInput = ref<HTMLInputElement | null>(null);
/** 正在确认删除的会话 id。 */
const confirmId = ref("");

function onNew() {
  void sess.create();
}

function onSelect(id: string) {
  if (editingId.value || confirmId.value) return;
  void sess.select(id);
}

function openMenu(id: string, ev: MouseEvent) {
  ev.stopPropagation();
  confirmId.value = "";
  menuFor.value = menuFor.value === id ? "" : id;
}

function startRename(id: string, ev: MouseEvent) {
  ev.stopPropagation();
  menuFor.value = "";
  editingId.value = id;
  draftTitle.value = sess.summaries.value.find((s) => s.id === id)?.title ?? "";
  nextTick(() => editInput.value?.focus());
}

async function commitRename() {
  const id = editingId.value;
  const title = draftTitle.value.trim();
  editingId.value = "";
  if (id && title) await sess.rename(id, title);
}

function cancelRename() {
  editingId.value = "";
  draftTitle.value = "";
}

function askDelete(id: string, ev: MouseEvent) {
  ev.stopPropagation();
  menuFor.value = "";
  confirmId.value = id;
}

async function doDelete(id: string, ev: MouseEvent) {
  ev.stopPropagation();
  confirmId.value = "";
  await sess.remove(id);
}
</script>

<template>
  <aside class="flex w-[282px] shrink-0 flex-col border-r border-border bg-muted/30">
    <div class="p-2.5">
      <button
        type="button"
        class="flex w-full items-center justify-center gap-2 rounded-lg border border-border bg-card px-2.5 py-[9px] text-[14px] font-medium transition-colors hover:border-primary/40 hover:bg-accent"
        @click="onNew"
      >
        <Plus :size="15" class="text-primary" />
        {{ t("session.new") }}
      </button>
    </div>

    <div class="min-h-0 flex-1 overflow-y-auto px-2 pb-3">
      <p v-if="sess.ordered.value.length === 0" class="px-1.5 py-2 text-[13px] text-muted-foreground">
        {{ t("session.empty") }}
      </p>

      <template v-if="sess.ordered.value.length > 0">
        <p class="px-1.5 pb-1 pt-2.5 text-[12px] font-medium uppercase tracking-wide text-muted-foreground">
          {{ t("session.all") }}
        </p>

        <div v-for="s in sess.ordered.value" :key="s.id" class="relative">
          <!-- 重命名：行内输入 -->
          <div v-if="editingId === s.id" class="flex items-center gap-1 rounded-lg bg-accent px-2 py-[5px]">
            <input
              ref="editInput"
              v-model="draftTitle"
              class="min-w-0 flex-1 rounded border border-input bg-background px-1.5 py-0.5 text-[14px] outline-none focus:border-ring"
              @keydown.enter.prevent="commitRename"
              @keydown.esc.prevent="cancelRename"
              @click.stop
            />
            <button type="button" class="shrink-0 rounded p-0.5 text-muted-foreground hover:text-foreground" @click.stop="commitRename">
              <Check :size="13" />
            </button>
            <button type="button" class="shrink-0 rounded p-0.5 text-muted-foreground hover:text-foreground" @click.stop="cancelRename">
              <X :size="13" />
            </button>
          </div>

          <!-- 删除确认：也在行内 -->
          <div v-else-if="confirmId === s.id" class="flex items-center gap-1.5 rounded-lg bg-destructive/10 px-2.5 py-[7px]">
            <span class="min-w-0 flex-1 truncate text-[13px] text-destructive">{{ t("session.confirmDelete") }}</span>
            <button
              type="button"
              class="shrink-0 rounded px-1.5 py-0.5 text-[12.5px] font-medium text-destructive hover:bg-destructive/15"
              @click.stop="doDelete(s.id, $event)"
            >
              {{ t("session.delete") }}
            </button>
            <button
              type="button"
              class="shrink-0 rounded px-1.5 py-0.5 text-[12.5px] text-muted-foreground hover:bg-accent"
              @click.stop="confirmId = ''"
            >
              {{ t("session.cancel") }}
            </button>
          </div>

          <!-- 正常行 -->
          <div
            v-else
            class="group relative flex items-center gap-1.5 rounded-lg px-2.5 py-[7px] text-[14px] transition-colors"
            :class="
              s.id === sess.currentId.value
                ? 'bg-accent font-medium text-accent-foreground'
                : 'text-muted-foreground hover:bg-accent/60'
            "
            role="button"
            tabindex="0"
            @click="onSelect(s.id)"
            @keydown.enter="onSelect(s.id)"
            @contextmenu.prevent="openMenu(s.id, $event)"
          >
            <span
              v-if="s.id === sess.currentId.value"
              class="absolute left-0 top-1.5 bottom-1.5 w-[2px] rounded-full bg-primary"
              aria-hidden="true"
            />
            <span class="min-w-0 flex-1 truncate">{{ s.title || t("session.untitled") }}</span>
            <button
              type="button"
              class="hidden shrink-0 rounded p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground group-hover:block"
              :title="t('session.more')"
              @click="openMenu(s.id, $event)"
            >
              <MoreVertical :size="14" />
            </button>
          </div>

          <!-- 菜单：只有重命名和删除 -->
          <div
            v-if="menuFor === s.id"
            class="absolute right-1 top-8 z-30 w-[132px] rounded-lg border border-border bg-background py-1 shadow-xl"
            @click.stop
          >
            <button
              type="button"
              class="flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-[13px] hover:bg-accent"
              @click="startRename(s.id, $event)"
            >
              <Pencil :size="13" class="text-muted-foreground" />
              {{ t("session.rename") }}
            </button>
            <button
              type="button"
              class="flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-[13px] text-destructive hover:bg-destructive/10"
              @click="askDelete(s.id, $event)"
            >
              <Trash2 :size="13" />
              {{ t("session.delete") }}
            </button>
          </div>
        </div>
      </template>
    </div>

    <!-- 点空白处关掉菜单 -->
    <div v-if="menuFor" class="fixed inset-0 z-20" @click="menuFor = ''" />
  </aside>
</template>
