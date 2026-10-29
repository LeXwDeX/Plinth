import { DRAFT_KEY_PREFIX } from '@/lib/chat/composer-draft';
import { LAST_OBJECT_KEY_PREFIX } from '@/lib/workspace/last-object';

// 账号相关的本地键：输入区草稿、舞台上次对象。界面偏好（语言、模型选择等）不属于账号，保留。
const ACCOUNT_LOCAL_KEY_PREFIXES = [DRAFT_KEY_PREFIX, LAST_OBJECT_KEY_PREFIX];

/**
 * 主动登出后清掉当前账号留在本浏览器里的状态（#134）。服务端在 HTTPS/localhost
 * 才发 `Clear-Site-Data`，浏览器在其他环境（如 IP 地址的明文 HTTP 部署）里会报错并忽略它，
 * 所以这些环境由前端显式清理。登录不清理：会话过期后同账号重新登录必须保留草稿（docs/product.md）；草稿、上次对象与待处理任务都按会话 ID 存放，会话归属于账号，换账号登录也读不到。
 * 安全上下文里浏览器已处理缓存，这里不重复动手。存储不可用时静默跳过。
 */
export function clearAccountClientStateWithoutSecureContext(): void {
  if (typeof window === 'undefined' || window.isSecureContext) return;
  try {
    window.sessionStorage.clear();
  } catch {
    // 存储被禁用时没有可清的内容。
  }
  try {
    const store = window.localStorage;
    const doomed: string[] = [];
    for (let index = 0; index < store.length; index += 1) {
      const key = store.key(index);
      if (key !== null && ACCOUNT_LOCAL_KEY_PREFIXES.some((prefix) => key.startsWith(prefix))) doomed.push(key);
    }
    for (const key of doomed) store.removeItem(key);
  } catch {
    // 同上。
  }
}
