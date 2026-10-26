import { readFileSync } from 'node:fs';

import { describe, expect, it } from 'vitest';

/**
 * 门禁对齐守门（#257）。dev_test.md §2「自动化门禁项」是门禁的唯一真源：每项写明
 * 门禁名「…」与执行位置（本地 + CI / 仅本地 / 仅 CI）。这里要求：
 * - 标为 CI 的每项命令逐字出现在恰好一个 CI 作业里，同一项的命令在同一作业、按 §2 顺序；
 *   CI 作业里没有 §2 以外的命令；内网 GitLab（.gitlab-ci.yml）与公共 GitHub Actions
 *   （.github/workflows/ci.yml）的作业名与命令完全相同，都不构建镜像（#257）；
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

function gitlabJobs(): Map<string, string[]> {
  const jobs = new Map<string, string[]>();
  let job: string | null = null;
  let inScript = false;
  for (const line of read('.gitlab-ci.yml').split('\n')) {
    const top = /^([^\s#.][^:]*):\s*$/.exec(line);
    if (top) {
      job = ['stages', 'default', 'variables', 'workflow'].includes(top[1]) ? null : top[1];
      if (job) jobs.set(job, []);
      inScript = false;
      continue;
    }
    if (!job) continue;
    if (/^ {2}\S/.test(line) && !/^ {2}#/.test(line)) {
      inScript = line.trim() === 'script:';
      continue;
    }
    const step = /^ {4}- (.+)$/.exec(line);
    if (inScript && step) jobs.get(job)!.push(step[1].trim());
  }
  return jobs;
}

function githubJobs(): Map<string, string[]> {
  const text = read('.github/workflows/ci.yml');
  const jobs = new Map<string, string[]>();
  let current: string[] | null = null;
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
    const run = /^ {6}- run: (.+)$/.exec(line);
    if (current && run && !run[1].startsWith('pnpm install')) current.push(run[1].trim());
  }
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

  it('the internal GitLab runner runs every CI gate in exactly one job and nothing else', () => {
    expectCiCovers(gitlabJobs(), gates);
  });

  it('the public GitHub workflow runs exactly the same jobs and commands as GitLab', () => {
    const gitlab = gitlabJobs();
    const github = githubJobs();
    expect([...github.keys()]).toEqual([...gitlab.keys()]);
    gitlab.forEach((steps, job) => expect(github.get(job), job).toEqual(steps));
    expectCiCovers(github, gates);
  });

  it('the public GitHub workflow reaches no internal host and pins every action to a commit', () => {
    const text = read('.github/workflows/ci.yml');
    expect(text).not.toMatch(/yc[g]ame|192\.168\.|pkgs\./);
    const uses = [...text.matchAll(/uses: (\S+)/g)].map((m) => m[1]);
    expect(uses.length).toBeGreaterThan(0);
    uses.forEach((ref) => expect(ref).toMatch(/^[\w.-]+\/[\w.-]+@[0-9a-f]{40}$/));
  });

  it('the internal GitLab pipeline names no internal host and uses the public npm registry', () => {
    // #257: the npm source is fixed to the public registry; CI no longer accepts a mirror override.
    const text = read('.gitlab-ci.yml');
    expect(text).not.toMatch(/yc[g]ame|192\.168\.|pkgs\./);
    expect(text).not.toMatch(/NPM_REGISTRY/);
    expect(text).toMatch(/registry\.npmjs\.org/);
    // Project/group CI/CD variables can override job variables, and registry environment
    // variables (npm_config_registry, PNPM_CONFIG_REGISTRY) outrank ~/.npmrc, so both
    // install commands pass --registry explicitly.
    const installs = [...text.matchAll(/^ {4}- ((?:npm|pnpm) install .+)$/gm)].map((match) => match[1]);
    expect(installs.filter((step) => step.startsWith('npm install ')).length).toBeGreaterThan(0);
    expect(installs.filter((step) => step.startsWith('pnpm install ')).length).toBeGreaterThan(0);
    installs.forEach((step) =>
      expect(step, step).toMatch(/(?:^|\s)--registry=https:\/\/registry\.npmjs\.org\/(?:\s|$)/),
    );
  });

  it('pins pnpm to the packageManager version in the Dockerfile and the GitLab runner', () => {
    const packageManager = /"packageManager": "pnpm@([0-9.]+)"/.exec(read('package.json'))![1];
    const dockerfile = /^ARG PNPM_VERSION=([0-9.]+)$/m.exec(read('Dockerfile.go-runtime'))![1];
    const gitlab = /pnpm-([0-9.]+)\.tgz/.exec(read('.gitlab-ci.yml'))![1];
    expect([dockerfile, gitlab]).toEqual([packageManager, packageManager]);
  });
});
