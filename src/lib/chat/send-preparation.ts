import { InFlightGuard } from '@/lib/chat/in-flight-guard';

/** Claim synchronously before any parameter write; repeated Enter cannot start a second preparation. */
export async function withSessionSendLock(
  guard: InFlightGuard,
  sessionId: string,
  operation: () => Promise<void>,
): Promise<boolean> {
  if (!guard.begin(sessionId)) return false;
  try {
    await operation();
    return true;
  } finally {
    guard.end(sessionId);
  }
}
