<script setup lang="ts">
/**
 * 跨平台标题栏。
 *
 * 两个平台的差异不是「换个图标」那么小，而是结构性的：
 *
 *   macOS  红绿灯是**系统画的**，位置固定在左上角。我们不能自己画
 *          （会盖住系统的，而且用户会觉得不对劲），所以左边留出
 *          78px 让位，中间放标题，右边对称留空保持标题居中。
 *          内容区在 main.go 里用 FullSizeContent 延伸到标题栏下方，
 *          所以这一条实际上浮在页面之上。
 *
 *   Windows 原生标题栏被 Frameless 去掉了，什么都不画就**没有关闭
 *          按钮**，用户没法关窗口。所以右上角必须自绘最小化/最大化/
 *          关闭三个按钮。宽度取 38px —— Win11 原生是 46px，但那个尺寸
 *          在 150% 缩放的 4K 屏上显得占地方，收窄一点更紧凑。
 *
 * 设置入口固定在这一条的最右侧。标题的锚点跟着平台走：
 *
 *   macOS   [红绿灯]    [标题居中]        [⚙]
 *   Windows [标题居左]                [⚙] [─ □ ✕]
 *
 * 为什么不一样：macOS 左边是系统红绿灯，标题居中才和它对称；Windows
 * 左边空着、右边一排控件，标题贴左是那一侧的原生惯例。两个平台都居中
 * 或者都居左，总有一个平台看着别扭。
 *
 * 为什么主题切换从标题栏挪进了设置窗口：主题是「设一次就不动」的东西，
 * 常驻一个按钮占位置；而且设置窗口里还有检测更新，两件事都归设置管，
 * 入口统一比分散好。
 *
 * 拖动：整条设 --wails-draggable: drag，按钮必须设 no-drag，
 * 否则点关闭变成拖窗口。
 */
import { computed, onMounted, ref } from "vue";
import { Minus, Square, Copy, X, Settings } from "@lucide/vue";
import { useI18n } from "../i18n";

const { t } = useI18n();

const props = withDefaults(
  defineProps<{
    title?: string;
    version?: string;
    /** 是否显示右侧的设置入口。主窗口开，子窗口关 —— 子窗口本身就是从
     *  设置里点开的东西，再放一个设置按钮会指向自己。 */
    showSettings?: boolean;
    /** 是否在标题旁显示版本号。主窗口显示，子窗口不显示 —— 子窗口的
     *  标题本身就是「关于」「设置」这种功能名，跟版本号并列没意义。 */
    showVersion?: boolean;
    /** 平台名，由 AppService.GetInfo() 的 runtime.GOOS 提供。
     *
     *  ★ 不要依赖前端自己探测。之前用 window._wails.environment.OS，在
     *  Windows 的 WebView 里取不到值，fallback 判成 darwin —— 结果走了
     *  macOS 分支：左边留 78px 给红绿灯、标题绝对居中，设置按钮和窗口
     *  控件就被挤到左边去了。表现是「按钮位置不对」，根因是平台判错。
     *
     *  Go 侧的 runtime.GOOS 是编译期常量，没有猜错的余地。 */
    platform?: string;
    /** 是否显示标题文字（应用名 + 版本号）。来自 config/windows.yaml 的
     *  showTitle。关掉后标题栏仍然存在 —— 它是 frameless 窗口唯一的拖动
     *  入口和窗口按钮载体，不能整条去掉。 */
    showTitle?: boolean;
  }>(),
  {
    title: "Wails3 Base",
    version: "",
    showSettings: true,
    showVersion: true,
    platform: "",
    showTitle: true,
  },
);

/** 设置入口被点。主窗口收到后切到内容区的设置视图，不开新窗口。 */
const emit = defineEmits<{ openSettings: [] }>();

// 平台检测。window._wails.environment.OS 只在 Wails 环境里存在，
// 纯浏览器跑 npm run dev 时是 undefined，退回 navigator 判断。
const platform = ref<string>("darwin");

function detectPlatform(): string {
  const wailsOS = (window as any)._wails?.environment?.OS;
  if (wailsOS) return String(wailsOS).toLowerCase();
  const ua = navigator.userAgent.toLowerCase();
  if (ua.includes("win")) return "windows";
  if (ua.includes("linux")) return "linux";
  return "darwin";
}

// prop 优先。为空时才退回前端探测 —— 那是给纯浏览器 npm run dev 用的，
// 在真正的 Wails 环境里 prop 一定有值。
const isMac = computed(
  () => (props.platform || platform.value) === "darwin",
);
const isMaximised = ref(false);

onMounted(async () => {
  platform.value = detectPlatform();
  try {
    const { WindowService } = await import(
      "../../bindings/github.com/klphp/excel-batch-tool/internal/services"
    );
    isMaximised.value = await WindowService.IsMaximised();
  } catch {
    /* bindings 未生成，忽略 */
  }
});

