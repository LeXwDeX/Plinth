'use client';

import { useProductSchema } from '@/components/product-schema-provider';
import { SessionStepsExtendButton } from '@/components/studio/asset-steps-extend-dialog';
import { useSessionSteps } from '@/hooks/use-session-steps';
import { sessionStepsLimit } from '@/lib/chat/session-steps';
import { formatSchemaText, schemaText } from '@/lib/product-schema';

export interface SessionStepsBarProps {
  sessionID: string | null;
  /** The session title, used as the dialog's asset name. */
  name?: string;
}

/**
 * #330 §6: the step standing of one asset session (an external MCP asset,
 * #332), shown above the conversation. It renders only for a session the
 * server reports as a steps asset in the cap mode; an ordinary chat, an
 * agent-managed or unlimited setting, and a deployment without the session
 * route show nothing.
 */
export function SessionStepsBar({ sessionID, name = '' }: SessionStepsBarProps) {
  const { schema } = useProductSchema();
  const { steps, extend } = useSessionSteps(sessionID);
  if (!steps || steps.mode !== 'cap') return null;
  const limit = sessionStepsLimit(steps);
  const label = formatSchemaText(schemaText(schema, 'ui.step_policy.session_steps'), {
    used: steps.used,
    limit,
  });
  return (
    <div
      className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border bg-panel-raised/60 px-3 py-1.5 text-[11px] text-muted-foreground"
      data-testid="session-steps-bar"
      data-mode={steps.mode}
      data-used={steps.used}
      data-limit={limit}
      data-exhausted={steps.used >= limit ? 'true' : 'false'}
    >
      <span className="tabular-nums" data-testid="session-steps-standing">{label}</span>
      <SessionStepsExtendButton
        sessionID={sessionID ?? ''}
        name={name}
        steps={steps}
        schema={schema}
        onSubmit={async (target) => {
          await extend(target);
        }}
      />
    </div>
  );
}