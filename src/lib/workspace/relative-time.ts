import { formatSchemaText, isSingularCount, type CanvasProductSchema } from '@/lib/product-schema';
import { canvasText } from '@/lib/canvas/presentation';

/**
 * 相对时间：刚刚、几分钟前、几小时前、几天前；超过一个月写日期。单位换算和文案
 * 都来自 Schema（`canvas.frontend.relative_time` 与 `canvas.presentation.text` 的 `relative.*`）。
 * 活动语言的复数规则选 `*_one`，缺失时回退普通文案。
 */
export function relativeTime(schema: CanvasProductSchema, value: string | number | Date, now: number = Date.now()): string {
  const time = value instanceof Date ? value.getTime() : new Date(value).getTime();
  if (!Number.isFinite(time)) return '';
  const config = schema.frontend.relative_time;
  const minutes = Math.floor((now - time) / config.milliseconds_per_minute);
  if (minutes < 1) return canvasText(schema, 'relative.just_now');
  const fill = (key: string, count: number) => {
    const singular = isSingularCount(config.locale, count) ? schema.presentation.text[`${key}_one`] : '';
    return formatSchemaText(singular?.trim() ? singular : canvasText(schema, key), { count });
  };
  if (minutes < config.minutes_per_hour) return fill('relative.minutes_ago', minutes);
  const hours = Math.floor(minutes / config.minutes_per_hour);
  if (hours < config.hours_per_day) return fill('relative.hours_ago', hours);
  const days = Math.floor(hours / config.hours_per_day);
  if (days < config.days_per_month) return fill('relative.days_ago', days);
  return new Date(time).toLocaleDateString(config.locale);
}
