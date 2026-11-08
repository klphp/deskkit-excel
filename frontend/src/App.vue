<script setup lang="ts">
/**
 * 应用外壳：标题栏 + 会话栏 + 对话区，设置与关于是同窗口内的浮层。
 *
 * # 为什么设置/关于不做独立窗口
 *
 * 每个 Wails 窗口是独立 WebView、独立 JS 运行时。之前它们在各自窗口里，
 * 在设置里把语言切成英文，主窗口的 i18n 状态根本收不到 —— 用户看到的是
 * 「只有那个窗口变了」。主题能跨窗口是因为额外挂了 localStorage 的
 * storage 事件同步，语言没做，就露馅了。
 *
 * 改成浮层之后：同一个运行时、同一份状态，切换立刻处处生效；
 * 也省掉窗口定位、内容自适应高度那一整套跨进程调用。
 */
import { computed, onMounted, onUnmounted, ref } from "vue";
import { initTheme } from "./composables/useTheme";
import { initChatFont } from "./composables/useChatFont";
import { initI18n, useI18n } from "./i18n";
import { X } from "@lucide/vue";
import TitleBar from "./components/TitleBar.vue";
import SettingsView from "./components/SettingsView.vue";
import AboutView from "./components/AboutView.vue";
import AiChatPanel from "./components/AiChatPanel.vue";
import SessionSidebar from "./components/SessionSidebar.vue";

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

// 当前窗口的视图名。空 = 主窗口。
/**
 * 设置 / 关于改成应用内浮层，不再开独立窗口。
 *
 * 之前各开一个 Wails 窗口，每个窗口是独立 WebView、独立 JS 运行时 ——
 * 在设置里切语言，主窗口的 i18n 状态根本不知道，于是「只有设置的窗口变了」。
 * 主题那边靠 localStorage 的 storage 事件跨窗口同步过，语言没做，就露馅了。
 *
 * 浮层还有一个好处：同一个运行时、同一份状态，切换立刻处处生效，
 * 也少了窗口定位/尺寸自适应这一堆麻烦。
 */
const overlay = ref<"" | "settings" | "about">("");
const active = ref("workbench");
const appInfo = ref<AppInfo | null>(null);

const { t } = useI18n();

const title = computed(() => appInfo.value?.name ?? t("app.name"));

onMounted(async () => {

  // 主题必须在每个窗口各自初始化一次 —— 子窗口是独立的 WebView，
  // 虽然 localStorage 同源共享，但 .dark 类是挂在各自 document 上的。
  initTheme();
  initChatFont();

  try {
    const { AppService } = await import("../bindings/github.com/klphp/excel-batch-tool/internal/services");
    appInfo.value = await AppService.GetInfo();
    // 拿到配置才知道要不要启用多语言、用哪个语言
    await initI18n(appInfo.value?.i18n);
  } catch {
    /* bindings 未生成 */
  }

});

/** Esc 关闭浮层：弹层最容易被期待的退出方式。 */
function onEsc(e: KeyboardEvent) {
  if (e.key === "Escape" && overlay.value) {
    e.preventDefault();
    overlay.value = "";
  }
}
window.addEventListener("keydown", onEsc);
onUnmounted(() => window.removeEventListener("keydown", onEsc));
</script>

<template>
  <div class="flex h-full flex-col">
    <!-- 所有窗口都有标题栏：frameless 下这是唯一的拖动和关闭入口 -->
    <TitleBar
      :title="title"
      :version="appInfo?.version ?? '0.1.0'"
      :show-settings="appInfo?.showSettingsButton ?? true"
      :show-about="true"
      :show-version="true"
      :show-title="appInfo?.showTitle ?? true"
      :platform="appInfo?.platform ?? ''"
      @open-settings="overlay = 'settings'"
      @open-about="overlay = 'about'"
    />

    <!-- ── 主窗口：只有一个工作区 ──
         没有侧边栏：这个应用只有一件事要做，导航除了占地方没有别的用处。
         设置与关于改由标题栏的图标进入（各自开一个子窗口）。 -->
    <div class="relative flex min-h-0 flex-1">
      <main class="flex min-w-0 flex-1 flex-col">
        <!-- 不要再加一条工具栏：标题栏上已经有应用名和设置入口，
             再加一条就是把同样的东西写两遍。工作台自己就是内容区。 -->
        <div class="flex min-h-0 flex-1">
          <!-- 左边会话历史：对话是有历史的，用户要能新开一轮、回看上一轮 -->
          <SessionSidebar />
          <div class="min-h-0 min-w-0 flex-1 overflow-hidden">
            <AiChatPanel />
          </div>
        </div>
      </main>

      <!-- ── 设置 / 关于浮层 ──
           点背景关闭，右上角叉关闭，Esc 关闭；内容区自己滚。
           不做 backdrop-blur：WKWebView 上模糊整块要重新合成，打开那一下会卡。 -->
      <div
        v-if="overlay"
        class="absolute inset-0 z-[60] flex items-start justify-center bg-background/60 px-4 pt-[7vh]"
        @click.self="overlay = ''"
      >
        <div
          class="flex max-h-[82vh] w-full flex-col overflow-hidden rounded-xl border border-border bg-background shadow-2xl"
          :class="overlay === 'about' ? 'max-w-[420px]' : 'max-w-[560px]'"
        >
          <div class="flex shrink-0 items-center justify-between border-b border-border px-5 py-2.5">
            <h2 class="text-[14px] font-semibold">
              {{ overlay === "settings" ? t("nav.settings") : t("nav.about") }}
            </h2>
            <button
              type="button"
              class="grid h-7 w-7 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
              @click="overlay = ''"
            >
              <X :size="15" />
            </button>
          </div>
          <div class="min-h-0 flex-1 overflow-y-auto">
            <SettingsView v-if="overlay === 'settings'" :info="appInfo" />
            <AboutView v-else :info="appInfo" />
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
