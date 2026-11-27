'use client';

import { useEffect, useState } from 'react';
import { Popover } from '@base-ui/react/popover';
import { Gauge } from 'lucide-react';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { anchoredPositionMethod, useAnchoredPositioner } from '@/components/ui/overlay';
import {
  StepPolicyRequestError,
  useAccountStepPolicy,
  type AccountStepPolicy,
  type StepPolicyMode,
  type StepPolicySetting,
} from '@/hooks/use-account-step-policy';
import { useAuth } from '@/hooks/use-auth';
import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';
import { cn } from '@/lib/utils';

type EntryKey = 'list' | 'mcp';

interface DraftEntry {
  mode: string;
  cap: string;
}

type Draft = Record<EntryKey, DraftEntry>;

type Notice = { kind: 'conflict' | 'error'; text: string };

/** The mode labels are the browser copy, keyed by the server's mode values. */
const MODE_LABEL_KEYS: Record<string, string> = {
  agent: 'ui.step_policy.mode.agent',
  unlimited: 'ui.step_policy.mode.unlimited',
  cap: 'ui.step_policy.mode.cap',
};

const ENTRIES: Array<{ key: EntryKey; labelKey: string }> = [
  { key: 'list', labelKey: 'ui.step_policy.list' },
  { key: 'mcp', labelKey: 'ui.step_policy.mcp' },
];

function draftOf(policy: AccountStepPolicy): Draft {
  const entry = (setting: StepPolicySetting): DraftEntry => ({ mode: setting.mode, cap: String(setting.cap) });
  return { list: entry(policy.list), mcp: entry(policy.mcp) };
}

function modeLabel(schema: ProductSchema, modes: readonly StepPolicyMode[], value: string): string {
  const key = MODE_LABEL_KEYS[value];
  const label = key ? schemaText(schema, key) : '';
  return label || modes.find((mode) => mode.value === value)?.label || value;
}

/** The cap to save for one entry: only the cap mode edits it; the stored cap is kept otherwise. */
function capForSave(
  entry: DraftEntry,
  stored: number,
  range: AccountStepPolicy['cap_range'],
): number | null {
  if (entry.mode !== 'cap') return stored;
  const cap = Number(entry.cap);
  if (!Number.isInteger(cap) || cap < range.minimum || cap > range.maximum) return null;
  return cap;
}

/**
 * #330: the person's 3D step mode in the top bar. The badge shows the mode the
 * asset lists use; the panel sets both entries (asset lists and the external
 * MCP). Saving sends the version it read, so a change made in another tab is
 * reported as a conflict and re-read, never silently overwritten.
 */
