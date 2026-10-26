import { safeAssetURL } from '@/lib/core/tasks/asset-url-rewriter';
import type { VersionTile } from '@/lib/workspace/versions';

/**
 * 批量下载：把所选版本的文件在浏览器里打成一个 ZIP。只取平台自己的资产路由
 * （同源的 `/api/…`），不会去请求任何外部地址；一个文件取不到不拖垮整个包。
 */

export interface ExportEntry {
  name: string;
  url: string;
}

const EXTENSIONS: Record<string, string> = {
  'image/png': 'png',
  'image/jpeg': 'jpg',
  'image/webp': 'webp',
  'image/gif': 'gif',
  'model/gltf-binary': 'glb',
  'model/gltf+json': 'gltf',
};

export function extensionOf(mediaType: string): string {
  const known = EXTENSIONS[mediaType.toLowerCase()];
  if (known) return known;
  const subtype = mediaType.split('/')[1] ?? '';
  const cleaned = subtype.replace(/[^a-z0-9]/gi, '').toLowerCase();
  return cleaned || 'bin';
}

/** 同源的平台资产路由；外部地址、协议相对地址和 blob 都不行。 */
export function isPlatformAssetUrl(url: string): boolean {
  const safe = safeAssetURL(url);
  return safe.startsWith('/api/');
}

