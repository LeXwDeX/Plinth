import { describe, expect, it } from 'vitest';

import {
  AppErrorBoundary,
} from '@/components/common/app-error-boundary';

describe('AppErrorBoundary', () => {
  it('derives error state from a thrown error', () => {
    const error = new Error('boom');
    expect(AppErrorBoundary.getDerivedStateFromError(error)).toEqual({
      error,
    });
  });

  it('starts without an error state', () => {
    const boundary = new AppErrorBoundary({ children: null });
    expect(boundary.state).toEqual({ error: null });
  });
});
