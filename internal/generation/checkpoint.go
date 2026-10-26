package generation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrDispatchPending 表示当前 Attempt 存在未回执的 dispatching 私有派发
// 检查点：恢复期禁止再次发送，任何新的 Checkpoint 调用都不会获得发送
// 许可（§4.3 唯一合法路径）。
var ErrDispatchPending = errors.New("private dispatch checkpoint pending")

// DispatchIntent 是 Provider 私有派发意图：字节对内核不透明、由内核原样
// 持久化到既有 opaque_json。intent 必须自含协调标识（client token/查询键）
// 与阶段标记，不得依赖进程内存（§4.5）。
type DispatchIntent struct {
	Bytes json.RawMessage
}

// DispatchAck 是一次成功检查点授予的发送许可：当次调用是唯一持有者，
// 一次 ack 至多授权一次 HTTP 提交尝试。
type DispatchAck struct {
	Sequence int64           // 每 Attempt 单调递增
	AckToken string          // 单次使用；随机 32 hex
	Opaque   json.RawMessage // 已持久化的 intent 回显
}

// DispatchCheckpointer 由内核实现并注入 Start/Observe 调用上下文；只有
// 内核可写库，Provider 只能调用（§4.2）。
type DispatchCheckpointer interface {
	// 成功 => 意图已持久化，且本次调用获得唯一发送许可。
	// 返回 ErrDispatchPending（可经 errors.Is 判定）=> 存在未回执的
	// dispatching 意图，禁止再次发送。
	Checkpoint(ctx context.Context, intent DispatchIntent) (DispatchAck, error)
}

type dispatchCheckpointerKey struct{}

// WithDispatchCheckpointer 把检查点回调绑定到调用上下文。
func WithDispatchCheckpointer(ctx context.Context, cp DispatchCheckpointer) context.Context {
	return context.WithValue(ctx, dispatchCheckpointerKey{}, cp)
}

// DispatchCheckpointerFromContext 取出检查点回调；不存在（旧调用面/
// 单测直连）时返回 false，Provider 保持既有行为（§4.5.4 向后兼容）。
func DispatchCheckpointerFromContext(ctx context.Context) (DispatchCheckpointer, bool) {
	cp, ok := ctx.Value(dispatchCheckpointerKey{}).(DispatchCheckpointer)
	return cp, ok && cp != nil
}

// DispatchCheckpointWrite 描述一次原子检查点写入（内核所有）。
type DispatchCheckpointWrite struct {
	TaskID          TaskID
	AttemptID       AttemptID
	LeaseToken      string
	ExpectedVersion int64
	Now             time.Time
	Intent          json.RawMessage
	AckToken        string
}

// DispatchCheckpointAcknowledge 是检查点写入的持久化结果。
type DispatchCheckpointAcknowledge struct {
	Sequence   int64
	AckToken   string
	NewVersion int64
}

// DispatchCheckpointStore 是仓储的可选能力：按 §4.2 全谓词（version +
// lease_token + lease_until>now + 非 tombstone）单事务持久化意图与内核
// 检查点状态，并写入 opaque_json（Opaque 往返格式不变）。
type DispatchCheckpointStore interface {
	CommitDispatchCheckpoint(
		context.Context,
		DispatchCheckpointWrite,
	) (DispatchCheckpointAcknowledge, error)
}

// dispatchCheckpointer 是内核侧实现：绑定当前租约与 Attempt，负责意图
// 大小校验、ack 生成与值语义版本推进（§4.2 值语义调用纪律——回调成功后
// 必须把同一份 lease 拷贝的 Task.Version 推进到本次 CAS 写入的新版本，
// 保证随后 observation 提交使用当前版本）。
type dispatchCheckpointer struct {
	store     DispatchCheckpointStore
	lease     *Lease
	attempt   *Attempt
	maxBytes  int64
	clock     Clock
	lastAck   *DispatchAck
	settled   bool
	ackTokens func() string
}

