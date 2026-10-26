import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

/** 外部任务类型兼容逻辑已迁移到消费 Product Schema 的 presenter。 */
