'use client';

import { useProductSchema } from '@/components/product-schema-provider';
import { ToolRail, type ToolHandler } from '@/components/studio/tool-rail';
import { useRailExpanded } from '@/hooks/use-rail-expanded';
import type { ViewerToolId } from '@/lib/workspace/tools';
import type { ViewMode } from '@/lib/workspace/view-mode';

/**
 * 3D 舞台左上角的视图条：贴图 / 白模 / 线框。按钮、图标、名字和悬停说明来自 Schema
 * `canvas.frontend.tools`（rail 为 viewer），画法和 2D 工具栏是同一个 `ToolRail`，展开开关也共用。
 * 白模和线框只在模型加载好、且读得出材质时可用；灰着的时候悬停说明第一句写原因：
 * 还没加载出来（加载中或加载失败）说「还没加载好」，加载好却读不出材质才说「只能看贴图」。
 */
export function Stage3DViews({ mode, onChange, loaded, supported }: { mode: ViewMode; onChange: (mode: ViewMode) => void; loaded: boolean; supported: boolean }) {
  const { text } = useProductSchema();
  const [expanded, setExpanded] = useRailExpanded();
  const why = loaded ? text('ui.studio.stage.view.unsupported') : text('ui.studio.stage.view.not_loaded');
  const handler = (value: ViewMode): ToolHandler => {
    const disabled = value !== 'texture' && !(loaded && supported);
    return { run: () => onChange(value), pressed: mode === value, disabled, reason: disabled ? why : undefined };
  };
  // `Record<ViewerToolId, ...>` makes a missing view mode a compile error.
  const handlers: Record<ViewerToolId, ToolHandler> = { texture: handler('texture'), clay: handler('clay'), wire: handler('wire') };
  return (
    <ToolRail
      rail="viewer"
      label={text('ui.studio.stage.view.label')}
      handlers={handlers}
      expanded={expanded}
      onExpandedChange={setExpanded}
      testId="stage-3d-views"
      itemTestId={(id) => `stage-3d-view-${id}`}
      className="left-6 top-6"
    />
  );
}
