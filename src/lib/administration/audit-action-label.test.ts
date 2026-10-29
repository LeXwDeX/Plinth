import { describe, expect, it } from 'vitest';
import { auditActionLabel, auditActionLabels } from './audit-action-label';

describe('auditActionLabel', () => {
  const zh = auditActionLabels([{ value: 'download_asset', label: '下载资产' }]);
  const en = auditActionLabels([{ value: 'download_asset', label: 'Download asset' }]);

  it('shows the Schema label of the active locale for a known key', () => {
    expect(auditActionLabel(zh, 'download_asset')).toBe('下载资产');
    expect(auditActionLabel(en, 'download_asset')).toBe('Download asset');
  });

  it('falls back to the raw key only for an unknown key', () => {
    expect(auditActionLabel(zh, 'future_action')).toBe('future_action');
    expect(auditActionLabel({ empty: '' }, 'empty')).toBe('empty');
  });
});
