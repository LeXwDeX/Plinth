import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';
import { describe, expect, it } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';

// Execute the actual acceptance script's balance block with HTTP replaced by a fixture.
const script = readFileSync(resolve('scripts/test-go-candidate-http.sh'), 'utf8');
const balanceBlock = script.slice(
  script.indexOf('candidate_expected_mock_balance='),
  script.indexOf('candidate_skills_json='),
);
const response = { accounts: [{ provider: 'meshy', balance: 1000 }, { provider: 'tripo', balance: 5000 }, { provider: 'hyper3d', balance: 100 }] };

function check(body: unknown, schema = structuredClone(catalog.application), mock = true) {
  const directory = mkdtempSync(join(tmpdir(), 'provider-balance-acceptance-'));
  const schemaPath = join(directory, 'schema.json');
  try {
    // The catalog application schema exceeds Linux MAX_ARG_STRLEN (128 KiB) as a single
    // execve env value (E2BIG -> spawnSync status null on CI), so pass it via a temp file
    // and read it into the bash variable inside the script (internal bash vars are unbounded).
    writeFileSync(schemaPath, JSON.stringify({ schema }));
    return spawnSync('bash', ['-c', `set -euo pipefail
      candidate_schema_json="$(cat "$TEST_SCHEMA_FILE")"
      curl() { printf '%s' "$TEST_BALANCE_JSON"; }
      ${balanceBlock}`], {
      encoding: 'utf8',
      env: {
        ...process.env,
        TEST_BALANCE_JSON: JSON.stringify(body),
        TEST_SCHEMA_FILE: schemaPath,
        candidate_allow_generation: String(mock),
        candidate_cookie_jar: '/unused',
        candidate_web_base_url: 'http://fixture.invalid',
      },
    });
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}

describe('HTTP acceptance balance protocol', () => {
  it('accepts all configured provider balances in mock mode', () => {
    const result = check(response);
    expect(result.status, result.stderr).toBe(0);
  });
  it.each([
    { accounts: [{ provider: 'meshy', balance: 1000 }] },
    { accounts: [{ provider: 'meshy', balance: 1000 }, { provider: 'meshy', balance: 5000 }] },
    { accounts: [{ provider: 'meshy', balance: 1000 }, { provider: 'tripo', balance: 3 }] },
    { accounts: [{ provider: 'meshy', balance: 1000 }, { provider: 'tripo', error: 'failed' }] },
    { balance: 1000 },
  ])('rejects missing, duplicate, incorrect or failed accounts: %j', (body) => {
    expect(check(body).status).not.toBe(0);
  });
  it('rejects a declared account missing from provider_accounts', () => {
    const schema = structuredClone(catalog.application);
    schema.runtime.http.legacy_provider_balance.accounts[1].account_id = 'unknown';
    expect(check(response, schema).status).not.toBe(0);
  });
  it('accepts live balances without requiring the mock values', () => {
    expect(check({ accounts: [{ provider: 'meshy', balance: 12 }, { provider: 'tripo', balance: 42 }, { provider: 'hyper3d', balance: 7 }] }, undefined, false).status).toBe(0);
  });
});
