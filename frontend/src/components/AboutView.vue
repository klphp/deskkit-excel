<script setup lang="ts">
/**
 * 关于视图。同样被主窗口和独立子窗口共用。
 */
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

import { useI18n } from "../i18n";
import logoURL from "../assets/logo.svg";

defineProps<{ info: AppInfo | null }>();

const { t } = useI18n();
</script>

<template>
  <div class="mx-auto max-w-md space-y-4 p-6">
    <div class="rounded-lg border border-border bg-card p-6 text-center">
      <!-- 应用标志（不是模板占位字母）。圆角交给 SVG 自己画，
           外面不再套色块，避免出现「方块套方块」 -->
      <img :src="logoURL" alt="" class="mx-auto mb-3 h-14 w-14" />
      <h2 class="text-base font-semibold">{{ info?.name ?? t("app.name") }}</h2>
      <p class="mt-1 font-mono text-xs text-muted-foreground">
        v{{ info?.version ?? "0.1.0" }}
      </p>
    </div>

    <div class="rounded-lg border border-border bg-card p-6">
      <dl class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm">
        <dt class="text-muted-foreground">{{ t("about.platform") }}</dt>
        <dd class="font-mono">{{ info?.platform ?? "-" }}</dd>
        <dt class="text-muted-foreground">Arch</dt>
        <dd class="font-mono">{{ info?.arch ?? "-" }}</dd>
        <dt class="text-muted-foreground">框架</dt>
        <dd>Wails v3 + Vue 3</dd>
        <dt class="text-muted-foreground">样式</dt>
        <dd>Tailwind 4 + shadcn-vue</dd>
      </dl>
    </div>
  </div>
</template>
