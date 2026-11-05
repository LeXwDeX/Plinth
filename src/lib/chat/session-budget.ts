import type { SessionBudget } from '@/lib/chat/budget';
import type { ProductionBrief } from '@/lib/chat/production-brief';

/**
 * #103: after a plan decision the session is read again so the budget card and
 * badge show the server's new figures. A failed read must not leave a stale
 * budget on screen, so one retry is made; when both reads fail the caller shows
 * the reload notice instead of silently keeping the old budget.
 */

export interface SessionBudgetSource {
  getSession(sessionId: string): Promise<{ budget?: SessionBudget; brief?: ProductionBrief }>;
}

export interface SessionBudgetSnapshot {
  budget: SessionBudget | null;
  brief?: ProductionBrief;
}

export async function reloadSessionBudget(
  source: SessionBudgetSource,
  sessionId: string,
  attempts = 2,
): Promise<SessionBudgetSnapshot> {
  let failure: unknown;
  for (let attempt = 0; attempt < Math.max(1, attempts); attempt += 1) {
    try {
      const session = await source.getSession(sessionId);
      return {
        budget: session.budget ?? null,
        ...(session.brief ? { brief: session.brief } : {}),
      };
    } catch (error) {
      failure = error;
    }
  }
  throw failure;
}
