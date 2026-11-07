import { createApp } from "vue";
import "./assets/index.css";
import App from "./App.vue";

// 首屏前先应用主题，避免亮色闪一下再变暗（FOUC）
const saved = localStorage.getItem("deskkit-theme");
if (
  saved === "dark" ||
  (!saved && window.matchMedia("(prefers-color-scheme: dark)").matches)
) {
  document.documentElement.classList.add("dark");
}

createApp(App).mount("#app");
