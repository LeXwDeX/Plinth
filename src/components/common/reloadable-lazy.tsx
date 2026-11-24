import { lazy, type ComponentProps, type ComponentType, type LazyExoticComponent } from 'react';

/**
 * #511: the code of a page could not be loaded, even when asked once more. A tab opened before a release asks for the
 * page code by its old file name, which the new release no longer serves; only a reload of the page fixes that. The
 * app error boundary shows its reload notice for this error.
 */
export class PageCodeUnavailableError extends Error {
  readonly reason: unknown;

  constructor(reason: unknown) {
    super(reason instanceof Error ? reason.message : 'page code unavailable');
    this.name = 'PageCodeUnavailableError';
    this.reason = reason;
  }
}

export function isPageCodeUnavailable(error: unknown): error is PageCodeUnavailableError {
  return error instanceof PageCodeUnavailableError;
}

/** The resets of the pages whose code was refused, so the next render asks for it again. */
const refused = new Set<() => void>();

/**
 * Forgets every refused page code. React.lazy keeps a refused import for good, so without this the error boundary's
 * retry would throw the same error again; after it the next render imports the code anew.
 */
export function resetFailedPageCode(): void {
  for (const reset of [...refused]) reset();
}

/**
 * React.lazy for a page whose code may be gone: a failed import is tried once more at once (a passing network
 * failure), and a second failure is a `PageCodeUnavailableError`. The refused import is not kept:
 * `resetFailedPageCode` lets the next render load the code again.
 */
// The same constraint as React.lazy's own.
export function reloadableLazy<Page extends ComponentType<any>>(
  load: () => Promise<{ default: Page }>,
): ComponentType<ComponentProps<Page>> {
  const attempt = () => load()
    .catch(() => load())
    .catch((reason: unknown): never => {
      refused.add(reset);
      throw new PageCodeUnavailableError(reason);
    });
  let current: LazyExoticComponent<Page> = lazy(attempt);
  function reset() {
    refused.delete(reset);
    current = lazy(attempt);
  }
  function ReloadablePage(props: ComponentProps<Page>) {
    const Current = current as ComponentType<ComponentProps<Page>>;
    return <Current {...props} />;
  }
  return ReloadablePage;
}
