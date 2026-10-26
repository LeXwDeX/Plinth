import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { expect, it } from 'vitest';
import { paidStepFromTaskRegistered } from './browser-client';
import { useChatStore } from '@/store/chat-store';
import { PaidStepNotes } from '@/components/chat/paid-step-notes';
import { formatNativePrice } from '@/lib/pricing';
it('carries accepted native SSE evidence through the stream store and actual fee notes before refresh', () => {
  const q = { version: 1 as const, unit: 'vendor.credit', unit_label: 'Vendor credits', base_amount: '0.5', extra_amount: '0', total_amount: '0.5' };
  useChatStore.setState({ streamingBuffers: {} });
  const store = useChatStore.getState(); store.initStreaming('s', { content: 'go' });
  for (const [id, nativeQuote] of [['priced', q], ['unknown', null]] as const) {
    const step = paidStepFromTaskRegistered({ type: 'taskRegistered', taskID: id, capability: 'text-to-image', nativeQuote, approvedByPolicy: true });
    expect(step).not.toBeNull(); store.addStreamPaidStep('s', step!);
  }
  const steps = useChatStore.getState().streamingBuffers.s.paidSteps;
  const html = renderToStaticMarkup(<PaidStepNotes steps={steps} creditsText={n => `${n} legacy`} nativeText={v => `Frozen: ${v}`} unknownPriceText="Unknown quote" autoApprovedText="Auto approved" />);
  expect(html).toContain(`Frozen: ${formatNativePrice('0.5', 'Vendor credits')}`);
  expect(html).toContain('Unknown quote'); expect(html).toContain('Auto approved'); expect(html).not.toContain('0 legacy');
  expect(paidStepFromTaskRegistered({ type: 'taskRegistered', taskID: 'free', capability: 'x', estimatedCredits: 0 })?.credits).toBe(0);
  expect(paidStepFromTaskRegistered({ type: 'taskRegistered', taskID: 'none', capability: 'x' })).toBeNull();
});