function fileSafe(name: string): string {
  return name.replace(/[\\/:*?"<>|\u0000-\u001f]/g, '-').replace(/\s+/g, '-').replace(/-+/g, '-');
}

export interface ExportOptions {
  /**
   * `all` (default): a picture version brings every candidate. `chosen`: one file per
   * version, the candidate the person picked (see `chosenAssetId`), so four versions
   * selected is four files.
   */
  scope?: 'all' | 'chosen';
  /** The candidate each version stands for; unknown or absent falls back to the one the stage would show. */
  chosenAssetId?: (tile: VersionTile) => string | undefined;
}

/** The picture a version stands for: the picked one, else its source slot (front view), else the first. */
export function representativeAsset(tile: VersionTile, assets: readonly VersionTile['assets'][number][], chosenAssetId?: string) {
  const picked = chosenAssetId ? assets.find((asset) => asset.id === chosenAssetId) : undefined;
  if (picked) return picked;
  const front = tile.slots.find((slot) => slot.taskId === tile.taskId) ?? tile.slots[0];
  return (front ? assets.find((asset) => asset.id === front.assetId) : undefined) ?? assets[0];
}

/**
 * 所选版本要打进包里的文件，按版本条的顺序。图片版本带它的候选图（`scope: 'chosen'` 时
 * 只带选用的那一张），3D 版本带模型文件；还没完成的版本和不是平台资产路由的文件跳过。
 * 文件名是版本编号（一个版本有几张候选时再加候选序号），同名自动加序号。
 */
export function planBatchExport(
  tiles: readonly VersionTile[],
  selected: ReadonlySet<string>,
  labelOf: (tile: VersionTile) => string,
  options: ExportOptions = {},
): ExportEntry[] {
  const entries: ExportEntry[] = [];
  const used = new Set<string>();
  for (const tile of tiles) {
    if (!selected.has(tile.key) || tile.status !== 'ready') continue;
    const wanted = tile.domain === 'model' ? 'model' : 'image';
    const all = tile.assets.filter((asset) => asset.kind === wanted && isPlatformAssetUrl(asset.url));
    const assets = options.scope === 'chosen'
      ? [representativeAsset(tile, all, options.chosenAssetId?.(tile))].filter((asset): asset is NonNullable<typeof asset> => asset !== undefined)
      : all;
    assets.forEach((asset) => {
      const number = all.indexOf(asset) + 1;
      const base = fileSafe(labelOf(tile)) + (all.length > 1 ? `-${number}` : '');
      const extension = extensionOf(asset.mediaType);
      let name = `${base}.${extension}`;
      for (let copy = 2; used.has(name); copy += 1) name = `${base}-${copy}.${extension}`;
      used.add(name);
      entries.push({ name, url: safeAssetURL(asset.url) });
    });
  }
  return entries;
}

export interface ArchiveSummary {
  /** 打进包里的文件数。 */
  added: number;
  /** 没取到的文件数。 */
  failed: number;
}

export interface Archive extends ArchiveSummary {
  blob: Blob;
}

/** 逐文件进度；`done` 含取不到的文件，`bytes` 是已取回的字节数。 */
export interface ArchiveProgress {
  done: number;
  total: number;
  bytes: number;
}

type Fetcher = (url: string) => Promise<Response>;

const sameOriginFetcher: Fetcher = (url) => fetch(url, { credentials: 'same-origin' });

// The archives are STORE-only, built here instead of in JSZip so the page
// writes one entry at a time: no second full-size copy on the JS heap and no
// main-thread compression pass. Timestamps are fixed so the same selection
// produces the same bytes.
const ZIP_STORED = 0;
const ZIP_VERSION = 20;
const ZIP_UTF8_FLAG = 0x0800;
const ZIP_DOS_DATE_1980 = 0x0021;
const ZIP_SIGNATURE_LOCAL = 0x04034b50;
const ZIP_SIGNATURE_CENTRAL = 0x02014b50;
const ZIP_SIGNATURE_END = 0x06054b50;
const ZIP_MAX_OFFSET = 0xffffffff;
const ZIP_MAX_ENTRIES = 0xffff;

let crcTable: Uint32Array | null = null;

function crc32(data: Uint8Array): number {
  if (!crcTable) {
    const table = new Uint32Array(256);
    for (let index = 0; index < 256; index += 1) {
      let value = index;
      for (let bit = 0; bit < 8; bit += 1) value = value & 1 ? 0xedb88320 ^ (value >>> 1) : value >>> 1;
      table[index] = value >>> 0;
    }
    crcTable = table;
  }
  const table = crcTable;
  let crc = 0xffffffff;
  for (let index = 0; index < data.length; index += 1) crc = table[(crc ^ data[index]) & 0xff] ^ (crc >>> 8);
  return (crc ^ 0xffffffff) >>> 0;
}

function zipName(name: string): Uint8Array {
  return new TextEncoder().encode(name);
}

/** One local file header followed by the stored bytes; sizes and CRC are known before it is written. */
function localFileHeader(name: Uint8Array, crc: number, size: number): Uint8Array {
  const header = new Uint8Array(30 + name.length);
  const view = new DataView(header.buffer);
  view.setUint32(0, ZIP_SIGNATURE_LOCAL, true);
  view.setUint16(4, ZIP_VERSION, true);
  view.setUint16(6, ZIP_UTF8_FLAG, true);
  view.setUint16(8, ZIP_STORED, true);
  view.setUint16(10, 0, true);
  view.setUint16(12, ZIP_DOS_DATE_1980, true);
  view.setUint32(14, crc, true);
  view.setUint32(18, size, true);
  view.setUint32(22, size, true);
  view.setUint16(26, name.length, true);
  view.setUint16(28, 0, true);
  header.set(name, 30);
  return header;
}

function centralDirectoryHeader(name: Uint8Array, crc: number, size: number, offset: number): Uint8Array {
  const header = new Uint8Array(46 + name.length);
  const view = new DataView(header.buffer);
  view.setUint32(0, ZIP_SIGNATURE_CENTRAL, true);
  view.setUint16(4, ZIP_VERSION, true);
  view.setUint16(6, ZIP_VERSION, true);
  view.setUint16(8, ZIP_UTF8_FLAG, true);
  view.setUint16(10, ZIP_STORED, true);
  view.setUint16(12, 0, true);
  view.setUint16(14, ZIP_DOS_DATE_1980, true);
  view.setUint32(16, crc, true);
  view.setUint32(20, size, true);
  view.setUint32(24, size, true);
  view.setUint16(28, name.length, true);
  view.setUint16(30, 0, true);
  view.setUint16(32, 0, true);
  view.setUint16(34, 0, true);
  view.setUint16(36, 0, true);
  view.setUint32(38, 0, true);
  view.setUint32(42, offset, true);
  header.set(name, 46);
  return header;
}

function endOfCentralDirectory(count: number, centralSize: number, centralOffset: number): Uint8Array {
  const end = new Uint8Array(22);
  const view = new DataView(end.buffer);
  view.setUint32(0, ZIP_SIGNATURE_END, true);
  view.setUint16(8, count, true);
  view.setUint16(10, count, true);
  view.setUint32(12, centralSize, true);
  view.setUint32(16, centralOffset, true);
  return end;
}

function zipLimitError(): Error {
  return new Error('ZIP64 is not supported; select fewer or smaller files');
}

/** 取回每个文件并打包（图片已经压缩过，不再压一次）。全部取不到时返回 null。 */
export async function buildArchive(
  entries: readonly ExportEntry[],
  fetcher: Fetcher = sameOriginFetcher,
  concurrency = 4,
  onProgress?: (progress: ArchiveProgress) => void,
): Promise<Archive | null> {
  const files: (FetchedFile | null)[] = new Array(entries.length).fill(null);
  let failed = 0;
  let completed = 0;
  let bytes = 0;
  let cursor = 0;
  const worker = async () => {
    while (cursor < entries.length) {
      const index = cursor;
      cursor += 1;
      const entry = entries[index];
      try {
        const response = await fetcher(entry.url);
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        const data = new Uint8Array(await response.arrayBuffer());
        files[index] = { name: entry.name, bytes: data, crc: crc32(data) };
      } catch {
        failed += 1;
      }
      completed += 1;
      bytes += files[index]?.bytes.byteLength ?? 0;
      onProgress?.({ done: completed, total: entries.length, bytes });
    }
  };
  await Promise.all(Array.from({ length: Math.min(concurrency, entries.length) }, worker));
  const present = files.filter((file): file is FetchedFile => file !== null);
  if (present.length === 0) return null;
  if (present.length > ZIP_MAX_ENTRIES) throw zipLimitError();
  const parts: BlobPart[] = [];
  const central: Uint8Array[] = [];
  let offset = 0;
  for (const file of present) {
    const name = zipName(file.name);
    const header = localFileHeader(name, file.crc, file.bytes.byteLength);
    if (offset + header.byteLength + file.bytes.byteLength > ZIP_MAX_OFFSET) throw zipLimitError();
    parts.push(header as BlobPart, file.bytes as BlobPart);
    central.push(centralDirectoryHeader(name, file.crc, file.bytes.byteLength, offset));
    offset += header.byteLength + file.bytes.byteLength;
  }
  const centralOffset = offset;
  let centralSize = 0;
  for (const header of central) {
    parts.push(header as BlobPart);
    centralSize += header.byteLength;
  }
  if (centralOffset + centralSize > ZIP_MAX_OFFSET) throw zipLimitError();
  parts.push(endOfCentralDirectory(present.length, centralSize, centralOffset) as BlobPart);
  return { blob: new Blob(parts, { type: 'application/zip' }), added: present.length, failed };
}

interface FetchedFile {
  name: string;
  bytes: Uint8Array;
  crc: number;
}

interface ArchiveWritable {
  write(data: BlobPart): Promise<void>;
  close(): Promise<void>;
  abort(): Promise<void>;
}

interface ArchiveFileHandle {
  createWritable(): Promise<ArchiveWritable>;
}

interface SaveFilePickerOptions {
  suggestedName?: string;
  types?: Array<{ description: string; accept: Record<string, string[]> }>;
}

type SaveFilePicker = (options: SaveFilePickerOptions) => Promise<ArchiveFileHandle>;

function archivePicker(): SaveFilePicker | undefined {
  if (typeof window === 'undefined') return undefined;
  return (window as Window & { showSaveFilePicker?: SaveFilePicker }).showSaveFilePicker;
}

/**
 * 浏览器是否能在保存时边取边写（File System Access API）。它只在安全上下文
 * 可用：HTTPS 或 localhost 为 true，HTTP 测试栈为 false，此时退回
 * `buildArchive` 生成浏览器托管的 Blob。
 */
export function canSaveArchiveToDisk(): boolean {
  return typeof archivePicker() === 'function';
}

/**
 * 逐个文件取回并直接写进用户选定的文件，包里任意时刻只在内存里保留一个
 * 文件；进度按文件回报。返回 null 表示浏览器不支持流式保存（调用方应退回
 * `buildArchive`），或在保存对话框里取消。
 */
export async function saveArchiveToDisk(
  entries: readonly ExportEntry[],
  filename: string,
  onProgress?: (progress: ArchiveProgress) => void,
  fetcher: Fetcher = sameOriginFetcher,
): Promise<ArchiveSummary | null> {
  const picker = archivePicker();
  if (!picker) return null;
  const handle = await picker({
    suggestedName: filename,
    types: [{ description: 'ZIP', accept: { 'application/zip': ['.zip'] } }],
  });
  const writable = await handle.createWritable();
  try {
    const central: Uint8Array[] = [];
    let added = 0;
    let failed = 0;
    let completed = 0;
    let bytes = 0;
    let offset = 0;
    for (const entry of entries) {
      let data: Uint8Array | null = null;
      try {
        const response = await fetcher(entry.url);
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        data = new Uint8Array(await response.arrayBuffer());
      } catch {
        failed += 1;
      }
      if (data) {
        const name = zipName(entry.name);
        const crc = crc32(data);
        const header = localFileHeader(name, crc, data.byteLength);
        if (offset + header.byteLength + data.byteLength > ZIP_MAX_OFFSET) throw zipLimitError();
        await writable.write(header as BlobPart);
        await writable.write(data as BlobPart);
        central.push(centralDirectoryHeader(name, crc, data.byteLength, offset));
        offset += header.byteLength + data.byteLength;
        added += 1;
        bytes += data.byteLength;
      }
      completed += 1;
      onProgress?.({ done: completed, total: entries.length, bytes });
    }
    if (added === 0) {
      await writable.abort();
      return null;
    }
    if (added > ZIP_MAX_ENTRIES) throw zipLimitError();
    const centralOffset = offset;
    let centralSize = 0;
    for (const header of central) {
      await writable.write(header as BlobPart);
      centralSize += header.byteLength;
    }
    if (centralOffset + centralSize > ZIP_MAX_OFFSET) throw zipLimitError();
    await writable.write(endOfCentralDirectory(added, centralSize, centralOffset) as BlobPart);
    await writable.close();
    return { added, failed };
  } catch (cause) {
    await writable.abort().catch(() => undefined);
    throw cause;
  }
}

/** 把包交给浏览器保存。链接先挂到页面上再点（有的浏览器不挂载就不认文件名），稍后再释放。 */
export function saveArchive(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = filename;
  anchor.hidden = true;
  document.body.append(anchor);
  anchor.click();
  anchor.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}
