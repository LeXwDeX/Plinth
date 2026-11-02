/** 消息列表里最新一条助手消息已执行的工具调用数（#275）；没有助手消息时是 undefined。 */
export function latestToolCallCount(messages: readonly { role: string; toolCallCount?: number }[]): number | undefined {
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    if (messages[index].role === 'assistant') return messages[index].toolCallCount ?? 0;
  }
  return undefined;
}
