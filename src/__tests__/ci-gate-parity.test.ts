import { readFileSync } from 'node:fs';

import { describe, expect, it } from 'vitest';

/**
 * 门禁对齐守门（#257）。dev_test.md §2「自动化门禁项」是门禁的唯一真源：每项写明
 * 门禁名「…」与执行位置（本地 + CI / 仅本地 / 仅 CI）。这里要求：
 * - 标为 CI 的每项命令逐字出现在恰好一个 CI 作业里，同一项的命令在同一作业、按 §2 顺序；
 *   CI 作业里没有 §2 以外的命令；CI 只有 GitHub Actions（.github/workflows/ci.yml），不构建镜像（#257）；
 * - 本地入口（scripts/verify-local.py）默认集合 GATES 的门禁名与顺序等于标为本地的各项，
 *   --race 加跑的 RACE_GATE 等于唯一标为仅 CI 的一项。
 * 这里按文本解析，只依赖这些文件现有的固定写法；写法变了先改这里的解析。
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

function pythonGateNames(block: string): string[] {
  return [...block.matchAll(/\('([^']+)', \[/g)].map((m) => m[1]);
}

function verifyLocal(): { gates: string[]; race: string[] } {
  const text = read('scripts/verify-local.py');
  const start = text.indexOf('\nGATES = [');
  expect(start).toBeGreaterThan(-1);
  const race = /^RACE_GATE = (.+)$/m.exec(text);
  expect(race).not.toBeNull();
  return {
    gates: pythonGateNames(text.slice(start, text.indexOf('\n]', start))),
    race: pythonGateNames(race![1]),
  };
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

function githubJobs(): Map<string, string[]> {
  const text = read('.github/workflows/ci.yml');
  const jobs = new Map<string, string[]>();
  let current: string[] | null = null;
  let installs = 0;
  for (const line of text.slice(text.indexOf('\njobs:')).split('\n')) {
    if (/^ {2}[a-z0-9-]+:\s*$/.test(line)) {
      current = null;
      continue;
    }
    const name = /^ {4}name: (.+)$/.exec(line);
    if (name) {
      current = [];
      jobs.set(name[1].trim(), current);
      continue;
    }
    if (/^\s*(?:-\s*)?run:/.test(line)) {
      const run = /^ {6}- run: (.+)$/.exec(line);
      expect(current && run, `unsupported run step: ${line}`).toBeTruthy();
      if (run![1] === 'pnpm install --frozen-lockfile') installs++;
      else current!.push(run![1].trim());
    }
  }
  expect(installs).toBe(1);
  return jobs;
}

/** Every CI gate's commands sit, contiguous and in §2 order, in exactly one job; nothing else runs. */
function expectCiCovers(jobs: Map<string, string[]>, gates: Gate[]) {
  const ciGates = gates.filter((gate) => gate.ci);
  const owner = new Map<string, string>();
  for (const [job, steps] of jobs) {
    expect(steps.length, `${job} runs no gate`).toBeGreaterThan(0);
    let cursor = 0;
    let lastGate = -1;
    while (cursor < steps.length) {
      const index = ciGates.findIndex(
        (gate) => gate.commands.every((command, offset) => steps[cursor + offset] === command),
      );
      expect(index, `${job}: "${steps[cursor]}" is not a CI gate command in dev_test.md §2`).toBeGreaterThan(-1);
      expect(index, `${job}: gates out of dev_test.md §2 order`).toBeGreaterThan(lastGate);
      const gate = ciGates[index];
      expect(owner.get(gate.name), `${gate.name} runs in more than one CI job`).toBeUndefined();
      owner.set(gate.name, job);
      lastGate = index;
      cursor += gate.commands.length;
    }
  }
  expect([...owner.keys()].sort()).toEqual(ciGates.map((gate) => gate.name).sort());
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
    expect(local.gates).toEqual(gates.filter((gate) => gate.local).map((gate) => gate.name));
    expect(local.race).toEqual(gates.filter((gate) => !gate.local).map((gate) => gate.name));
  });

  it('the GitHub workflow runs every CI gate in exactly one job and nothing else', () => {
    expectCiCovers(githubJobs(), gates);
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
