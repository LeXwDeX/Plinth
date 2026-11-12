// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { SelfCheckRecordCard } from '@/components/chat/self-check-record';
import type { MessagePicture } from '@/lib/chat/message-pictures';
import type { ProductSchema } from '@/lib/product-schema';
import type { SelfCheckCheck, SelfCheckRecord } from '@/lib/chat/self-check';
import { useStudioStore } from '@/store/studio-store';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = defaultCatalog.application.content.messages as Record<string, string>;

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));
vi.mock('@/components/ui/hint-tooltip', () => ({
  HintTooltip: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock('@/hooks/use-asset-url-rewriter', () => ({
  useAssetURLRewriter: () => ({ previewAssetUrl: (url: string) => (url ? `preview:${url}` : '') }),
}));

const picture = (url: string, index: number, handle?: string): MessagePicture => ({
  image: { url, taskId: 'task-a' }, index, ...(handle ? { handle } : {}),
});

const check = (overrides: Partial<SelfCheckCheck> = {}): SelfCheckCheck => ({
  round: 1, kind: 'inspect', tool: 'inspect_model', assetId: '', views: [], handles: [],
  metrics: null, ok: true, error: '', at: '', ...overrides,
});

const record = (overrides: Partial<SelfCheckRecord> = {}): SelfCheckRecord => ({
  taskId: 'task-a', status: 'passed', roundsUsed: 1, roundsLimit: 3, rendersInRound: 2, rendersLimit: 6,
  correctionsUsed: 1, correctionsLimit: 2,
  checks: [
    check(),
    check({ kind: 'reference', tool: 'compare_model_reference', views: ['front'], handles: ['@img1'], metrics: {
      silhouetteIou: 0.8, bboxAspectDelta: 0.1, dominantColorDistance: 12, dominantReference: '#cc2222',
      dominantRender: '#aa3333', referenceSubjectRatio: 0.2, renderSubjectRatio: 0.2,
    } }),
    check({ kind: 'render', tool: 'render_model_views', views: ['front', 'back'], handles: ['@img2'] }),
  ],
  conclusion: {
    status: 'passed', summary: 'matches the reference', findings: ['outline ok'], fixes: ['none'], remaining: [],
    waiver: '', purposeLabel: '', at: '',
  },
  updatedAt: '',
  ...overrides,
});

const renders = [picture('/api/v1/tasks/t/assets/ref', 0, '@img1'), picture('/api/v1/tasks/t/assets/render', 1, '@img2')];

beforeEach(() => { useStudioStore.setState({ stageImage: null }); });
afterEach(cleanup);

describe('the persisted self-check card (#246)', () => {
  it('shows the three-state badge, the counters and the written conclusion', () => {
    const view = render(<SelfCheckRecordCard records={[record()]} renders={renders} defaultOpen />);
    const item = view.getByTestId('chat-self-check-record-item');
    const badge = within(item).getByTestId('chat-self-check-status');
    expect(badge.textContent).toBe(messages['ui.chat.self_check.status_passed']);
    expect(badge.getAttribute('data-role')).toBe('passed');
    expect(within(item).getByTestId('chat-self-check-rounds').textContent).toBe(
      messages['ui.chat.self_check.rounds'].replace('{used}', '1').replace('{limit}', '3'),
    );
    expect(item.textContent).toContain(messages['ui.chat.self_check.corrections'].replace('{used}', '1').replace('{limit}', '2'));
    expect(item.textContent).toContain(messages['ui.chat.self_check.renders'].replace('{used}', '2').replace('{limit}', '6'));
    expect(within(item).getByTestId('chat-self-check-conclusion').textContent).toContain('matches the reference');
    expect(item.textContent).toContain('outline ok');
    expect(item.textContent).toContain(messages['ui.chat.self_check.no_remaining']);
    expect(view.getByTestId('chat-self-check-caveat').textContent).toContain('GLB');
  });

  it('renders the issues state as a warning, never as passed', () => {
    const view = render(<SelfCheckRecordCard records={[record({
      status: 'has_issues', conclusion: {
        status: 'has_issues', summary: 'one crack', findings: ['crack'], fixes: [], remaining: ['crack on the back'],
        waiver: '', purposeLabel: '', at: '',
      },
    })]} renders={renders} defaultOpen />);
    const badge = view.getByTestId('chat-self-check-status');
    expect(badge.textContent).toBe(messages['ui.chat.self_check.status_has_issues']);
    expect(badge.getAttribute('data-role')).toBe('issues');
    expect(view.getByTestId('chat-self-check-record-item').textContent).toContain('crack on the back');
  });

  it('names the judged purpose and an accepted waiver on the conclusion (#281)', () => {
    const view = render(<SelfCheckRecordCard records={[record({
      conclusion: {
        status: 'passed', summary: 'ok', findings: [], fixes: [], remaining: [],
        waiver: '用户接受面数略超', purposeLabel: '展示/渲染', at: '',
      },
    })]} renders={renders} defaultOpen />);
    expect(view.getByTestId('chat-self-check-purpose').textContent).toBe(
      messages['ui.chat.self_check.purpose'].replace('{purpose}', '展示/渲染'),
    );
    expect(view.getByTestId('chat-self-check-waiver').textContent).toContain('用户接受面数略超');
  });

  it('shows the uncheckable state apart from passed and issues (#281)', () => {
    const view = render(<SelfCheckRecordCard records={[record({
      status: 'not_inspectable', conclusion: {
        status: 'not_inspectable', summary: 'too large', findings: [], fixes: [], remaining: [],
        waiver: '', purposeLabel: '', at: '',
      },
    })]} renders={renders} defaultOpen />);
    const badge = view.getByTestId('chat-self-check-status');
    expect(badge.getAttribute('data-role')).toBe('uncheckable');
    expect(badge.textContent).not.toBe(messages['ui.chat.self_check.status_passed']);
    expect(view.queryByTestId('chat-self-check-waiver')).toBeNull();
  });

  it('degrades an unknown stored status to the declared unchecked state', () => {
    const view = render(<SelfCheckRecordCard records={[record({ status: 'mystery', conclusion: null })]} renders={[]} defaultOpen />);
    const badge = view.getByTestId('chat-self-check-status');
    expect(badge.getAttribute('data-role')).toBe('unchecked');
    expect(badge.textContent).toBe(messages['ui.chat.self_check.status_unchecked']);
  });

  it('folds the card, opens the evidence lines and binds each check to its handle', () => {
    const view = render(<SelfCheckRecordCard records={[record()]} renders={renders} defaultOpen />);
    expect(view.queryByTestId('chat-self-check-details')).toBeNull();
    fireEvent.click(view.getByTestId('chat-self-check-expand'));
    const lines = view.getAllByTestId('chat-self-check-line');
    expect(lines).toHaveLength(3);
    expect(lines[0].getAttribute('data-kind')).toBe('inspect');
    expect(lines[1].textContent).toContain('正面');
    expect(lines[1].textContent).toContain('轮廓 IoU');
    expect(lines[2].textContent).toContain('正面');
    const referenceThumbs = within(lines[1]).getAllByTestId('chat-self-check-thumbnail');
    expect(referenceThumbs).toHaveLength(1);
    expect(referenceThumbs[0].getAttribute('aria-label')).toContain('@img1');

    fireEvent.click(referenceThumbs[0]);
    expect(useStudioStore.getState().stageImage?.url).toBe('/api/v1/tasks/t/assets/ref');

    fireEvent.click(view.getByTestId('chat-self-check-record-toggle'));
    expect(view.queryByTestId('chat-self-check-record-item')).toBeNull();
  });

  it('starts folded to its one title line and opens on click', () => {
    const view = render(<SelfCheckRecordCard records={[record()]} renders={renders} />);
    const toggle = view.getByTestId('chat-self-check-record-toggle');
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    expect(view.queryByTestId('chat-self-check-record-item')).toBeNull();
    fireEvent.click(toggle);
    expect(view.getByTestId('chat-self-check-record-item')).toBeTruthy();
  });

  it('renders nothing when the reply has no record', () => {
    const view = render(<SelfCheckRecordCard records={[]} renders={[]} />);
    expect(view.queryByTestId('chat-self-check-record')).toBeNull();
  });

  it('shows an empty record without inventing evidence', () => {
    const view = render(<SelfCheckRecordCard records={[record({ status: 'unchecked', checks: [], conclusion: null })]} renders={[]} defaultOpen />);
    expect(view.queryByTestId('chat-self-check-expand')).toBeNull();
    expect(view.queryByTestId('chat-self-check-conclusion')).toBeNull();
  });
})