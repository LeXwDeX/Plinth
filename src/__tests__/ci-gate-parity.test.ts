import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';

import { describe, expect, it } from 'vitest';
import { isAlias, parseDocument, visit } from 'yaml';

/**
 * 门禁对齐守门（#257）。dev_test.md §2「自动化门禁项」是门禁的唯一真源：每项写明
 * 门禁名「…」与执行位置（本地 + CI / 仅本地 / 仅 CI）。这里要求：
 * - 标为 CI 的每项命令逐字出现在恰好一个 CI 作业里，同一项的命令在同一作业、按 §2 顺序；
 *   CI 作业里没有 §2 以外的命令；CI 只有 GitHub Actions（.github/workflows/ci.yml），不构建镜像（#257）；
 * - 本地入口（scripts/verify-local.py）默认集合 GATES 的门禁名与顺序等于标为本地的各项，
 *   --race 加跑的 RACE_GATE 等于唯一标为仅 CI 的一项；
 * - 本地入口、主机 CI（scripts/host-ci）与 ci.yml 的命令参数一致（#86）：本地命令只能比 §2 多出
 *   已登记的放宽项（单包超时、关闭增量、丢弃构建产物），环境变量一致；主机 CI 跑完整的
 *   `verify-local.py --race`，其 GOFLAGS 不得带入跳过或削弱测试的开关；
 * - ci.yml 用 yaml 库结构化解析（#86）：引号键、流式 `{}`、锚点别名等写法都归一成同一结构，
 *   门禁步骤上出现 if、continue-on-error、shell、env 等字段，或作业级 if/needs/strategy，都会被拒绝。
 * 其余检查（运行器路由、并发分组、权限）仍按文本解析，只依赖这些文件现有的固定写法；写法变了先改这里的解析。
 */
const read = (path: string) => readFileSync(path, 'utf8');

const GATE_COMMAND = /^(?:pnpm |node |go |CGO_ENABLED=|bash scripts\/)/;
const PLACEMENTS = { '本地 + CI': { local: true, ci: true }, 仅本地: { local: true, ci: false }, '仅 CI': { local: false, ci: true } };

interface Gate {
  name: string;
  local: boolean;
  ci: boolean;
  commands: string[];
}

