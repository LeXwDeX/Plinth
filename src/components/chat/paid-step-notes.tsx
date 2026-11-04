import type { ChatPaidStep } from '@/store/chat-store';
import { formatNativePrice } from '@/lib/pricing';
import { UnavailableNotice } from '@/components/ui/unavailable-notice';

/**
 * 消息里每个已提交付费步骤的积分与批准来源。积分是估算值；
 * 由批准方式自动批准的步骤会注明，避免被误读成用户点过批准；
 * 没做完的步骤改用「未完成」文案，不说成已经消耗。
 */
export function PaidStepNotes({
  steps,
  creditsText,
  failedText,
  autoApprovedText,
  failed,
  note,
  noteUnavailableLabel,
  nativeText,
  unknownPriceText,
}: {
  steps: ChatPaidStep[];
  creditsText: (credits: number) => string;
  failedText?: (credits: number) => string;
  autoApprovedText: string;
  /** 已确定未完成的步骤（任务 ID）。 */
  failed?: (taskId: string) => boolean;
  /** What running a failed step again would do (#326: certain to fail, or the saved file it uses); empty for nothing. */
  note?: (taskId: string) => string;
  /** The "无法使用，<reason>" line when that note says the step cannot run again (#94); the note itself then goes to the hint. */
  noteUnavailableLabel?: (taskId: string) => string;
  nativeText?: (amount: string) => string;
  unknownPriceText?: string;
}) {
  if (steps.length === 0) return null;
  return (
    <ul className="flex select-none flex-col gap-0.5 text-xs text-muted-foreground" data-testid="paid-step-notes">
      {steps.map((step) => {
        const isFailed = Boolean(failed?.(step.taskId)) && failedText !== undefined;
        const stepNote = isFailed ? note?.(step.taskId) ?? '' : '';
        return (
          <li key={step.taskId} data-failed={isFailed || undefined}>
            {step.nativeQuote !== undefined
              ? step.nativeQuote
                ? nativeText?.(formatNativePrice(step.nativeQuote.total_amount, step.nativeQuote.unit_label) ?? '') ?? unknownPriceText ?? ''
                : unknownPriceText ?? ''
              : isFailed ? failedText!(step.credits) : creditsText(step.credits)}
            {step.approvedByPolicy ? ` · ${autoApprovedText}` : ''}
            {stepNote ? (noteUnavailableLabel?.(step.taskId)
              ? <UnavailableNotice className="mt-0.5 flex" testId="paid-step-source-retry" label={noteUnavailableLabel(step.taskId)} detail={stepNote} />
              : <span className="mt-0.5 block text-warning" data-testid="paid-step-source-retry">{stepNote}</span>) : null}
          </li>
        );
      })}
    </ul>
  );
}
