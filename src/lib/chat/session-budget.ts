import { budgetAwaitsDecision, budgetIsShown, type SessionBudget } from '@/lib/chat/budget';
import type { ProductionBrief } from '@/lib/chat/production-brief';
import type { ProductSchema } from '@/lib/product-schema';

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

/**
 * #561: a decided plan keeps changing on the server after the decision: a step's
 * reservation is charged by the settlement sweep a moment after its Task ends,
 * and the plan then turns used up or completed. The page used to read the budget
 * only when the session opened and after a decision, so a finished plan kept the
 * figures and state of the moment it was approved. A plan the session shows and
 * that no longer waits for a decision follows the session's Tasks: whenever one
 * the page saw running ends, the budget is read again. Which plan follows is the
 * Schema role of its state, never a state id.
 */
export function budgetFollowsTasks(schema: ProductSchema, budget: SessionBudget | null | undefined): boolean {
  return budgetIsShown(schema, budget) && !budgetAwaitsDecision(schema, budget);
}

export interface SettledBudgetFollow {
  /** When to read, in milliseconds after the Tasks ended; the settlement runs on the server's sweep. */
  delays: readonly number[];
  /** Writes a read budget back; called only while the follow is not cancelled. */
  apply(snapshot: SessionBudgetSnapshot): void;
  schedule?: (run: () => void, delay: number) => unknown;
  cancel?: (handle: unknown) => void;
}

/**
 * #561: reads the session budget once at each delay after the session's Tasks
 * ended and returns the way to stop. A failed read is left alone: the next ended
 * Task, the next decision or the plan's reload reads it again, and a background
 * read must not replace the card with the reload notice. This is no polling loop:
 * it runs only when a Task ends, under the same delays as the late result read.
 */
export function followSettledBudget(
  source: SessionBudgetSource,
  sessionId: string,
  follow: SettledBudgetFollow,
): () => void {
  const schedule = follow.schedule ?? ((run: () => void, delay: number) => setTimeout(run, delay));
  const cancel = follow.cancel ?? ((handle: unknown) => clearTimeout(handle as ReturnType<typeof setTimeout>));
  let stopped = false;
  const read = () => {
    reloadSessionBudget(source, sessionId, 1).then((snapshot) => {
      if (!stopped) follow.apply(snapshot);
    }, () => {});
  };
  const handles = follow.delays.map((delay) => schedule(read, delay));
  return () => {
    stopped = true;
    handles.forEach(cancel);
  };
}
