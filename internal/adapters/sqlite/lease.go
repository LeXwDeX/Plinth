package sqlite

import (
	"context"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"time"
)

// Renewal retains the token and does not advance task version. A lost or expired
// token must never be revived, including after a concurrent cancel or webhook.
//
// M3：写入的 lease_until 由写事务内的时钟读数派生，不用调用方在事务前
// 采样的 now——连接池等待/调度饥饿会让旧采样（F1a 实测 524ms）写出一个
// 已经过去的 lease_until，抹掉全部调度延迟余量。有效性守卫
// （lease_until > now，即过期租约不得复活）仍用传入的 now，语义不变。
// lease_until 与守卫参数都写成固定宽度 UTC（formatTaskTime），TEXT 比较
// 才与纳秒时间序一致。
func (r *Repository) RenewLease(ctx context.Context, id generation.TaskID, token string, now time.Time, leaseFor time.Duration) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	writeNow := r.clock()
	result, err := tx.ExecContext(ctx, `UPDATE generation_tasks SET lease_until = ? WHERE id = ? AND lease_token = ? AND lease_until > ? AND NOT EXISTS (SELECT 1 FROM generation_task_tombstones t WHERE t.task_id = generation_tasks.id)`, formatTaskTime(writeNow.Add(leaseFor)), id, token, formatTaskTime(now))
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return generation.RepositoryConflict
	}
	return tx.Commit()
}
