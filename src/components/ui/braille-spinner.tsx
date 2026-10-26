'use client';

import { useState, useEffect } from 'react';
import { cn } from '@/lib/utils';

/**
 * 盲文点字（Unicode Braille Pattern）旋转加载器
 *
 * 选用字符：⠋ ⠙ ⠹ ⠸ ⠼ ⠴ ⠦ ⠧ ⠇ ⠏
 * - 来自 cli-spinners 库经典 "dots" 变体
 * - 优势：字符等宽、不破坏行高、与文字完美对齐（比 bounce 球/pulse 光标稳定得多）
 *
 * 设计原则：
 * - 单字符宽度（1ch），inline-block 不影响周围文本流
 * - font-mono 强制等宽（部分字体下 Braille 宽度略不一致）
 * - 颜色继承 currentColor（自动融入父元素上下文）
 * - 尊重 prefers-reduced-motion：减少动画模式下降级为静态字符
 *
 * @example
 *  <BrailleSpinner />                    // 单字符
 *  <BrailleSpinner label="生成中" />     // 带文字
 *  <BrailleSpinner size="lg" />          // 大号
 *  <BrailleSpinner className="text-warning" />
 */

const BRAILLE_FRAMES = ['⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'];

interface BrailleSpinnerProps {
  /** 尺寸（影响字号） */
  size?: 'sm' | 'md' | 'lg' | 'xl';
  /** 附加文字（如 "生成中"），与旋转符一起展示 */
  label?: string;
  /** 旋转速度（帧间隔 ms，越小越快，默认 80ms） */
  speed: number;
  /** 自定义 className */
  className?: string;
  /** 是否为 inline 文本模式（默认 true；false 时 flex 居中） */
  inline?: boolean;
  /** 旋转字符与 label 的间距（默认 ml-1.5） */
  labelClassName?: string;
  /** Schema 配置的 aria-label（用于无障碍） */
  'aria-label': string;
}

const SIZE_MAP = {
  sm: 'text-sm',
  md: 'text-base',
  lg: 'text-lg',
  xl: 'text-xl',
};

export function BrailleSpinner({
  size = 'md',
  label,
  speed,
  className,
  inline = true,
  labelClassName,
  'aria-label': ariaLabel,
}: BrailleSpinnerProps) {
  const [frame, setFrame] = useState(0);
  const [reduceMotion, setReduceMotion] = useState(false);

  // 监听 prefers-reduced-motion
  useEffect(() => {
    const mq = window.matchMedia('(prefers-reduced-motion: reduce)');
    setReduceMotion(mq.matches);
    const handler = (e: MediaQueryListEvent) => setReduceMotion(e.matches);
    mq.addEventListener('change', handler);
    return () => mq.removeEventListener('change', handler);
  }, []);

  // 启动帧循环（reduce-motion 时停在第一帧）
  useEffect(() => {
    if (reduceMotion) return;
    const id = setInterval(() => setFrame((f) => (f + 1) % BRAILLE_FRAMES.length), speed);
    return () => clearInterval(id);
  }, [speed, reduceMotion]);

  const char = BRAILLE_FRAMES[frame];

  // 单字符渲染（无 label）
  if (!label) {
    return (
      <span
        role="img"
        aria-label={ariaLabel}
        className={cn(
          'inline-block font-mono tabular-nums',
          'w-[1ch] text-center leading-none',
          'select-none pointer-events-none',
          SIZE_MAP[size],
          className,
        )}
      >
        {char}
      </span>
    );
  }

  // 带 label 渲染
  const Container = inline ? 'span' : 'div';
  return (
    <Container
      role="status"
      aria-label={ariaLabel}
      className={cn(
        'inline-flex items-center',
        inline ? '' : 'justify-center',
        SIZE_MAP[size],
        className,
      )}
    >
      <span
        className="inline-block font-mono tabular-nums w-[1ch] text-center leading-none select-none"
        aria-hidden
      >
        {char}
      </span>
      <span className={cn('ml-1.5', labelClassName)}>{label}</span>
    </Container>
  );
}
