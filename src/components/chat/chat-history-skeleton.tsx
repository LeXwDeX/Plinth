import { Skeleton } from '@/components/ui/skeleton';

/** 切换会话、历史消息还没到时的占位：交替的用户 / 助手气泡骨架。 */
export function ChatHistorySkeleton({ label }: { label: string }) {
  return (
    <div role="status" aria-busy="true" aria-label={label} data-testid="chat-history-skeleton" className="flex flex-col gap-4 px-1">
      <Skeleton className="ml-auto h-10 w-2/3" />
      <Skeleton className="h-20 w-5/6" />
      <Skeleton className="ml-auto h-10 w-1/2" />
      <Skeleton className="h-16 w-4/5" />
    </div>
  );
}
