import { isValidElement, type ReactNode } from 'react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { ModelViewer3D } from './model-viewer-3d';

const harness = vi.hoisted(() => ({
  states: [] as unknown[], cursor: 0, effectCursor: 0,
  effects: [] as Array<{ deps?: readonly unknown[]; cleanup?: () => void }>,
  pending: [] as Array<() => void>,
  loaderKey: Symbol('loader'), loader: {} as Record<symbol, unknown>, report: vi.fn(),
}));

// Exercise the component's async setup and Retry handler without a DOM/WebGL dependency.
vi.mock('react', async original => ({
  ...await original<typeof import('react')>(),
  useState: (initial: unknown) => {
    const index = harness.cursor++;
    if (!(index in harness.states)) harness.states[index] = initial;
    return [harness.states[index], (value: unknown) => {
      harness.states[index] = typeof value === 'function' ? value(harness.states[index]) : value;
    }];
  },
  useRef: (initial: unknown) => ({ current: initial }),
  useEffect: (effect: () => (() => void) | undefined, deps?: readonly unknown[]) => {
    const index = harness.effectCursor++;
    const previous = harness.effects[index];
    if (previous && deps?.every((value, i) => Object.is(value, previous.deps?.[i]))) return;
    harness.pending.push(() => {
      previous?.cleanup?.();
      harness.effects[index] = { deps, cleanup: effect() };
    });
  },
}));
vi.mock('@google/model-viewer', () => ({ ModelViewerElement: {} }));
vi.mock('@google/model-viewer/lib/three-components/Renderer.js', () => ({ Renderer: { singleton: { loader: harness.loader } } }));
vi.mock('@google/model-viewer/lib/three-components/CachingGLTFLoader.js', () => ({ $loader: harness.loaderKey, CachingGLTFLoader: { delete: vi.fn() } }));
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ text: (key: string) => key }) }));
vi.mock('@/hooks/use-asset-url-rewriter', () => ({ useAssetURLRewriter: () => ({ proxyAssetUrl: (src: string) => src }) }));
vi.mock('@/lib/client-error-report', () => ({ reportClientError: harness.report }));

function nodes(node: ReactNode): Array<{ type: unknown; props: Record<string, unknown> }> {
  if (Array.isArray(node)) return node.flatMap(nodes);
  if (!isValidElement<{ children?: ReactNode }>(node)) return [];
  return [{ type: node.type, props: node.props }, ...nodes(node.props.children)];
}

function render() {
  harness.cursor = 0;
  harness.effectCursor = 0;
  const result = nodes(ModelViewer3D({ src: '/model.glb' }));
  harness.pending.splice(0).forEach(effect => effect());
  return result;
}

beforeEach(() => {
  harness.states = []; harness.effects = []; harness.pending = [];
  harness.report.mockClear();
  harness.loader[harness.loaderKey] = undefined;
  vi.stubGlobal('window', {});
});
afterEach(() => { harness.effects.forEach(effect => effect.cleanup?.()); vi.unstubAllGlobals(); });

it.each(['missing', 'throws'])('shows a Schema failure when required setup %s, then retries setup before mounting', async mode => {
  if (mode === 'throws') harness.loader[harness.loaderKey] = { load: vi.fn(), register: () => { throw new Error('private setup diagnostic'); } };
  expect(render().some(node => node.type === 'model-viewer')).toBe(false);
  await vi.waitFor(() => expect(harness.report).toHaveBeenCalledWith('model-viewer-load:unknown', 'Model viewer initialization failed'));
  const failed = render();
  expect(failed.some(node => node.props['data-testid'] === 'model-viewer-error')).toBe(true);
  expect(failed.some(node => node.props['data-testid'] === 'model-viewer-loading')).toBe(false);
  expect(failed.some(node => node.props.children === 'ui.viewer.load_failed_unknown')).toBe(true);
  expect(failed.some(node => node.type === 'model-viewer')).toBe(false);
  const register = vi.fn();
  harness.loader[harness.loaderKey] = { load: vi.fn(), register };
  const retry = failed.find(node => node.props['data-testid'] === 'model-viewer-retry')!;
  (retry.props.onClick as () => void)();
  expect(render().some(node => node.type === 'model-viewer')).toBe(false);
  await vi.waitFor(() => expect(register).toHaveBeenCalledTimes(1));
  await vi.waitFor(() => expect(render().some(node => node.type === 'model-viewer')).toBe(true));
  expect(render().some(node => node.props['data-testid'] === 'model-viewer-error')).toBe(false);
});
