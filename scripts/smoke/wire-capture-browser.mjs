#!/usr/bin/env node
// Pre-submit browser entry for TestWireCaptureServer. No trace, HAR, screenshots,
// storage state, response dumps or console forwarding: even errors use fixed codes.
import { createHash } from 'node:crypto';
import { readFile, stat, lstat } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { homedir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';

const root = fileURLToPath(new URL('../..', import.meta.url));
const require = createRequire(import.meta.url);
// Fixed, non-private scenario input. Register this exact value before a later live round.
export const syntheticPrompt = '生成一个简单的无文字蓝色立方体 3D 模型。只使用已选的 Meshy 模型，只生成一次，不要重试。';
const digest = value => createHash('sha256').update(value).digest('hex');
const check = (condition, code) => { if (!condition) throw Error(code); };

export function numericOrigin(value) {
  // Reject URL normalization tricks, credentials, query, fragment and implicit ports.
  check(typeof value === 'string' && /^http:\/\/(127(?:\.(?:0|[1-9]\d{0,2})){3}|\[::1\]):[1-9]\d{0,4}$/.test(value), 'numeric_loopback_origin_required');
  const url = new URL(value);
  check(url.origin === value && Number(url.port) <= 65535, 'numeric_loopback_origin_required');
  return url.origin;
}

export function localRoute(value, base) {
  check(typeof value === 'string' && value.startsWith('/') && !value.startsWith('//'), 'schema_route_not_local');
  const url = new URL(value, base);
  check(url.origin === base && !url.username && !url.password && !url.hash && !value.includes('\\'), 'schema_route_not_local');
  return url.pathname + url.search;
}

function jsonObject(body) {
  try {
    if (typeof body !== 'string' || body.length > 16384) return null;
    const value = JSON.parse(body);
    return value && typeof value === 'object' && !Array.isArray(value) ? value : null;
  } catch { return null; }
}

const sameObject = (value, expected) => value && !Array.isArray(value)
  && Object.keys(value).length === Object.keys(expected).length
  && Object.keys(expected).every(key => Object.hasOwn(value, key) && value[key] === expected[key]);
const safeID = value => typeof value === 'string' && /^[a-zA-Z0-9_-]{1,256}$/.test(value);

export function requestDecision(raw, method, base, permittedWrites, body, live = false) {
  let url;
  try { url = new URL(raw); } catch { return 'blocked_origin'; }
  if (url.protocol === 'blob:' && url.origin === base && method === 'GET') return 'local_asset';
  if (url.origin !== base || url.username || url.password || !['http:', 'https:'].includes(url.protocol)) return 'blocked_origin';
  if (['GET', 'HEAD'].includes(method)) return 'read';
  if (live) return 'write';
  const key = `${method} ${url.pathname}`;
  if (raw === base + url.pathname && permittedWrites.has(key) && sameObject(jsonObject(body), permittedWrites.get(key))) {
    permittedWrites.delete(key); // One login and one new empty session; never retry a write.
    return 'write';
  }
  return 'blocked_write';
}

export function createCanvasInitialization(schema, base, ownerID) {
  const legacy = schema.runtime.http.legacy_routes, canvas = schema.canvas, f = canvas.api.fields;
  const projects = legacy.canvas_projects, token = `{${canvas.api.path_parameters.project_id}}`;
  check(typeof projects === 'string' && /^\/[a-zA-Z0-9_/-]+$/.test(projects) && !projects.includes('//')
    && legacy.canvas_project === `${projects}/${token}` && legacy.canvas_nodes === `${projects}/${token}/nodes`, 'canvas_routes_invalid');
  const names = ['id', 'user_id', 'session_id', 'created', 'nodes', 'revision', 'canvas_width', 'canvas_height',
    'coordinate_version', 'placed_results', 'project_id', 'type', 'x', 'y', 'width', 'height', 'z_index', 'data', 'visible', 'locked'];
  check(names.every(key => typeof f[key] === 'string' && f[key]) && new Set(names.map(key => f[key])).size === names.length, 'canvas_fields_invalid');
  const front = canvas.frontend, board = front.artboard, layer = front.initial_layer, version = canvas.defaults.coordinate_version;
  const width = board.default_width, height = board.default_height, delay = front.auto_save_delay_milliseconds;
  check([width, height, version].every(value => Number.isSafeInteger(value) && value > 0)
    && [board.center_x, board.center_y].every(Number.isFinite) && Number.isSafeInteger(delay) && delay >= 0 && delay <= 10000
    && typeof layer.enabled === 'boolean' && typeof layer.fill === 'string' && typeof ownerID === 'string' && ownerID, 'canvas_defaults_invalid');
  const data = { taskId: front.upload_task_id, name: schema.content.messages['ui.canvas.layers.initial_name'], blank: true,
    ...(layer.fill !== front.export.transparent_background_value ? { fillColor: layer.fill } : {}) };
  check(typeof data.taskId === 'string' && data.taskId && typeof data.name === 'string' && data.name, 'canvas_layer_invalid');
  const attributes = { [f.type]: 'image', [f.x]: board.center_x - width / 2, [f.y]: board.center_y - height / 2,
    [f.width]: width, [f.height]: height, [f.z_index]: 0, [f.visible]: true, [f.locked]: false };
  // planAutoJoin([], null) seeds an empty session with only this protocol watermark.
  const initialPlaced = 'since:' + new Date(0).toISOString();
  const validPlaced = value => Array.isArray(value) && value.length === 1 && value[0] === initialPlaced;
  const nodeRejection = nodes => {
    if (!Array.isArray(nodes) || nodes.length !== Number(layer.enabled)) return 'canvas_node_count';
    if (!layer.enabled) return null;
    if (!nodes[0] || typeof nodes[0] !== 'object') return 'canvas_node_shape';
    const { [f.id]: id, [f.data]: raw, ...rest } = nodes[0];
    if (!safeID(id)) return 'canvas_node_id';
    if (!sameObject(rest, attributes)) return 'canvas_node_geometry';
    return sameObject(jsonObject(raw), data) ? null : 'canvas_node_data';
  };
  let sessionID, projectID, nodeID, rejection;
  const deny = reason => { rejection = reason; return null; };
  const counts = { canvas_create: 0, canvas_save: 0 };
  const item = template => template.replace(token, projectID);
  const bound = project => project?.[f.id] === projectID && project[f.user_id] === ownerID && project[f.session_id] === sessionID;
  return {
    counts, projectsURL: base + projects, settleMS: delay + 500,
    get rejection() { return rejection; },
    bindSession(id) {
      check(!sessionID && safeID(id), 'canvas_session_binding_invalid');
      sessionID = id;
    },
    bindProject(project) {
      check(counts.canvas_create === 1 && !projectID && safeID(project?.[f.id]) && project[f.created] === true
        && project[f.user_id] === ownerID && project[f.session_id] === sessionID && project[f.revision] === 1, 'canvas_project_binding_invalid');
      projectID = project[f.id];
    },
    allow(raw, method, body) {
      const value = jsonObject(body);
      if (!value || !sessionID) return null;
      if (method === 'POST' && raw === base + projects && counts.canvas_create === 0
        && sameObject(value, { [f.session_id]: sessionID })) {
        counts.canvas_create += 1; // Consume before continue; an uncertain response never re-arms it.
        return 'canvas_create';
      }
      if (method !== 'PUT' || !projectID || raw !== base + item(legacy.canvas_nodes) || counts.canvas_save !== 0) return null;
      const { [f.nodes]: nodes, [f.placed_results]: placed, ...frame } = value;
      if (!sameObject(frame, { [f.revision]: 1, [f.canvas_width]: width, [f.canvas_height]: height, [f.coordinate_version]: version })) return deny('canvas_frame_mismatch');
      if (!validPlaced(placed)) return deny('canvas_placed_results_mismatch');
      const reason = nodeRejection(nodes);
      if (reason) return deny(reason);
      counts.canvas_save += 1;
      nodeID = nodes[0]?.[f.id];
      return 'canvas_save';
    },
    projectRoute() { check(projectID, 'canvas_project_unbound'); return item(legacy.canvas_project); },
    assertSaved(project) {
      const nodes = project?.[f.nodes];
      const keys = [f.id, f.data, ...Object.keys(attributes)];
      check(counts.canvas_save === 1 && bound(project) && project[f.revision] === 2
        && project[f.canvas_width] === width && project[f.canvas_height] === height && project[f.coordinate_version] === version
        && validPlaced(project[f.placed_results])
        && Array.isArray(nodes) && nodes.every(node => node?.[f.project_id] === projectID)
        && nodeRejection(nodes.map(node => Object.fromEntries(keys.map(key => [key, node[key]])))) === null
        && nodes[0]?.[f.id] === nodeID, 'canvas_saved_projection_invalid');
    },
  };
}

export function chooseOffering(offerings, requested, provider = 'meshy') {
  check(Array.isArray(offerings), 'offerings_missing');
  const models = offerings.filter(item => item.capability === 'text-to-3d'
    && (!provider || item.provider_id === provider));
  check(models.every(item => typeof item.available === 'boolean'), 'offering_availability_invalid');
  const available = models.filter(item => item.available);
  const selected = requested ? available.find(item => item.id === requested)
    : available.find(item => item.recommended) ?? available[0];
  check(selected, 'offering_unavailable');
  check(/^[a-zA-Z0-9._/-]+$/.test(selected.id), 'offering_id_invalid');
  return { models, selected };
}

function publicLabel(value) {
  check(typeof value === 'string' && value.length > 0 && value.length <= 256
    && !/[\r\n\x00-\x1f]|https?:|[?=&]/i.test(value) && !value.includes(syntheticPrompt), 'public_label_invalid');
  return value;
}

function priceSummary(offering) {
  if (offering.price_mode === 'native') {
    const quote = offering.default_native_quote;
    check(quote?.version === 1 && /^(0|[1-9]\d*)(\.\d{1,6})?$/.test(quote.total_amount)
      && /^[a-z][a-z0-9._-]{0,63}$/.test(quote.unit), 'declared_price_missing');
    return { mode: 'native', amount: quote.total_amount, unit: quote.unit, unit_label: publicLabel(quote.unit_label) };
  }
  check(offering.credits_metered === true && Number.isFinite(offering.estimated_credits), 'declared_price_missing');
  return { mode: 'legacy', estimated_credits: offering.estimated_credits };
}

async function privateJSON(filename) {
  const info = await lstat(filename);
  check(info.isFile() && (info.mode & 0o777) === 0o600 && info.uid === process.getuid()
    && info.size <= 1 << 20, 'private_manifest_invalid');
  return JSON.parse(await readFile(filename, 'utf8'));
}

export async function run(argv = process.argv.slice(2)) {
  const report = { status: 'failed', preflight: 'not_run', capture: 'blocked_capture', submitted: false,
    preflight_kind: 'restricted_writes',
    // These are current source-contract limits, not a fictitious server capability probe.
    blockers: ['deferred_body_8MiB_total_64MiB_no_media_spool', 'upstream_query_invalidates_capture',
      'public_candidate_requires_synthetic_media_recipe', 'exact_dynamic_id_url_policy_no_raw_getter'],
    network: { blocked_origin: 0, blocked_write: 0, websocket: 0, page_errors: 0, console_errors: 0 },
    allowed_writes: { mock_login: 0, session_create: 0, canvas_create: 0, canvas_save: 0 } };
  let browser;
  let phase = 'arguments';
  try {
    const { values: args } = parseArgs({ args: argv, options: {
      base: { type: 'string' }, 'capture-root': { type: 'string' }, 'web-assets-root': { type: 'string' },
      offering: { type: 'string' }, provider: { type: 'string', default: 'meshy' }, browser: { type: 'string', default: 'chromium' },
      'mock-preflight': { type: 'boolean', default: false }, live: { type: 'boolean', default: false },
    } });
    check(!process.env.DEBUG && !process.env.PWDEBUG, 'debug_logging_not_supported');
    check(['chromium', 'webkit'].includes(args.browser), 'browser_not_supported');
    check(!(args.live && args['mock-preflight']), 'live_mock_conflict');
    check(Boolean(args['capture-root']) !== args['mock-preflight'], 'capture_root_or_mock_preflight_required');
    report.mode = args['mock-preflight'] ? 'offline_mock_preflight' : args.live ? 'live_requested' : 'capture_preflight';
    let manifest;
    if (args['capture-root']) {
      check(path.isAbsolute(args['capture-root']) && !args['web-assets-root'], 'capture_root_invalid');
      manifest = await privateJSON(path.join(args['capture-root'], 'capture.json'));
      check(/^[a-f0-9]{40}$/.test(manifest.source_sha) && /^[a-f0-9]{64}$/.test(manifest.catalog_sha256), 'manifest_revision_invalid');
      check(digest(await readFile(path.join(args['capture-root'], 'catalog.json'))) === manifest.catalog_sha256, 'catalog_digest_mismatch');
      check(manifest.policy?.Allowed?.[syntheticPrompt] === true, 'synthetic_prompt_not_preregistered');
      report.source_sha_label = manifest.source_sha;
      report.catalog_sha256 = manifest.catalog_sha256;
      report.server_identity = 'manifest_only_not_runtime_attestation';
    }
    const base = numericOrigin(args.base ?? (manifest && `http://${manifest.address}`));
    if (manifest) check(base === numericOrigin(`http://${manifest.address}`), 'manifest_address_mismatch');
    phase = 'built_frontend';
    const webRoot = manifest?.web_assets_root ?? args['web-assets-root'] ?? path.join(root, 'build/go-web');
    check(path.isAbsolute(webRoot) && (await stat(path.join(webRoot, 'index.html'))).isFile(), 'built_frontend_missing');
    report.frontend_index_sha256 = digest(await readFile(path.join(webRoot, 'index.html')));

    phase = 'browser_launch';
    // Same resolver and managed launcher as smoke/browser-boot.mjs; no dependency or download added.
    let pw;
    for (const candidate of [process.env.PLAYWRIGHT_MODULE_PATH, 'playwright',
      path.join(homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright')].filter(Boolean)) {
      try { pw = require(candidate); break; } catch { /* existing L0 fallback */ }
    }
    check(pw, 'playwright_unavailable');
    browser = await pw[args.browser].launch({ headless: true });
    report.browser = args.browser;
    report.browser_version = browser.version();
    const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: 'zh-CN',
      serviceWorkers: 'block', acceptDownloads: false });
    const permittedWrites = new Map(), canvasRequests = {};
    let page, canvas, sessionReady, canvasReady, loginURL;
    await context.route('**/*', async route => {
      const request = route.request();
      let decision = requestDecision(request.url(), request.method(), base, permittedWrites, request.postData(), args.live);
      let allowedKind;
      const uiFetch = page && request.resourceType() === 'fetch' && request.frame() === page.mainFrame();
      if (decision === 'write') {
        if (uiFetch && canvas && new URL(request.frame().url()).pathname === '/') {
          if (request.method() === 'POST' && request.url() === canvas.projectsURL) await sessionReady;
          else if (request.method() === 'PUT' && request.url().startsWith(canvas.projectsURL + '/')) await canvasReady;
          allowedKind = canvas.allow(request.url(), request.method(), request.postData());
          if (allowedKind) canvasRequests[allowedKind] = request;
        }
        if (!allowedKind && request.url() === loginURL) allowedKind = 'mock_login';
        if (!allowedKind && !args.live) allowedKind = 'session_create';
      }
      if (decision === 'blocked_write' && canvas && uiFetch && new URL(request.frame().url()).pathname === '/') {
        if (request.method() === 'POST' && request.url() === canvas.projectsURL) await sessionReady;
        else if (request.method() === 'PUT' && request.url().startsWith(canvas.projectsURL + '/')) await canvasReady;
        allowedKind = canvas.allow(request.url(), request.method(), request.postData());
        if (allowedKind) {
          canvasRequests[allowedKind] = request;
          decision = 'write';
        }
      }
      if (decision.startsWith('blocked_')) {
        report.network[decision] += 1;
        if (canvas?.rejection) report.canvas_guard_rejection = canvas.rejection;
        return route.abort('blockedbyclient');
      }
      if (allowedKind) report.allowed_writes[allowedKind] += 1;
      return route.continue({ headers: { ...request.headers(), 'cache-control': 'no-cache' } });
    });
    await context.routeWebSocket('**/*', socket => { report.network.websocket += 1; socket.close(); });
    page = await context.newPage();
    page.setDefaultTimeout(20000);
    // Observe all contexts/redirects too. Never retain URLs or error/console messages.
    context.on('request', request => {
      const url = new URL(request.url());
      if (['http:', 'https:'].includes(url.protocol) && url.origin !== base) report.network.blocked_origin += 1;
    });
    page.on('pageerror', () => { report.network.page_errors += 1; });
    page.on('console', message => { if (message.type() === 'error') report.network.console_errors += 1; });
    await context.addInitScript(() => {
      window.__wireCSPCount = 0;
      document.addEventListener('securitypolicyviolation', () => { window.__wireCSPCount += 1; });
    });
    const json = async (route, method = 'GET') => {
      const local = localRoute(route, base);
      const response = await page.evaluate(async ({ local, method }) => {
        const result = await fetch(local, { method, redirect: 'error', cache: 'no-store',
          ...(method === 'POST' ? { headers: { 'Content-Type': 'application/json' }, body: '{}' } : {}) });
        return { status: result.status, body: await result.json() };
      }, { local, method });
      check(response.status === 200, 'public_endpoint_rejected');
      return response.body;
    };
    phase = 'login_page';
    check((await page.goto(`${base}/login`, { waitUntil: 'load' }))?.ok(), 'login_page_unavailable');
    await page.locator('#username').waitFor();
    const injected = await page.evaluate(() => window.__ART_AI_PRODUCT_SCHEMA__);
    check(injected?.revision, 'schema_not_injected');
    const schema = (await json(injected.runtime.http.routes.product_schema)).schema;
    check(schema?.revision === injected.revision, 'schema_revision_mismatch');
    report.schema_revision = publicLabel(schema.revision);
    const routes = schema.runtime.http.routes;
    const legacy = schema.runtime.http.legacy_routes;
    const text = key => { const value = schema.content.messages[key]; check(typeof value === 'string', 'schema_label_missing'); return value; };
    report.login_page = 'pass';
    phase = 'isolated_login';
    loginURL = base + localRoute(legacy.auth_mock, base);
    permittedWrites.set(`POST ${localRoute(legacy.auth_mock, base)}`, {});
    await json(legacy.auth_mock, 'POST');
    const auth = await json(legacy.auth_session);
    check(auth.user && auth.isAdmin === false, 'isolated_nonadmin_login_required');
    if (manifest) check(auth.user.id === 'wire-capture-owner', 'capture_owner_mismatch');
    const version = await json(legacy.version);
    report.app_version = /^[a-f0-9]{40}$/.test(version.version) ? version.version : 'unknown';
    phase = 'schema_offerings';
    const listed = await json(`${localRoute(routes.offerings, base)}?capability=text-to-3d`);
    const { models, selected } = chooseOffering(listed.offerings, args.offering, args.provider);
    report.meshy_options = models.map(item => ({ label: publicLabel(item.model_name), available: item.available }));
    report.offering_revision = publicLabel(selected.revision);
    report.offering_label = publicLabel(selected.model_name);
    report.price_declaration = priceSummary(selected);

    phase = 'isolated_empty_owner';
    const saved = await json(`${localRoute(legacy.chat_sessions, base)}?search=&limit=1`);
    const projects = await json(legacy.canvas_projects), baselineTasks = await json(`${routes.tasks}?limit=1`);
    check(Array.isArray(saved.sessions) && saved.sessions.length === 0 && saved.total === 0
      && Array.isArray(projects) && projects.length === 0 && Array.isArray(baselineTasks.tasks) && baselineTasks.tasks.length === 0, 'fresh_empty_owner_required');
    canvas = createCanvasInitialization(schema, base, auth.user.id);
    permittedWrites.set(`POST ${localRoute(legacy.chat_sessions, base)}`, { [schema.chat.api.fields.title]: '' });
    sessionReady = page.waitForResponse(response => response.url() === base + legacy.chat_sessions
      && response.request().method() === 'POST').then(async response => {
      check(response.status() === 201, 'session_creation_failed');
      const session = await response.json(), id = session[schema.chat.api.response_fields.id];
      canvas.bindSession(id);
      return id;
    }).catch(() => null);
    canvasReady = page.waitForResponse(response => response.request() === canvasRequests.canvas_create).then(async response => {
      check(response.status() === 201, 'canvas_creation_failed');
      canvas.bindProject(await response.json());
      return true;
    }).catch(() => null);
    const saveReady = page.waitForResponse(response => response.request() === canvasRequests.canvas_save).then(async response => {
      const body = await response.json(), f = schema.canvas.api.fields;
      check(response.status() === 200 && body[f.ok] === true && body[f.count] === Number(schema.canvas.frontend.initial_layer.enabled), 'canvas_save_failed');
      return true;
    }).catch(() => null);
    phase = 'workspace_session';
    check((await page.goto(`${base}/`, { waitUntil: 'load' }))?.ok(), 'workspace_unavailable');
    await page.getByTestId('studio-shell').waitFor();
    const sessionID = await sessionReady;
    check(sessionID, 'session_creation_failed');
    await page.waitForURL(url => url.searchParams.get('session') === sessionID);
    report.session_creation = 'workspace_initial';
    phase = 'canvas_initialization';
    check(await canvasReady && await saveReady, 'canvas_initialization_failed');
    canvas.assertSaved(await json(canvas.projectRoute()));
    report.canvas_initialization = { status: 'created_and_saved', binding: 'owner_session_project_verified',
      revision_before: 1, revision_after: 2, coordinate_version: schema.canvas.defaults.coordinate_version };
    const sessionRoute = template => template.replace('{sessionID}', encodeURIComponent(sessionID));
    const empty = async () => {
      const pending = await json(sessionRoute(routes.chat_session_pending_approvals));
      const tasks = await json(`${routes.tasks}?session_id=${encodeURIComponent(sessionID)}&limit=1`);
      check(Array.isArray(pending.items) && Array.isArray(tasks.tasks), 'empty_session_projection_invalid');
      check(pending.items.length === 0 && tasks.tasks.length === 0, 'unexpected_execution_in_session');
    };
    phase = 'empty_session_projection';
    await empty();
    phase = 'manual_approval';
    const approval = await json(sessionRoute(routes.chat_session_approval));
    check(approval[schema.chat.api.response_fields.approval_mode] === 'manual'
      && schema.chat.approval_policy.manual_approval_capabilities.includes(selected.capability), 'manual_approval_required');
    await page.getByTestId('studio-approval-menu').filter({ hasText: text('ui.workspace.approval.mode_manual') }).waitFor();
    report.approval = { mode: 'manual', pending: 0, decision: 'not_requested' };

    phase = 'meshy_picker';
    await page.getByTestId('workspace-model-picker').click();
    const row = page.getByTestId('workspace-model-picker-model').getByRole('menuitemradio')
      .filter({ has: page.getByText(selected.model_name, { exact: true }) });
    await row.waitFor();
    check(await row.count() === 1 && await row.isEnabled(), 'meshy_row_not_unique_or_enabled');
    await row.click();
    await row.and(page.locator('[aria-checked="true"]')).waitFor();
    await page.keyboard.press('Escape');
    await page.getByTestId('workspace-model-picker-panel').waitFor({ state: 'hidden' });
    report.model_selector = 'pass';

    phase = 'offering_cost_panel';
    await page.getByTestId('composer-params').click();
    const panel = page.locator('[data-testid="params-domain"][data-domain="model-3d"]');
    await panel.locator(`[data-testid="params-cost"]`).waitFor();
    check(await panel.getAttribute('data-offering') === selected.id, 'ui_offering_mismatch');
    const price = report.price_declaration;
    const amount = price.mode === 'native' ? `${Number(price.amount).toLocaleString('zh-CN', { maximumFractionDigits: 6 })} ${price.unit_label}` : null;
    const expectedCost = price.mode === 'native' ? text('ui.pricing.default_estimate').replace('{amount}', amount)
      : text('ui.studio.params.cost_one').replace('{total}', String(price.estimated_credits));
    check((await panel.getByTestId('params-cost').innerText()).includes(expectedCost), 'ui_price_mismatch');
    report.cost_label = publicLabel(expectedCost);
    await page.keyboard.press('Escape');
    await page.getByTestId('composer-params-panel').waitFor({ state: 'hidden' });
    phase = 'synthetic_draft';
    const draft = page.getByTestId('studio-composer').locator('textarea');
    await draft.fill(syntheticPrompt); // fill, never Enter: Enter sends a real Agent turn.
    check(await draft.inputValue() === syntheticPrompt, 'draft_mismatch');
    report.prompt_sha256 = digest(syntheticPrompt);
    report.send_enabled = await page.getByTestId('composer-send').isEnabled();
    await empty();
    if (args.live) {
      phase = 'live_send';
      check(report.send_enabled, 'send_disabled');
      await page.getByTestId('composer-send').click();
      report.submitted = true;
      phase = 'live_approval';
      const approve = page.getByTestId('studio-approval-approve');
      await approve.waitFor({ timeout: 180000 });
      await approve.click();
      report.approval = { mode: 'manual', pending: 0, decision: 'approved' };
      phase = 'live_task';
      const deadline = Date.now() + 1200000;
      let succeeded = false;
      while (!succeeded) {
        const listed = await json(`${routes.tasks}?session_id=${encodeURIComponent(sessionID)}&limit=20`);
        const tasks = Array.isArray(listed.tasks) ? listed.tasks : [];
        for (const task of tasks) {
          const state = typeof task.status === 'string' ? task.status : task.state;
          check(state !== 'FAILED' && state !== 'CANCELED', 'live_task_failed');
          if (state === 'SUCCEEDED') succeeded = true;
        }
        if (succeeded) break;
        check(Date.now() < deadline, 'live_task_timeout');
        await page.waitForTimeout(5000);
      }
      await page.waitForTimeout(3000);
      report.csp_violations = await page.evaluate(() => window.__wireCSPCount);
      check(report.csp_violations === 0 && report.network.blocked_origin === 0, 'live_browser_guard_failed');
      report.preflight = 'pass';
      report.status = 'live_completed';
      report.capture = 'live_candidate_pending_review';
      phase = 'browser_close';
      await context.close();
      return { report, exitCode: 0 };
    }
    await draft.fill('');
    await page.waitForTimeout(canvas.settleMS);
    canvas.assertSaved(await json(canvas.projectRoute()));
    await empty();
    report.csp_violations = await page.evaluate(() => window.__wireCSPCount);
    phase = 'browser_guard';
    await context.close();
    check(report.csp_violations === 0 && Object.values(report.network).every(count => count === 0)
      && Object.values(report.allowed_writes).every(count => count === 1), 'browser_guard_failed');
    report.preflight = 'pass';
    report.status = 'preflight_pass';
    return { report, exitCode: 0 };
  } catch {
    // Playwright errors can include DOM, prompt, URLs or headers. Never print them.
    report.failure_stage = phase;
    return { report, exitCode: 1 };
  } finally {
    try { await browser?.close(); } catch { report.browser_close = 'failed'; }
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const { report, exitCode } = await run();
  console.log(JSON.stringify(report));
  process.exitCode = exitCode;
}
