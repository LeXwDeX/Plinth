import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const read = (path: string) => readFileSync(fileURLToPath(new URL(path, import.meta.url)), 'utf8');

describe('#678: the 3D model confirmation is wired end to end', () => {
  it('is mounted once in the studio', () => {
    expect(read('./studio-shell.tsx')).toMatch(/<Model3DConfirmHost \/>/);
  });

  it('closes the result detail when 转 3D opens the confirmation instead of converting', () => {
    expect(read('../result/result-detail.tsx')).toMatch(/toThreeD\(object\) \|\| useModel3DConfirmStore\.getState\(\)\.request !== null/);
  });

  it('sends the confirmed model with that one turn, queued or not, without writing the workspace pick', () => {
    const chat = read('./chat-column.tsx');
    expect(chat).toMatch(/\.\.\.workspace\.modelByDomain, \.\.\.extra\?\.domainModels/);
    expect(chat).toMatch(/domainModels: turnRequest\.models/);
    expect(chat).toMatch(/turn\.domainModels \? \{ domainModels: turn\.domainModels \}/);
    expect(chat).not.toMatch(/setModel\w*\([^)]*turnRequest\.models/);
  });
});
