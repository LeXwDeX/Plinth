/**
 * Agent 输入框的稳定 DOM 标识。画布的"生成"入口过去用
 * document.querySelector('textarea') 聚焦文档里的第一个 textarea，
 * 参数面板、弹窗等一旦出现 textarea 就会聚焦错对象；改为按稳定 id 聚焦。
 */
export const CHAT_COMPOSER_ELEMENT_ID = 'art-ai-chat-composer';

export function focusChatComposer(
  root: Pick<Document, 'getElementById'> = document,
): boolean {
  const element = root.getElementById(CHAT_COMPOSER_ELEMENT_ID) as
    | { focus?: () => void }
    | null;
  if (typeof element?.focus !== 'function') return false;
  element.focus();
  return true;
}
