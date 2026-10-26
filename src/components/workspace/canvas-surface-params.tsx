'use client';

import { useMemo } from 'react';

import { useProductSchema } from '@/components/product-schema-provider';
import { useWorkspaceOfferings } from '@/hooks/use-workspace-offerings';
import { offeringParamSchemas } from '@/lib/agent-offerings';
import { useWorkspaceStore, type WorkspaceDomain } from '@/store/workspace-store';

/**
 * 当前模型声明为 surface=canvas 的参数 Schema（跨所选 Offering 求交集）。
 * 控件在对话输入区的“参数”弹层里；这里只给画板用它的宽高比决定虚线画板的几何。
 */
export function useCanvasSurfaceParamSchemas(targetDomain?: WorkspaceDomain) {
  const { schema } = useProductSchema();
  // The canvas parameters belong to the 2D board unless a caller asks otherwise.
  const domain = targetDomain ?? 'image';
  const modelByDomain = useWorkspaceStore((state) => state.modelByDomain);
  const { offeringsByCapability } = useWorkspaceOfferings(schema, domain);
  const selectedModelId = modelByDomain[domain];
  const selectedOfferings = useMemo(
    () => Object.values(offeringsByCapability ?? {})
      .flat()
      .filter((item) => item.model_id === selectedModelId),
    [offeringsByCapability, selectedModelId],
  );

  return useMemo(() => {
    const offering = selectedOfferings[0];
    if (!offering) return [];
    return offeringParamSchemas(offering)
      .filter((param) => param.surface === 'canvas')
      .filter((param) =>
        selectedOfferings.every((item) =>
          item.parameters.some((wire) => wire.name === param.name && wire.presentation?.surface === 'canvas'),
        ),
      );
  }, [selectedOfferings]);
}
