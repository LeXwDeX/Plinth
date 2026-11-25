// prod-long-run.mjs 的纯函数（#633）：不碰浏览器与环境变量，`node --test scripts/acceptance/prod-long-run-lib.test.mjs` 可单测。

/**
 * 在 Skills 页的行名里找要手选的 Skill：先找名字完全相同的行，再找以它开头的行；不按子串匹配。
 * 三视图的说明里写着「说四视图、四个视角时用 four-view」，按整行子串找「四视图」会先点中三视图（#633）。
 * 返回行下标，找不到返回 -1。
 */
export function skillRowIndex(names, wanted) {
  const target = String(wanted || '').trim();
  if (!target) return -1;
  const clean = names.map((name) => String(name || '').replace(/\s+/g, ' ').trim());
  const exact = clean.indexOf(target);
  if (exact >= 0) return exact;
  return clean.findIndex((name) => name.startsWith(target));
}

/**
 * 批准卡上读得出的报价：历史积分卡是「约 12 积分」，原生计价卡是「已冻结估算：50 Tripo credits」这类带单位的金额
 * （#633：原生卡没有 studio-approval-credits，旧逻辑读不出数而一律拒绝）。返回 { amount, unit }，读不出时 amount 为 NaN。
 * 金额里的千位逗号去掉；冒号之后的第一个数字才是金额，前面的标签不参与。
 */
export function quoteOf(text) {
  const value = String(text || '').replace(/\s+/g, ' ').trim();
  const body = value.includes('：') || value.includes(':') ? value.slice(value.search(/[：:]/) + 1) : value;
  const match = body.replace(/(\d),(?=\d{3}\b)/g, '$1').match(/(\d+(?:\.\d+)?)\s*([^\d\s(（][^(（]*)?/);
  if (!match) return { amount: NaN, unit: '' };
  return { amount: Number(match[1]), unit: (match[2] || '').trim() };
}

/**
 * 点卡片按钮或选项之后要观察多久才可以判定“这一轮已经安静”：点按钮只是发起一个可能耗时的请求（准备、批准卡、
 * 开始生成），没有新的对话轮次在跑，旧逻辑约 8 秒就判定结束并关浏览器，把请求掐断（#633）。
 * 发消息沿用 4 秒；点按钮与选项缺省 45 秒，可用 --min-wait 改。
 */
export function minimumWaitMs(options = {}) {
  const given = Number(options['min-wait']);
  if (options['min-wait'] !== undefined && options['min-wait'] !== true && Number.isFinite(given) && given >= 0) return given * 1000;
  return options.clickButton || options.choose ? 45000 : 4000;
}
