import { describe, expect, it } from 'vitest';

import type { CanvasProductSchema } from '@/lib/product-schema';
import { relativeTime } from './relative-time';

const schema = {
  frontend: { relative_time: { milliseconds_per_minute: 60000, minutes_per_hour: 60, hours_per_day: 24, days_per_month: 30, locale: 'zh-CN' } },
  presentation: { text: {
    'relative.just_now': '刚刚', 'relative.minutes_ago': '{count} 分钟前', 'relative.hours_ago': '{count} 小时前', 'relative.days_ago': '{count} 天前',
  } },
} as unknown as CanvasProductSchema;

const englishSchema = {
  frontend: { relative_time: { milliseconds_per_minute: 60000, minutes_per_hour: 60, hours_per_day: 24, days_per_month: 30, locale: 'en' } },
  presentation: { text: {
    'relative.just_now': 'Just now',
    'relative.minutes_ago': '{count} minutes ago', 'relative.minutes_ago_one': '{count} minute ago',
    'relative.hours_ago': '{count} hours ago', 'relative.hours_ago_one': '{count} hour ago',
    'relative.days_ago': '{count} days ago', 'relative.days_ago_one': '{count} day ago',
  } },
} as unknown as CanvasProductSchema;

describe('relativeTime', () => {
  const now = Date.parse('2026-10-08T12:00:00Z');
  const ago = (minutes: number) => new Date(now - minutes * 60000).toISOString();

  it('steps from just now through minutes, hours and days using the Schema text', () => {
    expect(relativeTime(schema, ago(0), now)).toBe('刚刚');
    expect(relativeTime(schema, ago(5), now)).toBe('5 分钟前');
    expect(relativeTime(schema, ago(59), now)).toBe('59 分钟前');
    expect(relativeTime(schema, ago(60), now)).toBe('1 小时前');
    expect(relativeTime(schema, ago(23 * 60), now)).toBe('23 小时前');
    expect(relativeTime(schema, ago(24 * 60), now)).toBe('1 天前');
    expect(relativeTime(schema, ago(29 * 24 * 60), now)).toBe('29 天前');
  });

  it('writes a date after a month, and nothing for a time that does not parse', () => {
    expect(relativeTime(schema, ago(31 * 24 * 60), now)).toBe(new Date(now - 31 * 24 * 60 * 60000).toLocaleDateString('zh-CN'));
    expect(relativeTime(schema, 'never', now)).toBe('');
  });

  it('uses the English singular text for one minute, hour and day', () => {
    expect(relativeTime(englishSchema, ago(1), now)).toBe('1 minute ago');
    expect(relativeTime(englishSchema, ago(5), now)).toBe('5 minutes ago');
    expect(relativeTime(englishSchema, ago(60), now)).toBe('1 hour ago');
    expect(relativeTime(englishSchema, ago(24 * 60), now)).toBe('1 day ago');
    expect(relativeTime(englishSchema, ago(2 * 24 * 60), now)).toBe('2 days ago');
  });

  it('falls back to the general text when a locale declares no singular form', () => {
    const withoutSingular = {
      ...englishSchema,
      presentation: { text: {
        'relative.just_now': 'Just now',
        'relative.minutes_ago': '{count} minutes ago',
        'relative.hours_ago': '{count} hours ago',
        'relative.days_ago': '{count} days ago',
      } },
    } as unknown as CanvasProductSchema;
    expect(relativeTime(withoutSingular, ago(1), now)).toBe('1 minutes ago');
  });
});
