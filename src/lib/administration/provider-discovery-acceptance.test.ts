import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';
import { describe, expect, it } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';

const script = readFileSync(resolve('scripts/test-go-candidate-http.sh'), 'utf8');
const block = script.slice(script.indexOf('candidate_provider_route='), script.indexOf('candidate_input_asset_json='));
const condition = { parameter: 'multi_view', operator: 'equals', value: true };
const notice = { when: condition, message_key: 'configured.warning', tone: 'warning' };
const offerings = { offerings: [
  { model_id: 'second', capability: 'image-to-image', estimated_credits: 9, available: false, parameters: [] },
  { model_id: 'first', capability: 'image-to-image', estimated_credits: 3, available: true, parameters: [
    { name: 'ratio', presentation: { surface: 'provider', disabled_when: [condition], notices: [notice] } },
    { name: 'prompt', presentation: { surface: 'prompt' } },
  ] },
] };
const response = { defaultModel: 'first', providers: [
  { id: 'first', estimatedCredits: 3, capabilities: ['image-to-image'], paramSchemas: { 'image-to-image': [{ name: 'ratio', disabled_when: [condition], notices: [notice] }] } },
  { id: 'second', estimatedCredits: 9, capabilities: ['image-to-image'], paramSchemas: { 'image-to-image': [] } },
] };

function check(body: unknown, source = offerings) {
  const directory = mkdtempSync(join(tmpdir(), 'provider-discovery-acceptance-'));
  const schemaPath = join(directory, 'schema.json');
  try {
    // The catalog application schema exceeds Linux MAX_ARG_STRLEN (128 KiB) as a single
    // execve env value (E2BIG -> spawnSync status null on CI), so pass it via a temp file
    // and read it into the bash variable inside the script (internal bash vars are unbounded).
    writeFileSync(schemaPath, JSON.stringify({ schema: catalog.application }));
    return spawnSync('bash', ['-c', `set -euo pipefail
      candidate_schema_json="$(cat "$TEST_SCHEMA_FILE")"
      curl() { case "$*" in *'/api/v1/offerings'*) printf '%s' "$TEST_OFFERINGS_JSON";; *) printf '%s' "$TEST_PROVIDERS_JSON";; esac; }
      ${block}`], { encoding: 'utf8', env: {
      ...process.env,
      TEST_OFFERINGS_JSON: JSON.stringify(source),
      TEST_PROVIDERS_JSON: JSON.stringify(body),
      TEST_SCHEMA_FILE: schemaPath,
      candidate_cookie_jar: '/unused',
      candidate_web_base_url: 'http://fixture.invalid',
    } });
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}

describe('HTTP acceptance provider discovery contract', () => {
  it('accepts the exact declared models regardless of order, count or live availability', () => {
    const result = check(response);
    expect(result.status, result.stderr).toBe(0);
  });
  it.each(['missing', 'extra', 'duplicate', 'credits', 'parameters', 'conditions', 'notices', 'default'])(
    'rejects a mismatched %s projection', (field) => {
      const changed = structuredClone(response);
      if (field === 'missing') changed.providers.pop();
      if (field === 'extra') changed.providers.push({ ...changed.providers[1], id: 'disabled-model' });
      if (field === 'duplicate') changed.providers[1] = structuredClone(changed.providers[0]);
      if (field === 'credits') changed.providers[1].estimatedCredits = 1;
      if (field === 'parameters') changed.providers[0].paramSchemas['image-to-image'] = [];
      if (field === 'conditions') changed.providers[0].paramSchemas['image-to-image'][0].disabled_when = [];
      if (field === 'notices') changed.providers[0].paramSchemas['image-to-image'][0].notices = [];
      if (field === 'default') changed.defaultModel = 'disabled-model';
      expect(check(changed).status).not.toBe(0);
    },
  );
  it('rejects an empty Offering source instead of accepting an empty comparison', () => {
    expect(check({ providers: [] }, { offerings: [] }).status).not.toBe(0);
  });
});
