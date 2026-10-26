'use client';

/**
 * 通用缩略图单元格组件
 *
 * 用于管理后台表格中展示缩略图预览，带 onerror 回退占位符。
 * generations-panel / user-assets 等 admin 组件共享此组件，不得各自复制。
 *
 * 架构层级：UI 层 — 通用展示组件
 */

import { useState } from 'react';
import { Image as ImageIcon } from 'lucide-react';
import { useProductSchema } from '@/components/product-schema-provider';

interface ThumbnailCellProps {
  /** 缩略图 src（null 或空则显示占位图标） */
  src: string | null;
  /** 自定义尺寸 class，默认 w-10 h-10 */
  sizeClass?: string;
}

/** 缩略图单元格，带 onerror 回退 */
export function ThumbnailCell({ src, sizeClass = 'w-10 h-10' }: ThumbnailCellProps) {
  const [failed, setFailed] = useState(false);
  const { text } = useProductSchema();

  if (!src || failed) {
    return (
      <div className={`${sizeClass} rounded bg-muted/50 flex items-center justify-center shrink-0`}>
        <ImageIcon className="w-4 h-4 text-muted-foreground/40" />
      </div>
    );
  }

  return (
    <img
      src={src}
      alt={text('ui.common.preview')}
      className={`${sizeClass} rounded object-cover bg-muted/30 shrink-0`}
      loading="lazy"
      onError={() => setFailed(true)}
    />
  );
}
