import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { SWRConfig, unstable_serialize } from 'swr';
import { describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';

const schema = defaultCatalog.application as unknown as ProductSchema;
const testState: { schema: ProductSchema } = { schema };
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: testState.schema, text: (key: string) => key }) }));
vi.mock('@/store/workspace-store', () => ({ useWorkspaceStore: (selector: (state: { registerTask: () => void }) => unknown) => selector({ registerTask: vi.fn() }) }));

import { TaskRecoveryPanel } from './task-recovery-panel';

const eligible = {
  sourceTaskId: 'source-1', eligible: true, receiptDigest: 'secret-receipt-digest', sessionId: 'session-1',
  capability: 'asset-recovery', idempotencyKey: 'secret-idempotency-key',
};
const text = schema.task_history.asset_recovery!;

function markup(data: unknown) {
  return renderToStaticMarkup(
    <SWRConfig value={{ provider: () => new Map(), fallback: { [unstable_serialize(['task-recovery', 'session-1', 'source-1'])]: data } }}>
      <TaskRecoveryPanel taskId="source-1" sessionId="session-1" capability="text-to-3d" prompt="chair" onOpenTask={vi.fn()} />
    </SWRConfig>,
  );
}

describe('TaskRecoveryPanel', () => {
  it('shows only the Schema-described eligible action and keeps receipt details private', () => {
    testState.schema = schema;
    const html = markup(eligible);
    expect(html).toContain('data-testid="task-recovery-panel"');
    expect(html).toContain(text.action);
    expect(html).toContain(text.description);
    expect(html).toContain('data-testid="task-recovery-prepare"');
    expect(html).not.toContain('secret-receipt-digest');
    expect(html).not.toContain('secret-idempotency-key');
  });

  it('hides ineligible recovery and obeys the optional Schema block on older servers', () => {
    testState.schema = schema;
    expect(markup({ sourceTaskId: 'source-1', eligible: false })).toBe('');
    const oldSchema = structuredClone(schema);
    delete oldSchema.task_history.asset_recovery;
    testState.schema = oldSchema;
    expect(markup(eligible)).toBe('');
    testState.schema = schema;
  });
});
