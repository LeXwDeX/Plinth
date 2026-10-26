// L0 真实浏览器启动检查（零费用、不产生任何生成任务）。
//
// 为什么需要这一层：站点曾在真实浏览器里整页空白——Go Web Host 内联注入
// Product Schema 的脚本被 Schema 声明的 CSP 按 default-src 拦掉，main.tsx
// 随即抛 "Go Web Host did not inject Product Schema"。当时的门禁只断言安全头
// "存在"，tsc/vitest/go test 与 API 级冒烟都不在浏览器里执行，于是全线绿灯
// 仍交付了一个打不开的站点。
//
// 用法：
//   python3 scripts/release-env.py smoke dev-test -- node scripts/smoke/browser-boot.mjs
//   ADMIN_USERNAME=... ADMIN_PASSWORD=... node scripts/smoke/browser-boot.mjs
//
// 有凭据时先登录再校验工作区首页；无凭据时只校验公开登录页。
import { createRequire } from 'node:module';
import { homedir } from 'node:os';
import path from 'node:path';

const require = createRequire(import.meta.url);

const base = (process.env.BASE || 'http://127.0.0.1:13333').replace(/\/+$/, '');
const browsers = (process.env.SMOKE_BROWSERS || 'chromium,webkit')
  .split(',')
  .map((name) => name.trim())
  .filter(Boolean);
const username = process.env.ADMIN_USERNAME || '';
const password = process.env.ADMIN_PASSWORD || '';

// playwright 不进 package.json 依赖：发布镜像只需构建前端，带上浏览器驱动
// 会无谓拖慢构建。按 环境变量 → 本地依赖 → 常见运行时目录 依次尝试解析。
function resolvePlaywright() {
  const candidates = [];
  if (process.env.PLAYWRIGHT_MODULE_PATH) {
    candidates.push(process.env.PLAYWRIGHT_MODULE_PATH);
  }
  try {
    candidates.push(require.resolve('playwright'));
  } catch {}
  candidates.push(
    path.join(
      homedir(),
      '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright',
    ),
  );
  for (const candidate of candidates) {
    try {
      return require(candidate);
    } catch {}
  }
  throw new Error(
    '未找到 playwright：请安装浏览器驱动（playwright install）或设置 PLAYWRIGHT_MODULE_PATH',
  );
}

// CSP 违规在 DevTools 里是 issue 而非 console error，页面脚本也读不到，
// 必须在文档创建前注册监听，否则这次检查就会重蹈"看不见"的覆辙。
const captureCSPViolations = () => {
  window.__cspViolations = [];
  document.addEventListener('securitypolicyviolation', (event) => {
    window.__cspViolations.push({
      directive: event.violatedDirective || event.effectiveDirective || '',
      blocked: event.blockedURI || '',
    });
  });
};

const playwright = resolvePlaywright();
let pass = 0;
let fail = 0;
let skip = 0;
const ok = (name) => {
  console.log(`PASS: ${name}`);
  pass += 1;
};
const bad = (name, detail) => {
  console.log(`FAIL: ${name}${detail ? ` — ${detail}` : ''}`);
  fail += 1;
};
const skipped = (name, detail) => {
  console.log(`SKIP: ${name}${detail ? ` — ${detail}` : ''}`);
  skip += 1;
};
// SMOKE_REQUIRE_BROWSERS 里点名的浏览器不允许降级为跳过。
const required = (process.env.SMOKE_REQUIRE_BROWSERS || '')
  .split(',')
  .map((name) => name.trim())
  .filter(Boolean);

