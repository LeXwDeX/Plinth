'use client';

import { useMemo } from 'react';

import { ResultDetailDialog } from '@/components/result/result-detail';
import type { ResultTarget } from '@/lib/workspace/result-target';

export interface PreviewAsset {
  name: string;
  mediaType: string;
  /** Safe same-origin URL, already checked by safeAssetURL. */
  href: string;
  detail?: string;
}

type PreviewKind = 'image' | 'video' | 'none';

export function previewKind(mediaType: string): PreviewKind {
  if (mediaType.startsWith('image/')) return 'image';
  if (mediaType.startsWith('video/')) return 'video';
  return 'none';
}

/**
 * 我的资源页的“查看”：上传的资源没有任务，所以是共用的 ResultDetail 的资源模式——预览、
 * 文件信息、下载和全屏。点击面板外面、点击关闭按钮或按 Esc 都会关闭。
 */
export function AssetPreviewDialog({ asset, onClose }: { asset: PreviewAsset | null; onClose: () => void }) {
  const target = useMemo<ResultTarget | null>(() => (asset ? {
    kind: previewKind(asset.mediaType) === 'none' ? 'other' : 'image',
    url: asset.href,
    title: asset.name,
    mediaType: asset.mediaType,
    fileName: asset.name,
    status: 'ready',
    ...(asset.detail ? { detail: asset.detail } : {}),
  } : null), [asset]);
  return <ResultDetailDialog target={target} onClose={onClose} />;
}
