import { describe, expect, it } from 'vitest';
import { readdirSync, readFileSync } from 'node:fs';
import ts from 'typescript';
import path from 'node:path';
import defaultCatalog from '../../internal/catalog/default_catalog.json';
import {
  buildTaskTabs,
  capabilityWorkflow,
  parameterDisabled,
  projectRuntimeProductSchema,
  schemaMessage,
  selectNavigation,
  schemaText,
  type ProductSchema,
  valueLabel,
  withRuntimeVersion,
} from '@/lib/product-schema';

function schemaFixture(): ProductSchema {
  return structuredClone(defaultCatalog.application) as ProductSchema;
}

function visibleLiteralText(path: string): string[] {
  const source = readFileSync(new URL(path, import.meta.url), 'utf8');
  const sourceFile = ts.createSourceFile(
    path,
    source,
    ts.ScriptTarget.Latest,
    true,
    ts.ScriptKind.TSX,
  );
  const result: string[] = [];
  const visit = (node: ts.Node) => {
    if (ts.isJsxText(node)) {
      const value = node.text.trim();
      if (/[一-龥A-Za-z0-9]/.test(value)) result.push(value);
    }
    if (
      ts.isJsxAttribute(node) &&
      ['placeholder', 'title', 'aria-label', 'alt'].includes(node.name.getText(sourceFile)) &&
      node.initializer &&
      ts.isStringLiteral(node.initializer)
    ) {
      result.push(node.initializer.text);
    }
    ts.forEachChild(node, visit);
  };
  visit(sourceFile);
  return result;
}

function applicationSourceFiles(
  root: string,
  extensions = new Set(['.ts', '.tsx']),
): string[] {
  return readdirSync(root, { withFileTypes: true }).flatMap((entry) => {
    const target = path.join(root, entry.name);
    if (entry.isDirectory()) {
      if (entry.name === '__tests__') return [];
      return applicationSourceFiles(target, extensions);
    }
    if (
      !extensions.has(path.extname(entry.name)) ||
      entry.name.endsWith('.d.ts') ||
      entry.name.includes('.test.') ||
      entry.name.includes('.spec.')
    ) {
      return [];
    }
    return [target];
  });
}

function applicationTSXFiles(root: string): string[] {
  return applicationSourceFiles(root, new Set(['.tsx']));
}

function schemaDrivenClientSourceFiles(): string[] {
  return [
    'app',
    'components',
    'hooks',
    'store',
    path.join('lib', 'canvas'),
  ].flatMap((directory) =>
    applicationSourceFiles(path.join(process.cwd(), 'src', directory)),
  ).filter((file) => path.basename(file) !== 'route.ts');
}

