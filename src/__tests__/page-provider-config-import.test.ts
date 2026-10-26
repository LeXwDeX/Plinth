import { describe, expect, it } from 'vitest';
import { readdirSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import ts from 'typescript';
import path from 'node:path';

const appRoot = fileURLToPath(new URL('../app', import.meta.url));

function collectPageFiles(root: string): string[] {
  return readdirSync(root, { withFileTypes: true }).flatMap((entry) => {
    const target = path.join(root, entry.name);
    if (entry.isDirectory()) return collectPageFiles(target);
    if (entry.name === 'page.tsx') return [target];
    return [];
  });
}

function importedModules(filePath: string): string[] {
  const sourceFile = ts.createSourceFile(
    filePath,
    readFileSync(filePath, 'utf8'),
    ts.ScriptTarget.Latest,
    true,
    ts.ScriptKind.TSX,
  );
  const modules: string[] = [];
  sourceFile.forEachChild((node) => {
    if (
      ts.isImportDeclaration(node) &&
      ts.isStringLiteral(node.moduleSpecifier)
    ) {
      modules.push(node.moduleSpecifier.text);
    }
  });
  return modules;
}

describe('页面层 Provider 配置门禁', () => {
  it('src/app 页面不得直读 provider-config，Provider 数据只能来自 useProviders/Product Schema', () => {
    const pages = collectPageFiles(appRoot);
    expect(pages.length).toBeGreaterThan(0);
    const violations = pages
      .filter((page) =>
        importedModules(page).some((specifier) =>
          specifier.includes('providers/provider-config'),
        ),
      )
      .map((page) => path.relative(appRoot, page));
    expect(violations).toEqual([]);
  });
});