async function getService(): Promise<any> {
  const mod: any = await import("../../bindings/github.com/klphp/excel-batch-tool/internal/services");
  return mod.WindowService;
}

async function onMinimise() {
  try {
    (await getService()).Minimise();
  } catch (e) {
    console.warn("最小化失败（bindings 未生成？）", e);
  }
}

async function onToggleMaximise() {
  try {
    isMaximised.value = await (await getService()).ToggleMaximise();
  } catch (e) {
    console.warn("最大化失败（bindings 未生成？）", e);
  }
}

/** 点设置齿轮。
 *
 *  只发事件给父组件，不自己开窗口 —— 设置在主窗口的内容区里显示。
 *  子窗口那套（config/windows.yaml 的 settings）保留着，将来需要独立
 *  设置窗口时把这里换回 ServiceProvider.Open("settings") 即可。 */
function onOpenSettings() {
  emit("openSettings");
}

async function onClose() {
  try {
    (await getService()).Close();
  } catch (e) {
    console.warn("关闭失败（bindings 未生成？）", e);
  }
}
</script>

<template>
  <header
    class="wails-drag relative flex h-[var(--titlebar-height)] shrink-0 select-none items-center border-b border-border bg-background"
  >
    <!-- macOS：给系统红绿灯让位。
         所有窗口（含模态子窗口）都是普通 NSWindow，红绿灯都在，
         所以无条件留位。 -->
    <div v-if="isMac" class="w-[78px] shrink-0" aria-hidden="true" />

    <!-- 标题：两个平台的锚点不一样。
         macOS   绝对定位到整条正中间 —— 左边是系统红绿灯，右边有主题切换，
                 两侧宽度不等，flex 居中必然偏，只能相对整条 absolute 居中。
         Windows 居左 —— 左侧没有红绿灯，右侧是三个窗口控件，标题贴在左边
                 才符合 Windows 应用的惯例（资源管理器、Edge、VS Code 都这样）。
                 这种布局下标题可以用 truncate 安全截断，不会像居中版那样
                 因为截断而视觉偏移。 -->
    <div
      v-if="props.showTitle"
      class="pointer-events-none flex items-baseline gap-2"
      :class="
        isMac
          ? 'absolute inset-x-0 justify-center px-20'
          : 'min-w-0 flex-1 justify-start pl-4 pr-2'
      "
    >
      <span
        class="text-sm font-semibold"
        :class="isMac ? 'whitespace-nowrap' : 'truncate'"
      >{{ props.title }}</span>
      <span
        v-if="props.showVersion && props.version"
        class="shrink-0 font-mono text-xs text-muted-foreground"
      >
        v{{ props.version }}
      </span>
    </div>

    <!-- ★ macOS 专用占位。
         这个 div 不能省 —— mac 的标题是 absolute 定位（为了相对整条居中），
         它不占 flex 空间。没有这个 flex-1，后面的设置按钮会紧跟在 78px
         红绿灯占位后面，跑到左边去。
         Windows 分支不需要它：那边的标题带 flex-1，自己就把右边撑开了。 -->
    <div v-if="isMac" class="flex-1" aria-hidden="true" />

    <!-- 设置入口：固定在右侧（Windows 上位于窗口控件左边） -->
    <div v-if="props.showSettings" class="wails-no-drag flex shrink-0 items-center pr-1">
      <button
        type="button"
        class="grid h-8 w-8 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
        :title="t('titlebar.settings')"
        :aria-label="t('titlebar.settings')"
        @click="onOpenSettings"
      >
        <Settings :size="15" :stroke-width="1.75" />
      </button>
    </div>

    <!-- Windows / Linux：自绘窗口控件 -->
    <div v-if="!isMac" class="wails-no-drag flex h-full shrink-0 items-stretch">
      <button
        type="button"
        class="grid w-[38px] place-items-center text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
        :aria-label="t('titlebar.minimize')"
        @click="onMinimise"
      >
        <Minus :size="12" :stroke-width="1.75" />
      </button>
      <button
        type="button"
        class="grid w-[38px] place-items-center text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
        :aria-label="isMaximised ? t('titlebar.restore') : t('titlebar.maximize')"
        @click="onToggleMaximise"
      >
        <Copy v-if="isMaximised" :size="11" :stroke-width="1.75" />
        <Square v-else :size="10" :stroke-width="1.75" />
      </button>
      <button
        type="button"
        class="grid w-[38px] place-items-center text-muted-foreground transition-colors hover:bg-destructive hover:text-destructive-foreground"
        :aria-label="t('titlebar.close')"
        @click="onClose"
      >
        <X :size="13" :stroke-width="1.75" />
      </button>
    </div>
  </header>
</template>
