import { describe, expect, it } from 'vitest';

import { RegionErrorBoundary } from '@/components/common/region-error-boundary';

describe('RegionErrorBoundary', () => {
  it('captures an error and remounts the region on reload', () => {
    const error = new Error('boom');
    expect(RegionErrorBoundary.getDerivedStateFromError(error)).toEqual({ error });
    const boundary = new RegionErrorBoundary({ region: 'canvas', children: null });
    expect(boundary.state).toEqual({ error: null, attempt: 0 });
  });
});
