/**
 * 打开子窗口的小封装。
 *
 * 这些窗口定义在 config/windows.yaml 里（settings / about），前端只需要名字。
 * 单独抽出来是为了让组件不直接依赖 bindings 的长路径。
 */
import * as WindowService from "../../bindings/github.com/klphp/excel-batch-tool/internal/window/serviceprovider";

export async function openWindow(name: string): Promise<void> {
  try {
    await WindowService.Open(name);
  } catch (err) {
    console.error("打开窗口失败:", name, err);
  }
}
