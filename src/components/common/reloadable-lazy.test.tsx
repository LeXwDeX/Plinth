// @vitest-environment jsdom

import { Suspense } from 'react';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { AppErrorBoundary } from '@/components/common/app-error-boundary';
import { RegionErrorBoundary } from '@/components/common/region-error-boundary';
import { reloadableLazy, resetFailedPageCode } from '@/components/common/reloadable-lazy';
import { schemaText, type ProductSchema } from '@/lib/product-schema';

const schema = defaultCatalog.application as unknown as ProductSchema;
const text = (key: string) => schemaText(schema, key);

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema, text }),
}));
vi.mock('@/lib/client-error-report', () => ({ reportClientError: vi.fn() }));

function Page() {
  return <div data-testid="page">page</div>;
}

const loaded = { default: Page };
const missing = () => new TypeError('Failed to fetch dynamically imported module: /assets/page-old.js');

function mount(Route: React.ComponentType) {
  return render(
    <AppErrorBoundary>
      <Suspense fallback={<div data-testid="loading" />}>
        <Route />
      </Suspense>
    </AppErrorBoundary>,
  );
}

async function settle() {
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
}

beforeEach(() => {
  vi.spyOn(console, 'error').mockImplementation(() => {});
});

afterEach(() => {
  cleanup();
  resetFailedPageCode();
  vi.restoreAllMocks();
});

// #511: a tab opened before a release asks for the page code by its old file name; React.lazy keeps a refused import
// for good, so the boundary's retry could never load the page again.
describe('reloadableLazy', () => {
  it('loads the page code once more when the first import fails', async () => {
    const load = vi.fn<() => Promise<typeof loaded>>()
      .mockRejectedValueOnce(missing())
      .mockResolvedValue(loaded);
    mount(reloadableLazy(load));
    await settle();

    expect(screen.getByTestId('page')).toBeTruthy();
    expect(load).toHaveBeenCalledTimes(2);
  });

  it('asks for a page reload when the code still cannot be loaded', async () => {
    const reload = vi.fn();
    vi.stubGlobal('location', { ...window.location, reload });
    const load = vi.fn<() => Promise<typeof loaded>>().mockRejectedValue(missing());
    mount(reloadableLazy(load));
    await settle();

    expect(load).toHaveBeenCalledTimes(2);
    expect(screen.getByText(text('ui.boundary.reload.title'))).toBeTruthy();
    expect(screen.getByText(text('ui.boundary.reload.description'))).toBeTruthy();
    expect(screen.queryByText(text('ui.boundary.error.retry'))).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: text('ui.boundary.reload.action') }));
    expect(reload).toHaveBeenCalledTimes(1);
    vi.unstubAllGlobals();
  });

  it('forgets a refused import, so the next render loads the code again', async () => {
    const load = vi.fn<() => Promise<typeof loaded>>()
      .mockRejectedValueOnce(missing())
      .mockRejectedValueOnce(missing())
      .mockResolvedValue(loaded);
    const Route = reloadableLazy(load);
    const view = mount(Route);
    await settle();
    expect(screen.queryByTestId('page')).toBeNull();

    resetFailedPageCode();
    view.unmount();
    mount(Route);
    await settle();
    expect(screen.getByTestId('page')).toBeTruthy();
    expect(load).toHaveBeenCalledTimes(3);
  });

  it('lets a region\'s reload load the code a refused import left behind', async () => {
    const load = vi.fn<() => Promise<typeof loaded>>()
      .mockRejectedValueOnce(missing())
      .mockRejectedValueOnce(missing())
      .mockResolvedValue(loaded);
    const Route = reloadableLazy(load);
    render(
      <RegionErrorBoundary region="canvas">
        <Suspense fallback={<div data-testid="loading" />}>
          <Route />
        </Suspense>
      </RegionErrorBoundary>,
    );
    await settle();
    expect(screen.getByTestId('region-error-canvas')).toBeTruthy();

    fireEvent.click(screen.getByRole('button', { name: text('ui.boundary.region.retry') }));
    await settle();
    expect(screen.getByTestId('page')).toBeTruthy();
    expect(load).toHaveBeenCalledTimes(3);
  });
});