function visibleConfigurationLiterals(file: string): string[] {
  const source = readFileSync(file, 'utf8');
  const sourceFile = ts.createSourceFile(
    file,
    source,
    ts.ScriptTarget.Latest,
    true,
    ts.ScriptKind.TSX,
  );
  const result: string[] = [];
  const visit = (node: ts.Node) => {
    if (
      (ts.isStringLiteral(node) ||
        ts.isNoSubstitutionTemplateLiteral(node) ||
        ts.isTemplateHead(node) ||
        ts.isTemplateMiddle(node) ||
        ts.isTemplateTail(node)) &&
      /[\u4e00-\u9fff]/.test(node.text)
    ) {
      let ancestor: ts.Node | undefined = node.parent;
      let call: ts.CallExpression | undefined;
      while (ancestor && !ts.isSourceFile(ancestor)) {
        if (ts.isCallExpression(ancestor)) {
          call = ancestor;
          break;
        }
        ancestor = ancestor.parent;
      }
      if (!call?.expression.getText(sourceFile).startsWith('console.')) {
        result.push(node.text);
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(sourceFile);
  return result;
}

/**
 * Tags on which `title=` is a native browser tooltip: every lowercase intrinsic element, the button
 * components, the menu or popover triggers, and the thin wrappers that spread their props onto one element
 * (Link → a, Surface and Badge → div/span, Input, Textarea). Another component's own `title` prop (a SideDrawer
 * heading, a ContextChip name) is not a tooltip and stays allowed.
 */
const NATIVE_TITLE_COMPONENTS = new Set(['Action', 'Button', 'Checkbox', 'Switch', 'Link', 'Surface', 'Badge', 'Input', 'Textarea']);

function isNativeTitleTag(tag: string): boolean {
  return /^[a-z]/.test(tag) || NATIVE_TITLE_COMPONENTS.has(tag) || /(^|\.)\w*Trigger$/.test(tag);
}

function nativeTitleAttributesFromSource(source: string, file = 'native-title-fixture.tsx'): string[] {
  const sourceFile = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const result: string[] = [];
  const visit = (node: ts.Node) => {
    if (ts.isJsxAttribute(node) && node.name.getText(sourceFile) === 'title') {
      const element = node.parent.parent;
      if (ts.isJsxOpeningElement(element) || ts.isJsxSelfClosingElement(element)) {
        const tag = element.tagName.getText(sourceFile);
        if (isNativeTitleTag(tag)) {
          const { line } = sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile));
          result.push(`<${tag} title> at line ${line + 1}`);
        }
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(sourceFile);
  return result;
}

const visibleExpressionAttributes = new Set([
  'placeholder',
  'title',
  'aria-label',
  'alt',
]);

const visibleConfigurationProperties = new Set([
  'label',
  'title',
  'description',
  'placeholder',
  'message',
  'error',
  'alt',
  'tooltip',
  'empty',
  'loading',
  'text',
]);

function isRenderedExpressionLiteral(
  literal: ts.Node,
  expression: ts.Expression,
): boolean {
  let current = literal;
  while (current !== expression) {
    const parent = current.parent;
    if (!parent || ts.isSourceFile(parent)) return false;
    if (ts.isCallExpression(parent) || ts.isFunctionLike(parent)) return false;
    if (ts.isConditionalExpression(parent)) {
      if (current === parent.condition) return false;
      current = parent;
      continue;
    }
    if (ts.isBinaryExpression(parent)) {
      const operator = parent.operatorToken.kind;
      if (
        operator === ts.SyntaxKind.EqualsEqualsToken ||
        operator === ts.SyntaxKind.EqualsEqualsEqualsToken ||
        operator === ts.SyntaxKind.ExclamationEqualsToken ||
        operator === ts.SyntaxKind.ExclamationEqualsEqualsToken ||
        operator === ts.SyntaxKind.LessThanToken ||
        operator === ts.SyntaxKind.LessThanEqualsToken ||
        operator === ts.SyntaxKind.GreaterThanToken ||
        operator === ts.SyntaxKind.GreaterThanEqualsToken
      ) {
        return false;
      }
      if (
        operator === ts.SyntaxKind.AmpersandAmpersandToken &&
        current === parent.left
      ) {
        return false;
      }
      if (
        operator !== ts.SyntaxKind.PlusToken &&
        operator !== ts.SyntaxKind.BarBarToken &&
        operator !== ts.SyntaxKind.QuestionQuestionToken &&
        operator !== ts.SyntaxKind.AmpersandAmpersandToken
      ) {
        return false;
      }
      current = parent;
      continue;
    }
    if (
      ts.isParenthesizedExpression(parent) ||
      ts.isAsExpression(parent) ||
      ts.isNonNullExpression(parent) ||
      ts.isSatisfiesExpression(parent) ||
      ts.isTemplateExpression(parent)
    ) {
      current = parent;
      continue;
    }
    return false;
  }
  return true;
}

function visibleExpressionLiteralsFromSource(
  source: string,
  file = 'schema-visible-copy-fixture.tsx',
): string[] {
  const sourceFile = ts.createSourceFile(
    file,
    source,
    ts.ScriptTarget.Latest,
    true,
    ts.ScriptKind.TSX,
  );
  const result: string[] = [];
  const visit = (node: ts.Node) => {
    if (
      ts.isStringLiteral(node) ||
      ts.isNoSubstitutionTemplateLiteral(node) ||
      ts.isTemplateHead(node) ||
      ts.isTemplateMiddle(node) ||
      ts.isTemplateTail(node)
    ) {
      let visible = false;
      if (ts.isPropertyAssignment(node.parent)) {
        const property = node.parent.name.getText(sourceFile).replace(/^['"]|['"]$/g, '');
        visible = visibleConfigurationProperties.has(property);
      }

      let ancestor: ts.Node | undefined = node.parent;
      while (ancestor && !ts.isSourceFile(ancestor)) {
        if (ts.isJsxAttribute(ancestor)) {
          visible ||= visibleExpressionAttributes.has(
            ancestor.name.getText(sourceFile),
          );
          break;
        }
        if (
          ts.isCallExpression(ancestor) ||
          ts.isFunctionLike(ancestor)
        ) {
          break;
        }
        if (ts.isJsxExpression(ancestor)) {
          const expression = ancestor.expression;
          if (
            expression &&
            isRenderedExpressionLiteral(node, expression)
          ) {
            visible ||= ts.isJsxAttribute(ancestor.parent)
              ? visibleExpressionAttributes.has(
                  ancestor.parent.name.getText(sourceFile),
                )
              : true;
          }
          break;
        }
        ancestor = ancestor.parent;
      }

      const value = node.text.trim();
      if (visible && /[A-Za-z0-9\u4e00-\u9fff]/.test(value)) {
        result.push(value);
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(sourceFile);
  return result;
}

function visibleExpressionLiterals(file: string): string[] {
  return visibleExpressionLiteralsFromSource(readFileSync(file, 'utf8'), file);
}

describe('Go Product Schema frontend projection', () => {
  it('detects English display copy hidden in expressions and configuration', () => {
    expect(visibleExpressionLiteralsFromSource(`
      const status = 'RUNNING';
      const option = { label: 'Provider label' };
      export function View({ loading }: { loading: boolean }) {
        return (
          <div className={'grid'}>
            {loading ? 'Loading' : 'Done'}
          </div>
        );
      }
    `)).toEqual([
      'Provider label',
      'Loading',
      'Done',
    ]);
  });

  it('drives exact sidebar order and admin visibility', () => {
    const schema = schemaFixture();
    // 主导航提供工作台与个人资源页，管理入口仍只对管理员可见。
    expect(selectNavigation(schema, 'main', false).map((item) => item.href)).toEqual([
      '/',
      '/account',
    ]);
    expect(selectNavigation(schema, 'bottom', false)).toEqual([]);
    expect(selectNavigation(schema, 'bottom', true).map((item) => item.href)).toEqual([
      '/admin',
    ]);
  });

  it('declares only navigation destinations the SPA actually routes', () => {
    const router = readFileSync(
      new URL('../../web/src/router.tsx', import.meta.url),
      'utf8',
    );
    const clientRoutes = (
      JSON.parse(
        readFileSync(
          new URL(
            '../../internal/catalog/default_catalog.json',
            import.meta.url,
          ),
          'utf8',
        ),
      ) as {
        application: {
          runtime: { frontend: { client_routes: Record<string, string> } };
          administration: { navigation: Array<{ href: string }> };
        };
      }
    ).application;
    const routed = new Set([
      ...Object.values(clientRoutes.runtime.frontend.client_routes),
      ...clientRoutes.administration.navigation.map((item) => item.href),
    ]);
    for (const item of schemaFixture().content.navigation) {
      if (!routed.has(item.href)) {
        throw new Error(`navigation ${item.key} 指向未路由的路径 ${item.href}`);
      }
    }
    // 路由真源不得反向依赖 navigation：否则死链会重新长回来。
    expect(router).not.toContain('content.navigation');
  });

  it('keeps retired per-capability detail and dashboard declarations out', () => {
    const schema = schemaFixture();
    for (const item of schema.content.navigation) {
      expect(item).not.toHaveProperty('dashboard');
    }
    expect(schema.runtime.frontend).not.toHaveProperty('dashboard');
    expect(schema.runtime.frontend).not.toHaveProperty(
      'task_detail_query_parameter',
    );
    for (const capability of schema.capabilities) {
      expect(capability).not.toHaveProperty('task_detail');
    }
  });

  it('drives task tabs from capability schema and all-label content', () => {
    const tabs = buildTaskTabs(schemaFixture());
    expect(tabs[0]).toEqual({ value: 'all', label: '全部' });
    expect(tabs.slice(1).map((tab) => tab.value)).toEqual([
      'text-to-3d',
      'image-to-3d',
      'text-to-image',
      'image-to-image',
      'remesh',
      'retexture',
      'rigging',
      'animation',
      'background-removal',
      'format-conversion',
      'uv-unwrap',
      'resize',
      'analyze-printability',
      'repair-printability',
      'multi-color-print',
      'text-to-motion',
      'rig-check',
      'asset-recovery',
      'creative-lab-prototype',
      'creative-lab-build',
      'mesh-segmentation',
      'mesh-completion',
      'image-to-splat',
      'image-to-multiview',
      'edit-multiview',
    ]);
  });

  it('changes visible text by changing schema only', () => {
    const schema = schemaFixture();
    schema.content.messages['ui.tasks.title'] = 'Configured task title';
    expect(schemaText(schema, 'ui.tasks.title')).toBe('Configured task title');
  });

  it('loads product schema from the configured core route', () => {
    const schema = schemaFixture();
    expect(schema.runtime.http.routes.product_schema).toBe('/api/v1/schema');

    const source = readFileSync(
      new URL('../components/product-schema-provider.tsx', import.meta.url),
      'utf8',
    );
    expect(source).not.toContain("'/api/v1/schema'");
    expect(source).toContain(
      'initialSchema.runtime.http.routes.product_schema',
    );
  });

  it('drives auth, balance and task history HTTP contracts from Product Schema', () => {
    const schema = schemaFixture();
    expect(schema.task_history.api.query.page).toBe('page');
    expect(schema.task_history.api.fields.thumbnail_url).toBe('thumbnailUrl');
    expect(schema.runtime.frontend.balance_refresh_milliseconds).toBeGreaterThan(0);

    for (const [path, hardcoded] of [
      ['../hooks/use-auth.ts', '/api/auth/session'],
      ['../hooks/use-balance.ts', '/api/meshy/balance'],
    ] as const) {
      const source = readFileSync(new URL(path, import.meta.url), 'utf8');
      expect(source, path).not.toContain(hardcoded);
      expect(source, path).toContain('useProductSchema');
    }
    const balanceSource = readFileSync(
      new URL('../hooks/use-balance.ts', import.meta.url),
      'utf8',
    );
    expect(balanceSource).not.toContain('refreshInterval: 60000');
  });

  it('keeps task-history query controls in task_history only', () => {
    const schema = schemaFixture();
    expect(schema.task_history.api.query.recent).toBe('recent');
    expect(schema.task_history.api.query.hide_failures).toBe('hideFailures');
    expect(schema.task_history.values.enabled).toBe('1');
  });

  it('drives root locale and toaster presentation from Product Schema', () => {
    const schema = schemaFixture();
    expect(schema.runtime.frontend.display_locale).toBe('zh-CN');
    expect(schema.runtime.frontend.toaster.position).toBe('top-right');
    expect(schema.runtime.frontend.toaster.rich_colors).toBe(true);
    expect(schema.runtime.frontend.spinner_frame_milliseconds).toBe(80);
    expect(schema.runtime.frontend.percentage_multiplier).toBe(100);
    expect(schema.runtime.frontend.percentage_suffix).toBe('%');
    expect(schema.runtime.frontend.advanced_model_form_feature).toBe(
      'advanced-model-form',
    );
    expect(schema.runtime.frontend.advanced_model_default_mode).toBe('regular');

    const source = readFileSync(
      new URL('../../web/src/main.tsx', import.meta.url),
      'utf8',
    );
    expect(source).not.toContain('lang="zh-CN"');
    expect(source).not.toContain('position="top-right"');
    expect(source).not.toContain('<Toaster position="');
  });

  it('selects the advanced model form through Offering features', () => {
    const schema = schemaFixture();
    const feature = schema.runtime.frontend.advanced_model_form_feature;
    const advancedOfferings = defaultCatalog.offerings.filter(
      (offering) => offering.enabled !== false && offering.features?.includes(feature),
    );
    expect(advancedOfferings.map((offering) => offering.id)).toEqual([
      'tripo/tripo-v3.1-full/text-to-3d',
      'tripo/tripo-v3.1-full/image-to-3d',
    ]);
    // The narrow uploaded-file route uses its public Schema parameters; the
    // legacy six-format route remains disabled independently of this feature.
    const uploadedConversion = defaultCatalog.offerings.find(
      (offering) => offering.id === 'tripo/format-conversion/uploaded-glb-to-stl',
    );
    expect(uploadedConversion?.enabled).toBe(true);
    expect(uploadedConversion?.features ?? []).not.toContain(feature);
    expect(
      defaultCatalog.offerings.find((offering) => offering.id === 'tripo/tripo-v3.1/format-conversion')?.enabled,
    ).toBe(false);

    // AGENTS 工作区重建后旧 3D 表单页已删除；feature 声明的唯一真源
    // 仍是 Offering Schema（上方断言）。
  });

  it('formats configured message templates and value labels', () => {
    const schema = schemaFixture();
    schema.content.messages['test.greeting'] = 'Hello {name}, {count} items';
    expect(schemaMessage(schema, 'test.greeting', { name: 'Schema', count: 3 })).toBe(
      'Hello Schema, 3 items',
    );
    expect(valueLabel(schema.content.asset_kinds, 'model:glb', 'fallback')).toBe(
      '模型(GLB)',
    );
  });

  it('evaluates provider parameter dependencies from schema data', () => {
    const parameter = {
      disabled_when: [
        {
          parameter: 'generate_multi_view',
          operator: 'equals' as const,
          value: true,
        },
      ],
    };
    expect(parameterDisabled(parameter, { generate_multi_view: true })).toBe(true);
    expect(parameterDisabled(parameter, { generate_multi_view: false })).toBe(false);
  });

  it('projects a runtime version without mutating the product schema source', () => {
    const schema = schemaFixture();
    const projected = withRuntimeVersion(schema, '20260726(12)');

    expect(projected.content.service.version).toBe('20260726(12)');
    expect(schema.content.service.version).toBe('0.1.0');
  });

  it('keeps the server runtime projection when the remote base schema arrives first', () => {
    const source = schemaFixture();
    const serverRuntime = withRuntimeVersion(source, '20260726(12)');
    const remoteBase = schemaFixture();

    const projected = projectRuntimeProductSchema(
      remoteBase,
      serverRuntime,
      undefined,
    );

    expect(projected.content.service.version).toBe('20260726(12)');
  });

  it('keeps migrated shell and task history off the legacy presentation config', () => {
    for (const path of [
      '../components/layout/sidebar.tsx',
      '../app/login/page.tsx',
      '../components/layout/app-topbar.tsx',
      '../components/chat/chat-message.tsx',
    ]) {
      const source = readFileSync(new URL(path, import.meta.url), 'utf8');
      expect(source).not.toContain('provider-config');
      expect(source).not.toContain('CAPABILITY_LABELS');
      expect(source).not.toContain('formatAssetKindCN');
      expect(source).not.toContain('estimateCredits');
    }
  });

  it('rejects visible literals in migrated schema-driven surfaces', () => {
    for (const path of [
      '../components/layout/sidebar.tsx',
      '../app/login/page.tsx',
      '../components/layout/app-topbar.tsx',
      '../components/chat/chat-message.tsx',
    ]) {
      expect(visibleLiteralText(path), path).toEqual([]);
    }
  });

  it('rejects visible literals across every application UI surface', () => {
    const literals = [
      ...applicationTSXFiles(path.join(process.cwd(), 'src', 'app')),
      ...applicationTSXFiles(path.join(process.cwd(), 'src', 'components')),
    ].flatMap((file) =>
      visibleLiteralText(file).map((literal) => ({ file, literal })),
    );

    expect(literals).toEqual([]);
  });

  it('finds a native title on an element or a button component, and leaves a component\'s own title prop alone', () => {
    const found = (source: string) => nativeTitleAttributesFromSource(source);
    expect(found('const a = <button title={label}>x</button>;')).toHaveLength(1);
    expect(found('const a = <a href="/x" download title={label}>x</a>;')).toHaveLength(1);
    expect(found('const a = <span title="t" />;')).toHaveLength(1);
    expect(found('const a = <Action type="button" title={label}>x</Action>;')).toHaveLength(1);
    expect(found('const a = <Button title={label}>x</Button>;')).toHaveLength(1);
    expect(found('const a = <Checkbox title={label} />;')).toHaveLength(1);
    expect(found('const a = <Popover.Trigger title={label}>x</Popover.Trigger>;')).toHaveLength(1);
    expect(found('const a = <DropdownMenuTrigger title={label}>x</DropdownMenuTrigger>;')).toHaveLength(1);
    // Thin wrappers spread the attribute onto their one element: the same native tooltip.
    expect(found('const a = <Link href="/" title={label}>x</Link>;')).toHaveLength(1);
    expect(found('const a = <Surface variant="info" title={label}>x</Surface>;')).toHaveLength(1);
    expect(found('const a = <Input title={label} />;')).toHaveLength(1);
    // Component props named title are headings or names, not tooltips.
    expect(found('const a = <SideDrawer title={heading} onClose={close} />;')).toEqual([]);
    expect(found('const a = <ContextChip title={name} subtitle={sub} />;')).toEqual([]);
    expect(found('const a = <ChipText title={name} />;')).toEqual([]);
    expect(found('const a = <HintTooltip name={label}><Action aria-label={label}>x</Action></HintTooltip>;')).toEqual([]);
  });

  it('keeps native title tooltips out of every component and page: the hover hint is a HintTooltip', () => {
    // Every .tsx under src (hooks and lib included), tests left out.
    const found = applicationTSXFiles(path.join(process.cwd(), 'src')).flatMap((file) => {
      const source = readFileSync(file, 'utf8');
      return nativeTitleAttributesFromSource(source, file).map((entry) => `${path.relative(process.cwd(), file)}: ${entry}`);
    });
    expect(found).toEqual([]);
  });

  it('rejects display copy hidden in UI expressions and configuration objects', () => {
    const literals = schemaDrivenClientSourceFiles().flatMap((file) => [
      ...visibleConfigurationLiterals(file).map((literal) => ({
        file,
        literal,
      })),
      ...visibleExpressionLiterals(file).map((literal) => ({
        file,
        literal,
      })),
    ]);

    expect(literals).toEqual([]);
  });

  it('drives administration tabs from Product Schema', () => {
    const schema = schemaFixture();
    schema.administration.navigation[0].label = 'Configured admin tab';
    expect(schema.administration.navigation[0]).toMatchObject({
      href: '/admin',
      label: 'Configured admin tab',
      icon: 'chart',
      match: 'exact',
    });

    const source = readFileSync(
      new URL('../app/admin/layout.tsx', import.meta.url),
      'utf8',
    );
    expect(source).not.toContain('NAV_ITEMS');
    expect(source).toContain('schema.administration.navigation.map');
  });

  it('keeps task progress state behavior and text out of component code', () => {
    const schema = schemaFixture();
    expect(
      Object.fromEntries(
        schema.task_states.map((state) => [
          state.internal_state,
          state.progress.mode,
        ]),
      ),
    ).toMatchObject({
      PROCESSING: 'reported',
      UPSTREAM_COMPLETED: 'fixed',
      EXPIRED: 'hidden',
      RETRY_PENDING: 'retry-countdown',
    });
  });

  it('drives Remesh and Rigging workflow defaults from capability schema', () => {
    const schema = schemaFixture();
    const remesh = schema.capabilities.find(
      (capability) => capability.id === 'remesh',
    );
    const rigging = schema.capabilities.find(
      (capability) => capability.id === 'rigging',
    );
    if (!remesh?.workflow || !rigging?.workflow) {
      throw new Error('workflow fixture missing');
    }
    remesh.workflow.initial_input_mode = 'upload';
    rigging.workflow.default_auto_remesh = false;

    expect(capabilityWorkflow(schema, 'remesh').initial_input_mode).toBe(
      'upload',
    );
    expect(capabilityWorkflow(schema, 'rigging').default_auto_remesh).toBe(
      false,
    );

    // 旧 rigging 表单页已随 AGENTS 工作区重建删除；capability.workflow
    // 默认值仍由 Schema 驱动（上方断言）。
  });

  it('keeps Chat runtime behavior in Product Schema instead of component literals', () => {
    const paths = {
      '../components/chat/chat-message.tsx': [
        "e.key === 'Escape'",
        "e.key === 'ArrowLeft'",
        "e.key === 'ArrowRight'",
        '.slice(0, 60)',
        "startsWith('/api/')",
        '.slice(0, 8)',
        ": 'img'",
        '}_${suffix}.png',
        'filename=${',
        '本条消息中生成的图片 URL:',
        "startsWith('__stream_')",
        'Math.min(progress, 100)',
      ],
    };

    for (const [path, literals] of Object.entries(paths)) {
      const source = readFileSync(new URL(path, import.meta.url), 'utf8');
      for (const literal of literals) {
        expect(source, `${path}: ${literal}`).not.toContain(literal);
      }
    }
  });

  it('keeps Canvas command copy and history policy in Product Schema', () => {
    const schema = schemaFixture();
    expect(schema.canvas.frontend.history.maximum_depth).toBe(50);
    expect(schema.canvas.frontend.history.recent_limit).toBe(10);
    expect(schema.canvas.frontend.clipboard.offset_x).toBe(20);
    expect(schema.canvas.frontend.clipboard.offset_y).toBe(20);
    for (const key of [
      'context.copy',
      'context.send_to_chat',
      'context.group',
      'context.ungroup',
      'context.paste',
      'context.bring_to_front',
      'context.bring_forward',
      'context.send_backward',
      'context.send_to_back',
      'context.delete',
      'context.unlock',
      'context.lock',
      'context.show',
      'context.hide',
      'context.select_all',
      'history.add.image',
      'history.add.text',
      'history.add.frame',
      'history.add.shape',
      'history.add.pencil',
      'history.add.generic',
      'history.delete',
      'history.move',
      'history.style',
      'history.edit_text',
      'history.front',
      'history.back',
      'history.forward',
      'history.backward',
      'history.visibility',
      'history.reorder',
      'history.delete_many',
      'history.paste',
      'history.group',
      'history.ungroup',
      'history.move_many',
    ]) {
      expect(schema.canvas.presentation.text[key], key).toBeTruthy();
    }

    for (const commandSurface of [
      '../components/canvas/layers-panel.tsx',
      '../components/workspace/canvas-image-actions.tsx',
    ]) {
      const contextSource = readFileSync(
        new URL(commandSurface, import.meta.url),
        'utf8',
      );
      expect(contextSource, commandSurface).not.toMatch(/label:\s*['"`][^'"`]*[\u4e00-\u9fff]/);
      expect(contextSource, commandSurface).not.toMatch(/title=\{?['"`][^'"`]*[\u4e00-\u9fff]/);
    }

    const storeSource = readFileSync(
      new URL('../store/canvas-store.ts', import.meta.url),
      'utf8',
    );
    expect(storeSource).not.toContain('const UNDO_MAX_DEPTH =');
    expect(storeSource).not.toContain('const UNDO_THROTTLE_MS =');
    expect(storeSource).not.toContain('typeLabels');
    expect(storeSource).not.toMatch(
      /description:\s*['"`][^'"`]*[\u4e00-\u9fff]/,
    );
  });

  it('keeps Canvas style, backgrounds and keyboard bindings in Product Schema', () => {
    const frontend = schemaFixture().canvas.frontend;
    expect(frontend.style.preset_colors).toHaveLength(10);
    expect(frontend.style.stroke_widths).toEqual([1, 2, 3, 5, 8]);
    expect(frontend.style.selection_border).toBe(
      '1px solid rgba(99, 102, 241, 0.5)',
    );
    expect(frontend.style.selection_background).toBe(
      'rgba(99, 102, 241, 0.1)',
    );
    expect(frontend.backgrounds.map((background) => background.value)).toEqual([
      'dots',
      'grid',
      'plain',
      'dark',
    ]);
    expect(frontend.keyboard.delete_keys).toEqual(['delete', 'backspace']);
    expect(frontend.viewport).toMatchObject({
      default_zoom: 1,
      default_pan_x: 0,
      default_pan_y: 0,
      minimum_zoom: 0.05,
      maximum_zoom: 3,
      zoom_step: 0.1,
      decimal_places: 2,
      percentage_multiplier: 100,
      fit_padding_pixels: 40,
    });
    expect(frontend.drawing).toMatchObject({
      default_node_size: 256,
      text_node_width: 200,
      text_node_height: 40,
      initial_shape_size: 2,
      pencil_minimum_points: 2,
      pencil_padding: 4,
      minimum_draw_size: 1,
      line_height: 4,
      minimum_committed_shape_size: 10,
      selection_drag_threshold_pixels: 4,
    });

    const styleSource = readFileSync(
      new URL('../components/canvas/canvas-tool-options.tsx', import.meta.url),
      'utf8',
    );
    expect(styleSource).not.toContain('const PRESET_COLORS =');
    expect(styleSource).not.toContain('const STROKE_WIDTHS =');
    expect(styleSource).not.toContain("|| '#333333'");
    expect(styleSource).not.toContain("|| 'transparent'");
    expect(styleSource).not.toContain('?? 2');

    const toolbarSource = readFileSync(
      new URL('../components/canvas/canvas-zoom-controls.tsx', import.meta.url),
      'utf8',
    );
    expect(toolbarSource).not.toContain("value: 'dots'");
    expect(toolbarSource).not.toContain("value: 'grid'");
    expect(toolbarSource).not.toContain("value: 'plain'");
    expect(toolbarSource).not.toContain("value: 'dark'");

    const boardSource = readFileSync(
      new URL('../components/canvas/canvas-board.tsx', import.meta.url),
      'utf8',
    );
    expect(boardSource).not.toContain("case 'grid'");
    expect(boardSource).not.toContain("case 'plain'");
    expect(boardSource).not.toContain("case 'dark'");
    expect(boardSource).not.toContain("case 'dots'");
    expect(boardSource).not.toContain("case 'v'");
    expect(boardSource).not.toContain("case 'h'");
    expect(boardSource).not.toContain("case 't'");
    expect(boardSource).not.toContain("case 'r'");
    expect(boardSource).not.toContain("case 'p'");
    expect(boardSource).not.toContain("case 'l'");
    expect(boardSource).not.toContain("case 'o'");
    expect(boardSource).not.toContain("case 'a'");
    for (const hardcoded of [
      'MIN_ZOOM',
      'MAX_ZOOM',
      'ZOOM_STEP',
      'DEFAULT_NODE_SIZE',
      'DEFAULT_VIEWPORT',
      'width: 200',
      'height: 40',
      'const pad = 4',
      'const lineH = 4',
      'node.width < 10',
      'right - left < 4',
      '* 100) / 100',
      "canvasBg === 'dark'",
      "'1px solid rgba(99, 102, 241, 0.5)'",
      "'rgba(99, 102, 241, 0.1)'",
    ]) {
      expect(boardSource, hardcoded).not.toContain(hardcoded);
    }

    const storeSource = readFileSync(
      new URL('../store/canvas-store.ts', import.meta.url),
      'utf8',
    );
    for (const hardcoded of [
      'export const MIN_ZOOM',
      'export const MAX_ZOOM',
      'export const ZOOM_STEP',
      'export const DEFAULT_NODE_SIZE',
      'export const MAX_NODE_EDGE',
      'export const DEFAULT_VIEWPORT',
      'const padding = 40',
      'function round2',
    ]) {
      expect(storeSource, hardcoded).not.toContain(hardcoded);
    }
  });
});
