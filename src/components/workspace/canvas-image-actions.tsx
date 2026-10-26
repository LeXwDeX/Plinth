'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import useSWR from 'swr';
import { toast } from 'sonner';
import { AlertTriangle, CopyPlus, Download, Trash2 } from 'lucide-react';

import { Surface } from '@/components/ui/surface';
import { Field } from '@/components/ui/field';
import { Textarea } from '@/components/ui/textarea';
import { Switch } from '@/components/ui/switch';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { useProductSchema } from '@/components/product-schema-provider';
import { safeAssetURL } from '@/lib/core/tasks/asset-url-rewriter';
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog';
import { fetchCapabilityOfferings, offeringShowsCredits, type OfferingWire } from '@/lib/agent-offerings';
import { schemaCountMessage, schemaMessage } from '@/lib/product-schema';
import { formatNativePrice, parseNativeQuote, type NativeQuote } from '@/lib/pricing';
import { layerName, type NamedLayer } from '@/lib/canvas/layer-name';
import { LAYER_BAR_MAX_WIDTH } from '@/lib/workspace/tool-rail-layout';
import { shortcutCaps } from '@/lib/workspace/stage-actions';
import { inputImageParameter } from '@/lib/workspace/processing-source';
import { buildInpaintInput, inpaintParameters } from '@/lib/workspace/model-picker';
import { canvasTurnOutput } from '@/lib/workspace/canvas-size';
import { effectiveOfferingValues } from '@/lib/workspace/generation-params';
import { samePixels } from '@/lib/canvas/artboard';
import { isMaskAssetId } from '@/lib/canvas/inpaint-source';
import { useChatStore } from '@/store/chat-store';
import { saveInpaintJob, type InpaintFrame, type InpaintJob } from '@/lib/canvas/inpaint-layer';
import { useWorkspaceStore } from '@/store/workspace-store';
import { RequestFailure, problemReason, reasonOf, responseRequestId, withErrorReference } from '@/lib/problem-reference';
import { captureSessionScope, isCurrentSessionScope } from '@/lib/workspace/session-scope';

/** The whole artboard rasterised into one uploaded picture, and the mask of the same pixel size and origin. */
export interface InpaintRequest {
  /** The uploaded picture. The board uploads it again on every reopen, so its URL is not an identity. */
  imageUrl: string;
  /** The exact rasterised board bytes the upload contains; this is what a resumable request belongs to. */
  source: Blob;
  mask: Blob;
  /** The dashed frame (board coordinates) the picture and the mask cover, and their pixel size: the result comes back as a layer there. */
  frame: InpaintFrame;
  size: { width: number; height: number };
  /** Called once the platform has accepted the task (not when it is only prepared). */
  onSubmitted?: () => void;
}

function nativeEstimate(schema: ReturnType<typeof useProductSchema>['schema'], offering: OfferingWire): string {
  const quote = parseNativeQuote(offering.default_native_quote);
  return quote
    ? schemaMessage(schema, 'ui.pricing.default_estimate', { amount: formatNativePrice(quote.total_amount, quote.unit_label) ?? schemaMessage(schema, 'ui.pricing.unknown', {}) })
    : schemaMessage(schema, 'ui.pricing.unknown', {});
}

