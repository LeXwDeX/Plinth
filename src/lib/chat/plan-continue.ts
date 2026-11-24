import type { InFlightGuard } from '@/lib/chat/in-flight-guard';
import { continueSessionBudget } from '@/lib/chat/pending-approvals';
import type { ProductSchema } from '@/lib/product-schema';

/** What the page draws around one 「继续」 request. */
export interface PlanContinueHooks {
  /** The plan is claimed and the request is about to go out. */
  started(): void;
  /** The server started the plan's turn. */
  succeeded(): void;
  /** The request failed or the server refused it. */
  failed(error: unknown): void;
  /** The request ended, however it ended; runs before the claim is released. */
  settled(): void;
}

/**
 * #410: runs a session's approved plan again, once per plan at a time. A click made
 * while the same plan's request still runs sends nothing and resolves false; the
 * claim is released when the request ends, so a later click is sent again (#503).
 */
export function continuePlanOnce(
  guard: InFlightGuard,
  schema: ProductSchema,
  sessionId: string,
  budgetId: string,
  hooks: PlanContinueHooks,
  fetcher: typeof fetch = fetch,
): Promise<boolean> {
  return guard.run(budgetId, async () => {
    hooks.started();
    try {
      await continueSessionBudget(schema, sessionId, budgetId, fetcher);
      hooks.succeeded();
    } catch (error) {
      hooks.failed(error);
    } finally {
      hooks.settled();
    }
  });
}