for (const name of browsers) {
  const engine = playwright[name];
  if (!engine) {
    bad(`${name} 启动检查`, `playwright 未提供 ${name}`);
    continue;
  }
  let browser;
  try {
    browser = await engine.launch();
  } catch (error) {
    // 浏览器二进制缺失属于环境未就绪，与"页面启动失败"是两回事：
    // 前者跳过并说明如何补齐，后者必须失败。点名 required 时不允许降级。
    const message = error.message.split('\n')[0];
    const missing = /Executable doesn't exist|please run the following command/i.test(
      error.message,
    );
    if (missing && !required.includes(name)) {
      skipped(`${name} 启动检查`, `浏览器未安装（${message}）；安装：npx playwright install ${name}`);
    } else {
      bad(`${name} 启动检查`, `浏览器不可用：${message}`);
    }
    continue;
  }
  try {
    const context = await browser.newContext();
    await context.addInitScript(captureCSPViolations);
    const page = await context.newPage();
    const pageErrors = [];
    const consoleErrors = [];
    const externalRequests = [];
    const appOrigin = new URL(base).origin;
    page.on('request', (request) => {
      try {
        const target = new URL(request.url());
        if (['http:', 'https:'].includes(target.protocol) && target.origin !== appOrigin) {
          externalRequests.push(`${request.method()} ${target.origin}`);
        }
      } catch {}
    });
    page.on('pageerror', (error) => pageErrors.push(error.message));
    page.on('console', (message) => {
      if (message.type() === 'error') {
        consoleErrors.push(message.text());
      }
    });

    if (username && password) {
      const response = await context.request.post(`${base}/api/auth/login`, {
        data: { username, password },
      });
      if (!response.ok()) {
        bad(`${name} 登录`, `HTTP ${response.status()}`);
        continue;
      }
    }

    const target = username && password ? '/' : '/login';
    const response = await page.goto(`${base}${target}`, {
      waitUntil: 'load',
      timeout: 30000,
    });
    if (!response || !response.ok()) {
      bad(`${name} 打开 ${target}`, `HTTP ${response ? response.status() : '无响应'}`);
      continue;
    }
    // SPA 在 load 之后才挂载 React，留出一次任务轮询周期。
    await page.waitForTimeout(1500);

    const state = await page.evaluate(() => ({
      schema: typeof window.__ART_AI_PRODUCT_SCHEMA__ === 'object'
        ? Boolean(window.__ART_AI_PRODUCT_SCHEMA__?.revision)
        : false,
      rootChildren: document.getElementById('root')?.childElementCount ?? 0,
      title: document.title,
      violations: window.__cspViolations || [],
      text: (document.body.innerText || '').trim().slice(0, 200),
    }));

    state.schema
      ? ok(`${name} Product Schema 已注入`)
      : bad(`${name} Product Schema 已注入`, 'window.__ART_AI_PRODUCT_SCHEMA__ 缺失');
    state.rootChildren > 0
      ? ok(`${name} #root 已渲染`)
      : bad(`${name} #root 已渲染`, 'React 未挂载，页面为空白');
    state.violations.length === 0
      ? ok(`${name} 无 CSP 违规`)
      : bad(
          `${name} 无 CSP 违规`,
          state.violations
            .map((violation) => `${violation.directive} ← ${violation.blocked}`)
            .join('; '),
        );
    pageErrors.length === 0
      ? ok(`${name} 无未捕获异常`)
      : bad(`${name} 无未捕获异常`, pageErrors.join('; '));
    consoleErrors.length === 0
      ? ok(`${name} 无 console error`)
      : bad(`${name} 无 console error`, consoleErrors.join('; '));
    externalRequests.length === 0
      ? ok(`${name} 客户端请求仅访问 Plinth 同源`)
      : bad(`${name} 客户端请求仅访问 Plinth 同源`, [...new Set(externalRequests)].join('; '));
    state.title
      ? ok(`${name} 文档标题已设置`)
      : bad(`${name} 文档标题已设置`, 'title 为空');
    if (username && password) {
      new URL(page.url()).pathname === '/'
        ? ok(`${name} 工作区首页可达`)
        : bad(`${name} 工作区首页可达`, `被重定向到 ${page.url()}`);
    }
    console.log(`INFO: ${name} 渲染文本样本：${state.text.replace(/\s+/g, ' ')}`);
  } finally {
    await browser.close();
  }
}

console.log(`L0 浏览器启动检查：PASS=${pass} FAIL=${fail} SKIP=${skip}`);
process.exit(fail === 0 ? 0 : 1);
