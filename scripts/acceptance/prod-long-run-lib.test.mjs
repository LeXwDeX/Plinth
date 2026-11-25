// node --test scripts/acceptance/prod-long-run-lib.test.mjs（#633）
import assert from 'node:assert/strict';
import test from 'node:test';

import { minimumWaitMs, quoteOf, skillRowIndex } from './prod-long-run-lib.mjs';

test('skillRowIndex picks the row named 四视图, not the 三视图 row whose description mentions it', () => {
  const names = ['三视图', '四视图', '角色设定'];
  assert.equal(skillRowIndex(names, '四视图'), 1);
  assert.equal(skillRowIndex(names, '三视图'), 0);
  assert.equal(skillRowIndex([' 四视图 ', '三视图'], '四视图'), 0);
  // a row whose name starts with the wanted one, when none is equal
  assert.equal(skillRowIndex(['三视图', '四视图（含右侧）'], '四视图'), 1);
  // no substring fallback: 「视图」 is neither a name nor the start of one
  assert.equal(skillRowIndex(names, '视图'), -1);
  assert.equal(skillRowIndex(names, ''), -1);
});

test('quoteOf reads legacy credits and provider-native quotes', () => {
  assert.deepEqual(quoteOf('约 12 积分'), { amount: 12, unit: '积分' });
  assert.deepEqual(quoteOf('12'), { amount: 12, unit: '' });
  assert.deepEqual(quoteOf('已冻结估算：50 Tripo credits'), { amount: 50, unit: 'Tripo credits' });
  assert.deepEqual(quoteOf('Frozen estimate: 1,234.5 Meshy credits'), { amount: 1234.5, unit: 'Meshy credits' });
  assert.ok(Number.isNaN(quoteOf('已冻结估算：未知').amount));
  assert.ok(Number.isNaN(quoteOf('').amount));
});

test('minimumWaitMs waits longer after a clicked button or a chosen option', () => {
  assert.equal(minimumWaitMs({}), 4000);
  assert.equal(minimumWaitMs({ clickButton: '继续生成' }), 45000);
  assert.equal(minimumWaitMs({ choose: '正面' }), 45000);
  assert.equal(minimumWaitMs({ clickButton: '继续生成', 'min-wait': '90' }), 90000);
  assert.equal(minimumWaitMs({ 'min-wait': true }), 4000);
});
