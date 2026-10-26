import { existsSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';
import { afterAll, describe, expect, it } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';

type OfferingIdentity = {
  provider_id: string;
  capability: string;
  enabled: boolean;
  model_id: string;
};

const script = readFileSync(resolve('scripts/test-go-candidate-http.sh'), 'utf8');
const modelAssignment = script
  .split('\n')
  .find((line) => line.startsWith('candidate_model='));
const enabledOpenAIModel = ((catalog.offerings as OfferingIdentity[]).find(
  (offering) =>
    offering.provider_id === 'openai' &&
    offering.capability === 'text-to-image' &&
    offering.enabled,
) ?? { model_id: '' }).model_id;

function slice(startMarker: string, endMarker: string) {
  const start = script.indexOf(startMarker);
  return script.slice(start, script.indexOf(endMarker, start));
}

const promptEnhanceBlock = slice('candidate_enhance_json="$(', '\nfi\n');
const chatSubmitBlock = slice(
  'candidate_chat_submit_json="$(',
  'curl --fail-with-body --silent --show-error --max-time 30',
);

const scratchDirectory = mkdtempSync(join(tmpdir(), 'candidate-model-'));
let captureSequence = 0;

function runBlock(
  block: string,
  response: Record<string, unknown>,
  override?: string,
) {
  const captureFile = join(scratchDirectory, `capture-${captureSequence++}.json`);
  const result = spawnSync(
    'bash',
    [
      '-c',
      `set -euo pipefail
    curl() {
      local candidate_arg
      for candidate_arg in "$@"; do
        case "$candidate_arg" in
          "{"*) printf '%s' "$candidate_arg" >"$TEST_CAPTURE_FILE" ;;
        esac
      done
      printf '%s' "$TEST_RESPONSE_JSON"
    }
    ${modelAssignment}
    ${block}`,
    ],
    {
      encoding: 'utf8',
      env: {
        ...process.env,
        CANDIDATE_MODEL: override ?? '',
        TEST_CAPTURE_FILE: captureFile,
        TEST_RESPONSE_JSON: JSON.stringify(response),
        candidate_owner: 'candidate-model-owner',
        candidate_cookie_jar: '/unused',
        candidate_web_base_url: 'http://fixture.invalid',
        candidate_skill_id: 'candidate-skill',
        candidate_chat_session_id: 'candidate-session',
      },
    },
  );
  const captured = existsSync(captureFile)
    ? readFileSync(captureFile, 'utf8')
    : '';
  return {
    status: result.status,
    stderr: result.stderr,
    body: (captured ? JSON.parse(captured) : null) as Record<
      string,
      unknown
    > | null,
  };
}

const cases: Array<[string, string, Record<string, unknown>]> = [
  ['prompt enhancement', promptEnhanceBlock, {
    prompt: 'candidate prompt enhancement',
    negative_prompt: 'candidate mock negative prompt',
  }],
  ['chat submission', chatSubmitBlock, {
    messageId: 'candidate-message',
    sessionId: 'candidate-session',
  }],
];

afterAll(() => {
  rmSync(scratchDirectory, { recursive: true, force: true });
});

describe('candidate generation model identity', () => {
  it('pins the script default to the enabled OpenAI text-to-image identity', () => {
    expect(enabledOpenAIModel).not.toBe('');
    expect(modelAssignment).toBe(
      `candidate_model="\${CANDIDATE_MODEL:-${enabledOpenAIModel}}"`,
    );
  });

  it('contains no stale gpt-image-2 model literal that no enabled offering carries', () => {
    expect(script).not.toMatch(/gpt-image-2(?!\.)/);
  });

  it.each(cases)(
    '%s propagates the CANDIDATE_MODEL override into the request body',
    (_label, block, response) => {
      const result = runBlock(block, response, 'candidate-override-model');
      expect(result.status, result.stderr).toBe(0);
      expect(result.body?.model ?? result.body?.ai_model).toBe(
        'candidate-override-model',
      );
    },
  );

  it.each(cases)(
    '%s falls back to the declared default model identity',
    (_label, block, response) => {
      const result = runBlock(block, response);
      expect(result.status, result.stderr).toBe(0);
      expect(result.body?.model ?? result.body?.ai_model).toBe(
        enabledOpenAIModel,
      );
    },
  );
});
