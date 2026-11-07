<script setup lang="ts">
/**
 * 应用外壳 / 多窗口入口。
 *
 * # 一个 SPA 服务所有窗口
 *
 * 所有窗口加载的是同一份前端，靠 URL 里的 ?view= 决定渲染什么：
 *
 *   /                    → 主窗口（标题栏 + 侧边栏 + 内容区）
 *   /?view=settings      → 设置子窗口（只有标题栏 + 设置页）
 *   /?view=about         → 关于子窗口
 *
 * 这样做的好处：
 *   · 只构建一次，多个窗口共享 WebView 缓存
 *   · 新增窗口不用改构建配置，windows.yaml 加一段即可
 *   · 窗口之间主题、组件、工具函数全部通用
 *
 * 新增一个窗口视图的完整步骤：
 *   ① config/windows.yaml 加一段（url: /?view=xxx）
 *   ② 下面的 views 里加一个 xxx 组件
 *   ③ 调 WindowService.Open("xxx") 打开
 */
import { computed, nextTick, onMounted, ref, watch } from "vue";
import { Events } from "@wailsio/runtime";
import { initTheme } from "./composables/useTheme";
import { initI18n, useI18n } from "./i18n";
import TitleBar from "./components/TitleBar.vue";
import { openWindow } from "./lib/windows";
import SettingsView from "./components/SettingsView.vue";
import AboutView from "./components/AboutView.vue";
import Workbench from "./components/Workbench.vue";

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
const view = ref<string>("");
const active = ref("workbench");
const appInfo = ref<AppInfo | null>(null);

const { t } = useI18n();

// 子窗口不显示侧边栏 —— 它就是干一件事的小窗口
const isSubWindow = computed(() => view.value !== "");

// ── 模态子窗口打开时，主窗口内容区显示遮罩 ──
//
// 为什么不直接让原生窗口忽略鼠标事件：那是个整窗开关，一开连标题栏
// 拖动都失效。这里只挡内容区，标题栏照常可拖、可最小化关闭 —— 用户
// 可以在子窗口开着的时候顺手挪一下主窗口。
//
// 事件由 internal/window/manager.go 的 emitBlockState 发出。
const blocked = ref(false);

/**
 * 点遮罩时的自救检查。
 *
 * 正常情况下子窗口关闭会发 unblocked 事件撤掉遮罩。但万一事件没到
 * （子窗口被系统以非常规方式销毁、或者跟随移动时出了岔子而
 * WindowClosing 没触发），遮罩就会永久卡住，主窗口彻底点不动 ——
 * 用户只能重启应用。
 *
 * 所以点一下遮罩就主动核对一次：真没子窗口了就把遮罩撤掉。
 */
async function onMaskClick() {
  try {
    const { ServiceProvider } = await import(
      "../bindings/github.com/klphp/excel-batch-tool/internal/window"
    );
    const opened = (await ServiceProvider.Opened()) ?? [];
    // 只剩主窗口自己 → 没有模态窗口了，遮罩该撤
    const others = opened.filter((n: string) => n !== "main");
    if (others.length === 0) {
      blocked.value = false;
    }
  } catch {
    // bindings 不可用就别把用户困在遮罩后面
    blocked.value = false;
  }
}

const title = computed(() => {
  if (view.value === "settings") return t("nav.settings");
  if (view.value === "about") return t("nav.about");
  return appInfo.value?.name ?? t("app.name");
});

onMounted(async () => {
  // 主题必须在每个窗口各自初始化一次 —— 子窗口是独立的 WebView，
  // 虽然 localStorage 同源共享，但 .dark 类是挂在各自 document 上的。
  initTheme();

  view.value = new URLSearchParams(location.search).get("view") ?? "";

  try {
    const { AppService } = await import("../bindings/github.com/klphp/excel-batch-tool/internal/services");
    appInfo.value = await AppService.GetInfo();
    // 拿到配置才知道要不要启用多语言、用哪个语言
    await initI18n(appInfo.value?.i18n);
  } catch {
    /* bindings 未生成 */
  }

  // 子窗口：内容渲染完再贴合尺寸
  if (view.value) {
    nextTick(() => fitWindowToContent());
  }

  // 只有主窗口需要响应遮罩事件
  if (!view.value) {
    Events.On("wails3-base:window:blocked", () => {
      blocked.value = true;
    });
    Events.On("wails3-base:window:unblocked", () => {
      blocked.value = false;
    });
  }
});

// ── 子窗口自适应尺寸 ──
//
// 固定尺寸的窗口内容少时会挂一条用不上的滚动条，内容多又要滚。
// 这里量出内容实际高度，把窗口调到刚好。
//
// 两个容易做错的地方：
//   · 必须等 document.fonts.ready —— 字体没加载完时行高是按 fallback
//     算的，测出来的高度偏小，窗口会切掉底部内容
//   · 标题栏高度要自己加回去，内容区只量到标题栏下沿
const contentRef = ref<HTMLElement | null>(null);
const TITLEBAR_HEIGHT = 38;