export async function resolveReferenceAssetId(
  imageUrl: string,
  uploadBlob: (blob: Blob, filename: string) => Promise<string>,
  assetRoute?: string,
): Promise<string> {
  const safeURL = safeAssetURL(imageUrl);
  if (!safeURL) throw new Error('reference asset unavailable');
  // 资产 ID 提取规则来自 Schema 声明的资产路由（FEA-03），缺省时回退
  // 原生路由形态。
  const pattern = assetRoute
    ? new RegExp(assetRoute.replace(/\{assetID\}/, '([^/?]+)').replace(/\//g, '\\/') + '$')
    : /\/api\/v1\/assets\/([^/?]+)/;
  const direct = safeURL.match(pattern);
  if (direct) return decodeURIComponent(direct[1]);
  const response = await fetch(safeURL);
  if (!response.ok) throw new Error(`reference ${response.status}`);
  const uploaded = await uploadBlob(await response.blob(), 'canvas-reference.png');
  const match = uploaded.match(/\/api\/v1\/assets\/([^/?]+)/);
  if (!match) throw new Error('reference asset unresolved');
  return decodeURIComponent(match[1]);
}

export type PendingDirectTask = {
  preparedInputId: string;
  requestId: string;
  key: string;
  sessionId: string;
  offeringId: string;
  prompt?: string;
  /** 服务端已按会话批准方式批准这一步：不需要、也不能再点一次人工批准。 */
  approvedByPolicy?: boolean;
  nativeQuote?: NativeQuote | null;
  priceMode?: 'native';
  /** The server reports an already-approved grant; no further grant decision is needed. */
  grantApproved?: boolean;
  /** Inpaint only: what the finished result needs to land on the board as a layer (X8). */
  inpaint?: Omit<InpaintJob, 'taskId' | 'sessionId'>;
};

function pendingTaskStorageKey(sessionId: string, sourceIdentity: string): string {
  let hash = 2166136261;
  for (const character of sourceIdentity) {
    hash = Math.imul(hash ^ character.charCodeAt(0), 16777619);
  }
  return `art-ai:pending-direct-task:${sessionId}:${(hash >>> 0).toString(16)}`;
}

// This browser-side fingerprint only selects a resumable UI operation. The
// server separately hashes and verifies the actual frozen input asset bytes.
export async function contentFingerprint(blob: Blob): Promise<string> {
  const bytes = new Uint8Array(await blob.arrayBuffer());
  let first = 2166136261;
  let second = 0x9e3779b9;
  for (const byte of bytes) {
    first = Math.imul(first ^ byte, 16777619);
    second = Math.imul(second ^ byte, 2246822519);
  }
  return `${bytes.length.toString(16)}-${(first >>> 0).toString(16)}-${(second >>> 0).toString(16)}`;
}

/** Browser-side content digests of the two exported pictures; they select a resumable UI operation only. */
type InpaintFingerprints = { source: string; mask: string };

/**
 * The frozen request belongs to the actual source and mask bytes, the frame, the output size and the
 * model. A fresh upload URL after reopening must not break that identity; changed bytes or geometry must.
 */
export function inpaintSourceIdentity(input: {
  offeringId: string;
  sourceFingerprint: string;
  maskFingerprint: string;
  frame: InpaintFrame;
  size: { width: number; height: number };
  target: string;
}): string {
  const { x, y, width, height } = input.frame;
  return `inpaint:${input.offeringId}:${input.sourceFingerprint}:${input.maskFingerprint}`
    + `:${x},${y},${width},${height}:${input.size.width}x${input.size.height}:${input.target}`;
}

export function readPendingDirectTask(sessionId: string | null, sourceIdentity: string): PendingDirectTask | null {
  if (!sessionId || typeof sessionStorage === 'undefined') return null;
  try {
    const raw = sessionStorage.getItem(pendingTaskStorageKey(sessionId, sourceIdentity));
    if (!raw) return null;
    const envelope: { sourceIdentity?: string; pending?: PendingDirectTask } = JSON.parse(raw);
    const pending = envelope.pending;
    if (envelope.sourceIdentity !== sourceIdentity || pending?.sessionId !== sessionId ||
      !pending.preparedInputId || !pending.requestId || !pending.key || !pending.offeringId) return null;
    return pending;
  } catch {
    return null;
  }
}

export function savePendingDirectTask(sessionId: string, sourceIdentity: string, pending: PendingDirectTask): void {
  if (typeof sessionStorage === 'undefined') return;
  try {
    sessionStorage.setItem(pendingTaskStorageKey(sessionId, sourceIdentity), JSON.stringify({ sourceIdentity, pending }));
  } catch { /* Private browsing may disable session storage; in-memory state still works. */ }
}

/**
 * Removes the stored request. With `pending`, a settlement from an old closure only removes the record it
 * started with; a newer draft stored under the same identity survives.
 */
export function clearPendingDirectTask(sessionId: string | null, sourceIdentity: string, pending?: PendingDirectTask): void {
  if (!sessionId || typeof sessionStorage === 'undefined') return;
  try {
    if (pending && readPendingDirectTask(sessionId, sourceIdentity)?.key !== pending.key) return;
    sessionStorage.removeItem(pendingTaskStorageKey(sessionId, sourceIdentity));
  } catch { /* See savePendingDirectTask. */ }
}

function randomExecutionKey(): string {
  const words = new Uint32Array(4);
  globalThis.crypto.getRandomValues(words);
  return Array.from(words, (word) => word.toString(16).padStart(8, '0')).join('');
}

async function checkedJSON<T>(route: string, method: string, body: Record<string, unknown>, key?: string): Promise<T> {
  const response = await fetch(route, {
    method,
    headers: { 'Content-Type': 'application/json', ...(key ? { 'Idempotency-Key': key } : {}) },
    body: JSON.stringify(body),
  });
  if (!response.ok) {
    const problem: { detail?: string } = await response.json().catch(() => ({}));
    throw new RequestFailure(problem.detail || `HTTP ${response.status}`, responseRequestId(response, problem), problemReason(problem));
  }
  return response.json() as Promise<T>;
}

export async function prepareDirectTask(
  routes: { task_prepare: string; task_grants: string },
  body: Record<string, unknown>,
  sessionId: string,
  offeringId: string,
  prompt?: string,
  priceMode?: 'native',
  isActive: () => boolean = () => true,
): Promise<PendingDirectTask> {
  if (!isActive()) throw new Error('task preparation inactive');
  const prepared = await checkedJSON<{ prepared_input_id: string }>(routes.task_prepare, 'POST', body);
  // The dialog may have been cancelled while preparation was in flight; do not create a grant then.
  if (!isActive()) throw new Error('task preparation inactive');
  const key = randomExecutionKey();
  const grant = await checkedJSON<{ request_id: string; status?: string; approved_by_policy?: boolean; native_quote?: unknown }>(routes.task_grants, 'POST',
    { prepared_input_id: prepared.prepared_input_id }, key);
  const nativeQuote = Object.hasOwn(grant, 'native_quote') ? parseNativeQuote(grant.native_quote) : undefined;
  if (priceMode === 'native' && !nativeQuote) throw new Error('Native price quote is unavailable');
  return {
    preparedInputId: prepared.prepared_input_id, requestId: grant.request_id, key, sessionId, offeringId, prompt,
    ...(priceMode === 'native' ? { priceMode, nativeQuote: nativeQuote ?? null } : {}),
    ...(grant.approved_by_policy === true && grant.status === 'approved' ? { approvedByPolicy: true } : {}),
    ...(grant.status === 'approved' ? { grantApproved: true } : {}),
  };
}

export async function submitDirectTask(
  routes: { tasks: string; task_grant_decision: string },
  pending: PendingDirectTask,
  isActive: () => boolean = () => true,
): Promise<string> {
  if (!isActive()) throw new Error('task submission inactive');
  // 批准方式已经批准的步骤没有人工决定；对它再发批准会被服务端拒绝。
  if (!pending.approvedByPolicy && !pending.grantApproved) {
    await checkedJSON(routes.task_grant_decision.replace('{requestID}', encodeURIComponent(pending.requestId)),
      'PUT', { decision: 'approve' });
  }
  // An already-sent request may settle, but leaving the initiating UI cannot authorize the next write.
  if (!isActive()) throw new Error('task submission inactive');
  const payload = await checkedJSON<{ task_id?: unknown; id?: unknown }>(routes.tasks, 'POST', {
    prepared_input_id: pending.preparedInputId,
    authorization_request_id: pending.requestId,
  }, pending.key);
  return String(payload.task_id ?? payload.id ?? '');
}

export function InpaintDialog({
  request,
  offerings,
  modelId,
  uploadBlob,
  onClose,
}: {
  request: InpaintRequest;
  offerings: OfferingWire[] | undefined;
  modelId: string | null;
  uploadBlob: (blob: Blob, filename: string) => Promise<string>;
  onClose: () => void;
}) {
  const { schema, text } = useProductSchema();
  const registerTask = useWorkspaceStore((state) => state.registerTask);
  const sessionID = useChatStore((state) => state.activeSessionId);
  const [prompt, setPrompt] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [pending, setPending] = useState<PendingDirectTask | null>(null);
  const [hashes, setHashes] = useState<InpaintFingerprints | null>(null);
  const [maskPreviewUrl, setMaskPreviewUrl] = useState<string | null>(null);
  // This dialog instance, its request and its model are the submission's identity. A cancel, unmount,
  // session switch or model change after an await stops every further write (and its UI callbacks).
  // A new identity object is issued on every change (never reissued on A-B-A), and the captured session
  // revision makes a session round trip stale even when the id string is the same again.
  const activeRef = useRef(true);
  const submissionIdentity = useMemo(() => ({}), [request, modelId, sessionID]);
  // Read the identity of the latest render: a closing closure keeps the identity it started with.
  const identityRef = useRef(submissionIdentity);
  identityRef.current = submissionIdentity;
  useEffect(() => {
    activeRef.current = true;
    return () => { activeRef.current = false; };
  }, []);
  const close = () => { activeRef.current = false; onClose(); };
  const jobWarning = text('ui.canvas.inpaint.job_storage_unavailable');
  const rememberJob = (job: InpaintJob) => {
    if (!saveInpaintJob(job) && jobWarning) toast.error(jobWarning);
  };
  const maskError = text('ui.canvas.inpaint.failed');
  useEffect(() => {
    let active = true;
    setHashes(null);
    void Promise.all([contentFingerprint(request.source), contentFingerprint(request.mask)])
      .then(([source, mask]) => {
        if (active) setHashes({ source, mask });
      })
      .catch(() => {
        if (active) setError(maskError);
      });
    return () => { active = false; };
  }, [request.source, request.mask, maskError]);
  useEffect(() => {
    if (typeof URL === 'undefined' || typeof URL.createObjectURL !== 'function') return;
    const url = (() => {
      try { return URL.createObjectURL(request.mask); } catch { return null; }
    })();
    if (!url) return;
    setMaskPreviewUrl(url);
    return () => { URL.revokeObjectURL(url); };
  }, [request.mask]);
  const offering = useMemo(
    () =>
      (offerings ?? []).find(
        (item) =>
          item.available &&
          item.model_id === modelId &&
           inpaintParameters(item) !== null,
      ),
    [offerings, modelId],
  );
  // #308: the size this submission really asks for, by the same Schema rule as a canvas turn. A size that
  // is not the canvas pixels cannot be put back without stretching the result, so it is shown and blocks.
  const target = useMemo(
    () => (offering ? canvasTurnOutput(offering, effectiveOfferingValues(offering, {}), request.size, schema.canvas.frontend.artboard) : null),
    [offering, request.size, schema.canvas.frontend.artboard],
  );
  const targetExact = Boolean(target?.pixels && samePixels(target.pixels, request.size));
  const targetBlocks = !targetExact;
  const canvasLabel = `${request.size.width}×${request.size.height}`;
  const targetLabel = target?.pixels ? `${target.pixels.width}×${target.pixels.height}` : target?.value ?? '';
  const sourceIdentity = hashes && offering
    ? inpaintSourceIdentity({
        offeringId: offering.id,
        sourceFingerprint: hashes.source,
        maskFingerprint: hashes.mask,
        frame: request.frame,
        size: request.size,
        target: target?.value ?? 'model-default',
      })
    : null;
  useEffect(() => {
    if (!sourceIdentity) { setPending(null); return; }
    const restored = readPendingDirectTask(sessionID, sourceIdentity);
    const matched = restored && samePixels(restored.inpaint?.size, request.size) ? restored : null;
    setPending(matched);
    if (matched?.prompt) setPrompt(matched.prompt);
  }, [sessionID, sourceIdentity, request.size.height, request.size.width]);

  const submit = async () => {
    if (!offering || !sessionID || !sourceIdentity || prompt.trim() === '' || targetBlocks) return;
    const submitted = { identity: submissionIdentity, scope: captureSessionScope() };
    const stillActive = () => activeRef.current && identityRef.current === submitted.identity
      && isCurrentSessionScope(submitted.scope);
    if (!stillActive()) return;
    setBusy(true);
    setError('');
    try {
      if (pending) {
        if (pending.offeringId !== offering.id || pending.sessionId !== sessionID || pending.prompt !== prompt.trim()
          || !samePixels(pending.inpaint?.size, request.size)) {
          setPending(null);
          clearPendingDirectTask(sessionID, sourceIdentity);
          throw new Error('prepared input changed');
        }
        const taskId = await submitDirectTask(schema.runtime.http.routes, pending, stillActive);
        if (!taskId) throw new Error('task accepted without id');
        // The Task POST may already be accepted after the dialog closed. Keep the job and the tracked task
        // (the same id is never posted again), but a closed dialog must not clear a newer draft or dialog.
        if (pending.inpaint) rememberJob({ taskId, sessionId: sessionID, ...pending.inpaint });
        registerTask({ taskId, sessionId: sessionID, capability: 'image-to-image', prompt: pending.prompt,
          progressMaximum: schema.runtime.frontend.percentage_multiplier, autoPreview: false });
        // Only this accepted request is cleared: a later click may have stored a newer draft meanwhile.
        clearPendingDirectTask(sessionID, sourceIdentity, pending);
        if (!stillActive()) return;
        request.onSubmitted?.();
        close();
        return;
      }
      const [referenceId, maskUrl] = await Promise.all([
        resolveReferenceAssetId(request.imageUrl, uploadBlob),
        uploadBlob(request.mask, 'inpaint-mask.png'),
      ]);
      if (!stillActive()) return;
      const maskId = await resolveReferenceAssetId(maskUrl, uploadBlob);
      if (!stillActive()) return;
      if (!isMaskAssetId(maskId)) throw new Error('mask asset id invalid');
      // The explicit size is frozen with the input and the grant; the picture and mask are both request.size.
      const input = buildInpaintInput(offering, prompt, referenceId, maskId, request.size);
      const prepared: PendingDirectTask = {
        ...(await prepareDirectTask(schema.runtime.http.routes, {
          capability: 'image-to-image',
          offering_id: offering.id,
          session_id: sessionID,
          input,
        }, sessionID, offering.id, prompt.trim(), offering.price_mode === 'native' ? 'native' : undefined, stillActive)),
        inpaint: { maskUrl, maskAssetId: maskId, frame: request.frame, size: request.size },
      };
      if (!stillActive()) return;
      // Persist the whole prepared request before any Task POST. When the POST is accepted and its response
      // is lost, the next click repeats this same identity (same idempotency key) instead of preparing and
      // charging a second task.
      setPending(prepared);
      savePendingDirectTask(sessionID, sourceIdentity, prepared);
      if (prepared.approvedByPolicy) {
        // 批准方式已批准：没有第二次确认，直接提交这份冻结的输入。
        const taskId = await submitDirectTask(schema.runtime.http.routes, prepared, stillActive);
        if (!taskId) throw new Error('task accepted without id');
        rememberJob({ taskId, sessionId: sessionID, ...prepared.inpaint! });
        registerTask({ taskId, sessionId: sessionID, capability: 'image-to-image', prompt: prepared.prompt,
          progressMaximum: schema.runtime.frontend.percentage_multiplier, autoPreview: false });
        clearPendingDirectTask(sessionID, sourceIdentity, prepared);
        if (!stillActive()) return;
        request.onSubmitted?.();
        close();
        return;
      }
    } catch (cause) {
      // FEA-03：原始错误（HTTP 状态/内部文本）不直显给用户，走 Schema
      // 文案；细节进控制台供排查。服务端标明的拒绝原因（#288，Schema 文案）
      // 例外：它告诉用户该换什么输入。
      console.error('inpaint failed', cause);
      if (stillActive()) setError(withErrorReference(reasonOf(cause) || text('ui.canvas.inpaint.failed'), cause));
    } finally {
      if (activeRef.current) setBusy(false);
    }
  };

  return (
    <Dialog
      open
      onOpenChange={(nextOpen) => {
        if (!nextOpen) close();
      }}
    >
      <DialogContent closeLabel={text('ui.dialog.close')} className="flex flex-col gap-0 p-5">
        <DialogTitle className="mb-3 text-sm font-semibold">{text('ui.canvas.inpaint.title')}</DialogTitle>
        {!offering ? (
          <p role="alert" className="flex items-start gap-2 text-xs text-warning">
            <AlertTriangle className="mt-0.5 size-4 shrink-0" />
            {text('ui.canvas.inpaint.missing_semantics')}
          </p>
        ) : (
          <>
            {pending?.priceMode === 'native' ? <p className="mb-3 rounded-md bg-muted/40 p-2 text-xs text-muted-foreground" data-testid="inpaint-native-price">
              {pending.nativeQuote
                ? schemaMessage(schema, 'ui.pricing.frozen_estimate', { amount: formatNativePrice(pending.nativeQuote.total_amount, pending.nativeQuote.unit_label) ?? schemaMessage(schema, 'ui.pricing.unknown', {}) })
                : schemaMessage(schema, 'ui.pricing.unknown', {})}
            </p> : null}
            <div className="mb-3 space-y-2" data-testid="inpaint-target">
              <p
                role={targetBlocks ? 'alert' : undefined}
                className={`rounded-md p-2 text-xs ${targetBlocks ? 'bg-warning/10 text-warning' : 'bg-muted/40 text-muted-foreground'}`}
                data-testid="inpaint-output-size"
              >
                {targetExact
                  ? schemaMessage(schema, 'ui.canvas.inpaint.output_size', { size: targetLabel })
                  : target
                    ? schemaMessage(schema, 'ui.canvas.inpaint.output_size_nearest', { canvas: canvasLabel, size: targetLabel })
                    : schemaMessage(schema, 'ui.canvas.inpaint.output_size_unconfirmed', { size: canvasLabel })}
              </p>
              <div className="flex gap-2 rounded-md bg-muted/40 p-2">
                {maskPreviewUrl ? (
                  <img
                    src={maskPreviewUrl}
                    alt={text('ui.canvas.inpaint.mask_preview_alt')}
                    className="h-20 w-28 shrink-0 rounded border border-border bg-muted/20 object-contain"
                  />
                ) : null}
                <p className="text-xs text-muted-foreground" data-testid="inpaint-mask-scope">
                  {schemaMessage(schema, 'ui.canvas.inpaint.mask_scope', { canvas: canvasLabel })}
                </p>
              </div>
            </div>
            <Field id="inpaint-prompt" label={text('ui.canvas.inpaint.prompt_label')} help={offering.price_mode === 'native'
                ? nativeEstimate(schema, offering)
                : !offeringShowsCredits(offering)
                ? undefined
                : offering.estimated_credits !== undefined
                  ? schemaCountMessage(schema, 'ui.canvas.inpaint.cost', offering.estimated_credits, {
                      credits: offering.estimated_credits,
                    })
                  : text('ui.canvas.inpaint.cost_unknown')}>
              {(controlProps) => (<Textarea
              {...controlProps}
              value={prompt}
              onChange={(event) => { setPrompt(event.target.value); setPending(null); if (sourceIdentity) clearPendingDirectTask(sessionID, sourceIdentity); }}
              placeholder={text('ui.canvas.inpaint.prompt_placeholder')}
              className="min-h-20 resize-none"
            />)}
            </Field>
            {pending && <p className="mt-2 text-xs text-warning">{text('ui.canvas.inpaint.confirm_prepared')}</p>}
            {error ? (
              <p role="alert" className="mt-2 flex items-start gap-2 text-xs text-destructive">
                <AlertTriangle className="mt-0.5 size-4 shrink-0" />
                {error}
              </p>
            ) : null}
            <div className="mt-4 flex justify-end gap-2">
              <Action size="sm"
                type="button"
                onClick={close}
                variant="ghost"
              >
                {text('ui.canvas.inpaint.cancel')}
              </Action>
              <Action size="sm"
                type="button"
                loading={busy} disabled={ !sessionID || !sourceIdentity || prompt.trim() === '' || targetBlocks}
                onClick={() => void submit()}
              >
                {text(pending ? 'ui.canvas.inpaint.confirm_execution' : 'ui.canvas.inpaint.prepare')}
              </Action>
            </div>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

export function BackgroundRemovalDialog({
  imageUrl,
  uploadBlob,
  onClose,
}: {
  imageUrl: string;
  uploadBlob: (blob: Blob, filename: string) => Promise<string>;
  onClose: () => void;
}) {
  const { schema, text } = useProductSchema();
  const registerTask = useWorkspaceStore((state) => state.registerTask);
  const sessionID = useChatStore((state) => state.activeSessionId);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [pending, setPending] = useState<PendingDirectTask | null>(null);
  const [choices, setChoices] = useState<Record<string, boolean>>({});
  const { data: offerings } = useSWR(
    ['bg-removal-offerings'],
    () => fetchCapabilityOfferings(schema, 'background-removal'),
    { revalidateOnFocus: false },
  );
  const offering = (offerings ?? []).find((item) => item.available);
  // Options the Offering declares for the person to choose (a boolean shown on the provider surface, such as
  // 透明物体模式) are drawn from the Schema and sent with the task; their defaults come from the same place.
  const options = useMemo(
    () => (offering?.parameters ?? []).filter((parameter) => parameter.type === 'boolean'
      && parameter.presentation?.surface === 'provider' && parameter.name !== offering?.extra_credit_consent_parameter),
    [offering],
  );
  const optionValue = (name: string, fallback: unknown) => choices[name] ?? fallback === true;
  // The prepared task belongs to the picture and to the options it was prepared with.
  const sourceIdentity = `background-removal:${imageUrl}:${options.map((option) => `${option.name}=${optionValue(option.name, option.default)}`).join(',')}`;
  useEffect(() => {
    setPending(readPendingDirectTask(sessionID, sourceIdentity));
  }, [sessionID, sourceIdentity]);

  const submit = async () => {
    if (!offering || !sessionID) return;
    setBusy(true);
    setError('');
    try {
      if (pending) {
        if (pending.offeringId !== offering.id || pending.sessionId !== sessionID) {
          setPending(null);
          clearPendingDirectTask(sessionID, sourceIdentity);
          throw new Error('prepared offering changed');
        }
        const taskId = await submitDirectTask(schema.runtime.http.routes, pending);
        if (!taskId) throw new Error('task accepted without id');
        registerTask({ taskId, sessionId: sessionID, capability: 'background-removal',
          progressMaximum: schema.runtime.frontend.percentage_multiplier, autoPreview: false });
        clearPendingDirectTask(sessionID, sourceIdentity, pending);
        onClose();
        return;
      }
      const inputParameter = inputImageParameter(offering);
      if (!inputParameter) {
        throw new Error('background removal offering has no single-image input parameter');
      }
      const sourceId = await resolveReferenceAssetId(imageUrl, uploadBlob);
      const prepared = await prepareDirectTask(schema.runtime.http.routes, {
        capability: 'background-removal',
        offering_id: offering.id,
        session_id: sessionID,
        input: {
          [inputParameter.name]: inputParameter.type === 'asset' ? sourceId : [sourceId],
          ...Object.fromEntries(options.map((option) => [option.name, optionValue(option.name, option.default)])),
        },
      }, sessionID, offering.id, undefined, offering.price_mode === 'native' ? 'native' : undefined);
      // Same rule as inpaint: keep the frozen request before the Task POST so a lost response can be retried
      // with the same identity instead of preparing a second task.
      setPending(prepared);
      savePendingDirectTask(sessionID, sourceIdentity, prepared);
      if (prepared.approvedByPolicy) {
        const taskId = await submitDirectTask(schema.runtime.http.routes, prepared);
        if (!taskId) throw new Error('task accepted without id');
        registerTask({ taskId, sessionId: sessionID, capability: 'background-removal',
          progressMaximum: schema.runtime.frontend.percentage_multiplier, autoPreview: false });
        clearPendingDirectTask(sessionID, sourceIdentity, prepared);
        onClose();
        return;
      }
    } catch (cause) {
      // FEA-03：原始错误不直显，走 Schema 文案；服务端标明的拒绝原因（#288）除外。
      console.error('background removal failed', cause);
      setError(withErrorReference(reasonOf(cause) || text('ui.canvas.bgremoval.failed'), cause));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog
      open
      onOpenChange={(nextOpen) => {
        if (!nextOpen) onClose();
      }}
    >
      <DialogContent closeLabel={text('ui.dialog.close')} className="flex flex-col gap-0 p-5">
        <DialogTitle className="mb-3 text-sm font-semibold">{text('ui.canvas.bgremoval.title')}</DialogTitle>
        {!offering ? (
          <>
            <p role="alert" className="flex items-start gap-2 text-xs text-warning">
              <AlertTriangle className="mt-0.5 size-4 shrink-0" />
              {text('ui.canvas.bgremoval.unavailable')}
            </p>
            {/* UE-07：服务不可用也必须有关闭按钮，不允许死路。 */}
            <div className="mt-4 flex justify-end">
              <Action size="sm"
                type="button"
                onClick={onClose}
                variant="ghost"
              >
                {text('ui.canvas.bgremoval.cancel')}
              </Action>
            </div>
          </>
        ) : (
          <>
            <p className="text-xs leading-5 text-muted-foreground">
              {offering.price_mode === 'native'
                ? nativeEstimate(schema, offering)
                : !offeringShowsCredits(offering)
                ? text('ui.canvas.bgremoval.confirm_unmetered')
                : offering.estimated_credits !== undefined
                  ? schemaCountMessage(schema, 'ui.canvas.bgremoval.confirm', offering.estimated_credits, {
                      credits: offering.estimated_credits,
                    })
                  : text('ui.canvas.bgremoval.confirm_unknown')}
            </p>
            {options.length > 0 ? (
              <ul className="mt-3 flex flex-col gap-2" data-testid="bgremoval-options">
                {options.map((option) => (
                  <li key={option.name} className="flex items-start justify-between gap-3">
                    <label htmlFor={`bgremoval-${option.name}`} className="flex min-w-0 flex-col gap-0.5 text-xs">
                      <span className="font-medium">{option.label}</span>
                      {option.description ? <span className="leading-4 text-muted-foreground">{option.description}</span> : null}
                    </label>
                    <Switch
                      id={`bgremoval-${option.name}`}
                      size="sm"
                      checked={optionValue(option.name, option.default)}
                      onCheckedChange={(checked) => setChoices((current) => ({ ...current, [option.name]: checked }))}
                    />
                  </li>
                ))}
              </ul>
            ) : null}
            {pending && <p className="mt-2 text-xs text-warning">{text('ui.canvas.bgremoval.confirm_prepared')}</p>}
            {pending?.priceMode === 'native' ? <p className="mt-2 text-xs text-muted-foreground" data-testid="bgremoval-native-price">
              {pending.nativeQuote
                ? schemaMessage(schema, 'ui.pricing.frozen_estimate', { amount: formatNativePrice(pending.nativeQuote.total_amount, pending.nativeQuote.unit_label) ?? schemaMessage(schema, 'ui.pricing.unknown', {}) })
                : schemaMessage(schema, 'ui.pricing.unknown', {})}
            </p> : null}
            {error ? (
              <p role="alert" className="mt-2 flex items-start gap-2 text-xs text-destructive">
                <AlertTriangle className="mt-0.5 size-4 shrink-0" />
                {error}
              </p>
            ) : null}
            <div className="mt-4 flex justify-end gap-2">
              <Action size="sm"
                type="button"
                onClick={onClose}
                variant="ghost"
              >
                {text('ui.canvas.bgremoval.cancel')}
              </Action>
              <Action size="sm"
                type="button"
                loading={busy} disabled={ !sessionID}
                onClick={() => void submit()}
              >
                {text(pending ? 'ui.canvas.bgremoval.confirm_execution' : 'ui.canvas.bgremoval.prepare')}
              </Action>
            </div>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

/**
 * 画布浮条（选中一个图层时出现在画布右上角）：左边是图层的名字，后面三个只有图标的按钮——复制一份、下载这一层、
 * 删除这一层。都是对这一层的手工编辑，免费；不放任何会调用模型的动作，也不放发送到对话、转 3D（那些在舞台上方
 * 工具条）。去背景也在舞台工具条，作用于画板上选中的这一张图。
 * 每个按钮的名字、快捷键和说明走悬停说明，文字来自 Schema（`ui.canvas.layer_bar.*`、`ui.canvas.menu.duplicate`、
 * `ui.canvas.action.{download,delete_layer}`）。
 */
export function CanvasImageActionBar({
  layer,
  onDuplicate,
  onDownload,
  onDelete,
}: {
  /** The selected layer: its name leads the bar; a locked layer cannot be deleted. */
  layer: NamedLayer & { locked?: boolean };
  onDuplicate: () => void;
  onDownload: () => void;
  onDelete: () => void;
}) {
  const { schema, text } = useProductSchema();
  const name = layerName(layer, text, schema.localization?.persisted_default_texts);
  const duplicateName = text('ui.canvas.menu.duplicate');
  const downloadName = text('ui.canvas.action.download');
  const deleteName = text('ui.canvas.action.delete_layer');
  const locked = layer.locked === true;
  return (
    <Surface
      variant="frosted"
      radius="xl"
      padding="sm"
      elevation="xl"
      role="toolbar"
      aria-label={text('ui.canvas.layer_bar.label')}
      data-testid="canvas-layer-bar"
      style={{ maxWidth: LAYER_BAR_MAX_WIDTH }}
      className="absolute right-3 top-3 z-20 flex items-center gap-0.5"
    >
      <span className="max-w-32 min-w-0 truncate px-1.5 text-xs font-medium text-foreground" data-testid="canvas-layer-bar-name">{name}</span>
      <span aria-hidden className="mx-0.5 h-5 w-px shrink-0 bg-border" />
      <HintTooltip name={duplicateName} shortcut={shortcutCaps(schema, 'duplicate_shortcut')} hint={text('ui.canvas.layer_bar.duplicate_hint')}>
        <Action variant="ghost" size="icon-sm" type="button" aria-label={duplicateName} data-testid="canvas-layer-duplicate" onClick={onDuplicate}>
          <CopyPlus className="size-3.5" />
        </Action>
      </HintTooltip>
      <HintTooltip name={downloadName} hint={text('ui.canvas.layer_bar.download_hint')}>
        <Action variant="ghost" size="icon-sm" type="button" aria-label={downloadName} data-testid="canvas-layer-download" onClick={onDownload}>
          <Download className="size-3.5" />
        </Action>
      </HintTooltip>
      <HintTooltip name={deleteName} hint={text('ui.canvas.layer_bar.delete_hint')} reason={locked ? text('ui.canvas.layer_bar.delete_locked') : undefined}>
        <Action variant="ghost" size="icon-sm" type="button" aria-label={deleteName} data-testid="canvas-layer-delete" disabled={locked} onClick={onDelete}>
          <Trash2 className="size-3.5" />
        </Action>
      </HintTooltip>
    </Surface>
  );
}