export function StepPolicyControl() {
  const { schema } = useProductSchema();
  const { user, isLoading: authLoading } = useAuth();
  const supported = Boolean(schema.step_policy && schema.runtime.http.routes.account_step_policy);
  const { policy, loading, error, update, refresh } = useAccountStepPolicy();
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState<Notice | null>(null);
  const layer = useAnchoredPositioner();

  // A read (initial, after a save, or after a conflict) replaces the form so it
  // always shows the values now in force.
  const policyKey = policy
    ? `${policy.version}:${policy.list.mode}:${policy.list.cap}:${policy.mcp.mode}:${policy.mcp.cap}`
    : '';
  useEffect(() => {
    if (!policy) return;
    setDraft(draftOf(policy));
  }, [policyKey]); // eslint-disable-line react-hooks/exhaustive-deps

  // The setting is a signed-in person's own; a token alone cannot save it. A
  // Schema without application.step_policy has no control and no route.
  if (authLoading || !user || !supported) return null;

  const badgeMode = policy ? modeLabel(schema, policy.modes, policy.list.mode) : '';
  const badgeText = badgeMode
    ? formatSchemaText(schemaText(schema, 'ui.step_policy.badge'), { mode: badgeMode })
    : schemaText(schema, 'ui.step_policy.title');

  const changeEntry = (key: EntryKey, patch: Partial<DraftEntry>) => {
    setNotice(null);
    setDraft((current) => current ? { ...current, [key]: { ...current[key], ...patch } } : current);
  };

  const save = async () => {
    if (!policy || !draft || saving) return;
    const range = policy.cap_range;
    const listCap = capForSave(draft.list, policy.list.cap, range);
    const mcpCap = capForSave(draft.mcp, policy.mcp.cap, range);
    if (listCap === null || mcpCap === null) {
      setNotice({
        kind: 'error',
        text: formatSchemaText(schemaText(schema, 'ui.step_policy.cap_invalid'), {
          minimum: range.minimum,
          maximum: range.maximum,
        }),
      });
      return;
    }
    setSaving(true);
    setNotice(null);
    const result = await update({
      expected_version: policy.version,
      list: { mode: draft.list.mode, cap: listCap },
      mcp: { mode: draft.mcp.mode, cap: mcpCap },
    });
    setSaving(false);
    if (result.status === 'saved') {
      setOpen(false);
      return;
    }
    if (result.status === 'conflict') {
      setNotice({ kind: 'conflict', text: schemaText(schema, 'ui.step_policy.conflict') });
      return;
    }
    // #670: only the server's own words are quoted; an internal reason reads as the generic failure.
    setNotice({
      kind: 'error',
      text: result.error.detail
        ? formatSchemaText(schemaText(schema, 'ui.step_policy.error'), { reason: result.error.detail })
        : schemaText(schema, 'ui.step_policy.error_generic'),
    });
  };

  const errorReason = error instanceof StepPolicyRequestError ? error.detail : '';
  const loadFailed = Boolean(error) && !policy;

  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger
        data-testid="studio-step-policy-badge"
        aria-label={badgeText}
        className={cn(
          'flex h-8 shrink-0 items-center gap-1.5 rounded-md px-2 text-xs text-muted-foreground transition-colors',
          'hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring data-popup-open:bg-primary/15 data-popup-open:text-primary',
        )}
      >
        <Gauge aria-hidden className="size-3.5" />
        <span>{badgeText}</span>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Positioner positionMethod={anchoredPositionMethod} side="bottom" align="end" sideOffset={6} collisionPadding={12} className={layer}>
          <Popover.Popup
            data-testid="studio-step-policy-panel"
            className="flex w-80 max-w-(--available-width) flex-col gap-2 rounded-lg bg-popover p-2 text-popover-foreground shadow-xl outline-none ring-1 ring-foreground/10"
          >
            <Popover.Title className="px-1 text-xs font-semibold">{schemaText(schema, 'ui.step_policy.title')}</Popover.Title>
            {loadFailed ? (
              <div role="alert" data-testid="step-policy-load-error" className="flex flex-col items-start gap-2 rounded-md bg-destructive/10 p-2 text-xs text-destructive">
                <span>{errorReason
                  ? formatSchemaText(schemaText(schema, 'ui.step_policy.load_failed'), { reason: errorReason })
                  : schemaText(schema, 'ui.step_policy.load_failed_generic')}</span>
                <Action type="button" size="sm" variant="outline" onClick={() => void refresh()}>
                  {schemaText(schema, 'ui.step_policy.retry')}
                </Action>
              </div>
            ) : !policy || !draft ? (
              <p className="px-1 text-xs text-muted-foreground">{schemaText(schema, 'ui.step_policy.loading')}</p>
            ) : (
              <>
                {ENTRIES.map(({ key, labelKey }) => {
                  const entry = draft[key];
                  const rowLabel = schemaText(schema, labelKey);
                  return (
                    <fieldset key={key} className="flex flex-col gap-1 rounded-md border border-border p-2">
                      <legend className="px-1 text-xs font-medium">{rowLabel}</legend>
                      <div role="radiogroup" aria-label={rowLabel} className="flex gap-1">
                        {policy.modes.map((mode) => {
                          const selected = entry.mode === mode.value;
                          return (
                            <button
                              key={mode.value}
                              type="button"
                              role="radio"
                              aria-checked={selected}
                              disabled={saving}
                              data-testid={`step-policy-${key}-mode-${mode.value}`}
                              onClick={() => changeEntry(key, { mode: mode.value })}
                              className={cn(
                                'flex-1 rounded-md px-1.5 py-1 text-[11px] transition-colors hover:bg-panel-raised focus-visible:outline-2 focus-visible:outline-ring disabled:opacity-50',
                                selected && 'bg-primary/15 font-medium text-primary',
                              )}
                            >
                              {modeLabel(schema, policy.modes, mode.value)}
                            </button>
                          );
                        })}
                      </div>
                      {entry.mode === 'cap' ? (
                        <label className="flex items-center gap-2 px-0.5 pt-1 text-[11px] text-muted-foreground">
                          <span className="shrink-0">{schemaText(schema, 'ui.step_policy.cap')}</span>
                          <input
                            type="number"
                            inputMode="numeric"
                            step={1}
                            min={policy.cap_range.minimum}
                            max={policy.cap_range.maximum}
                            value={entry.cap}
                            disabled={saving}
                            data-testid={`step-policy-${key}-cap`}
                            onChange={(event) => changeEntry(key, { cap: event.target.value })}
                            className="h-7 w-20 rounded-md border border-input bg-background px-2 text-xs text-foreground tabular-nums focus-visible:outline-2 focus-visible:outline-ring"
                          />
                        </label>
                      ) : null}
                    </fieldset>
                  );
                })}
                <p className="px-1 text-[11px] leading-snug text-muted-foreground" data-testid="step-policy-cap-hint">
                  {formatSchemaText(schemaText(schema, 'ui.step_policy.cap_hint'), {
                    minimum: policy.cap_range.minimum,
                    maximum: policy.cap_range.maximum,
                  })}
                </p>
                {notice ? (
                  <p
                    role="alert"
                    data-testid="step-policy-notice"
                    className={cn(
                      'rounded-md p-2 text-[11px] leading-snug',
                      notice.kind === 'conflict' ? 'bg-warning/10 text-warning' : 'bg-destructive/10 text-destructive',
                    )}
                  >
                    {notice.text}
                  </p>
                ) : null}
                <div className="flex justify-end gap-1.5 px-0.5">
                  {notice?.kind === 'error' ? (
                    <Action type="button" size="sm" variant="outline" disabled={saving} onClick={() => void save()}>
                      {schemaText(schema, 'ui.step_policy.retry')}
                    </Action>
                  ) : null}
                  <Action type="button" size="sm" loading={saving} disabled={saving} onClick={() => void save()}>
                    {saving
                      ? schemaText(schema, 'ui.step_policy.saving')
                      : schemaText(schema, 'ui.step_policy.save')}
                  </Action>
                </div>
              </>
            )}
          </Popover.Popup>
        </Popover.Positioner>
      </Popover.Portal>
    </Popover.Root>
  );
}