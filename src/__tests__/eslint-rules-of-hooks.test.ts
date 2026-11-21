import { ESLint } from 'eslint';
import { describe, expect, it } from 'vitest';

/**
 * 前端 lint 自检（#436）。#387 的聊天区 React #300（Rendered fewer hooks than expected）
 * 是条件 hook：组件在调用部分 hook 之后提前 return，下一次渲染就少调用几个 hook。
 * 仓库此前没有 ESLint，这个自检用真实配置（eslint.config.mjs）lint 一段源码，证明
 * `pnpm lint` 能拦住同一类写法，同时证明正常组件不会被误报。
 *
 * 用例里的源码只是 `lintText` 的输入，不落盘：违规样例不进 src，也不会被构建。
 */
const lint = async (fileName: string, code: string) => {
  const eslint = new ESLint({ cwd: process.cwd() });
  const [result] = await eslint.lintText(code, { filePath: `src/__tests__/${fileName}` });
  return result.messages;
};

// 与 MultiviewUploadPanel 在 #387 中的写法同型：提前 return 之后才调用 useState。
const CONDITIONAL_HOOK = `
import { useState } from 'react';

export function Panel({ open }: { open: boolean }) {
  if (!open) return null;
  const [count] = useState(0);
  return <span>{count}</span>;
}
`;

// 修复后的形态：所有 hook 都在提前 return 之前，渲染顺序恒定。
const UNCONDITIONAL_HOOK = `
import { useState } from 'react';

export function Panel({ open }: { open: boolean }) {
  const [count] = useState(0);
  if (!open) return null;
  return <span>{count}</span>;
}
`;

describe('ESLint 拦住条件 hook（#436）', () => {
  it('把提前 return 之后调用的 hook 报为 rules-of-hooks 错误', async () => {
    const messages = await lint('conditional-hook.fixture.tsx', CONDITIONAL_HOOK);
    expect(messages).toEqual([
      expect.objectContaining({ ruleId: 'react-hooks/rules-of-hooks', severity: 2, line: 6 }),
    ]);
  });

  it('接受所有 hook 都在提前 return 之前的组件', async () => {
    expect(await lint('unconditional-hook.fixture.tsx', UNCONDITIONAL_HOOK)).toEqual([]);
  });
});