func (c *dispatchCheckpointer) Checkpoint(
	ctx context.Context,
	intent DispatchIntent,
) (DispatchAck, error) {
	if c == nil || c.store == nil {
		return DispatchAck{}, &ProviderError{
			Failure: &Failure{
				Class:      FailureInternal,
				SafeDetail: "私有派发检查点未配置",
			},
		}
	}
	if int64(len(intent.Bytes)) > c.maxBytes {
		return DispatchAck{}, &ProviderError{
			Failure: &Failure{
				Class:      FailureProtocol,
				SafeDetail: fmt.Sprintf("私有派发意图超过调度器声明上限（%d 字节）", c.maxBytes),
			},
		}
	}
	ack, err := c.store.CommitDispatchCheckpoint(ctx, DispatchCheckpointWrite{
		TaskID:          c.lease.Task.ID,
		AttemptID:       c.attempt.ID,
		LeaseToken:      c.lease.Token,
		ExpectedVersion: c.lease.Task.Version,
		Now:             c.clock.Now(),
		Intent:          intent.Bytes,
		AckToken:        c.ackTokens(),
	})
	if err != nil {
		if errors.Is(err, ErrDispatchPending) {
			return DispatchAck{}, &ProviderError{
				Failure: &Failure{
					Class:      FailureSubmissionUnknown,
					Ambiguous:  true,
					SafeDetail: "存在未回执的私有派发检查点，已停止自动重发以避免重复扣费",
				},
				Cause: err,
			}
		}
		if errors.Is(err, RepositoryConflict) || errors.Is(err, RepositoryNotFound) {
			// 租约丢失、CAS 失败或任务已删除：本许可未持久化，
			// 请求未发出，保守返回可重试的非歧义失败（§4.2）。
			return DispatchAck{}, &ProviderError{
				Failure: &Failure{
					Class:      FailureTransport,
					Retryable:  true,
					SafeDetail: "私有派发检查点未获得发送许可（租约、版本或删除状态冲突），未发送任何请求",
				},
				Cause: err,
			}
		}
		return DispatchAck{}, &ProviderError{
			Failure: &Failure{
				Class:      FailureTransport,
				Retryable:  true,
				SafeDetail: "私有派发检查点持久化失败，未发送任何请求",
			},
			Cause: err,
		}
	}
	// 值语义调用纪律：同一份 lease 拷贝推进到 CAS 写入的新版本。
	c.lease.Task.Version = ack.NewVersion
	// Opaque 同步纪律（W6）：检查点事务把 opaque_json 覆盖为 intent
	// 字节，随后任何携带 Attempt 的提交（重排/合并）都会整体重写
	// opaque_json——内存 Attempt 必须立即反映已持久化的 intent，否则
	// 一次失败重排就会把意图抹回旧值，破坏 Provider 私有状态机。
	c.attempt.Opaque = append(json.RawMessage(nil), intent.Bytes...)
	c.lastAck = &DispatchAck{
		Sequence: ack.Sequence,
		AckToken: ack.AckToken,
		Opaque:   append(json.RawMessage(nil), intent.Bytes...),
	}
	c.settled = false
	return *c.lastAck, nil
}

// receiptValid 校验 observation 携带的回执是否与当次租约内最后授予且
// 尚未消费的 ack 精确匹配（sequence + token + 合法 result）。
func (c *dispatchCheckpointer) receiptValid(receipt *DispatchReceipt) bool {
	if c == nil || c.lastAck == nil || receipt == nil || c.settled {
		return false
	}
	switch receipt.Result {
	case DispatchResultDispatched, DispatchResultNotDispatched:
	default:
		return false
	}
	return receipt.Sequence == c.lastAck.Sequence && receipt.AckToken == c.lastAck.AckToken
}

func (c *dispatchCheckpointer) markSettled() {
	c.settled = true
}

// dispatchAckToken 生成单次使用的随机 32 位十六进制 token。
func dispatchAckToken() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		panic(fmt.Sprintf("生成派发 ack token 失败: %v", err))
	}
	return hex.EncodeToString(buffer)
}

// PrivateDispatchPermit 供 Provider 在非幂等付费私有提交前取得发送许可
// （§4.3 唯一合法路径）：上下文未安装检查点回调时返回 nil 回执（旧调用
// 面/单测直连兼容，§4.5.4），调用方保持既有行为；失败（含
// ErrDispatchPending）时必须放弃发送并把错误原样上抛。
func PrivateDispatchPermit(
	ctx context.Context,
	intent DispatchIntent,
) (*DispatchReceipt, error) {
	checkpointer, ok := DispatchCheckpointerFromContext(ctx)
	if !ok {
		return nil, nil
	}
	ack, err := checkpointer.Checkpoint(ctx, intent)
	if err != nil {
		return nil, err
	}
	return &DispatchReceipt{
		Sequence: ack.Sequence,
		AckToken: ack.AckToken,
		Result:   DispatchResultDispatched,
	}, nil
}

// deniedDispatchCheckpointer 显式拒绝私有派发（STD1）：物化刷新等无租约
// 栅栏的观察路径不得借"无检查点回调"兼容分支发起付费派发——该路径注入
// 本实现后，任何 Checkpoint 调用都会得到明确的非歧义拒绝，Provider 必须
// 放弃发送（零上游调用），而不是退回直连发送。
type deniedDispatchCheckpointer struct{}

func (deniedDispatchCheckpointer) Checkpoint(
	context.Context,
	DispatchIntent,
) (DispatchAck, error) {
	return DispatchAck{}, &ProviderError{
		Failure: &Failure{
			Class:      FailureRejected,
			Retryable:  false,
			SafeDetail: "该执行路径不允许私有付费派发（无检查点栅栏）",
		},
	}
}

// WithDeniedDispatchCheckpointer 把显式拒绝的检查点回调绑定到上下文。
func WithDeniedDispatchCheckpointer(ctx context.Context) context.Context {
	return WithDispatchCheckpointer(ctx, deniedDispatchCheckpointer{})
}
