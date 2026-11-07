import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import tailwindcss from "@tailwindcss/vite";
import wails from "@wailsio/runtime/plugins/vite";
import { fileURLToPath, URL } from "node:url";

// ─────────────────────────────────────────────────────────────
// 端口绝对不能写死
// ─────────────────────────────────────────────────────────────
// 开发时同时开两个 Wails 应用是常态（比如一个模板一个实际项目），
// 官方模板里写死 9245 + strictPort:true，第二个项目会直接报
// 「Port 9245 is already in use」起不来。
//
// 规则：
//   · Wails dev 会通过 WAILS_VITE_PORT 传一个自己分配的空闲端口，
//     优先用它 —— 多个应用各用各的，互不干扰
//   · 纯 `npm run dev`（没走 Wails）时用 0，让系统分配空闲端口，
//     终端会打印实际地址
//   · strictPort 必须 false：端口被占时自动换一个，而不是直接失败
//
// 同理，Go 侧内嵌服务的监听地址是 127.0.0.1:0（见 config/app.yaml），
// 永远不写固定端口。
const vitePort = Number(process.env.WAILS_VITE_PORT) || 0;

// https://vitejs.dev/config/
export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: vitePort,
    strictPort: false,
  },
  resolve: {
    alias: {
      // shadcn-vue 生成的文件内部都用 @/ 引用，必须配
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  plugins: [vue(), tailwindcss(), wails("./bindings")],
});
