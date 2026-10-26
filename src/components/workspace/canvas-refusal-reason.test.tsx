import React from 'react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { BackgroundRemovalDialog } from '@/components/workspace/canvas-image-actions';
import { configureErrorReference } from '@/lib/problem-reference';

/**
 * #288：服务端拒绝准备时，若错误体标了 user_facing，对话框显示这条 Schema 原因（例如参考图超出像素上限），
 * 不再显示通用的「去背景失败」；没有该标记时仍显示通用文案，不把服务端的通用说明或 HTTP 状态直显。
 * 不用 DOM：保留 hook 状态，直接调用提交按钮的 onClick，再重新渲染读出提示。
 */

const fixture = vi.hoisted(() => ({ states: [] as unknown[], cursor: 0 }));
vi.mock('react', async (original) => {
  const actual = await original<typeof import('react')>();
  return {
    ...actual,
    useEffect: () => {},
    useMemo: (fn: () => unknown) => fn(),
    useState: (initial: unknown) => {
      const index = fixture.cursor++;
      if (!(index in fixture.states)) fixture.states[index] = typeof initial === 'function' ? (initial as () => unknown)() : initial;
      return [fixture.states[index], (value: unknown) => {
        fixture.states[index] = typeof value === 'function' ? (value as (current: unknown) => unknown)(fixture.states[index]) : value;
      }];
    },
  };
});

const messages = (defaultCatalog.application as { content?: { messages?: Record<string, string> } }).content?.messages ?? {};
vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application, text: (key: string) => messages[key] ?? key }),
}));
vi.mock('@/store/chat-store', () => ({ useChatStore: (select: (state: unknown) => unknown) => select({ activeSessionId: 's1' }) }));
vi.mock('@/store/workspace-store', () => ({ useWorkspaceStore: (select: (state: unknown) => unknown) => select({ registerTask: vi.fn() }) }));
vi.mock('swr', () => ({
  default: () => ({
    data: [{
      id: 'remover', revision: '1', provider_id: 'p', provider_name: 'P', model_id: 'm', model_name: 'M',
      capability: 'background-removal', available: true,
      parameters: [{ name: 'image', type: 'asset', semantic: 'reference-image', label: '图片', required: true }],
    }],
  }),
}));
vi.mock('@/components/ui/dialog', () => ({
  Dialog: ({ children }: { children?: React.ReactNode }) => <>{children}</>,
  DialogContent: ({ children }: { children?: React.ReactNode }) => <>{children}</>,
  DialogTitle: ({ children }: { children?: React.ReactNode }) => <h3>{children}</h3>,
}));

type Props = Record<string, unknown> & { children?: React.ReactNode };
function elements(node: React.ReactNode): React.ReactElement<Props>[] {
  if (Array.isArray(node)) return node.flatMap(elements);
  if (!React.isValidElement<Props>(node)) return [];
  return [node, ...elements(node.props.children)];
}
function render() {
  fixture.cursor = 0;
  return elements(BackgroundRemovalDialog({ imageUrl: '/api/v1/assets/source-1', uploadBlob: vi.fn(), onClose: vi.fn() }));
}
function text(node: React.ReactNode): string {
  if (typeof node === 'string' || typeof node === 'number') return String(node);
  if (Array.isArray(node)) return node.map(text).join('');
  return React.isValidElement<Props>(node) ? text(node.props.children) : '';
}
async function prepareAndReadAlert(): Promise<string> {
  const prepare = render().find((node) => node.props.loading !== undefined && typeof node.props.onClick === 'function')!;
  (prepare.props.onClick as () => void)();
  await vi.waitFor(() => expect(fixture.states.some((state) => typeof state === 'string' && state !== '')).toBe(true));
  return text(render().find((node) => node.props.role === 'alert'));
}

const reason = '「图片」的图片为 8192×8192，超过当前模型的上限 36000000 像素，这一步没有提交。';
function answer(body: Record<string, unknown>) {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(body), {
    status: 400, headers: { 'Content-Type': 'application/json' },
  })));
}

beforeEach(() => {
  fixture.states = [];
  vi.spyOn(console, 'error').mockImplementation(() => {});
  configureErrorReference('（错误编号 {id}）');
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  configureErrorReference(undefined);
});

it('shows the server refusal reason instead of the generic failure', async () => {
  answer({ code: 'invalid_input', detail: reason, user_facing: true, request_id: 'req_0123abcd' });
  const alert = await prepareAndReadAlert();
  expect(alert).toBe(`${reason}（错误编号 req_0123abcd）`);
  expect(alert).not.toContain(messages['ui.canvas.bgremoval.failed']);
});

it('keeps the generic failure when the server gives no user-facing reason', async () => {
  answer({ code: 'invalid_input', detail: messages['error.invalid_input'], request_id: 'req_0123abcd' });
  const alert = await prepareAndReadAlert();
  expect(alert).toBe(`${messages['ui.canvas.bgremoval.failed']}（错误编号 req_0123abcd）`);
  expect(alert).not.toContain(messages['error.invalid_input']);
});
