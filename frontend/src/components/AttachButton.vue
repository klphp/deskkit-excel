<script setup lang="ts">
/**
 * 附件按钮：加号 + 上弹菜单（选文件 / 选文件夹）。
 *
 * 抽成组件的理由：这个按钮在**两处**出现 —— 正常输入框、以及全屏编辑时的
 * 输入框。两份代码的后果是改一处忘一处（这次就是这么发现的：全屏那份还是旧的
 * 文件夹图标、还带着「功能选择」）。同一个交互只留一份实现。
 *
 * 菜单向上弹出并贴着按钮左边缘，不是居中浮在窗口中间：
 * 它属于这个按钮，就该长在按钮旁边。
 */
import { ref } from "vue";
import { FileText, FolderOpen, Loader2, Plus } from "@lucide/vue";
import { useI18n } from "../i18n";
import { useWorkspace } from "../composables/workspace";

const { t } = useI18n();
const ws = useWorkspace();

const open = ref(false);

async function chooseFiles() {
  open.value = false;
  await ws.pickFiles(t("ai.pickFiles"));
}

async function chooseDirs() {
  open.value = false;
  await ws.pickDirs(t("ai.pickFolder"));
}
</script>

<template>
  <div class="relative">
    <button
      type="button"
      class="grid size-9 place-items-center rounded-full text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground disabled:opacity-50"
      :class="open ? 'bg-accent text-accent-foreground' : ''"
      :title="t('ai.attach')"
      :disabled="ws.busy.value"
      @click="open = !open"
    >
      <Loader2 v-if="ws.busy.value" :size="17" class="animate-spin" />
      <Plus v-else :size="17" />
    </button>

    <!-- 遮罩和菜单是**兄弟**，不是父子。
         把菜单塞进 fixed 遮罩里，菜单的 bottom-full 就会相对整个视口算，
         于是跑到屏幕上方外面 —— 表现就是「点了加号什么也没弹出来」。 -->
    <div v-if="open" class="fixed inset-0 z-[60]" @click="open = false" />
    <div
      v-if="open"
      class="absolute bottom-full left-0 z-[70] mb-2 w-[188px] rounded-xl border border-border bg-background p-1.5 shadow-xl"
    >
      <button
        type="button"
        class="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-[13.5px] transition-colors hover:bg-accent"
        @click="chooseFiles"
      >
        <FileText :size="15" class="text-muted-foreground" />
        {{ t("ai.pickFiles") }}
      </button>
        <button
          type="button"
          class="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-[13.5px] transition-colors hover:bg-accent"
          @click="chooseDirs"
        >
          <FolderOpen :size="15" class="text-muted-foreground" />
        {{ t("ai.pickFolder") }}
      </button>
    </div>
  </div>
</template>
