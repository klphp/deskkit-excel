import { type ClassValue, clsx } from "clsx";
import { twMerge } from "tailwind-merge";

/**
 * 合并 Tailwind 类名。
 *
 * clsx 负责条件拼接，twMerge 负责消解冲突 —— 后写的覆盖先写的
 * （cn("px-2", "px-4") → "px-4"）。没有 twMerge 的话两个都留着，
 * 最终生效哪个取决于 CSS 里的顺序，非常难 debug。
 */
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
