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

import { TaskRefetchPanel } from './task-refetch-panel';

const messages = schema.content.messages;

function markup(data: unknown) {
  return renderToStaticMarkup(
    <SWRConfig value={{ provider: () => new Map(), fallback: { [unstable_serialize(['task-refetch', 'session-1', 'source-1'])]: data } }}>
      <TaskRefetchPanel taskId="source-1" sessionId="session-1" capability="text-to-3d" prompt="stool" onOpenTask={vi.fn()} />
    </SWRConfig>,
  );
}

describe('TaskRefetchPanel', () => {
  it('offers the no-charge refetch with Schema texts', () => {
    testState.schema = schema;
    const html = markup({ sourceTaskId: 'source-1', eligible: true });
    expect(html).toContain('data-testid="task-refetch-start"');
    expect(html).toContain(messages['ui.task.refetch.action']);
    expect(html).toContain(messages['ui.task.refetch.description']);
  });

  it('says how many credits the failed delivery already charged (#330)', () => {
    testState.schema = schema;
    const charged = markup({ sourceTaskId: 'source-1', eligible: true, chargedCredits: 1 });
    expect(charged).toContain('data-testid="task-refetch-charged"');
    expect(charged).toContain(messages['ui.task.refetch.charged_one'].replace('{credits}', '1'));
    const closed = markup({ sourceTaskId: 'source-1', eligible: false, reason: 'expired', chargedCredits: 3 });
    expect(closed).toContain(messages['ui.task.refetch.charged'].replace('{credits}', '3'));
    expect(markup({ sourceTaskId: 'source-1', eligible: true })).not.toContain('task-refetch-charged');
  });

  it('opens the refetch that already runs instead of starting another', () => {
    testState.schema = schema;
    const html = markup({ sourceTaskId: 'source-1', eligible: true, refetchTaskId: 'task-2', refetchState: 'MATERIALIZING' });
    expect(html).toContain('data-testid="task-refetch-open"');
    expect(html).not.toContain('data-testid="task-refetch-start"');
    expect(html).toContain(messages['ui.task.refetch.in_progress']);
  });

  it('explains why a result cannot be fetched again and hides tasks with nothing to fetch', () => {
    testState.schema = schema;
    for (const reason of ['not_supported', 'expired', 'provider_unavailable']) {
      const html = markup({ sourceTaskId: 'source-1', eligible: false, reason });
      expect(html).toContain(`data-reason="${reason}"`);
      expect(html).toContain(messages[`ui.task.refetch.unavailable.${reason}`]);
      expect(html).not.toContain('data-testid="task-refetch-start"');
    }
    expect(markup({ sourceTaskId: 'source-1', eligible: false, reason: 'not_applicable' })).toBe('');
  });

  it('closes a task whose refetch repeated its failure and names the cause', () => {
    testState.schema = schema;
    const html = markup({ sourceTaskId: 'source-1', eligible: false, reason: 'repeated_failure', failureClass: 'integrity_failure' });
    expect(html).toContain('data-reason="repeated_failure"');
    expect(html).toContain(messages['failure.integrity_failure']);
    expect(html).not.toContain('{cause}');
    expect(html).not.toContain('data-testid="task-refetch-start"');
  });

  // #330 second follow-ups, observation 4: a delivered refetch is not eligible any more; the panel opens it.
  it('opens a delivered refetch, never offering another one', () => {
    testState.schema = schema;
    const html = markup({ sourceTaskId: 'source-1', eligible: false, reason: 'delivered', refetchTaskId: 'task-2', refetchState: 'SUCCEEDED' });
    expect(html).toContain('data-testid="task-refetch-open"');
    expect(html).toContain(messages['ui.task.refetch.succeeded']);
    expect(html).not.toContain('data-testid="task-refetch-start"');
    expect(html).not.toContain('data-testid="task-refetch-unavailable"');
  });

  it('stays hidden on a server without the refetch route', () => {
    const oldSchema = structuredClone(schema);
    delete oldSchema.runtime.http.routes.task_refetch;
    testState.schema = oldSchema;
    expect(markup({ sourceTaskId: 'source-1', eligible: true })).toBe('');
    testState.schema = schema;
  });
});
