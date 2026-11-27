import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

function source(relative: string): string {
  return readFileSync(new URL(relative, import.meta.url), 'utf8');
}

const userAssets = source('./user-assets/page.tsx');
const generations = source('../../components/admin/generations-panel.tsx');
const llm = source('./llm/page.tsx');

// #670: admin tables show product names, not raw capability ids, state enums or ISO strings.
describe('admin tables show product labels', () => {
  it('names the task type and state on the user assets page', () => {
    expect(userAssets).toContain('{capabilityLabels.get(record.taskType) ?? record.taskType}');
    expect(userAssets).not.toMatch(/>\s*\{record\.taskType\}\s*</);
    expect(userAssets).not.toMatch(/>\s*\{record\.taskStatus\}\s*</);
    const badge = userAssets.slice(userAssets.indexOf('className={state.color}'), userAssets.indexOf('</Badge>', userAssets.indexOf('className={state.color}')));
    expect(badge).toContain('{state.label}');
  });

  it('names the task type on the generations panel', () => {
    expect(generations).toContain('{taskTypeLabel(record.taskType)}');
    expect(generations).not.toMatch(/>\s*\{record\.taskType\}\s*</);
  });

  it('formats the LLM check and history times', () => {
    expect(llm).toContain('{formatDateTime(schema, check.checked_at)}');
    expect(llm).toContain('{formatDateTime(schema, version.created_at)}');
    expect(llm).not.toContain('{check.checked_at}');
    expect(llm).not.toContain('{version.created_at}');
  });
});