function devTestGates(): Gate[] {
  const text = read('dev_test.md');
  const start = text.indexOf('### 自动化门禁项');
  const end = text.indexOf('\n### ', start + 1);
  expect(start).toBeGreaterThan(-1);
  return text
    .slice(start, end)
    .split('\n')
    .filter((line) => /^\d+\. /.test(line))
    .map((line) => {
      const head = /^\d+\. \*\*[^*]+\*\*「([^」]+)」（([^）]+)）：/.exec(line);
      expect(head, line).not.toBeNull();
      const placement = PLACEMENTS[head![2] as keyof typeof PLACEMENTS];
      expect(placement, `${head![1]}: unknown placement ${head![2]}`).toBeDefined();
      const commands = [...line.matchAll(/`([^`]+)`/g)]
        .map((m) => m[1])
        .filter((span) => GATE_COMMAND.test(span));
      expect(commands.length, head![1]).toBeGreaterThan(0);
      return { name: head![1], ...placement, commands };
    });
}

interface LocalGate {
  name: string;
  argv: string[];
  env: Record<string, string>;
}

function verifyLocal(): { gates: LocalGate[]; race: LocalGate } {
  const output = execFileSync('python3', ['scripts/verify-local.py', '--list-gates'], { encoding: 'utf8' });
  return JSON.parse(output);
}

/** Flags the local gate adds on top of dev_test.md §2 (each is documented there): they only relax a limit or skip an artifact. */
const LOCAL_ONLY_FLAGS: string[][] = [['--incremental', 'false'], ['-timeout=30m'], ['-o', '/dev/null']];

function docCommand(command: string): { env: Record<string, string>; argv: string[] } {
  const tokens = command.split(' ');
  const env: Record<string, string> = {};
  while (tokens.length && /^[A-Z_]+=/.test(tokens[0])) {
    const [key, value] = tokens.shift()!.split('=');
    env[key] = value;
  }
  return { env, argv: tokens };
}

function stripLocalOnly(argv: string[]): string[] {
  const rest = [...argv];
  for (const flag of LOCAL_ONLY_FLAGS) {
    const at = rest.findIndex((_, index) => flag.every((part, offset) => rest[index + offset] === part));
    if (at > -1) rest.splice(at, flag.length);
  }
  return rest;
}

/** Differences between a local gate and the (first) command §2 documents for it. */
function localArgvProblems(local: LocalGate, gate: Gate): string[] {
  const doc = docCommand(gate.commands[0]);
  const problems: string[] = [];
  if (JSON.stringify(stripLocalOnly(local.argv)) !== JSON.stringify(doc.argv)) {
    problems.push(`${gate.name}: local argv [${local.argv.join(' ')}] differs from §2 [${doc.argv.join(' ')}] beyond the registered local-only flags`);
  }
  if (JSON.stringify(local.env) !== JSON.stringify(doc.env)) {
    problems.push(`${gate.name}: local env ${JSON.stringify(local.env)} differs from §2 ${JSON.stringify(doc.env)}`);
  }
  return problems;
}

/**
 * Runner routing (#79 phase 1): push, pull_request and a default manual dispatch stay on
 * GitHub-hosted runners. Only an explicit workflow_dispatch with use_self_hosted=true
 * selects the repository-level trusted label set. This is a routing rule, not an isolation
 * boundary: a pull request can still edit this workflow.
 */
const runnerExpression = '${{ github.event_name == \'workflow_dispatch\' && inputs.use_self_hosted && fromJSON(\'["self-hosted","Linux","X64","plinth-trusted-ci"]\') || \'ubuntu-latest\' }}';
const concurrencyGroup = "ci-${{ github.workflow }}-${{ github.event_name == 'workflow_dispatch' && inputs.use_self_hosted && 'trusted-' || '' }}${{ github.event.pull_request.number || github.ref }}";

function hasRunnerRoute(text: string): boolean {
  const expressions = [...text.matchAll(/^ {4}runs-on: (.+)$/gm)].map((match) => match[1].trim());
  return expressions.length === 3 && expressions.every((expression) => expression === runnerExpression);
}

function hasConcurrencyGroup(text: string): boolean {
  return [...text.matchAll(/^ {2}group: (.+)$/gm)].map((match) => match[1]).join('\n') === concurrencyGroup;
}

function hasSafePermissions(text: string): boolean {
  const blocks = [...text.matchAll(/^permissions:\n((?:[ \t].*\n|\n|#.*\n)*)/gm)];
  const jobs = /^jobs:\n((?:[ \t].*\n|\n|#.*\n)*)/m.exec(text);
  return blocks.length === 1 && blocks[0][1].trim() === 'contents: read' && jobs !== null
    && jobs[1].split('\n').every((line) => {
      if (!line.trim() || /^\s*#/.test(line)) return true;
      if (/^ {2}\S/.test(line)) return /^ {2}[a-z0-9-]+:$/.test(line);
      if (/^ {4}\S/.test(line)) return /^ {4}(?:name|runs-on|timeout-minutes|steps|env):(?:\s|$)/.test(line);
      return /^ {6,}\S/.test(line);
    });
}

function triggerBlock(onBlock: string, trigger: string): string {
  const lines = onBlock.split('\n');
  const start = lines.indexOf(`  ${trigger}:`);
  expect(start, `${trigger} trigger`).toBeGreaterThan(-1);
  const body: string[] = [];
  for (const line of lines.slice(start + 1)) {
    if (/^ {2}\S/.test(line)) break;
    body.push(line);
  }
  return body.join('\n');
}

function validManualInput(manual: string): boolean {
  return /^ {4}inputs:\s*$/m.test(manual)
    && (manual.match(/^ {6}use_self_hosted:\s*$/gm) || []).length === 1
    && /^ {8}type: boolean\s*$/m.test(manual)
    && /^ {8}default: false\s*$/m.test(manual);
}

function validEvents(onBlock: string): boolean {
  return /^ {2}push:\n {4}branches: \[main\]$/m.test(onBlock)
    && /^ {2}pull_request:\s*$/m.test(onBlock)
    && /^ {2}workflow_dispatch:\s*$/m.test(onBlock);
}

// Keys a workflow, job or step may carry. Anything else (if, needs, strategy, continue-on-error, shell,
// env on a step, working-directory, defaults, container, services, ...) can skip or weaken a gate.
const WORKFLOW_KEYS = ['name', 'on', 'permissions', 'concurrency', 'env', 'jobs'];
const JOB_KEYS = ['name', 'runs-on', 'timeout-minutes', 'steps', 'env'];
const USES_STEP_KEYS = ['uses', 'with', 'name'];
const RUN_STEP_KEYS = ['run', 'name'];
const USES_WITH_KEYS = ['node-version', 'cache', 'cache-dependency-path', 'go-version', 'persist-credentials'];
const WORKFLOW_ENV_KEYS = ['NODE_VERSION', 'GO_VERSION'];
const SETUP_COMMAND = 'pnpm install --frozen-lockfile';
/** The only GOFLAGS a job may set: a longer per-package test timeout (at least Go's default 10m). */
const allowedGoflags = (value: unknown) => typeof value === 'string' && /^-timeout=(\d+)m$/.test(value) && Number(/\d+/.exec(value)![0]) >= 10;

type Json = Record<string, unknown>;
const isMap = (value: unknown): value is Json => typeof value === 'object' && value !== null && !Array.isArray(value);
const stray = (value: Json, allowed: string[]) => Object.keys(value).filter((key) => !allowed.includes(key));

/** Parses with a real YAML parser; every spelling (quoted keys, flow style, ...) lands in the same structure. */
function parseWorkflow(text: string): Json {
  const doc = parseDocument(text, { uniqueKeys: true, prettyErrors: false });
  const failures = [...doc.errors, ...doc.warnings].map((error) => error.message);
  visit(doc, {
    Alias: () => void failures.push('YAML alias'),
    Node: (_, node) => {
      if ('anchor' in node && node.anchor) failures.push(`YAML anchor ${node.anchor}`);
      if (isAlias(node)) failures.push('YAML alias');
    },
  });
  if (failures.length) throw new Error(`workflow is not plain YAML: ${failures.join('; ')}`);
  const value = doc.toJS();
  if (!isMap(value)) throw new Error('workflow is not a mapping');
  return value;
}

/** Everything that makes the CI workflow differ from "run exactly the §2 CI gates, unconditionally". */
function workflowProblems(text: string, gates: Gate[]): string[] {
  let workflow: Json;
  try {
    workflow = parseWorkflow(text);
  } catch (error) {
    return [(error as Error).message];
  }
  const problems: string[] = [];
  stray(workflow, WORKFLOW_KEYS).forEach((key) => problems.push(`workflow key ${key} is not allowed`));
  if (isMap(workflow.env)) {
    stray(workflow.env, WORKFLOW_ENV_KEYS).forEach((key) => problems.push(`workflow env ${key} is not allowed`));
  } else problems.push('workflow env is missing');
  if (!isMap(workflow.jobs)) return [...problems, 'workflow has no jobs'];

  const ciGates = gates.filter((gate) => gate.ci);
  const owner = new Map<string, string>();
  for (const [jobId, job] of Object.entries(workflow.jobs)) {
    if (!isMap(job)) {
      problems.push(`${jobId}: not a mapping`);
      continue;
    }
    const label = typeof job.name === 'string' ? job.name : jobId;
    stray(job, JOB_KEYS).forEach((key) => problems.push(`${label}: job key ${key} can skip or weaken a gate`));
    if (job.env !== undefined) {
      if (!isMap(job.env)) problems.push(`${label}: env is not a mapping`);
      else {
        stray(job.env, ['GOFLAGS']).forEach((key) => problems.push(`${label}: env ${key} is not allowed`));
        if ('GOFLAGS' in job.env && !allowedGoflags(job.env.GOFLAGS)) {
          problems.push(`${label}: GOFLAGS ${JSON.stringify(job.env.GOFLAGS)} may only raise the per-package test timeout`);
        }
      }
    }
    if (!Array.isArray(job.steps) || job.steps.length === 0) {
      problems.push(`${label}: no steps`);
      continue;
    }
    const commands: string[] = [];
    for (const step of job.steps) {
      if (!isMap(step)) {
        problems.push(`${label}: step is not a mapping`);
      } else if (typeof step.uses === 'string') {
        stray(step, USES_STEP_KEYS).forEach((key) => problems.push(`${label}: step key ${key} on ${step.uses} can skip or weaken a gate`));
        if (!/^[\w.-]+\/[\w.-]+@[0-9a-f]{40}$/.test(step.uses)) problems.push(`${label}: ${step.uses} is not pinned to a commit`);
        if (isMap(step.with)) stray(step.with, USES_WITH_KEYS).forEach((key) => problems.push(`${label}: ${step.uses} input ${key} is not allowed`));
      } else if (typeof step.run === 'string') {
        stray(step, RUN_STEP_KEYS).forEach((key) => problems.push(`${label}: step key ${key} on "${step.run}" can skip or weaken a gate`));
        commands.push(step.run);
      } else {
        problems.push(`${label}: a step with neither uses nor run (${Object.keys(step).join(', ')})`);
      }
    }
    const setup = commands.filter((command) => command === SETUP_COMMAND);
    if (setup.length > 1) problems.push(`${label}: dependency install runs more than once`);
    const steps = commands.filter((command) => command !== SETUP_COMMAND);
    if (steps.length === 0) problems.push(`${label} runs no gate`);
    let cursor = 0;
    let lastGate = -1;
    while (cursor < steps.length) {
      const index = ciGates.findIndex((gate) => gate.commands.every((command, offset) => steps[cursor + offset] === command));
      if (index < 0) {
        problems.push(`${label}: "${steps[cursor]}" is not a CI gate command in dev_test.md §2`);
        cursor++;
        continue;
      }
      if (index <= lastGate) problems.push(`${label}: gates out of dev_test.md §2 order`);
      const gate = ciGates[index];
      if (owner.has(gate.name)) problems.push(`${gate.name} runs more than once in CI`);
      owner.set(gate.name, label);
      lastGate = index;
      cursor += gate.commands.length;
    }
  }
  const missing = ciGates.filter((gate) => !owner.has(gate.name)).map((gate) => gate.name);
  if (missing.length) problems.push(`CI does not run: ${missing.join(', ')}`);
  return problems;
}

describe('CI and local gate parity with dev_test.md §2', () => {
  const gates = devTestGates();

  it('dev_test.md §2 names every gate once, with commands and a placement', () => {
    expect(gates.length).toBeGreaterThan(0);
    gates.forEach((gate) => expect(gate.commands.length, gate.name).toBeGreaterThan(0));
    expect(new Set(gates.map((gate) => gate.name)).size).toBe(gates.length);
  });

  it('the default local run is exactly the gates marked local; --race adds the CI-only gate', () => {
    const local = verifyLocal();
    expect(local.gates.map((gate) => gate.name)).toEqual(gates.filter((gate) => gate.local).map((gate) => gate.name));
    expect([local.race.name]).toEqual(gates.filter((gate) => !gate.local).map((gate) => gate.name));
  });

  it('every local gate runs the §2 command, with only the registered local-only flags added', () => {
    const local = verifyLocal();
    const byName = new Map(gates.map((gate) => [gate.name, gate]));
    const problems = [...local.gates, local.race].flatMap((gate) => localArgvProblems(gate, byName.get(gate.name)!));
    expect(problems).toEqual([]);
    // The check must be able to fail: weaken or drop an argument and it is reported.
    const typecheck = local.gates.find((gate) => gate.name === 'frontend typecheck')!;
    expect(localArgvProblems({ ...typecheck, argv: [...typecheck.argv, '--skipLibCheck'] }, byName.get(typecheck.name)!)).toHaveLength(1);
    const race = local.race;
    expect(localArgvProblems({ ...race, env: {} }, byName.get(race.name)!)).toHaveLength(1);
    expect(localArgvProblems({ ...race, argv: race.argv.filter((arg) => arg !== '-race') }, byName.get(race.name)!)).toHaveLength(1);
  });

  it('the GitHub workflow runs every CI gate in exactly one job and nothing else', () => {
    expect(workflowProblems(read('.github/workflows/ci.yml'), gates)).toEqual([]);
  });

  it('separates only explicit self-hosted dispatch concurrency from hosted runs', () => {
    const text = read('.github/workflows/ci.yml');
    expect(hasConcurrencyGroup(text)).toBe(true);
    expect(text).toContain("  cancel-in-progress: ${{ github.event_name == 'pull_request' }}\n");
    expect(hasConcurrencyGroup(text.replace("github.event_name == 'workflow_dispatch' && inputs.use_self_hosted && 'trusted-'", "inputs.use_self_hosted && 'trusted-'"))).toBe(false);
    expect(hasConcurrencyGroup(text.replace('github.event.pull_request.number || ', ''))).toBe(false);
    expect(hasConcurrencyGroup(text.replace("'trusted-'", 'inputs.prefix'))).toBe(false);
    // Model only the exact expression above, not the GitHub expression engine.
    const cases = [
      { event: 'push', input: undefined, number: undefined, expected: 'ci-ci-refs/heads/main' },
      { event: 'push', input: true, number: undefined, expected: 'ci-ci-refs/heads/main' },
      { event: 'pull_request', input: undefined, number: 79, expected: 'ci-ci-79' },
      { event: 'pull_request', input: true, number: 79, expected: 'ci-ci-79' },
      { event: 'workflow_dispatch', input: undefined, number: undefined, expected: 'ci-ci-refs/heads/main' },
      { event: 'workflow_dispatch', input: false, number: undefined, expected: 'ci-ci-refs/heads/main' },
      { event: 'workflow_dispatch', input: true, number: undefined, expected: 'ci-ci-trusted-refs/heads/main' },
    ];
    for (const { event, input, number, expected } of cases) {
      const prefix = event === 'workflow_dispatch' && input === true ? 'trusted-' : '';
      expect(`ci-ci-${prefix}${number || 'refs/heads/main'}`, event).toBe(expected);
    }
  });

  it('does not collide across different dispatch refs', () => {
    const dispatchGroup = (ref: string, selfHosted: boolean) => `ci-ci-${selfHosted ? 'trusted-' : ''}${ref}`;
    expect(dispatchGroup('refs/heads/main', true)).not.toBe(dispatchGroup('refs/heads/main-self-hosted', false));
    for (const hostedKey of ['refs/heads/trusted-main', 'refs/tags/main', '79']) {
      expect(dispatchGroup('refs/heads/main', true)).not.toBe(dispatchGroup(hostedKey, false));
    }
  });

  it('the workflow has no known internal host literal and pins every action to a commit', () => {
    const text = read('.github/workflows/ci.yml');
    expect(text).not.toMatch(/yc[g]ame|192\.168\.|pkgs\./);
    const uses = [...text.matchAll(/uses: (\S+)/g)].map((m) => m[1]);
    expect(uses.length).toBeGreaterThan(0);
    uses.forEach((ref) => expect(ref).toMatch(/^[\w.-]+\/[\w.-]+@[0-9a-f]{40}$/));
  });

  it('statically fixes the runner expression and models the known event cases', () => {
    const text = read('.github/workflows/ci.yml');
    expect(hasRunnerRoute(text)).toBe(true);
    expect(hasRunnerRoute(text.replaceAll('plinth-trusted-ci', 'another-runner'))).toBe(false);
    expect(hasRunnerRoute(text.replaceAll("github.event_name == 'workflow_dispatch' && ", ''))).toBe(false);
    expect(hasRunnerRoute(text.replaceAll('"X64",', ''))).toBe(false);
    const trusted = ['self-hosted', 'Linux', 'X64', 'plinth-trusted-ci'];
    const cases = [
      { github: { event_name: 'push' }, inputs: { use_self_hosted: undefined }, expected: 'ubuntu-latest' },
      { github: { event_name: 'pull_request' }, inputs: { use_self_hosted: undefined }, expected: 'ubuntu-latest' },
      { github: { event_name: 'workflow_dispatch' }, inputs: { use_self_hosted: undefined }, expected: 'ubuntu-latest' },
      { github: { event_name: 'workflow_dispatch' }, inputs: { use_self_hosted: false }, expected: 'ubuntu-latest' },
      { github: { event_name: 'workflow_dispatch' }, inputs: { use_self_hosted: true }, expected: trusted },
      { github: { event_name: 'push' }, inputs: { use_self_hosted: true }, expected: 'ubuntu-latest' },
      { github: { event_name: 'pull_request' }, inputs: { use_self_hosted: true }, expected: 'ubuntu-latest' },
    ];
    for (const testCase of cases) {
      expect(
        testCase.github.event_name === 'workflow_dispatch' && testCase.inputs.use_self_hosted === true ? trusted : 'ubuntu-latest',
        `${testCase.github.event_name} with use_self_hosted=${String(testCase.inputs.use_self_hosted)}`,
      ).toEqual(testCase.expected);
    }
  });

  it('declares the manual runner opt-in as a boolean defaulting to hosted', () => {
    const text = read('.github/workflows/ci.yml');
    const onBlock = text.slice(text.indexOf('\non:'), text.indexOf('\npermissions:'));
    expect(validEvents(onBlock)).toBe(true);
    for (const event of ['push', 'pull_request', 'workflow_dispatch']) {
      expect(validEvents(onBlock.replace(`  ${event}:`, `  missing_${event}:`)), event).toBe(false);
    }
    const manual = triggerBlock(onBlock, 'workflow_dispatch');
    expect(validManualInput(manual)).toBe(true);
    expect(validManualInput(manual.replace('type: boolean', 'type: string'))).toBe(false);
    expect(validManualInput(manual.replace('default: false', 'default: true'))).toBe(false);
  });

  it('keeps the CI security preconditions independent of the runner route', () => {
    const text = read('.github/workflows/ci.yml');
    expect(hasSafePermissions(text)).toBe(true);
    expect(text.match(/^ {10}persist-credentials: false$/gm)).toHaveLength(3);
    // The opt-in reaches only the three runs-on lines and the concurrency group.
    expect(text.match(/inputs\.use_self_hosted/g)).toHaveLength(4);
    expect(text).not.toContain('github.event.inputs');
  });

  it('rejects extra top-level permissions', () => {
    const text = read('.github/workflows/ci.yml');
    expect(hasSafePermissions(text.replace('  contents: read\n', '  contents: read\n  actions: write\n'))).toBe(false);
    expect(hasSafePermissions(text.replace('  contents: read\n', '  issues: read\n  contents: read\n'))).toBe(false);
  });

  it('rejects job-level permission overrides', () => {
    const text = read('.github/workflows/ci.yml');
    expect(hasSafePermissions(text.replace('  frontend:\n', '  frontend:\n    permissions: write-all\n'))).toBe(false);
    expect(hasSafePermissions(text.replace('  go-check:\n', '  go-check:\n    permissions:\n      issues: write\n'))).toBe(false);
    expect(hasSafePermissions(text.replace('  go-test-race:\n', '  go-test-race:\n    "permissions": {}\n'))).toBe(false);
  });

  it('pins pnpm to the packageManager version in the Dockerfile', () => {
    const packageManager = /"packageManager": "pnpm@([0-9.]+)"/.exec(read('package.json'))![1];
    const dockerfile = /^ARG PNPM_VERSION=([0-9.]+)$/m.exec(read('Dockerfile.go-runtime'))![1];
    expect(dockerfile).toEqual(packageManager);
  });
});

describe('CI workflow gate integrity (#86)', () => {
  const gates = devTestGates();
  const text = read('.github/workflows/ci.yml');
  const mutate = (from: string, to: string) => {
    expect(text, `mutation anchor missing: ${from}`).toContain(from);
    return text.replace(from, to);
  };
  const flagged = (mutated: string, fragment: RegExp | string) => {
    const problems = workflowProblems(mutated, gates);
    expect(problems.length).toBeGreaterThan(0);
    expect(problems.join('\n')).toMatch(fragment);
  };
  const TEST_STEP = '      - run: pnpm test\n';

  it('accepts the checked-in workflow', () => {
    expect(workflowProblems(text, gates)).toEqual([]);
  });

  it('rejects an extra step however its YAML is spelled', () => {
    flagged(mutate(TEST_STEP, `${TEST_STEP}      - run: echo extra\n`), /not a CI gate command/);
    flagged(mutate(TEST_STEP, `${TEST_STEP}      - "run": echo extra\n`), /not a CI gate command/);
    flagged(mutate(TEST_STEP, `${TEST_STEP}      - 'run': 'echo extra'\n`), /not a CI gate command/);
    flagged(mutate(TEST_STEP, `${TEST_STEP}      - {run: echo extra}\n`), /not a CI gate command/);
    flagged(mutate(TEST_STEP, `${TEST_STEP}      - { "run": "echo extra" }\n`), /not a CI gate command/);
    flagged(mutate(TEST_STEP, `${TEST_STEP}      - ? run\n        : echo extra\n`), /not a CI gate command/);
    flagged(mutate(TEST_STEP, `${TEST_STEP}      - run: |\n          echo extra\n`), /not a CI gate command/);
    flagged(mutate(TEST_STEP, `${TEST_STEP}      - run: >-\n          echo\n          extra\n`), /not a CI gate command/);
  });

  it('rejects a gate command that is chained, wrapped or duplicated', () => {
    flagged(mutate(TEST_STEP, '      - run: pnpm test || true\n'), /not a CI gate command/);
    flagged(mutate(TEST_STEP, '      - run: pnpm test; true\n'), /not a CI gate command/);
    flagged(mutate(TEST_STEP, '      - { "run": "pnpm test" }\n      - { "run": "pnpm test" }\n'), /more than once/);
    flagged(mutate(TEST_STEP, ''), /CI does not run: frontend test/);
  });

  it('rejects if, continue-on-error and other fields on a gate step', () => {
    for (const field of ['if: false', "if: ${{ always() }}", 'continue-on-error: true', 'continue-on-error: ${{ true }}', 'shell: bash -c true {0}', 'working-directory: build', 'timeout-minutes: 1', 'id: gate']) {
      flagged(mutate(TEST_STEP, `      - ${field}\n        run: pnpm test\n`), /can skip or weaken a gate/);
      flagged(mutate(TEST_STEP, `      - run: pnpm test\n        ${field}\n`), /can skip or weaken a gate/);
    }
    flagged(mutate(TEST_STEP, '      - { "if": "false", "run": "pnpm test" }\n'), /can skip or weaken a gate/);
    flagged(mutate(TEST_STEP, '      - "continue-on-error": true\n        run: pnpm test\n'), /can skip or weaken a gate/);
  });

  it('rejects environment overrides that skip or weaken a gate', () => {
    for (const goflags of ['-run=NoSuchTest', '-short', '-tags=skip', '-timeout=1m', '-timeout=30m -run=X', '-p=1']) {
      flagged(mutate(TEST_STEP, `${TEST_STEP}        env:\n          GOFLAGS: ${goflags}\n`), /can skip or weaken a gate/);
      flagged(mutate('      GOFLAGS: -timeout=30m\n', `      GOFLAGS: ${goflags}\n`), /GOFLAGS/);
    }
    flagged(mutate('      GOFLAGS: -timeout=30m\n', '      GOFLAGS: -timeout=30m\n      CGO_ENABLED: 0\n'), /env CGO_ENABLED is not allowed/);
    flagged(mutate("  NODE_VERSION: '22'\n", "  NODE_VERSION: '22'\n  GOFLAGS: -run=X\n"), /workflow env GOFLAGS is not allowed/);
    flagged(mutate('\njobs:\n', '\ndefaults:\n  run:\n    shell: bash -c true {0}\njobs:\n'), /workflow key defaults/);
  });

  it('keeps a longer per-package test timeout as the only allowed GOFLAGS', () => {
    expect(workflowProblems(mutate('GOFLAGS: -timeout=30m', 'GOFLAGS: -timeout=45m'), gates)).toEqual([]);
    expect(workflowProblems(mutate('GOFLAGS: -timeout=30m', 'GOFLAGS: -timeout=9m'), gates).join()).toMatch(/GOFLAGS/);
  });

  it('rejects job-level fields that skip, reorder or fan out a gate job', () => {
    for (const field of ['if: false', 'continue-on-error: true', 'needs: go-check', 'strategy:\n      matrix:\n        os: [a]', 'container: alpine', 'services: {}', 'permissions: write-all', 'defaults:\n      run:\n        shell: true', 'outputs: {}']) {
      flagged(mutate('  frontend:\n', `  frontend:\n    ${field}\n`), /job key/);
    }
    flagged(mutate('  frontend:\n', '  "frontend":\n    "if": false\n'), /duplicate|job key|already/i);
  });

  it('rejects anchors, aliases, merge keys and duplicate keys', () => {
    flagged(mutate(TEST_STEP, '      - &gate\n        run: pnpm test\n'), /anchor/);
    flagged(mutate(TEST_STEP, `${TEST_STEP}      - *gate\n`), /alias|anchor|Unresolved/i);
    flagged(mutate(TEST_STEP, '      - <<: {if: false}\n        run: pnpm test\n'), /can skip or weaken a gate/);
    flagged(mutate(TEST_STEP, '      - run: pnpm test\n        run: echo changed\n'), /duplicate|unique|Map keys/i);
  });

  it('rejects steps that are neither a pinned action nor a plain run', () => {
    flagged(mutate(TEST_STEP, `${TEST_STEP}      - uses: ./local-action\n`), /not pinned to a commit/);
    flagged(mutate(TEST_STEP, `${TEST_STEP}      - name: nothing\n`), /neither uses nor run/);
    flagged(mutate('      - run: pnpm exec tsc --noEmit\n', '      - run: pnpm exec tsc --noEmit --skipLibCheck\n'), /not a CI gate command/);
  });

  it('rejects a gate moved to the wrong order or duplicated across jobs', () => {
    flagged(mutate('      - run: pnpm exec tsc --noEmit\n      - run: pnpm test\n', '      - run: pnpm test\n      - run: pnpm exec tsc --noEmit\n'), /out of dev_test.md §2 order/);
    flagged(mutate('      - run: go vet ./...\n', '      - run: go vet ./...\n      - run: pnpm test\n'), /more than once/);
  });
});

describe('host CI parity with the gates (#86)', () => {
  const gates = devTestGates();
  const entry = read('scripts/host-ci/entry.sh');
  const hostRun = read('scripts/host-ci/host_run.py');
  const runPy = read('scripts/host-ci/run.py');
  const dockerfile = read('Dockerfile.ci');
  const workflow = parseWorkflow(read('.github/workflows/ci.yml'));
  const workflowEnv = workflow.env as Record<string, string>;
  const dev = read('dev_test.md');

  it('runs the whole local gate set including the race gate, not a private list', () => {
    expect(entry).toContain("python3 scripts/verify-local.py --race --force 2>&1 | tee /out/verify.log");
    expect(entry).toContain('module.selected_gates(race=True)');
    // The race gate is placed right after go test, so --race yields every local gate plus the CI-only one.
    const local = verifyLocal();
    const names = [...local.gates.map((gate) => gate.name), local.race.name];
    expect(names.sort()).toEqual(gates.map((gate) => gate.name).sort());
    expect(hostRun).toContain("'command': 'GOFLAGS=-p=4 python3 scripts/verify-local.py --race --force'");
  });

  it('runs the same external-link check that §2 documents for the build gate', () => {
    const build = gates.find((gate) => gate.name === 'frontend build')!;
    const links = build.commands.find((command) => command.startsWith('node '))!;
    expect(entry).toContain(`phase 'external links' ${links}`);
  });

  it('sets only a parallelism GOFLAGS, never a flag that skips or weakens tests', () => {
    const goflags = [...entry.matchAll(/GOFLAGS=(\S+)/g)].map((match) => match[1]);
    expect(goflags).toEqual(['-p=4']);
    for (const flag of goflags) expect(flag).toMatch(/^-p=\d+$/);
  });

  it('reads the toolchain versions from the workflow with a pattern that matches the real file', () => {
    expect(entry, 'entry.sh workflow_version() no longer has the expected sed form').toContain(
      String.raw`sed -n "s/^  $1: '\\([0-9.]*\\)'.*/\\1/p" .github/workflows/ci.yml`,
    );
    for (const key of WORKFLOW_ENV_KEYS) {
      const match = new RegExp(`^  ${key}: '([0-9.]*)'.*$`, 'm').exec(read('.github/workflows/ci.yml'));
      expect(match?.[1], key).toBe(workflowEnv[key]);
    }
  });

  it('builds the image from the same Node and Go versions as the workflow', () => {
    expect(dockerfile).toMatch(new RegExp(`^FROM node:${workflowEnv.NODE_VERSION.replace('.', '\\.')}-bookworm@sha256:[0-9a-f]{64} AS node$`, 'm'));
    expect(dockerfile).toMatch(new RegExp(`^FROM golang:${workflowEnv.GO_VERSION.replace('.', '\\.')}-bookworm@sha256:[0-9a-f]{64}$`, 'm'));
  });

  it('keeps the tunable defaults identical in run.py, host_run.py and dev_test.md', () => {
    const pythonDict = (source: string, name: string) => {
      const body = new RegExp(`^${name} = \\{(.+)\\}$`, 'm').exec(source);
      expect(body, name).not.toBeNull();
      return Object.fromEntries([...body![1].matchAll(/'(\w+)': (\d+)/g)].map((match) => [match[1], Number(match[2])]));
    };
    const limits = pythonDict(runPy, 'LIMIT_DEFAULTS');
    expect(pythonDict(hostRun, 'DEFAULTS')).toEqual(limits);
    const start = dev.indexOf('### 主机 CI');
    const section = dev.slice(start, dev.indexOf('\n### ', start + 1));
    expect(section).toContain(`\`MemAvailable\` 至少 ${limits.min_mem_gib} GiB`);
    expect(section).toContain(`各至少 ${limits.min_disk_gib} GiB 空闲`);
    expect(section).toContain(`默认 ${limits.timeout_min} 分钟`);
    expect(section).toContain(`${limits.cpus} CPU、${limits.memory_gib} GiB`);
  });

  it('holds the memory threshold above the container limit by the documented margin', () => {
    const limits = Object.fromEntries([...(/LIMIT_DEFAULTS = \{(.+)\}/.exec(runPy)![1]).matchAll(/'(\w+)': (\d+)/g)].map((m) => [m[1], Number(m[2])]));
    const margin = Number(/^MIN_MEMORY_MARGIN_GIB = (\d+)$/m.exec(hostRun)![1]);
    expect(limits.min_mem_gib).toBeGreaterThanOrEqual(limits.memory_gib + margin);
  });
});

describe('host CI driver unit tests (#86, #97)', () => {
  it('pass: OOM, timeout, preflight, lock and limit validation run against a stubbed Docker', () => {
    const output = execFileSync('python3', ['scripts/host-ci/test_host_ci.py'], { encoding: 'utf8', env: { ...process.env, PYTHONDONTWRITEBYTECODE: '1' }, stdio: ['ignore', 'pipe', 'pipe'], timeout: 120_000 });
    expect(output).toBe('');
  }, 130_000);
});
