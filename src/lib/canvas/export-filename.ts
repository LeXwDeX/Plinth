import { artboardRatioLabel } from './artboard';

/** "<session title>-<ratio>.png", e.g. "sunset-3x2.png"; the schema fallback name when the session has no title. */
export function exportBoardFilename(
  title: string,
  artboard: { width: number; height: number } | null,
  frontend: { export_filename_fallback: string; export_extension: string; artboard: { label: { common_ratios: string[] } } },
): string {
  const base = title
    .trim()
    // Characters file systems refuse, and whitespace runs.
    .replace(/[\\/:*?"<>|\u0000-\u001f]/g, '')
    .replace(/\s+/g, '-')
    .slice(0, 60);
  const ratio = artboard
    ? artboardRatioLabel(artboard.width, artboard.height, frontend.artboard.label.common_ratios).replace(':', 'x')
    : '';
  const name = [base || frontend.export_filename_fallback, ratio].filter(Boolean).join('-');
  return `${name}${frontend.export_extension}`;
}