async function fitWindowToContent() {
  if (!isSubWindow.value || !contentRef.value) return;

  // 字体没加载完时行高不准，等它 —— 但必须加超时。
  //
  // document.fonts.ready 在 Wails 的 WebView 里可能永远不 resolve
  // （取决于字体加载状态机），await 会一直挂着，整个 resize 静默失效，
  // 前端也不报错。加 500ms 兜底，测不准也比完全不执行好。
  await Promise.race([
    (document.fonts?.ready as Promise<unknown>) ?? Promise.resolve(),
    new Promise((resolve) => setTimeout(resolve, 500)),
  ]);
  await nextTick();

  const el = contentRef.value;
  if (!el) return;

  // ★ 不能直接读 el.scrollHeight。
  //
  // 这个容器是 flex-1 + overflow-y-auto，它的 scrollHeight 是
  // max(内容高度, 容器当前高度) —— 窗口比内容大时读到的是容器高度，
  // 拿它去 resize 等于「按当前高度再设一次」，窗口永远缩不下来，
  // 底部留白就一直留着。
  //
  // 正确做法：量里面那个内容 div 的实际高度，再加上容器自身的 padding。
  const inner = el.firstElementChild as HTMLElement | null;
  const cs = getComputedStyle(el);
  const padTop = parseFloat(cs.paddingTop) || 0;
  const padBottom = parseFloat(cs.paddingBottom) || 0;

  const contentHeight = inner
    ? inner.getBoundingClientRect().height + padTop + padBottom
    : el.scrollHeight;

  // 标题栏 + 1px 边框余量
  const target = Math.ceil(contentHeight) + TITLEBAR_HEIGHT + 1;

  // 高度没变就别动窗口了 —— ResizeToContent + CenterOnParent 是两次
  // 跨进程调用，每次都让窗口重排一次。内容本来就没变时纯属浪费，
  // 而且连着调几次会让「打开窗口」这一下明显卡顿。
  if (Math.abs(target - window.innerHeight) < 2) return;

  try {
    const { WindowService } = await import("../bindings/github.com/klphp/excel-batch-tool/internal/services");
    const { ServiceProvider } = await import("../bindings/github.com/klphp/excel-batch-tool/internal/window");
    // 宽度传 0 = 保持当前宽度，只调整高度
    await WindowService.ResizeToContent(0, target);
    // 高度变了要重新居中，否则窗口往下长，看着偏
    await ServiceProvider.CenterOnParent();
  } catch (e) {
    // 别静默吞掉 —— 之前就是这样导致「什么都没发生」查了半天
    console.warn("调整窗口尺寸失败", e);
  }
}

// 内容变化（切换视图、数据加载完）时重新贴合。
//
// 加 80ms 防抖：view 和 appInfo 是先后就绪的，不加防抖会连着跑两三轮
// 完整流程 —— 每轮都要等字体、量 DOM、调 ResizeToContent + CenterOnParent
// 两次 IPC。实测能打出 4 条「子窗口居中」，窗口在打开那一下被反复重排，
// 看起来就是卡顿。
let fitTimer: ReturnType<typeof setTimeout> | undefined;
watch(
  () => [view.value, appInfo.value],
  () => {
    if (fitTimer) clearTimeout(fitTimer);
    fitTimer = setTimeout(() => {
      nextTick(() => fitWindowToContent());
    }, 80);
  }
);

</script>

<template>
  <div class="flex h-full flex-col">
    <!-- 所有窗口都有标题栏：frameless 下这是唯一的拖动和关闭入口 -->
    <TitleBar
      :title="title"
      :version="appInfo?.version ?? '0.1.0'"
      :show-settings="!isSubWindow && (appInfo?.showSettingsButton ?? true)"
      :show-about="!isSubWindow"
      :show-version="!isSubWindow"
      :show-title="isSubWindow || (appInfo?.showTitle ?? true)"
      :platform="appInfo?.platform ?? ''"
      @open-settings="openWindow('settings')"
      @open-about="openWindow('about')"
    />

    <!-- ── 子窗口：只渲染对应视图 ── -->
    <div
      v-if="isSubWindow"
      ref="contentRef"
      class="min-h-0 flex-1 overflow-y-auto"
    >
      <SettingsView v-if="view === 'settings'" :info="appInfo" />
      <AboutView v-else-if="view === 'about'" :info="appInfo" />
      <div v-else class="grid h-full place-items-center text-sm text-muted-foreground">
        未知视图：{{ view }}
      </div>
    </div>

    <!-- ── 主窗口：只有一个工作区 ──
         没有侧边栏：这个应用只有一件事要做，导航除了占地方没有别的用处。
         设置与关于改由标题栏的图标进入（各自开一个子窗口）。 -->
    <div v-else class="relative flex min-h-0 flex-1">
      <!-- 模态遮罩：吃掉内容区的所有点击，所以子窗口开着时主窗口点不动。
           标题栏在它外面（上方），拖动和窗口按钮不受影响。
           半透明而不是全遮：让人看得见主窗口内容，知道自己回到了哪里。

           ★ 不要给这层加 backdrop-blur / backdrop-filter。
           遮罩盖满整个内容区，加模糊会让 WebView 对整块区域重新采样合成，
           打开子窗口那一下明显卡顿（WKWebView 上尤其重）。纯半透明色块
           几乎零成本，视觉效果差别很小。 -->
      <div
        v-if="blocked"
        class="absolute inset-0 z-50 cursor-not-allowed bg-background/45"
        aria-hidden="true"
        @click.stop="onMaskClick"
      />

      <main class="flex min-w-0 flex-1 flex-col">
        <!-- 不要再加一条工具栏：标题栏上已经有应用名和设置入口，
             再加一条就是把同样的东西写两遍。工作台自己就是内容区。 -->
        <div class="min-h-0 flex-1 overflow-hidden">
          <Workbench />
        </div>
      </main>
    </div>
  </div>
</template>
