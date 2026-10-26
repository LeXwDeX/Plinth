-- 0061: 任务调度/租约时间列使用固定九位小数 UTC 文本（#313）。
-- RFC3339Nano 裁掉小数末尾的零，SQLite TEXT 的字节序与时间序不一致：
-- '...52.588Z' > '...52.5881Z'，但前者更早。就绪谓词、租约守卫与领取
-- 排序都直接比较这些文本，必须统一成固定宽度。
-- 只改写 generation_tasks.next_action_at 与 lease_until；created_at 仍在
-- recovery receipt 的冻结行摘要内，不重写，领取排序用 rtrim(created_at,'Z')
-- 比较。已有 recovery receipt 的任务行也不改写，保证摘要不变。其他表、
-- Job/Opaque 与 frozen JSON/Grant 均不触碰。NULL 与无法识别的历史格式
-- 保持原样；规范化后的 30 字符值不再匹配，重复执行幂等。
UPDATE generation_tasks
SET next_action_at =
    substr(next_action_at, 1, 19) ||
    CASE
        WHEN length(next_action_at) = 20 THEN '.000000000'
        ELSE '.' || substr(next_action_at, 21, length(next_action_at) - 21) ||
             substr('000000000', 1, 30 - length(next_action_at))
    END ||
    'Z'
WHERE next_action_at IS NOT NULL
  AND length(next_action_at) BETWEEN 20 AND 29
  AND substr(next_action_at, 1, 19) GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]'
  AND substr(next_action_at, -1, 1) = 'Z'
  AND (length(next_action_at) = 20
       OR (substr(next_action_at, 20, 1) = '.'
           AND substr(next_action_at, 21, length(next_action_at) - 21) NOT GLOB '*[^0-9]*'))
  AND NOT EXISTS (
      SELECT 1 FROM generation_recovery_receipts receipt
      WHERE receipt.source_task_id = generation_tasks.id
  );

UPDATE generation_tasks
SET lease_until =
    substr(lease_until, 1, 19) ||
    CASE
        WHEN length(lease_until) = 20 THEN '.000000000'
        ELSE '.' || substr(lease_until, 21, length(lease_until) - 21) ||
             substr('000000000', 1, 30 - length(lease_until))
    END ||
    'Z'
WHERE lease_until IS NOT NULL
  AND length(lease_until) BETWEEN 20 AND 29
  AND substr(lease_until, 1, 19) GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]'
  AND substr(lease_until, -1, 1) = 'Z'
  AND (length(lease_until) = 20
       OR (substr(lease_until, 20, 1) = '.'
           AND substr(lease_until, 21, length(lease_until) - 21) NOT GLOB '*[^0-9]*'))
  AND NOT EXISTS (
      SELECT 1 FROM generation_recovery_receipts receipt
      WHERE receipt.source_task_id = generation_tasks.id
  );
