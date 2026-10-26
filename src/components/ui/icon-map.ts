import {
  ClipboardCheck, Clapperboard, Printer, Ruler, ScanLine, UnfoldHorizontal, Wrench,
  ArrowLeftRight, Bone, Box, Camera, Circle, Columns2, Cylinder, Eraser, FileBox, FileOutput, GitBranch, Grid3x3, Hand, History,
  Image as ImageGlyph, ImageMinus, ImagePlus, Info, Lasso, Layers, ListChecks, Magnet, Maximize2, Merge, MessageSquarePlus, MousePointer2, Network, PackagePlus,
  PaintBucket, Paintbrush, Palette, Pencil, PersonStanding, Pipette, Puzzle, Redo2, Rotate3d, Scissors, SlidersHorizontal, SprayCan,
  Square, SquareDashed, Type, Undo2, Wallpaper, WandSparkles, ZoomIn, type LucideIcon,
} from 'lucide-react';

/**
 * Turns an icon name from the Product Schema (a lucide kebab-case name such as `wand-sparkles`) into the
 * component. It only maps names to pictures and carries no product meaning: which button uses which icon is
 * declared in the Schema (`canvas.frontend.stage_actions[].icon`, `canvas.frontend.tools[].icon`,
 * `canvas.frontend.panel_tabs.tabs[].icon`, `capabilities[].icon`) and nowhere else.
 * An icon the Schema names that is missing here draws a plain circle rather than breaking the page; the
 * icon-map test fails in that case, so a new Schema icon cannot ship without its entry.
 */
const ICONS: Readonly<Record<string, LucideIcon>> = {
  'clipboard-check': ClipboardCheck,
  clapperboard: Clapperboard,
  printer: Printer,
  ruler: Ruler,
  'scan-line': ScanLine,
  'unfold-horizontal': UnfoldHorizontal,
  wrench: Wrench,
  'arrow-left-right': ArrowLeftRight,
  bone: Bone,
  box: Box,
  camera: Camera,
  'columns-2': Columns2,
  cylinder: Cylinder,
  eraser: Eraser,
  'file-box': FileBox,
  'file-output': FileOutput,
  'git-branch': GitBranch,
  'grid-3x3': Grid3x3,
  hand: Hand,
  history: History,
  image: ImageGlyph,
  'image-minus': ImageMinus,
  'image-plus': ImagePlus,
  info: Info,
  lasso: Lasso,
  layers: Layers,
  'list-checks': ListChecks,
  magnet: Magnet,
  'maximize-2': Maximize2,
  merge: Merge,
  'message-square-plus': MessageSquarePlus,
  'mouse-pointer-2': MousePointer2,
  network: Network,
  'package-plus': PackagePlus,
  'paint-bucket': PaintBucket,
  paintbrush: Paintbrush,
  palette: Palette,
  pencil: Pencil,
  'person-standing': PersonStanding,
  pipette: Pipette,
  puzzle: Puzzle,
  'redo-2': Redo2,
  'rotate-3d': Rotate3d,
  scissors: Scissors,
  'sliders-horizontal': SlidersHorizontal,
  'spray-can': SprayCan,
  square: Square,
  'square-dashed': SquareDashed,
  type: Type,
  'undo-2': Undo2,
  wallpaper: Wallpaper,
  'wand-sparkles': WandSparkles,
  'zoom-in': ZoomIn,
};

/** The icon names this module can draw. */
export const ICON_NAMES: readonly string[] = Object.keys(ICONS);

export function hasIcon(name: string): boolean {
  return Object.hasOwn(ICONS, name);
}

/** The icon for a Schema name; a plain circle for a name this module does not know. */
export function iconByName(name: string): LucideIcon {
  return Object.hasOwn(ICONS, name) ? ICONS[name] : Circle;
}
