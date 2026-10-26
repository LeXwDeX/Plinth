package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agentscope"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat/dsml"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

var ErrOrchestratorQueueFull = errors.New("chat orchestrator: queue full")

type ProcessTurn struct {
	OwnerID            string
	SessionID          string
	AssistantMessageID string
	Options            TurnOptions
	// ConsentWaitDeadline 非零表示恢复路径：本轮此前因等待同意挂起，
	// 工具在超过该时刻且仍无决定时返回等待文案。零值=全新挂起。
	ConsentWaitDeadline time.Time
}

type ToolCommand struct {
	Configuration *AgentConfigurationSnapshot
	OwnerID       string
	SessionID     string
	// AssistantMessageID 界定轮次：agenttools 以它统计单轮生成次数。
	AssistantMessageID string
	// BoundTaskID 非空表示恢复路径中该调用已有持久化任务：生成工具
	// 直接观察等待，不重建输入/不重复提交（B11）。
	BoundTaskID string
	// FrozenIdempotencyKey is persisted with the planned call; replay must not
	// derive a different paid-operation identity from a changed tool schema.
	FrozenIdempotencyKey string
	TaskWaitDeadline     time.Time
	PriorGenerationCalls int
	// ConsentWaitDeadline 非零表示本轮已因等待同意而挂起过：工具在
	// 决定未出且已过该时刻时应返回等待文案，而不是再次登记挂起。
	ConsentWaitDeadline time.Time
	Call                agents.ToolCall
	Tool                catalog.ChatToolSchema
	History             []Message
	Options             TurnOptions
	// EarlierCalls are the tool calls this Agent reply ran before this one, in
	// order (a resumed reply included). A tool with a per-reply bound replays
	// them instead of keeping state of its own.
	EarlierCalls []ToolCall
	// ExplicitParameters preserves caller inputs before legacy defaults merge.
	// Non-nil enables offering overrides without changing the command hash.
	ExplicitParameters map[string]json.RawMessage `json:"-"`
}

func (command ToolCommand) IdempotencyKey() string {
	if command.FrozenIdempotencyKey != "" {
		return command.FrozenIdempotencyKey
	}
	return strings.NewReplacer(
		"{session_id}", command.SessionID,
		"{call_id}", command.Call.ID,
	).Replace(command.Tool.IdempotencyKeyTemplate)
}

type ToolObservation struct {
	Registered  bool
	TaskID      string
	FinalTaskID string
	Capability  string
	Prompt      string
	AIModel     string
	Status      string
	Progress    int
	ImageURLs   []string
	Error       string
	// EstimatedCredits and ApprovalSource describe the accepted paid step on
	// its Registered observation ("policy" or "human"; empty when unknown).
	EstimatedCredits int
	NativeQuote      *schema.NativeQuote
	ApprovalSource   string
}

type ToolResult struct {
	TaskID       string
	ImageURLs    []string
	Error        string
	AgentContent string
	// NoSubmission is internal evidence that execution stopped before Gateway.Submit.
	// It permits releasing an unused consent claim; unknown submit results never set it.
	NoSubmission bool `json:"-"`
	// Suspend 表示该工具已登记等待用户输入（AGT-12）：调用方应中断
	// 本批剩余工具并向编排器返回挂起信号，而不是在 worker 内阻塞。
	Suspend   bool
	AwaitTask bool
	WaitUntil time.Time
	// Pictures are session pictures a read-only tool asks to show the Agent
	// (#279). The orchestrator reads each for the turn's owner and, when the
	// Agent reads images, shows it after the tool results; a picture it cannot
	// read is named as unreadable instead.
	Pictures []ToolPicture `json:"-"`
}

// ToolPicture is one picture a tool shows the Agent, by handle and platform URL.
type ToolPicture struct {
	Handle string
	URL    string
}

// ToolTaskIdentity contains only a server-verified paid-step binding. Skill
// calls use their persisted step key, rather than a generated Chat key.
type ToolTaskIdentity struct {
	TaskID         string
	IdempotencyKey string
}

// ErrTurnSuspended 表示轮次已挂起等待用户输入（AGT-12）：编排器把它
// 与普通失败区分，跳过收尾并等待唤醒后重新入队续跑。
var ErrTurnSuspended = errors.New("chat orchestrator: turn suspended awaiting user input")
var ErrTurnAwaitTask = errors.New("chat orchestrator: turn awaiting task")

// ErrWakeNotReleased 表示唤醒入队时原 Process 尚未返回（命令 ID 仍在
// queued 去重集合），需要稍后重试；见 enqueueForWake。
var ErrWakeNotReleased = errors.New("chat orchestrator: wake target not released yet")

type ToolExecutor interface {
	Execute(
		context.Context,
		ToolCommand,
		func(ToolObservation) error,
	) (ToolResult, error)
}

type OrchestratorConfig struct {
	Configuration func(context.Context, string) (AgentConfigurationSnapshot, error)
	Service       *Service
	Schema        catalog.ChatSchema
	Model         string
	// DSMLFor reports whether a turn on the model reads DSML tool calls
	// (its profile's dsml_tool_calls, #253/#322); nil keeps Schema.DSML as is.
	DSMLFor func(model string) bool
	Agent   agents.Adapter
	Tools   ToolExecutor
	Logger  *slog.Logger
	// VisionInline 把用户拥有的资产/任务产物 URL 转成模型可直接读取的
	// data URI；返回 ok=false 时该图片只以文本标签进入上下文。
	VisionInline func(
		ctx context.Context,
		owner generation.OwnerID,
		url string,
	) (string, bool)
	// TaskCanceler 供停止轮次时取消该轮已绑定的生成任务（尽力而为，
	// 失败只记录日志，不阻塞停止语义）。
	TaskCanceler func(ctx context.Context, ownerID, taskID string) error
	// ToolNote is what a turn adds to a generation tool's description, from
	// the turn's options (#329 E': how many reference pictures the picked model
	// uses). Nil, or "", leaves the declared description as it is.
	ToolNote func(tool catalog.ChatToolSchema, options TurnOptions) string
	// TaskLookup and TaskGet read kernel facts without current Catalog checks.
	// They let a stopped turn find paid submissions whose response or binding
	// was lost before the assistant message could be updated.
	TaskLookup func(ctx context.Context, ownerID, idempotencyKey string) (generation.Task, error)
	TaskGet    func(ctx context.Context, ownerID, taskID string) (generation.Task, error)
	// StopConsent settles only the matching durable claim after task lookup.
	StopConsent func(ownerID, sessionID, callID, idempotencyKey, taskID string) error
	// ToolAllowed 声明 capability 是否属于当前会话领域（B14：2D 会话
	// 不收 3D 工具）。nil 时不过滤（测试）。
	ToolAllowed func(capability, domain string) bool
	// OfferingAllowed reports whether an enabled, available Offering with the
	// model_id serves the capability; it narrows offering_argument choices
	// (#189). Nil offers the declared choices (tests).
	OfferingAllowed func(capability, modelID string) bool
	// Texts gives the copy of the turn's UI locale. Nil keeps the Schema's
	// default-locale copy.
	Texts *catalog.TextLocalizer
}

type Orchestrator struct {
	configuration func(context.Context, string) (AgentConfigurationSnapshot, error)
	service       *Service
	schema        catalog.ChatSchema
	model         string
	dsmlFor       func(string) bool
	agent         agents.Adapter
	tools         ToolExecutor
	logger        *slog.Logger
	registry      *toolRegistry
	agentScope    agentscope.Policy
	visionInline  func(
		ctx context.Context,
		owner generation.OwnerID,
		url string,
	) (string, bool)
	taskCanceler func(ctx context.Context, ownerID, taskID string) error
	toolNote     func(tool catalog.ChatToolSchema, options TurnOptions) string
	taskLookup   func(ctx context.Context, ownerID, idempotencyKey string) (generation.Task, error)
	taskGet      func(ctx context.Context, ownerID, taskID string) (generation.Task, error)
	stopConsent  func(ownerID, sessionID, callID, idempotencyKey, taskID string) error
	texts        *catalog.TextLocalizer
	queue        chan ProcessTurn
	queueMu      sync.Mutex
	// titleWaiters counts background title summaries in flight.
	titleWaiters sync.WaitGroup
	queued       map[string]struct{}

	// activeMu 保护活动轮次的取消句柄与用户取消标记：
	// key = turnKey(ownerID, sessionID)，同会话同时只有一个活动轮次。
	activeMu     sync.Mutex
	activeCancel map[string]context.CancelFunc
	canceledTurn map[string]struct{}

	// consentWaits 是因等待同意而挂起的轮次登记表（AGT-12）：
	// key = turnKey(ownerID, sessionID)。挂起不占用 worker；唤醒
	// （用户决定/超时/停止）只允许一个来源生效——弹出即失效。
	suspendMu    sync.Mutex
	consentWaits map[string]*consentSuspend

	// compacting serializes the context compactions of each session (#322).
	compactMu  sync.Mutex
	compacting map[string]*compactionLock
}

type consentSuspend struct {
	command  ProcessTurn
	deadline time.Time
	timer    *time.Timer
}

func (orchestrator *Orchestrator) turnKey(ownerID, sessionID string) string {
	return ownerID + "\x00" + sessionID
}

func (orchestrator *Orchestrator) registerActiveTurn(
	key string,
	cancel context.CancelFunc,
) {
	orchestrator.activeMu.Lock()
	defer orchestrator.activeMu.Unlock()
	orchestrator.activeCancel[key] = cancel
}

func (orchestrator *Orchestrator) unregisterActiveTurn(key string) {
	orchestrator.activeMu.Lock()
	defer orchestrator.activeMu.Unlock()
	delete(orchestrator.activeCancel, key)
}

func (orchestrator *Orchestrator) turnCanceled(key string) bool {
	orchestrator.activeMu.Lock()
	defer orchestrator.activeMu.Unlock()
	_, canceled := orchestrator.canceledTurn[key]
	return canceled
}

// consumeTurnCanceled 读取并清除用户取消标记（轮次开始时调用一次）。
func (orchestrator *Orchestrator) consumeTurnCanceled(key string) bool {
	orchestrator.activeMu.Lock()
	defer orchestrator.activeMu.Unlock()
	_, canceled := orchestrator.canceledTurn[key]
	delete(orchestrator.canceledTurn, key)
	return canceled
}

// clearTurnCanceled 兜底清除取消标记：标记只在活动轮次存在时写入，
// 轮次结束时再清一次，保证残留标记永远不会毒化下一轮。
func (orchestrator *Orchestrator) clearTurnCanceled(key string) {
	orchestrator.activeMu.Lock()
	defer orchestrator.activeMu.Unlock()
	delete(orchestrator.canceledTurn, key)
}

// reconcileStoppedTasks observes only persisted task identities and frozen
// idempotency keys. A transient read/cancel error keeps the durable turn open
// for restart recovery; a missing key never triggers a new paid submission.
func (orchestrator *Orchestrator) reconcileStoppedTasks(
	ctx context.Context, ownerID, sessionID, assistantID string, calls []ToolCall, options TurnOptions,
) error {
	var combined error
	for index, call := range calls {
		tool, known := orchestrator.registry.byName[call.Name]
		if known && tool.generation != nil {
			// A generation call whose approval card was detached belongs to the
			// pending-approval service: the person may approve it after this
			// stop, and the task that creates carries the call's frozen key.
			// Stop must neither cancel that task nor settle the call.
			if reader, ok := orchestrator.tools.(interface {
				DetachedOperationForCall(context.Context, ToolCommand) (bool, error)
			}); ok {
				detached, err := reader.DetachedOperationForCall(ctx, ToolCommand{OwnerID: ownerID, SessionID: sessionID,
					AssistantMessageID: assistantID, Call: agents.ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments},
					BoundTaskID: call.TaskID, FrozenIdempotencyKey: call.IdempotencyKey, Options: options})
				if err != nil {
					combined = errors.Join(combined, fmt.Errorf("read stopped generation call %s: %w", call.ID, err))
					continue
				}
				if detached {
					continue
				}
			}
		}
		if known && tool.generation == nil && paidTool(tool.execution) {
			reader, ok := orchestrator.tools.(interface {
				TaskIdentityForCall(context.Context, ToolCommand) (ToolTaskIdentity, error)
			})
			if !ok {
				combined = errors.Join(combined, fmt.Errorf("stopped paid call %s has no recovery reader", call.ID))
				continue
			}
			identity, err := reader.TaskIdentityForCall(ctx, ToolCommand{OwnerID: ownerID, SessionID: sessionID,
				AssistantMessageID: assistantID, Call: agents.ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments},
				BoundTaskID: call.TaskID, FrozenIdempotencyKey: call.IdempotencyKey, Options: options})
			if err != nil {
				combined = errors.Join(combined, fmt.Errorf("read stopped paid call %s: %w", call.ID, err))
				continue
			}
			if identity.IdempotencyKey == "" {
				if call.TaskID != "" || call.IdempotencyKey != "" {
					combined = errors.Join(combined, fmt.Errorf("stopped paid call %s binding has no ledger", call.ID))
				}
				continue
			}
			call.TaskID, call.IdempotencyKey = identity.TaskID, identity.IdempotencyKey
			calls[index] = call
		}
		if call.TaskID == "" && call.IdempotencyKey == "" {
			// Pre-upgrade tool_calls_json has no frozen key. A generation
			// call (or a tool since removed from the registry) might have
			// committed a paid Task without a binding. Keep stop recoverable
			// rather than incorrectly finalizing it as task-free.
			if !known || paidTool(tool.execution) {
				combined = errors.Join(combined, fmt.Errorf("stopped turn call %s has no recoverable task identity", call.ID))
				continue
			}
			continue
		}
		taskID := call.TaskID
		if call.IdempotencyKey != "" && orchestrator.taskLookup != nil {
			task, err := orchestrator.taskLookup(ctx, ownerID, call.IdempotencyKey)
			switch {
			case err == nil:
				if task.OwnerID != generation.OwnerID(ownerID) || task.SessionID != sessionID || task.ID == "" ||
					(taskID != "" && taskID != string(task.ID)) {
					combined = errors.Join(combined, fmt.Errorf("stopped turn task identity mismatch for call %s", call.ID))
					continue
				}
				taskID = string(task.ID)
			case generation.IsCode(err, generation.ErrNotFound):
				if taskID == "" {
					if orchestrator.stopConsent != nil {
						combined = errors.Join(combined, orchestrator.stopConsent(ownerID, sessionID, call.ID, call.IdempotencyKey, ""))
					}
					continue
				}
			case generation.IsCode(err, generation.ErrConflict):
				// A tombstoned key is permanently consumed, with no live Task
				// left to cancel. Settle only this call's claim and continue.
				if orchestrator.stopConsent != nil {
					combined = errors.Join(combined, orchestrator.stopConsent(ownerID, sessionID, call.ID, call.IdempotencyKey, taskID))
				}
				continue
			default:
				combined = errors.Join(combined, fmt.Errorf("lookup stopped turn task for call %s: %w", call.ID, err))
				continue
			}
		}
		if taskID == "" {
			continue
		}
		if orchestrator.taskGet != nil {
			task, err := orchestrator.taskGet(ctx, ownerID, taskID)
			if err != nil {
				combined = errors.Join(combined, fmt.Errorf("read stopped turn task for call %s: %w", call.ID, err))
				continue
			}
			if task.OwnerID != generation.OwnerID(ownerID) || task.SessionID != sessionID || task.ID != generation.TaskID(taskID) {
				combined = errors.Join(combined, fmt.Errorf("stopped turn bound task identity mismatch for call %s", call.ID))
				continue
			}
		}
		calls[index].TaskID = taskID
		if orchestrator.stopConsent != nil {
			combined = errors.Join(combined, orchestrator.stopConsent(ownerID, sessionID, call.ID, call.IdempotencyKey, taskID))
		}
		if orchestrator.taskCanceler != nil {
			if err := orchestrator.taskCanceler(ctx, ownerID, taskID); err != nil {
				combined = errors.Join(combined, fmt.Errorf("cancel stopped turn task %s: %w", taskID, err))
			}
		}
	}
	return combined
}

// CancelTurn 用户主动停止当前会话的活动轮次：中断正在进行的 LLM/工具
// 执行、尽力取消该轮已绑定的生成任务。只作用于当前活动轮次；空闲时
// 停止是 no-op（返回 false），取消标记不会残留毒化下一轮。
func (orchestrator *Orchestrator) CancelTurn(
	ctx context.Context,
	ownerID string,
	sessionID string,
) (bool, error) {
	// Persist intent before interrupting execution. If reconciliation later
	// cannot read the kernel, this active turn survives a restart for retry.
	turn, active, err := orchestrator.service.RequestTurnStop(ctx, ownerID, sessionID)
	if err != nil || !active {
		return active, err
	}
	turnKey := orchestrator.turnKey(ownerID, sessionID)
	orchestrator.activeMu.Lock()
	cancel := orchestrator.activeCancel[turnKey]
	orchestrator.canceledTurn[turnKey] = struct{}{}
	orchestrator.activeMu.Unlock()
	// AGT-12：轮次可能正处于挂起（consentWaits 有登记）——无论当前
	// 是否有活动 ctx，都要尝试弹出挂起登记并按停止唤醒；pop 成功后
	// 重写取消标记（defer clearTurnCanceled 可能已抹掉第一次写入），
	// 重放时 consumeTurnCanceled 命中即落停止终态。标记在重放消费前
	// 一直保留，即使唤醒入队经历回滚重试也不会毒化其他轮次（标记按
	// turnKey 隔离，且同会话无新轮次可被误伤——StartTurn 被活跃轮次
	// 的 stream 行拒绝）。
	if command, ok := orchestrator.popConsentWait(ownerID, sessionID); ok {
		orchestrator.activeMu.Lock()
		orchestrator.canceledTurn[turnKey] = struct{}{}
		orchestrator.activeMu.Unlock()
		orchestrator.enqueueSuspendedResume(command)
		return true, nil
	}
	if cancel == nil {
		if err := orchestrator.Enqueue(ProcessTurn{OwnerID: ownerID, SessionID: sessionID,
			AssistantMessageID: turn.AssistantMessageID, Options: turn.Options}); err != nil {
			orchestrator.logger.Warn("停止轮次入队暂失败，保留持久停止意图", "error", err)
		}
		return true, nil
	}
	// 先中断执行，再扫描绑定任务，避免停止期间继续提交新任务拉长
	// TOCTOU 窗口（残余窗口内提交的任务由 finalizeStoppedTurn 兜底取消）。
	cancel()
	// 只处理最近一条带工具调用的 assistant 消息（即活动轮次的计划），
	// 历史轮次的任务已是终态。
	messages, err := orchestrator.service.ListMessages(ctx, ownerID, sessionID)
	if err != nil {
		orchestrator.logger.Warn(
			"停止轮次时读取消息失败",
			"session_id", sessionID,
			"error", err,
		)
		return true, nil
	}
	for index := range messages {
		if messages[index].ID != turn.AssistantMessageID {
			continue
		}
		if err := orchestrator.reconcileStoppedTasks(context.WithoutCancel(ctx),
			ownerID, sessionID, turn.AssistantMessageID, messages[index].ToolCalls, turn.Options); err != nil {
			orchestrator.logger.Warn("停止轮次即时核对任务失败，等待恢复重试", "error", err)
		}
		break
	}
	return true, nil
}

// finalizeStoppedTurn 把被用户停止的轮次落成可见的停止终态：追加停止
// 文案、写 done 事件并让 defer 以“未失败”收尾。不标记 failed，
// 进程停机之外的停止不应把轮次伪装成错误。
func (orchestrator *Orchestrator) finalizeStoppedTurn(
	ctx context.Context,
	command ProcessTurn,
	assistant *Message,
) error {
	ctx = catalog.WithLocale(context.WithoutCancel(ctx), command.Options.Locale)
	// A submit callback may have persisted a TaskID after Process copied the
	// assistant message. Merge that durable binding before stop writes it back.
	if messages, err := orchestrator.service.ListMessages(ctx, command.OwnerID, command.SessionID); err == nil {
		for _, stored := range messages {
			if stored.ID != command.AssistantMessageID {
				continue
			}
			for index := range assistant.ToolCalls {
				for _, call := range stored.ToolCalls {
					if assistant.ToolCalls[index].ID == call.ID {
						mergeStoredToolCall(&assistant.ToolCalls[index], call)
					}
				}
			}
			break
		}
	} else {
		return err
	}
	// 停止收尾兜底取消在飞任务：CancelTurn 扫描与工具提交之间的
	// TOCTOU 窗口里落库的绑定也在这里被取消（计费止损）。
	if err := orchestrator.reconcileStoppedTasks(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID, assistant.ToolCalls, command.Options); err != nil {
		return err
	}
	stopped := orchestrator.text(ctx, orchestrator.schema.Presentation.Messages.TurnStopped)
	if assistant.Content == "" ||
		assistant.Content == orchestrator.schema.State.PendingContent {
		assistant.Content = stopped
	} else if !strings.HasSuffix(assistant.Content, stopped) {
		assistant.Content += "\n" + stopped
	}
	assistant.ImageURLs = nil
	assistant.Attachments = nil
	if err := orchestrator.service.UpdateMessage(
		ctx,
		command.OwnerID,
		*assistant,
	); err != nil {
		return err
	}
	return orchestrator.appendEvent(
		ctx,
		command.SessionID,
		map[string]any{
			orchestrator.schema.Stream.EventFields.Type: orchestrator.
				schema.Stream.EventTypes.Done,
		},
	)
}

func NewOrchestrator(config OrchestratorConfig) (*Orchestrator, error) {
	if config.Service == nil ||
		config.Agent == nil ||
		config.Tools == nil ||
		config.Model == "" && !agents.IsNotConfigured(config.Agent) ||
		config.Schema.Agent.WorkerCount <= 0 ||
		config.Schema.Agent.QueueSize <= 0 {
		return nil, errors.New("chat orchestrator: incomplete dependencies")
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	registry, err := compileToolRegistry(config.Schema, config.ToolAllowed)
	if err != nil {
		return nil, err
	}
	registry.offeringAllowed = config.OfferingAllowed
	return &Orchestrator{
		service:       config.Service,
		schema:        config.Schema,
		model:         config.Model,
		dsmlFor:       config.DSMLFor,
		visionInline:  config.VisionInline,
		agent:         config.Agent,
		tools:         config.Tools,
		logger:        config.Logger,
		registry:      registry,
		agentScope:    registry.scope,
		configuration: config.Configuration,
		taskCanceler:  config.TaskCanceler,
		toolNote:      config.ToolNote,
		taskLookup:    config.TaskLookup,
		taskGet:       config.TaskGet,
		stopConsent:   config.StopConsent,
		texts:         config.Texts,
		queue:         make(chan ProcessTurn, config.Schema.Agent.QueueSize),
		queued:        map[string]struct{}{},
		activeCancel:  map[string]context.CancelFunc{},
		canceledTurn:  map[string]struct{}{},
		consentWaits:  map[string]*consentSuspend{},
		compacting:    map[string]*compactionLock{},
	}, nil
}

// suspendForConsent 把因等待同意而中断的轮次登记进挂起表并安排超时
// 唤醒（AGT-12）。挂起本身不占用 worker：登记完成后 Process 返回，
// worker 立即可服务其他会话。deadline 零值表示全新挂起，按 Schema
// 声明的 consent_wait_timeout 起算；非零表示重启恢复重挂起，重新起算。
// 返回 false 表示轮次已被用户取消（取消标记在场，Process 应原地按
// 停止收束而不是挂起）。检查与登记同在 activeMu 临界区内，与
// CancelTurn 写标记互斥，消除“停止落在挂起登记前后”的竞态（审查 R2）。
func (orchestrator *Orchestrator) suspendForConsent(
	command ProcessTurn,
) bool {
	deadline := command.ConsentWaitDeadline
	if !deadline.After(time.Now()) {
		deadline = time.Now().Add(orchestrator.consentWaitTimeout())
	}
	key := orchestrator.turnKey(command.OwnerID, command.SessionID)
	orchestrator.activeMu.Lock()
	_, canceled := orchestrator.canceledTurn[key]
	orchestrator.activeMu.Unlock()
	if canceled {
		return false
	}
	timeoutEnqueue := func() {
		// 超时唤醒：弹出登记（幂等）后带过期 deadline 重新入队，
		// 工具在重放时看到已过期即返回等待文案（与旧阻塞语义一致）。
		if _, ok := orchestrator.popConsentWait(
			command.OwnerID,
			command.SessionID,
		); !ok {
			return
		}
		command.ConsentWaitDeadline = deadline
		orchestrator.enqueueSuspendedResume(command)
	}
	orchestrator.suspendMu.Lock()
	defer orchestrator.suspendMu.Unlock()
	if existing := orchestrator.consentWaits[key]; existing != nil {
		// 同会话同时只有一个活动轮次（StartTurn UPSERT 保证），
		// 理论上不会重复登记；防御性保留旧登记。
		return true
	}
	entry := &consentSuspend{command: command, deadline: deadline}
	// timer 在持锁期间创建：entry 的发布与 timer 字段写入对
	// popConsentWait（同样持锁）可见，避免 timer 字段竞态。
	entry.timer = time.AfterFunc(time.Until(deadline), timeoutEnqueue)
	orchestrator.consentWaits[key] = entry
	return true
}

// consentWaitTimeout 返回同意等待的最长挂起时长。catalog 校验保证
// 生产 schema 恒为正数；<=0 的兜底只为手造测试 schema 不至于零时长
// 立即超时，不是第二真源。
func (orchestrator *Orchestrator) consentWaitTimeout() time.Duration {
	seconds := orchestrator.schema.Agent.ConsentWaitTimeoutSeconds
	if seconds <= 0 {
		seconds = 120
	}
	return time.Duration(seconds) * time.Second
}

// popConsentWait 原子弹出挂起登记：第一个调用者拿走登记并负责入队，
// 后续（重复 wake、超时、停止）全部 no-op——幂等由此保证。
func (orchestrator *Orchestrator) popConsentWait(
	ownerID, sessionID string,
) (ProcessTurn, bool) {
	key := orchestrator.turnKey(ownerID, sessionID)
	orchestrator.suspendMu.Lock()
	entry := orchestrator.consentWaits[key]
	if entry != nil {
		delete(orchestrator.consentWaits, key)
	}
	orchestrator.suspendMu.Unlock()
	if entry == nil {
		return ProcessTurn{}, false
	}
	if entry.timer != nil {
		entry.timer.Stop()
	}
	command := entry.command
	command.ConsentWaitDeadline = entry.deadline
	return command, true
}

// PendingConsentWaits 返回当前因等待同意而挂起的轮次数（观测/指标）。
func (orchestrator *Orchestrator) PendingConsentWaits() int {
	orchestrator.suspendMu.Lock()
	defer orchestrator.suspendMu.Unlock()
	return len(orchestrator.consentWaits)
}

// ResumeConsentWait 在用户写入同意决定后唤醒挂起轮次：重新入队续跑。
// 返回是否存在被唤醒的挂起轮次。不占用调用方 goroutine。
func (orchestrator *Orchestrator) ResumeConsentWait(
	ownerID, sessionID string,
) bool {
	command, ok := orchestrator.popConsentWait(ownerID, sessionID)
	if !ok {
		return false
	}
	orchestrator.enqueueSuspendedResume(command)
	return true
}

// enqueueForWake 是唤醒路径专用入队：与普通 Enqueue 的去重语义不同。
// 挂起轮次的命令ID在原 Process 返回前仍留在 queued 去重集合里
// （runWorker 在 Process 返回后才 unmarkQueued），若此时唤醒方直接
// Enqueue，会被去重分支静默吞掉（返回 nil 假成功）——登记已删、timer
// 已停、命令未入队，轮次永久僵尸。这里显式区分三种结果：
// ErrWakeNotReleased（原 Process 尚未返回，稍后重试）、
// ErrOrchestratorQueueFull（队列满，稍后重试）、nil（已入队）。
func (orchestrator *Orchestrator) enqueueForWake(
	command ProcessTurn,
) error {
	if !orchestrator.markQueued(command.AssistantMessageID) {
		return ErrWakeNotReleased
	}
	select {
	case orchestrator.queue <- command:
		return nil
	default:
		orchestrator.unmarkQueued(command.AssistantMessageID)
		return ErrOrchestratorQueueFull
	}
}

// wakeEnqueueRetryParams 界定唤醒入队的重试：先在进程内短促重试
// （覆盖原 Process defer 链的毫秒级释放窗口），仍失败则重新登记挂起
// 并用延迟 timer 再次尝试——唤醒凭据在入队可观测成功前绝不丢失。
const (
	wakeRetryAttempts   = 100
	wakeRetryInterval   = 2 * time.Millisecond
	wakeRetryReArmDelay = time.Second
)

// enqueueSuspendedResume 把弹出的挂起轮次重新入队；短暂重试仍失败时
// 回滚登记（保留原 deadline）并用延迟 timer 重试，杜绝“pop 后凭据
// 丢失”的死锁路径（审查 R1）。
func (orchestrator *Orchestrator) enqueueSuspendedResume(
	command ProcessTurn,
) {
	deadline := command.ConsentWaitDeadline
	for attempt := 0; attempt < wakeRetryAttempts; attempt++ {
		err := orchestrator.enqueueForWake(command)
		if err == nil {
			return
		}
		if errors.Is(err, ErrWakeNotReleased) {
			time.Sleep(wakeRetryInterval)
			continue
		}
		break // ErrOrchestratorQueueFull：短促重试无意义，走延迟重试
	}
	orchestrator.logger.Warn(
		"挂起轮次唤醒入队暂失败，已回滚登记并安排延迟重试",
		"session_id", command.SessionID,
		"assistant_message_id", command.AssistantMessageID,
	)
	orchestrator.reRegisterConsentWait(command, deadline,
		time.Now().Add(wakeRetryReArmDelay))
}

// reRegisterConsentWait 把弹出的登记放回挂起表并安排重试 timer。
// 幂等：已存在登记（理论上不会）时保留旧登记。
func (orchestrator *Orchestrator) reRegisterConsentWait(
	command ProcessTurn,
	deadline time.Time,
	fireAt time.Time,
) {
	key := orchestrator.turnKey(command.OwnerID, command.SessionID)
	orchestrator.suspendMu.Lock()
	defer orchestrator.suspendMu.Unlock()
	if existing := orchestrator.consentWaits[key]; existing != nil {
		return
	}
	entry := &consentSuspend{command: command, deadline: deadline}
	entry.timer = time.AfterFunc(time.Until(fireAt), func() {
		if _, ok := orchestrator.popConsentWait(
			command.OwnerID,
			command.SessionID,
		); !ok {
			return
		}
		orchestrator.enqueueSuspendedResume(command)
	})
	orchestrator.consentWaits[key] = entry
}

func (orchestrator *Orchestrator) Enqueue(command ProcessTurn) error {
	if command.AssistantMessageID == "" {
		return errors.New("chat orchestrator: assistant message ID is required")
	}
	if !orchestrator.markQueued(command.AssistantMessageID) {
		return nil
	}
	select {
	case orchestrator.queue <- command:
		return nil
	default:
		orchestrator.unmarkQueued(command.AssistantMessageID)
		return ErrOrchestratorQueueFull
	}
}

func (orchestrator *Orchestrator) Schedule(
	ctx context.Context,
	command ProcessTurn,
) error {
	if err := orchestrator.Enqueue(command); err != nil {
		rejectErr := orchestrator.reject(
			context.WithoutCancel(ctx),
			command,
		)
		return errors.Join(err, rejectErr)
	}
	return nil
}

func (orchestrator *Orchestrator) reject(
	ctx context.Context,
	command ProcessTurn,
) error {
	messages, err := orchestrator.service.ListMessages(
		ctx,
		command.OwnerID,
		command.SessionID,
	)
	if err != nil {
		return errors.Join(
			err,
			orchestrator.service.FinishStream(
				ctx,
				command.SessionID,
				command.AssistantMessageID,
				true,
			),
			orchestrator.service.FinishTurn(
				ctx,
				command.AssistantMessageID,
				true,
			),
		)
	}
	for index := range messages {
		if messages[index].ID != command.AssistantMessageID {
			continue
		}
		return errors.Join(
			orchestrator.fail(
				ctx,
				command.OwnerID,
				&messages[index],
				orchestrator.text(catalog.WithLocale(ctx, command.Options.Locale), orchestrator.schema.Presentation.Messages.AgentBusy),
			),
			orchestrator.service.FinishStream(
				ctx,
				command.SessionID,
				command.AssistantMessageID,
				true,
			),
			orchestrator.service.FinishTurn(
				ctx,
				command.AssistantMessageID,
				true,
			),
		)
	}
	return errors.Join(
		ErrMessageNotFound,
		orchestrator.service.FinishStream(
			ctx,
			command.SessionID,
			command.AssistantMessageID,
			true,
		),
		orchestrator.service.FinishTurn(
			ctx,
			command.AssistantMessageID,
			true,
		),
	)
}

func (orchestrator *Orchestrator) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var workers sync.WaitGroup
	workers.Add(1)
	go func() { defer workers.Done(); orchestrator.runWaitingScanner(runCtx) }()
	for range orchestrator.schema.Agent.WorkerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			orchestrator.runWorker(runCtx)
		}()
	}
	turns, err := orchestrator.service.RecoverTurns(runCtx)
	if err != nil {
		cancel()
		workers.Wait()
		return err
	}
	for _, turn := range turns {
		if err := orchestrator.enqueueRecovered(
			runCtx,
			ProcessTurn{
				OwnerID:            turn.OwnerID,
				SessionID:          turn.SessionID,
				AssistantMessageID: turn.AssistantMessageID,
				Options:            turn.Options,
			},
		); err != nil {
			cancel()
			workers.Wait()
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
	}
	workers.Wait()
	return nil
}

func (orchestrator *Orchestrator) runWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case command := <-orchestrator.queue:
			if err := orchestrator.service.MarkTurnRunning(
				context.WithoutCancel(ctx),
				command.AssistantMessageID,
			); err != nil {
				orchestrator.logger.Error(
					"更新 Chat Agent Turn 状态失败",
					"assistant_message_id", command.AssistantMessageID,
					"error", err,
				)
				orchestrator.unmarkQueued(command.AssistantMessageID)
				continue
			}
			if err := orchestrator.Process(ctx, command); err != nil &&
				!errors.Is(err, context.Canceled) {
				orchestrator.logger.Error(
					"Chat Agent 任务失败",
					"session_id", command.SessionID,
					"assistant_message_id", command.AssistantMessageID,
					"error", err,
				)
			}
			orchestrator.unmarkQueued(command.AssistantMessageID)
		}
	}
}

func (orchestrator *Orchestrator) enqueueRecovered(
	ctx context.Context,
	command ProcessTurn,
) error {
	if !orchestrator.markQueued(command.AssistantMessageID) {
		return nil
	}
	select {
	case orchestrator.queue <- command:
		return nil
	case <-ctx.Done():
		orchestrator.unmarkQueued(command.AssistantMessageID)
		return ctx.Err()
	}
}

func (orchestrator *Orchestrator) markQueued(
	assistantMessageID string,
) bool {
	orchestrator.queueMu.Lock()
	defer orchestrator.queueMu.Unlock()
	if _, exists := orchestrator.queued[assistantMessageID]; exists {
		return false
	}
	orchestrator.queued[assistantMessageID] = struct{}{}
	return true
}

func (orchestrator *Orchestrator) unmarkQueued(
	assistantMessageID string,
) {
	orchestrator.queueMu.Lock()
	defer orchestrator.queueMu.Unlock()
	delete(orchestrator.queued, assistantMessageID)
}

func (orchestrator *Orchestrator) Process(
	ctx context.Context,
	command ProcessTurn,
) (processErr error) {
	messages, err := orchestrator.service.ListMessages(
		ctx,
		command.OwnerID,
		command.SessionID,
	)
	if err != nil {
		return err
	}
	assistantIndex := -1
	for index := range messages {
		if messages[index].ID == command.AssistantMessageID {
			assistantIndex = index
			break
		}
	}
	if assistantIndex < 0 {
		return ErrMessageNotFound
	}
	assistant := messages[assistantIndex]
	turn, err := orchestrator.service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil {
		return err
	}
	// The selection is only ever the one the service verified and stored with
	// the turn; whatever the caller put in the command is not trusted.
	command.Options.Selection = turn.Options.Selection
	command.Options.SelectionPicked = turn.Options.SelectionPicked
	command.Options.InputSource = turn.Options.InputSource
	// So are the generation model picks: a retired model was moved to its
	// successor when the turn started (#329 follow-up).
	command.Options.AIModel = turn.Options.AIModel
	command.Options.DomainModels = turn.Options.DomainModels
	command.Options.RetiredModels = turn.Options.RetiredModels
	// The Agent scope is the one the service stored with the turn when it
	// started, for the same reason.
	command.Options.AgentScope = orchestrator.agentScope.Normalize(turn.Options.AgentScope)
	// So are the model and the thinking level (#322).
	command.Options.Model = turn.Options.Model
	command.Options.ModelConnection = turn.Options.ModelConnection
	command.Options.ReasoningEffort = turn.Options.ReasoningEffort
	// DSML tool calls are read only for a model whose profile writes them.
	dsmlSchema := orchestrator.dsmlSchema(command.Options)
	command.Options.Harness = turn.Options.Harness
	// The UI locale is the one frozen with the turn when it started.
	command.Options.Locale = turn.Options.Locale
	ctx = catalog.WithLocale(ctx, command.Options.Locale)
	if command.Options.Harness != nil {
		ctx = context.WithValue(ctx, harnessContextKey{}, command.Options.Harness)
	}
	if turn.State == orchestrator.service.TurnQueuedState() {
		if err := orchestrator.service.MarkTurnRunning(ctx, command.AssistantMessageID); err != nil {
			return err
		}
		turn.State = orchestrator.service.TurnRunningState()
	}
	// The repository may return a message whose ToolCalls slice still shares
	// backing storage with its cache. Keep Process mutations private until the
	// next explicit UpdateMessage, including while CancelTurn reads it.
	assistant.ToolCalls = append([]ToolCall(nil), assistant.ToolCalls...)
	if turn.CheckpointVersion > 0 && turn.Checkpoint.Assistant.ID == assistant.ID {
		persistedCalls := assistant.ToolCalls
		assistant = turn.Checkpoint.Assistant
		assistant.ToolCalls = append([]ToolCall(nil), assistant.ToolCalls...)
		for index := range assistant.ToolCalls {
			for _, stored := range persistedCalls {
				if assistant.ToolCalls[index].ID == stored.ID {
					mergeStoredToolCall(&assistant.ToolCalls[index], stored)
				}
			}
		}
	}
	storedCalls := append([]ToolCall(nil), assistant.ToolCalls...)
	// 每轮持有可取消的 ctx：用户停止（CancelTurn）会中断 LLM 流式调用
	// 与工具执行；进程停机沿用 worker 的 runCtx，语义不变。
	processCtx, cancelProcess := context.WithCancel(ctx)
	turnKey := orchestrator.turnKey(command.OwnerID, command.SessionID)
	orchestrator.registerActiveTurn(turnKey, cancelProcess)
	defer orchestrator.unregisterActiveTurn(turnKey)
	defer orchestrator.clearTurnCanceled(turnKey)
	defer cancelProcess()
	failed := false
	// suspended=true 表示轮次已挂起等待用户输入（AGT-12）：返回时不
	// 收尾流与轮次，等唤醒方重新入队续跑。
	suspended := false
	stopFinalized := false
	finalizeStop := func() error {
		err := orchestrator.finalizeStoppedTurn(ctx, command, &assistant)
		if err == nil {
			stopFinalized = true
		}
		return err
	}
	// 进程停机（区别于用户取消：仅 runCtx 被取消）时轮次保持可恢复：
	// 不落失败、不收尾流，交给重启后的 RecoverTurns 重新入队续跑。
	defer func() {
		// 挂起（AGT-12）与进程停机都保持轮次可恢复：不落失败、不
		// 收尾流。挂起轮次由唤醒方重新入队，重启后由 RecoverTurns
		// 重新入队续跑。
		if suspended || ctx.Err() != nil {
			return
		}
		requested, err := orchestrator.service.TurnStopRequested(
			context.WithoutCancel(ctx), command.OwnerID, command.SessionID, command.AssistantMessageID)
		if err != nil {
			processErr = errors.Join(processErr, err)
			return
		}
		if requested && !stopFinalized {
			if err := finalizeStop(); err != nil {
				processErr = errors.Join(processErr, err)
				return
			}
		}
		finishErr := errors.Join(
			orchestrator.service.FinishStream(
				context.WithoutCancel(ctx),
				command.SessionID,
				command.AssistantMessageID,
				failed || processErr != nil,
			),
			orchestrator.service.FinishTurn(
				context.WithoutCancel(ctx),
				command.AssistantMessageID,
				failed || processErr != nil,
			),
		)
		if err := finishErr; processErr == nil && err != nil {
			processErr = err
		}
	}()
	requested, err := orchestrator.service.TurnStopRequested(
		context.WithoutCancel(ctx), command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil {
		return err
	}
	if requested || orchestrator.consumeTurnCanceled(turnKey) {
		return finalizeStop()
	}
	// Stop can win before the first configuration snapshot or while its read
	// is in flight. Install the finalizer first and never freeze a stopped turn.
	if turn.Checkpoint.Configuration == nil && orchestrator.configuration != nil {
		snapshot, snapshotErr := orchestrator.configuration(processCtx, command.OwnerID)
		if snapshotErr != nil {
			if requested, checkErr := orchestrator.service.TurnStopRequested(context.WithoutCancel(ctx),
				command.OwnerID, command.SessionID, command.AssistantMessageID); checkErr == nil && requested {
				return finalizeStop()
			}
			suspended = true
			return snapshotErr
		}
		snapshot.SkillExecution = orchestrator.schema.Skills.Execution
		turn.Checkpoint.Configuration = &snapshot
		version, saveErr := orchestrator.service.SaveTurnCheckpoint(processCtx, turn, turn.State, turn.Checkpoint, false)
		if saveErr != nil {
			if requested, checkErr := orchestrator.service.TurnStopRequested(context.WithoutCancel(ctx),
				command.OwnerID, command.SessionID, command.AssistantMessageID); checkErr == nil && requested {
				return finalizeStop()
			}
			suspended = true
			return saveErr
		}
		turn.CheckpointVersion = version
	}
	if turn.Checkpoint.Configuration != nil {
		ctx = context.WithValue(ctx, configurationContextKey{}, turn.Checkpoint.Configuration)
		processCtx = context.WithValue(processCtx, configurationContextKey{}, turn.Checkpoint.Configuration)
	}

	// 恢复路径的持久化任务绑定必须在 assistant.ToolCalls 被重赋值
	// 之前捕获：B11 恢复重放对已绑定调用直进观察等待，不重建输入。
	boundTasks := map[string]string{}
	for _, call := range assistant.ToolCalls {
		if call.TaskID != "" {
			boundTasks[call.ID] = call.TaskID
		}
	}
	// AGT-12 审查 R3：恢复重放把绑定同步回消息并立即落库——否则
	// 重放全程（含 follow-up LLM 流的秒级窗口）消息上没有 TaskID，
	// 该窗口内用户停止时 CancelTurn 扫描不到在飞任务，计费止损漏。
	if len(boundTasks) > 0 {
		backfilled := false
		for index := range assistant.ToolCalls {
			if id := boundTasks[assistant.ToolCalls[index].ID]; id != "" &&
				assistant.ToolCalls[index].TaskID == "" {
				assistant.ToolCalls[index].TaskID = id
				backfilled = true
			}
		}
		if backfilled {
			if err := orchestrator.service.UpdateMessage(
				context.WithoutCancel(ctx),
				command.OwnerID,
				assistant,
			); err != nil {
				return err
			}
		}
	}
	history := append([]Message(nil), messages[:assistantIndex]...)
	completion := agents.Completion{}
	checkpointResuming := turn.CheckpointVersion > 0 && len(turn.Checkpoint.Completion.ToolCalls) > 0
	resumingPlan := assistant.Content !=
		orchestrator.schema.State.PendingContent ||
		len(assistant.ToolCalls) > 0
	if !checkpointResuming && !resumingPlan {
		// A new turn first compacts the context when the session's newest
		// usage reached the threshold (#322); a resumed one keeps its request.
		orchestrator.autoCompact(processCtx, command, history)
	}
	request := orchestrator.agentRequest(ctx, history, command.Options, command.OwnerID, command.SessionID)
	if checkpointResuming {
		request = turn.Checkpoint.Request
		completion = turn.Checkpoint.Completion
	} else if resumingPlan {
		completion.Content = assistant.Content
		if completion.Content == orchestrator.schema.State.PendingContent {
			completion.Content = ""
		}
		completion.ToolCalls = messageCallsToAgent(assistant.ToolCalls)
	} else {
		streamFilter := dsml.NewFilter(dsmlSchema)
		round := orchestrator.startThinkingRound(processCtx, command.SessionID)
		completion, err = orchestrator.agent.Stream(
			processCtx,
			request,
			func(delta agents.Delta) error {
				return round.receive(delta, streamFilter)
			},
		)
		if err != nil {
			if orchestrator.turnCanceled(turnKey) {
				return finalizeStop()
			}
			if ctx.Err() != nil {
				// 进程停机：保留可恢复轮次，交给 RecoverTurns 续跑。
				return nil
			}
			failed = true
			orchestrator.logger.Error(
				"Chat Agent 请求失败",
				"session_id", command.SessionID,
				"assistant_message_id", command.AssistantMessageID,
				"error", err,
			)
			return orchestrator.fail(
				ctx,
				command.OwnerID,
				&assistant,
				orchestrator.agentFailureMessage(ctx, err),
			)
		}
		orchestrator.recordUsage(ctx, command, request.Model, completion.Usage)
		if err := streamFilter.Flush(round.emitText); err != nil {
			if orchestrator.turnCanceled(turnKey) {
				return finalizeStop()
			}
			if ctx.Err() != nil {
				// 进程停机：保留可恢复轮次，交给 RecoverTurns 续跑。
				return nil
			}
			failed = true
			return err
		}
	}
	maxToolCalls := max(1, orchestrator.schema.Agent.MaxToolCallsPerReply)
	// summarizeAtLimit runs the tool-less summary round once the reply's tool
	// budget is spent (AGT-04, #275). DSML the model still writes is stripped,
	// never streamed or stored; the unexecuted calls are listed with Schema
	// texts. exit reports that Process must return err right away.
	summarizeAtLimit := func(base agents.Request, pendingCalls []agents.ToolCall) (bool, error) {
		pendingCalls = append([]agents.ToolCall(nil), pendingCalls...)
		summaryRequest := base
		summaryRequest.Tools = nil
		summaryFilter := dsml.NewFilter(dsmlSchema)
		summaryRound := orchestrator.startThinkingRound(processCtx, command.SessionID)
		summary, summaryErr := orchestrator.agent.Stream(
			processCtx,
			summaryRequest,
			func(delta agents.Delta) error {
				return summaryRound.receive(delta, summaryFilter)
			},
		)
		if summaryErr == nil {
			orchestrator.recordUsage(ctx, command, summaryRequest.Model, summary.Usage)
			summaryErr = summaryFilter.Flush(summaryRound.emitText)
		}
		if summaryErr != nil {
			if orchestrator.turnCanceled(turnKey) {
				return true, finalizeStop()
			}
			if ctx.Err() != nil {
				// 进程停机：保留可恢复轮次，交给 RecoverTurns 续跑。
				return true, nil
			}
			orchestrator.logger.Warn(
				"工具预算用完后的总结请求失败",
				"session_id", command.SessionID,
				"assistant_message_id", command.AssistantMessageID,
				"error", summaryErr,
			)
		}
		if summary.Content != "" || len(summary.ToolCalls) > 0 {
			parsedSummary := dsml.Parse(
				dsmlSchema,
				summary.Content,
			)
			if strings.TrimSpace(parsedSummary.Content) != "" {
				assistant.Content += "\n" + parsedSummary.Content
			}
			pendingCalls = append(pendingCalls, parsedSummary.ToolCalls...)
			pendingCalls = append(pendingCalls, summary.ToolCalls...)
		}
		assistant.Content += "\n" + strings.ReplaceAll(
			orchestrator.text(ctx, orchestrator.schema.Presentation.Messages.ToolRoundLimit),
			"{limit}",
			strconv.Itoa(maxToolCalls),
		)
		if pending := orchestrator.pendingCallsText(pendingCalls); pending != "" {
			assistant.Content += "\n" + pending
		}
		return false, nil
	}
	// B4 有界工具循环：模型（例如同意获批后）可在同一轮内继续调用
	// 工具。每轮调用 ID 按轮次唯一，已执行轮的调用与 TaskID 绑定
	// 累积在 assistant 消息上（停止可取消全部在飞任务）。
	// 恢复路径的 completion 来自已落库计划（ID 已定型、内容已剥离
	// DSML），标记 parsed 跳过重新解析与改写。
	allImageURLs := append([]string(nil), assistant.ImageURLs...)
	parsed := resumingPlan
	if checkpointResuming {
		parsed = true
	}
	// AGT-12 审查 R3：恢复重放后，新 LLM 轮的调用 ID 以落库调用中的
	// 最大轮次为偏移编号（effectiveRound），避免与已落库的同名轮次
	// 调用 ID 撞车（严格上游 tool_calls 配对 400，或幂等键静默回放
	// 旧任务）。循环控制仍从 0 起：round=0 保留“首次赋值”语义；
	// 工具预算按本次回复已执行的调用数计（#275），不随续跑重置。
	resumeRound := 0
	if len(assistant.ToolCalls) > 0 {
		resumeRound = maxRoundFromCalls(assistant.ToolCalls) + 1
	}
	startRound := 0
	if checkpointResuming {
		startRound = turn.Checkpoint.Round
		resumeRound = maxRoundFromCalls(assistant.ToolCalls) - startRound
	}
	for round := startRound; ; round++ {
		effectiveRound := resumeRound + round
		if !parsed {
			parsedDSML := dsml.Parse(
				dsmlSchema,
				completion.Content,
			)
			completion.Content = parsedDSML.Content
			if len(completion.ToolCalls) == 0 {
				completion.ToolCalls = parsedDSML.ToolCalls
			}
			completion.ToolCalls = perTurnToolCallIDs(
				command.AssistantMessageID,
				completion.ToolCalls,
				effectiveRound,
			)
		}
		if checkpointResuming && round == startRound {
			// The assistant plan and completed tool replies are already durable.
		} else if round == 0 {
			assistant.Content = completion.Content
			assistant.ToolCalls = agentCallsToMessages(completion.ToolCalls)
			for index := range assistant.ToolCalls {
				for _, stored := range storedCalls {
					if stored.ID == assistant.ToolCalls[index].ID {
						assistant.ToolCalls[index].TaskID = stored.TaskID
						assistant.ToolCalls[index].IdempotencyKey = stored.IdempotencyKey
						assistant.ToolCalls[index].EstimatedCredits = stored.EstimatedCredits
						assistant.ToolCalls[index].NativeQuote = stored.NativeQuote
						assistant.ToolCalls[index].ApprovalSource = stored.ApprovalSource
						break
					}
				}
			}
		} else {
			assistant.ToolCalls = append(
				assistant.ToolCalls,
				agentCallsToMessages(completion.ToolCalls)...,
			)
			assistant.Content += completion.Content
		}
		orchestrator.freezeToolCallKeys(command.SessionID, assistant.ToolCalls, storedCalls)
		if len(completion.ToolCalls) == 0 {
			break
		}
		// #275：工具预算按本次回复实际执行的调用数计（Schema
		// chat.agent.max_tool_calls_per_reply，可由运行设置覆盖），每次新
		// 回复从 0 起算。assistant.ToolCalls 只保留已执行或正要执行的调用，
		// 恢复/续跑时已执行的调用仍计入同一次回复的预算。
		executedCalls := len(assistant.ToolCalls) - len(completion.ToolCalls)
		remainingCalls := maxToolCalls - executedCalls
		if remainingCalls <= 0 {
			// 预算用完：不再执行本批调用，从消息上移除；进入无工具的
			// 总结轮，把已执行的失败/结果归纳给用户（AGT-04）。
			assistant.ToolCalls = assistant.ToolCalls[:executedCalls]
			if exit, err := summarizeAtLimit(request, completion.ToolCalls); exit {
				return err
			}
			break
		}
		var overflowCalls []agents.ToolCall
		if len(completion.ToolCalls) > remainingCalls {
			// 本批超出剩余预算：只执行前 remainingCalls 个，其余在总结轮
			// 列给用户确认。
			overflowCalls = append([]agents.ToolCall(nil), completion.ToolCalls[remainingCalls:]...)
			completion.ToolCalls = completion.ToolCalls[:remainingCalls]
			assistant.ToolCalls = assistant.ToolCalls[:executedCalls+remainingCalls]
		}
		if !checkpointResuming || round != startRound {
			turn.Checkpoint = TurnCheckpoint{Configuration: turn.Checkpoint.Configuration, Round: round, Assistant: assistant, Request: request, Completion: completion}
			version, saveErr := orchestrator.service.SaveTurnCheckpoint(context.WithoutCancel(ctx), turn,
				orchestrator.service.TurnRunningState(), turn.Checkpoint, false)
			if saveErr != nil {
				if errors.Is(saveErr, ErrTurnCheckpointConflict) {
					if requested, checkErr := orchestrator.service.TurnStopRequested(context.WithoutCancel(ctx), command.OwnerID, command.SessionID, command.AssistantMessageID); checkErr == nil && requested {
						return finalizeStop()
					}
				}
				suspended = true
				return saveErr
			}
			turn.CheckpointVersion, turn.State = version, orchestrator.service.TurnRunningState()
		}
		if err := orchestrator.service.UpdateMessage(
			context.WithoutCancel(ctx),
			command.OwnerID,
			assistant,
		); err != nil {
			suspended = true
			return err
		}
		if err := orchestrator.appendEvent(
			processCtx,
			command.SessionID,
			map[string]any{
				orchestrator.schema.Stream.EventFields.Type: orchestrator.
					schema.Stream.EventTypes.Status,
				orchestrator.schema.Stream.EventFields.Content: orchestrator.text(
					ctx, orchestrator.schema.Presentation.Status.Generating),
			},
		); err != nil {
			if orchestrator.turnCanceled(turnKey) {
				return finalizeStop()
			}
			if ctx.Err() != nil {
				// 进程停机：保留可恢复轮次，交给 RecoverTurns 续跑。
				return nil
			}
			failed = true
			return err
		}
		toolContent, imageURLs, taskBindings, toolErr :=
			orchestrator.executeTools(
				processCtx,
				command,
				history,
				completion.ToolCalls,
				boundTasks,
				assistant.ToolCalls,
				&turn,
			)
		if errors.Is(toolErr, ErrTurnAwaitTask) {
			suspended = true
			return nil
		}
		if errors.Is(toolErr, ErrTurnCheckpointConflict) {
			if requested, checkErr := orchestrator.service.TurnStopRequested(context.WithoutCancel(ctx), command.OwnerID, command.SessionID, command.AssistantMessageID); checkErr == nil && requested {
				return finalizeStop()
			}
			suspended = true
			return toolErr
		}
		if errors.Is(toolErr, ErrTurnSuspended) {
			// AGT-12：request_consent 已写待决状态，挂起轮次并立即
			// 释放 worker。assistant 计划（含工具调用）已在上方
			// UpdateMessage 落库，唤醒后按 resumingPlan 路径重放续跑。
			// 登记失败（suspendForConsent 返回 false）或登记后复查到
			// 取消标记，都表示停止与挂起竞态命中：丢弃登记并原地按
			// 停止收束——不得在取消后继续挂起续跑计费（审查 R2）。
			registered := orchestrator.suspendForConsent(command)
			orchestrator.activeMu.Lock()
			_, canceled := orchestrator.canceledTurn[turnKey]
			orchestrator.activeMu.Unlock()
			if !registered || canceled {
				if registered {
					orchestrator.popConsentWait(
						command.OwnerID,
						command.SessionID,
					)
				}
				orchestrator.activeMu.Lock()
				delete(orchestrator.canceledTurn, turnKey)
				orchestrator.activeMu.Unlock()
				return finalizeStop()
			}
			suspended = true
			return nil
		}
		// A submit callback persisted the task binding together with its estimated
		// credits and approval source. Merge them before the next write so that
		// write cannot drop them.
		orchestrator.mergeDurableToolCalls(ctx, command, &assistant)
		for index := range assistant.ToolCalls {
			if assistant.ToolCalls[index].TaskID == "" {
				assistant.ToolCalls[index].TaskID =
					taskBindings[assistant.ToolCalls[index].ID]
			}
		}
		allImageURLs = append(allImageURLs, imageURLs...)
		assistant.ImageURLs = allImageURLs
		if toolErr != nil {
			if orchestrator.turnCanceled(turnKey) {
				return finalizeStop()
			}
			if ctx.Err() != nil {
				// 进程停机：保留可恢复轮次，交给 RecoverTurns 续跑。
				return nil
			}
			failed = true
			orchestrator.logger.Error(
				"对话生成工具执行失败",
				"session_id", command.SessionID,
				"assistant_message_id", command.AssistantMessageID,
				"error", toolErr,
			)
		}
		if len(overflowCalls) > 0 {
			// 本批已执行到预算上限：带上已执行结果直接进入总结轮。
			base := request
			if len(toolContent) > 0 {
				base = orchestrator.followUpRequest(request, completion, toolContent)
			}
			if exit, err := summarizeAtLimit(base, overflowCalls); exit {
				return err
			}
			break
		}
		if len(toolContent) == 0 {
			break
		}
		followUp := orchestrator.followUpRequest(
			request,
			completion,
			toolContent,
		)
		// 续跑流同样要过滤 DSML：模型可能在总结中继续发起调用。
		streamFilter := dsml.NewFilter(dsmlSchema)
		followUpRound := orchestrator.startThinkingRound(processCtx, command.SessionID)
		next, streamErr := orchestrator.agent.Stream(
			processCtx,
			followUp,
			func(delta agents.Delta) error {
				return followUpRound.receive(delta, streamFilter)
			},
		)
		if streamErr != nil {
			if orchestrator.turnCanceled(turnKey) {
				return finalizeStop()
			}
			if ctx.Err() != nil {
				// 进程停机：保留可恢复轮次，交给 RecoverTurns 续跑。
				return nil
			}
			orchestrator.logger.Error(
				"对话工具续跑请求失败",
				"session_id", command.SessionID,
				"assistant_message_id", command.AssistantMessageID,
				"error", streamErr,
			)
			if len(assistant.ImageURLs) == 0 && toolErr == nil {
				failed = true
			}
			break
		}
		orchestrator.recordUsage(ctx, command, followUp.Model, next.Usage)
		if err := streamFilter.Flush(followUpRound.emitText); err != nil {
			if orchestrator.turnCanceled(turnKey) {
				return finalizeStop()
			}
			if ctx.Err() != nil {
				// 进程停机：保留可恢复轮次，交给 RecoverTurns 续跑。
				return nil
			}
			failed = true
			return err
		}
		completion = next
		checkpointResuming = false
		parsed = false
		// AGT-02：续跑请求累积——下一轮 followUp 基于本轮完整上下文，
		// 模型不再"失忆"重复已执行的工具调用。
		request = followUp
	}
	if assistant.Content == "" && len(assistant.ImageURLs) == 0 {
		assistant.Content = orchestrator.text(ctx, orchestrator.schema.Presentation.Messages.NoResult)
		if failed {
			assistant.Content = orchestrator.withErrorReference(ctx, assistant.Content, assistant.ID)
		}
	}
	orchestrator.attachAssistantResults(context.WithoutCancel(ctx), command.OwnerID, &assistant)
	if err := orchestrator.service.UpdateMessage(
		context.WithoutCancel(ctx),
		command.OwnerID,
		assistant,
	); err != nil {
		failed = true
		return err
	}
	orchestrator.startFirstTurnTitle(command, history)
	if err := orchestrator.appendEvent(
		context.WithoutCancel(ctx),
		command.SessionID,
		map[string]any{
			orchestrator.schema.Stream.EventFields.Type: orchestrator.
				schema.Stream.EventTypes.Done,
		},
	); err != nil {
		failed = true
		return err
	}
	return nil
}

// startFirstTurnTitle summarises the session title from the first round in the
// background. The turn never waits for it: it has already been saved, and the
// summary is a convenience that may fail without anyone noticing. Only the
// first round qualifies (the history holds just that one user message), and the
// write is conditional on the title still being pending, so restarts, retries
// and a manual rename can never produce a second or overriding title.
func (orchestrator *Orchestrator) startFirstTurnTitle(command ProcessTurn, history []Message) {
	if len(history) != 1 || history[0].Role != orchestrator.schema.Roles.User {
		return
	}
	content := history[0].Content
	orchestrator.titleWaiters.Add(1)
	go func() {
		defer orchestrator.titleWaiters.Done()
		timeout := time.Duration(orchestrator.schema.Agent.TitleTimeoutSeconds) * time.Second
		ctx, cancel := context.WithTimeout(catalog.WithLocale(context.Background(), command.Options.Locale), timeout)
		defer cancel()
		if err := orchestrator.generateFirstTurnTitle(ctx, command, content); err != nil {
			orchestrator.logger.Warn(
				"生成首轮对话标题失败，保留当前标题",
				"session_id", command.SessionID,
				"error", err,
			)
		}
	}()
}

// WaitForTitles blocks until the background title summaries started so far
// have finished. Application.Run waits on it (bounded by the HTTP shutdown timeout) during graceful
// shutdown, and tests use it; a turn never does.
func (orchestrator *Orchestrator) WaitForTitles() {
	orchestrator.titleWaiters.Wait()
}

func (orchestrator *Orchestrator) generateFirstTurnTitle(
	ctx context.Context,
	command ProcessTurn,
	userContent string,
) error {
	session, found, err := orchestrator.service.Session(ctx, command.OwnerID, command.SessionID)
	if err != nil || !found || session.TitleSource != TitleSourcePending {
		return err
	}
	maximum := orchestrator.titleMaxChars(ctx)
	prompt := strings.NewReplacer(
		"{maximum}", fmt.Sprint(maximum),
		"{content}", userContent,
	).Replace(orchestrator.text(ctx, orchestrator.schema.Agent.TitlePrompt))
	// The title is written by the turn's own model and connection (#322).
	model := orchestrator.turnChatModel(command.Options)
	completion, err := orchestrator.agent.Complete(
		ctx,
		agents.Request{
			Model:       model.name,
			Connection:  model.connection,
			Temperature: orchestrator.schema.Agent.Temperature,
			Messages: []agents.Message{
				{Role: orchestrator.schema.Roles.System, Content: prompt},
				{Role: orchestrator.schema.Roles.User, Content: userContent},
			},
		},
	)
	if err != nil {
		return err
	}
	title := cleanGeneratedTitle(completion.Content, maximum)
	if title == "" {
		return errors.New("模型没有返回可用的标题")
	}
	applied, err := orchestrator.service.ApplyAutoTitle(ctx, command.OwnerID, command.SessionID, title)
	if err != nil || !applied {
		return err
	}
	return orchestrator.appendEvent(ctx, command.SessionID, map[string]any{
		orchestrator.schema.Stream.EventFields.Type: orchestrator.
			schema.Stream.EventTypes.Title,
		orchestrator.schema.Stream.EventFields.Content: title,
	})
}

// titleMaxChars is the title length for the turn's UI locale.
func (orchestrator *Orchestrator) titleMaxChars(ctx context.Context) int {
	if maximum := orchestrator.schema.Agent.TitleMaxCharsByLocale[catalog.LocaleFrom(ctx)]; maximum > 0 {
		return maximum
	}
	return orchestrator.schema.Agent.TitleMaxChars
}

// cleanGeneratedTitle reduces a model answer to a plain title: quotes and line
// breaks are removed, leading and trailing punctuation and spaces are trimmed,
// and the result is clipped to maximum characters.
func cleanGeneratedTitle(raw string, maximum int) string {
	raw = strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t', '"', '\'', '`', '“', '”', '‘', '’', '「', '」', '『', '』', '《', '》':
			return -1
		}
		return r
	}, raw)
	raw = strings.TrimFunc(raw, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	})
	return strings.TrimSpace(truncateRunes(raw, maximum))
}

func (orchestrator *Orchestrator) agentRequest(
	ctx context.Context,
	history []Message,
	options TurnOptions,
	ownerID string,
	sessionID string,
) agents.Request {
	if options.Harness != nil {
		ctx = context.WithValue(ctx, harnessContextKey{}, options.Harness)
	}
	systemPrompt := orchestrator.schema.Agent.SystemPrompt
	domainContexts := orchestrator.schema.Agent.DomainContext
	if snapshot := configurationFromContext(ctx); snapshot != nil {
		systemPrompt = snapshot.SystemPrompt
		domainContexts = snapshot.DomainContext
	}
	if domainContext := domainContexts[options.CreativeDomain]; domainContext != "" {
		systemPrompt += "\n\n" + domainContext
	}
	registry, registryErr := orchestrator.registryFor(ctx)
	scope := orchestrator.agentScope.Normalize(options.AgentScope)
	if registryErr == nil {
		// The scope follows the domain hint it overrides. The selection and the
		// framed session notes stay at the end of the prompt.
		systemPrompt += orchestrator.scopePrompt(registry, scope)
	}
	if registryErr == nil {
		systemPrompt += orchestrator.skillToolDirective(ctx, registry, options)
	}
	// The session summary stands in for the messages it covers (#322). Image
	// indices and handles are counted over the whole history either way.
	var latest *Compaction
	if orchestrator.service != nil && sessionID != "" {
		if state, err := orchestrator.service.ContextState(ctx, ownerID, sessionID); err == nil {
			latest = state.Latest
		}
	}
	summarized := summarizedThrough(history, latest)
	indexedByMessage, lastUserMessage := orchestrator.indexHistoryImages(history)
	systemPrompt += orchestrator.summaryPrompt(latest)
	systemPrompt += orchestrator.compactedHandlePrompt(history, indexedByMessage, summarized, lastUserMessage)
	systemPrompt += orchestrator.selectionPrompt(options)
	systemPrompt += orchestrator.notesPrompt(ctx, ownerID, sessionID)
	var choices []Choice
	if orchestrator.service != nil && sessionID != "" {
		choices, _ = orchestrator.service.ListChoices(ctx, ownerID, sessionID)
	}
	systemPrompt += orchestrator.pendingChoicePrompt(choices)
	systemPrompt += orchestrator.harnessPrompt(options.Harness, registry, scope)
	systemPrompt += orchestrator.replyLanguagePrompt(ctx)
	request := agents.Request{
		Model:           orchestrator.turnModel(options),
		Connection:      orchestrator.turnChatModel(options).connection,
		Temperature:     orchestrator.schema.Agent.Temperature,
		ReasoningEffort: options.ReasoningEffort,
		Messages: []agents.Message{{
			Role:    orchestrator.schema.Roles.System,
			Content: systemPrompt,
		}},
	}
	visibleImages := orchestrator.selectVisionImages(
		indexedByMessage,
		options.ReferenceImageIndices,
	)
	// The newest generated pictures (a 3D result's thumbnail included) are shown
	// to the model too, so "the one you just made" can be looked at. They ride
	// with the latest user message: image parts are not valid in assistant turns.
	generatedParts := orchestrator.generatedVisionParts(ctx, ownerID, history, indexedByMessage)
	// 内联失败（超过大小上限、非图片、打开失败）的图不能占住
	// maximum_vision_images 名额：只有真正渲染成图片部分的才计数。
	renderedVisionImages := 0
	visionImageLimit := min(
		orchestrator.schema.Context.MaximumVisionImages,
		orchestrator.schema.Context.RecentImageLimit,
	)
	for messageIndex, message := range history {
		if messageIndex <= summarized || !orchestrator.contextMessage(message) {
			continue
		}
		content := orchestrator.contextText(message)
		images := indexedByMessage[messageIndex]
		var parts []agents.ContentPart
		// Handle labels must reach the model even when the message already has
		// picture parts, because the upstream drops Content beside Parts.
		appendText := func(text string) {
			if text == "" {
				return
			}
			if len(parts) > 0 {
				parts = append(parts, agents.ContentPart{
					Kind: orchestrator.schema.Context.TextPartKind,
					Text: text,
				})
				return
			}
			content += text
		}
		for _, image := range images {
			visionURL := ""
			if visibleImages[image.index] &&
				message.Role == orchestrator.schema.Roles.User {
				visionURL = image.url
				if !visionSafeImage(visionURL) && orchestrator.visionInline != nil {
					inlined, ok := orchestrator.visionInline(
						ctx,
						generation.OwnerID(ownerID),
						image.url,
					)
					if !ok {
						visionURL = ""
					} else {
						visionURL = inlined
					}
				}
			}
			if visionURL != "" && renderedVisionImages >= visionImageLimit {
				visionURL = ""
			}
			if visionURL != "" {
				renderedVisionImages++
				if len(parts) == 0 && content != "" {
					parts = append(parts, agents.ContentPart{
						Kind: orchestrator.schema.Context.TextPartKind,
						Text: content,
					})
					content = ""
				}
				parts = append(parts, agents.ContentPart{
					Kind:   orchestrator.schema.Context.ImagePartKind,
					URL:    visionURL,
					Detail: orchestrator.schema.Context.ImageDetail,
				})
				// The picture itself carries no name; its handle label does.
				if label, structured := orchestrator.attachmentLabel(message, image.slot, image.index); structured {
					parts = append(parts, agents.ContentPart{
						Kind: orchestrator.schema.Context.TextPartKind,
						Text: label,
					})
				}
				continue
			}
			if label, structured := orchestrator.attachmentLabel(message, image.slot, image.index); structured {
				appendText(label)
				continue
			}
			template := orchestrator.schema.Context.AIImageLabelTemplate
			if message.Role == orchestrator.schema.Roles.User {
				template = orchestrator.schema.Context.UserImageLabelTemplate
			}
			content += strings.ReplaceAll(
				template,
				"{index}",
				fmt.Sprint(image.index),
			)
		}
		appendText(orchestrator.unpicturedLabels(message))
		if messageIndex == lastUserMessage && message.Role == orchestrator.schema.Roles.User {
			// A card answered on the card: the Agent reads the exact next call (#321).
			appendText(orchestrator.choiceAnswerText(choices, message.ID))
		}
		if messageIndex == lastUserMessage &&
			message.Role == orchestrator.schema.Roles.User &&
			len(images) > 0 {
			toolName := orchestrator.schema.Agent.MockToolName
			if len(orchestrator.schema.Tools) > 0 {
				toolName = orchestrator.schema.Tools[0].Name
			}
			hint := strings.NewReplacer(
				"{count}", fmt.Sprint(len(images)),
				"{tool}", toolName,
				"{handles}", orchestrator.currentUploadHandles(message, images),
			).Replace(orchestrator.schema.Context.CurrentUploadHint)
			if len(parts) > 0 {
				parts = append(parts, agents.ContentPart{
					Kind: orchestrator.schema.Context.TextPartKind,
					Text: hint,
				})
			} else {
				content += hint
			}
		}
		if messageIndex == lastUserMessage && len(generatedParts) > 0 {
			if len(parts) == 0 && content != "" {
				parts = append(parts, agents.ContentPart{
					Kind: orchestrator.schema.Context.TextPartKind,
					Text: content,
				})
				content = ""
			}
			parts = append(parts, generatedParts...)
		}
		// 历史里的 assistant.tool_calls 折叠为文本摘要：严格的 OpenAI
		// 兼容上游要求 assistant.tool_calls 跟随配对的 tool 消息，而
		// 截断/跳过 tool 角色会造成悬挂调用被整体 400。
		messageOut := agents.Message{
			Role:    message.Role,
			Content: content,
			Parts:   parts,
		}
		if len(message.ToolCalls) > 0 &&
			message.Role == orchestrator.schema.Roles.Assistant {
			names := make([]string, 0, len(message.ToolCalls))
			for _, call := range message.ToolCalls {
				names = append(names, call.Name)
			}
			note := strings.NewReplacer(
				"{tools}", strings.Join(names, ", "),
			).Replace(orchestrator.schema.Context.ToolCallNote)
			switch {
			case len(messageOut.Parts) > 0:
				messageOut.Parts = append(messageOut.Parts, agents.ContentPart{
					Kind: orchestrator.schema.Context.TextPartKind,
					Text: note,
				})
			case messageOut.Content != "":
				messageOut.Content += "\n" + note
			default:
				messageOut.Content = note
			}
		}
		request.Messages = append(request.Messages, messageOut)
	}
	if registryErr == nil {
		request.Tools = registry.describe(scope)
		orchestrator.noteTools(registry, request.Tools, options)
		request.ScopeWithheld = registry.withheld(scope)
	}
	// With compaction on, the request carries the system prompt, the summary
	// and every message after it: compaction (the token threshold, or the
	// message threshold while tokens are unknown) bounds the history, and no
	// message leaves the request without being summarized first (#322, note
	// 98983). With compaction off (or no store to keep a summary in) there is
	// no summary, so the newest chat.context.maximum_messages messages are
	// kept.
	maximumMessages := orchestrator.schema.Context.MaximumMessages
	if !orchestrator.compactionActive() && len(request.Messages) > maximumMessages+1 {
		system := request.Messages[0]
		request.Messages = append(
			[]agents.Message{system},
			request.Messages[len(request.Messages)-maximumMessages:]...,
		)
	}
	return request
}

type indexedChatImage struct {
	index int
	// slot is the 1-based position of the picture in its message.
	slot int
	url  string
	role string
}

func (orchestrator *Orchestrator) selectVisionImages(
	images [][]indexedChatImage,
	selected []int,
) map[int]bool {
	result := map[int]bool{}
	if !orchestrator.schema.Agent.Vision {
		return result
	}
	limit := min(
		orchestrator.schema.Context.MaximumVisionImages,
		orchestrator.schema.Context.RecentImageLimit,
	)
	byIndex := map[int]indexedChatImage{}
	for _, messageImages := range images {
		for _, image := range messageImages {
			byIndex[image.index] = image
		}
	}
	for _, index := range selected {
		image, found := byIndex[index]
		if !found ||
			image.role != orchestrator.schema.Roles.User ||
			!orchestrator.visionCandidate(image.url) {
			continue
		}
		result[index] = true
		if len(result) == limit {
			return result
		}
	}
	if len(selected) > 0 {
		return result
	}
	for messageIndex := len(images) - 1; messageIndex >= 0; messageIndex-- {
		messageImages := images[messageIndex]
		for imageIndex := len(messageImages) - 1; imageIndex >= 0; imageIndex-- {
			image := messageImages[imageIndex]
			if image.role != orchestrator.schema.Roles.User ||
				!orchestrator.visionCandidate(image.url) {
				continue
			}
			result[image.index] = true
			if len(result) == limit {
				return result
			}
		}
	}
	return result
}

func (orchestrator *Orchestrator) contextText(message Message) string {
	content := message.Content
	for _, call := range message.ToolCalls {
		if call.Name == "" || len(call.Arguments) == 0 {
			continue
		}
		var arguments map[string]json.RawMessage
		if json.Unmarshal(call.Arguments, &arguments) != nil {
			continue
		}
		var prompt string
		_ = json.Unmarshal(
			arguments[orchestrator.schema.Context.ToolPromptArgument],
			&prompt,
		)
		prompt = truncateRunes(
			prompt,
			orchestrator.schema.Context.ToolPromptPreviewChars,
		)
		if prompt != "" {
			content += strings.ReplaceAll(
				orchestrator.schema.Context.PreviousPromptTemplate,
				"{prompt}",
				prompt,
			)
		}
	}
	return content
}

func visionSafeImage(value string) bool {
	return strings.HasPrefix(strings.ToLower(value), "data:image/")
}

// visionCandidate 判断图片能否进入视觉上下文：内联 data URI 直接可用；
// 其他 URL 只有在配置了 VisionInline（可把用户拥有的资产转成 data URI）
// 时才候选，最终是否可见由 agentRequest 的内联结果决定。
func (orchestrator *Orchestrator) visionCandidate(value string) bool {
	return visionSafeImage(value) || orchestrator.visionInline != nil
}

// pendingCallsText lists, with Schema texts, the tool calls the model still
// wanted once the tool rounds ran out (#275): one line per distinct call with
// the tool name and its prompt argument as the intent, never the raw markup.
func (orchestrator *Orchestrator) pendingCallsText(calls []agents.ToolCall) string {
	messages := orchestrator.schema.Presentation.Messages
	maximum := orchestrator.schema.Context.ToolPromptPreviewChars
	lines := make([]string, 0, len(calls))
	seen := make(map[string]struct{}, len(calls))
	for _, call := range calls {
		name := truncateRunes(strings.TrimSpace(call.Name), maximum)
		if name == "" {
			continue
		}
		var arguments map[string]json.RawMessage
		_ = json.Unmarshal(call.Arguments, &arguments)
		var intent string
		_ = json.Unmarshal(
			arguments[orchestrator.schema.Context.ToolPromptArgument],
			&intent,
		)
		intent = strings.TrimSpace(
			truncateRunes(strings.Join(strings.Fields(intent), " "), maximum),
		)
		line := strings.ReplaceAll(messages.ToolRoundLimitPendingTool, "{tool}", name)
		if intent != "" {
			line = strings.NewReplacer("{tool}", name, "{intent}", intent).
				Replace(messages.ToolRoundLimitPendingItem)
		}
		if _, duplicate := seen[line]; duplicate {
			continue
		}
		seen[line] = struct{}{}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	return messages.ToolRoundLimitPending + "\n" + strings.Join(lines, "\n")
}

func truncateRunes(value string, maximum int) string {
	if maximum <= 0 || utf8.RuneCountInString(value) <= maximum {
		return value
	}
	return string([]rune(value)[:maximum])
}

// thinkingRound shows the thinking status of one model round (#285). The
// status is announced when the round starts, so every model shows it until its
// first visible text whether or not it reports reasoning, and again when the
// adapter reports reasoning after visible text of the same round. Visible text
// ends it. The event carries only the Schema status text: reasoning never
// reaches the stream, the stored message or the log.
type thinkingRound struct {
	orchestrator *Orchestrator
	ctx          context.Context
	sessionID    string
	active       bool
}

func (orchestrator *Orchestrator) startThinkingRound(
	ctx context.Context,
	sessionID string,
) *thinkingRound {
	round := &thinkingRound{orchestrator: orchestrator, ctx: ctx, sessionID: sessionID}
	round.announce()
	return round
}

// announce appends the thinking event once per stretch without visible text.
// It is best effort: a status the client misses must not fail the turn.
func (round *thinkingRound) announce() {
	schema := round.orchestrator.schema
	if round.active || schema.Stream.EventTypes.Thinking == "" ||
		schema.Presentation.Status.Thinking == "" {
		return
	}
	round.active = true
	err := round.orchestrator.appendEvent(round.ctx, round.sessionID, map[string]any{
		schema.Stream.EventFields.Type:    schema.Stream.EventTypes.Thinking,
		schema.Stream.EventFields.Content: round.orchestrator.text(round.ctx, schema.Presentation.Status.Thinking),
	})
	if err != nil && round.ctx.Err() == nil {
		round.orchestrator.logger.Warn(
			"对话思考状态事件写入失败",
			"session_id", round.sessionID,
			"error", err,
		)
	}
}

// receive handles one adapter delta: a reasoning signal (re)announces the
// status, content goes through the round's DSML filter to emitText.
func (round *thinkingRound) receive(delta agents.Delta, filter *dsml.Filter) error {
	if delta.Thinking {
		round.announce()
	}
	if delta.Content == "" {
		return nil
	}
	return filter.Push(delta.Content, round.emitText)
}

// emitText appends visible text; text that is not blank ends the status.
func (round *thinkingRound) emitText(content string) error {
	if strings.TrimSpace(content) != "" {
		round.active = false
	}
	return round.orchestrator.appendTextEvent(round.ctx, round.sessionID, content)
}

func (orchestrator *Orchestrator) appendTextEvent(
	ctx context.Context,
	sessionID string,
	content string,
) error {
	return orchestrator.appendEvent(ctx, sessionID, map[string]any{
		orchestrator.schema.Stream.EventFields.Type: orchestrator.
			schema.Stream.EventTypes.Text,
		orchestrator.schema.Stream.EventFields.Content: content,
	})
}

func (orchestrator *Orchestrator) appendEvent(
	ctx context.Context,
	sessionID string,
	data map[string]any,
) error {
	_, err := orchestrator.service.AppendEvent(ctx, Event{
		SessionID: sessionID,
		Data:      data,
	})
	return err
}

func (orchestrator *Orchestrator) fail(
	ctx context.Context,
	ownerID string,
	assistant *Message,
	detail string,
) error {
	assistant.Content = detail
	if assistant.Content == "" {
		assistant.Content = orchestrator.text(ctx, orchestrator.schema.Presentation.Messages.UnknownError)
	}
	assistant.Content = orchestrator.withErrorReference(ctx, assistant.Content, assistant.ID)
	_ = orchestrator.appendEvent(
		context.WithoutCancel(ctx),
		assistant.SessionID,
		map[string]any{
			orchestrator.schema.Stream.EventFields.Type: orchestrator.
				schema.Stream.EventTypes.Error,
			orchestrator.schema.Stream.EventFields.Content: assistant.Content,
		},
	)
	if err := orchestrator.service.UpdateMessage(
		context.WithoutCancel(ctx),
		ownerID,
		*assistant,
	); err != nil {
		return err
	}
	_ = orchestrator.appendEvent(
		context.WithoutCancel(ctx),
		assistant.SessionID,
		map[string]any{
			orchestrator.schema.Stream.EventFields.Type: orchestrator.
				schema.Stream.EventTypes.Done,
		},
	)
	return nil
}

// persistToolCallBinding 尽力把工具调用的任务绑定即时写入 assistant
// 消息；取消轮次依赖这份持久化绑定发现在飞任务。失败只记日志。
func (orchestrator *Orchestrator) persistToolCallBinding(
	ctx context.Context,
	command ProcessTurn,
	callID string,
	observation ToolObservation,
) {
	taskID := observation.TaskID
	// 轮次被取消后回调仍须完成落库：CancelTurn 依赖这份持久化绑定
	// 发现在飞任务，因此读取与写回都不随 processCtx 取消而失败。
	ctx = context.WithoutCancel(ctx)
	messages, err := orchestrator.service.ListMessages(
		ctx,
		command.OwnerID,
		command.SessionID,
	)
	if err != nil {
		orchestrator.logger.Warn(
			"持久化任务绑定前读取消息失败",
			"session_id", command.SessionID,
			"error", err,
		)
		return
	}
	for index := range messages {
		if messages[index].ID != command.AssistantMessageID {
			continue
		}
		changed := false
		for callIndex := range messages[index].ToolCalls {
			call := &messages[index].ToolCalls[callIndex]
			if call.ID == callID && call.TaskID == "" {
				call.TaskID = taskID
				call.EstimatedCredits = observation.EstimatedCredits
				call.NativeQuote = observation.NativeQuote
				call.ApprovalSource = observation.ApprovalSource
				changed = true
			}
		}
		if changed {
			if err := orchestrator.service.UpdateMessage(
				context.WithoutCancel(ctx),
				command.OwnerID,
				messages[index],
			); err != nil {
				orchestrator.logger.Warn(
					"持久化任务绑定失败",
					"session_id", command.SessionID,
					"task_id", taskID,
					"error", err,
				)
			}
		}
		return
	}
}

func (orchestrator *Orchestrator) executeTools(
	ctx context.Context,
	command ProcessTurn,
	history []Message,
	calls []agents.ToolCall,
	boundTasks map[string]string,
	storedCalls []ToolCall,
	turn *Turn,
) ([]agents.Message, []string, map[string]string, error) {
	registry, registryErr := orchestrator.registryFor(ctx)
	if registryErr != nil {
		return nil, nil, nil, registryErr
	}
	results := append([]agents.Message(nil), turn.Checkpoint.Completed...)
	imageURLs := append([]string(nil), turn.Checkpoint.ImageURLs...)
	taskBindings := map[string]string{}
	var combined error
	if turn.Checkpoint.HadToolError {
		combined = errors.New("a completed tool failed")
	}
	if len(results) > len(calls) {
		return nil, nil, nil, ErrTurnCheckpointConflict
	}
	persist := func(state string) error {
		version, err := orchestrator.service.SaveTurnCheckpoint(context.WithoutCancel(ctx), *turn,
			state, turn.Checkpoint, false)
		if err != nil {
			return errors.Join(ErrTurnCheckpointConflict, err)
		}
		turn.CheckpointVersion, turn.State = version, state
		return nil
	}
	for callIndex := len(results); callIndex < len(calls); callIndex++ {
		call := calls[callIndex]
		frozenKey := ""
		for _, stored := range storedCalls {
			if stored.ID == call.ID {
				frozenKey = stored.IdempotencyKey
				break
			}
		}
		boundTaskID := boundTasks[call.ID]
		waitDeadline := time.Time{}
		if turn.Checkpoint.WaitingCallID == call.ID {
			if boundTaskID == "" {
				boundTaskID = turn.Checkpoint.WaitingTaskID
			}
			waitDeadline = turn.Checkpoint.WaitDeadline
		}
		priorTaskCalls := map[string]struct{}{}
		for id := range boundTasks {
			if id != call.ID {
				priorTaskCalls[id] = struct{}{}
			}
		}
		for id := range taskBindings {
			if id != call.ID {
				priorTaskCalls[id] = struct{}{}
			}
		}
		tool, found := registry.resolve(call.Name, boundTaskID != "")
		if !found && boundTaskID == "" {
			// Current catalog availability cannot hide an already accepted Task.
			// The optional lookup verifies the immutable call and its bound grant;
			// a read failure leaves the call unfinished for durable retry.
			if reader, ok := orchestrator.tools.(interface {
				BoundTaskForCall(context.Context, ToolCommand) (string, error)
			}); ok {
				candidate, readErr := reader.BoundTaskForCall(ctx, ToolCommand{
					OwnerID: command.OwnerID, SessionID: command.SessionID,
					AssistantMessageID: command.AssistantMessageID, Call: call,
					FrozenIdempotencyKey: frozenKey, Options: command.Options,
				})
				if readErr != nil {
					if errors.Is(readErr, ErrTurnCheckpointConflict) {
						return results, imageURLs, taskBindings, readErr
					}
					return results, imageURLs, taskBindings, ErrTurnSuspended
				}
				if candidate != "" {
					boundTaskID = candidate
					tool, found = registry.resolve(call.Name, true)
				}
			}
		}
		if found && orchestrator.scopeRefuses(registry, call.Name, command.Options.AgentScope, boundTaskID != "") {
			// Defense in depth for the tool list: a model that names a tool it
			// was not offered gets an explanation, not an execution. This is a
			// normal result, not a failed tool, so the turn goes on and the
			// Agent tells the user to switch the mode.
			orchestrator.logger.Info("工具调用被 Agent 范围拒绝",
				"session_id", command.SessionID, "tool", call.Name, "agent_scope", command.Options.AgentScope)
			results = append(results, agents.Message{
				Role:       orchestrator.schema.Roles.Tool,
				Content:    orchestrator.scopeRejection(call.Name, command.Options.AgentScope),
				ToolCallID: call.ID,
			})
			turn.Checkpoint.Completed = append(turn.Checkpoint.Completed, results[len(results)-1])
			if err := persist(orchestrator.service.TurnRunningState()); err != nil {
				return results, imageURLs, taskBindings, err
			}
			continue
		}
		if !found {
			combined = errors.Join(
				combined,
				fmt.Errorf("chat orchestrator: tool %q unavailable", call.Name),
			)
			// Strict tool-call protocols require a result for every call, even
			// when the name was unknown or not allowed in this domain.
			results = append(results, agents.Message{
				Role:       orchestrator.schema.Roles.Tool,
				Content:    fmt.Sprintf("tool %q is not available", call.Name),
				ToolCallID: call.ID,
			})
			turn.Checkpoint.Completed = append(turn.Checkpoint.Completed, results[len(results)-1])
			if err := persist(orchestrator.service.TurnRunningState()); err != nil {
				return results, imageURLs, taskBindings, err
			}
			continue
		}
		result, err := orchestrator.tools.Execute(
			ctx,
			ToolCommand{
				Configuration:        turn.Checkpoint.Configuration,
				OwnerID:              command.OwnerID,
				SessionID:            command.SessionID,
				AssistantMessageID:   command.AssistantMessageID,
				BoundTaskID:          boundTaskID,
				FrozenIdempotencyKey: frozenKey,
				TaskWaitDeadline:     waitDeadline,
				PriorGenerationCalls: len(priorTaskCalls),
				ConsentWaitDeadline:  command.ConsentWaitDeadline,
				Call:                 call,
				Tool:                 tool,
				History:              history,
				Options:              command.Options,
				EarlierCalls:         earlierCalls(storedCalls, call.ID),
			},
			func(observation ToolObservation) error {
				if observation.TaskID != "" &&
					taskBindings[call.ID] == "" {
					// 任务提交即持久化绑定：用户停止轮次时 CancelTurn
					// 要能在活动执行中发现在飞任务并取消上游计费。
					taskBindings[call.ID] = observation.TaskID
					orchestrator.persistToolCallBinding(
						ctx,
						command,
						call.ID,
						observation,
					)
				}
				return orchestrator.projectToolObservation(
					ctx,
					command.SessionID,
					observation,
				)
			},
		)
		if err != nil && !result.NoSubmission && result.TaskID == "" && paidTool(tool.Execution) {
			// Task acceptance may have committed while the response or chat
			// binding was lost. Keep this call outside Checkpoint.Completed so
			// restart can read its durable grant/idempotency binding and observe
			// the original paid Task. Never plan a fresh paid call here.
			return results, imageURLs, taskBindings, ErrTurnSuspended
		}
		if err != nil && result.AwaitTask && result.TaskID != "" {
			// The Task identity is known, but observation or chat binding failed.
			// Preserve the original Task for the waiting scanner to retry.
			err = nil
		}
		combined = errors.Join(combined, err)
		if err != nil {
			turn.Checkpoint.HadToolError = true
		}
		if result.Suspend {
			// AGT-12：等待用户决定的工具已登记状态；挂起返回并保留
			// 已完成部分的结果，未执行的调用在唤醒重放时重新执行。
			return results, imageURLs, taskBindings, ErrTurnSuspended
		}
		if result.AwaitTask {
			if result.TaskID == "" {
				return results, imageURLs, taskBindings, ErrTurnCheckpointConflict
			}
			turn.Checkpoint.WaitingTaskID = result.TaskID
			taskBindings[call.ID] = result.TaskID
			turn.Checkpoint.WaitingCallID = call.ID
			if result.WaitUntil.IsZero() {
				// Older executors may fail observation without returning a deadline.
				// Preserve this call's durable deadline; bound a first read failure
				// with the Schema request timeout rather than waiting forever.
				result.WaitUntil = waitDeadline
				if result.WaitUntil.IsZero() {
					result.WaitUntil = time.Now().Add(time.Duration(orchestrator.schema.Agent.RequestTimeoutSeconds) * time.Second)
				}
			}
			turn.Checkpoint.WaitDeadline = result.WaitUntil
			if err := persist(orchestrator.service.TurnWaitingTaskState()); err != nil {
				return results, imageURLs, taskBindings, err
			}
			return results, imageURLs, taskBindings, ErrTurnAwaitTask
		}
		imageURLs = append(imageURLs, result.ImageURLs...)
		// 观察失败时 result.TaskID 可能为空：不要清空回调里已持久化
		// 的绑定，否则停止轮次时找不到在飞任务（计费止损依赖它）。
		if result.TaskID != "" {
			taskBindings[call.ID] = result.TaskID
		}
		// 控制工具错误只填 Error 字段；tool 消息内容为空时回填错误，
		// 否则 LLM 看到空回执无法纠正参数（B16）。
		content := result.AgentContent
		if content == "" {
			content = result.Error
		}
		reply := agents.Message{
			Role:       orchestrator.schema.Roles.Tool,
			Content:    content,
			ToolCallID: call.ID,
		}
		orchestrator.attachToolPictures(ctx, command.OwnerID, &reply, result.Pictures)
		results = append(results, reply)
		turn.Checkpoint.Completed = append(turn.Checkpoint.Completed, results[len(results)-1])
		turn.Checkpoint.ImageURLs = append(turn.Checkpoint.ImageURLs, result.ImageURLs...)
		turn.Checkpoint.WaitingTaskID, turn.Checkpoint.WaitingCallID = "", ""
		turn.Checkpoint.WaitDeadline = time.Time{}
		if err := persist(orchestrator.service.TurnRunningState()); err != nil {
			return results, imageURLs, taskBindings, err
		}
	}
	return results, imageURLs, taskBindings, combined
}

func (orchestrator *Orchestrator) projectToolObservation(
	ctx context.Context,
	sessionID string,
	observation ToolObservation,
) error {
	fields := orchestrator.schema.Stream.EventFields
	types := orchestrator.schema.Stream.EventTypes
	if observation.Registered {
		event := map[string]any{
			fields.Type:       types.TaskRegistered,
			fields.TaskID:     observation.TaskID,
			fields.Capability: observation.Capability,
			fields.Prompt:     observation.Prompt,
			fields.AIModel:    observation.AIModel,
			fields.SessionID:  sessionID,
		}
		if observation.NativeQuote != nil {
			event["native_quote"] = observation.NativeQuote
		}
		if observation.NativeQuote == nil && observation.EstimatedCredits > 0 && fields.EstimatedCredits != "" {
			event[fields.EstimatedCredits] = observation.EstimatedCredits
		}
		if observation.ApprovalSource != "" && fields.ApprovalSource != "" {
			event[fields.ApprovalSource] = observation.ApprovalSource
		}
		return orchestrator.appendEvent(ctx, sessionID, event)
	}
	if len(observation.ImageURLs) > 0 {
		if err := orchestrator.appendEvent(
			ctx,
			sessionID,
			map[string]any{
				fields.Type:   types.Images,
				fields.TaskID: observation.TaskID,
				fields.URLs:   observation.ImageURLs,
			},
		); err != nil {
			return err
		}
	}
	if observation.Progress > 0 {
		if err := orchestrator.appendEvent(
			ctx,
			sessionID,
			map[string]any{
				fields.Type:    types.Progress,
				fields.Percent: observation.Progress,
			},
		); err != nil {
			return err
		}
	}
	update := map[string]any{
		fields.Type:   types.TaskUpdate,
		fields.TaskID: observation.TaskID,
	}
	if observation.FinalTaskID != "" {
		update[fields.FinalTaskID] = observation.FinalTaskID
	}
	if observation.Status != "" {
		update[fields.Status] = observation.Status
	}
	if observation.Progress > 0 {
		update[fields.Progress] = observation.Progress
	}
	if len(observation.ImageURLs) > 0 {
		update[fields.Result] = map[string]any{
			fields.ImageURLs: observation.ImageURLs,
		}
	}
	if observation.Error != "" {
		update[fields.Error] = observation.Error
	}
	return orchestrator.appendEvent(ctx, sessionID, update)
}

func (orchestrator *Orchestrator) followUpRequest(
	base agents.Request,
	completion agents.Completion,
	toolResults []agents.Message,
) agents.Request {
	// 克隆消息序列：续跑请求必须累积此前各轮的调用与回执（模型才能
	// 看到完整上下文），显式拷贝避免 append 复用底层数组造成的别名。
	messages := make(
		[]agents.Message,
		0,
		len(base.Messages)+1+len(toolResults),
	)
	messages = append(messages, base.Messages...)
	messages = append(messages, agents.Message{
		Role:             orchestrator.schema.Roles.Assistant,
		Content:          completion.Content,
		ToolCalls:        completion.ToolCalls,
		ReasoningContent: completion.ReasoningContent,
	})
	messages = append(messages, orchestrator.withToolPictures(toolResults)...)
	base.Messages = messages
	// B4 有界工具循环：续跑请求保留工具定义，模型可在同一轮内
	//（例如同意获批后）继续调用生成工具；调用次数上限由
	// chat.agent.max_tool_calls_per_reply 约束（#275）。
	return base
}

func agentCallsToMessages(calls []agents.ToolCall) []ToolCall {
	result := make([]ToolCall, 0, len(calls))
	for _, call := range calls {
		result = append(result, ToolCall{
			ID:        call.ID,
			Name:      call.Name,
			Arguments: append([]byte(nil), call.Arguments...),
		})
	}
	return result
}

func (orchestrator *Orchestrator) freezeToolCallKeys(sessionID string, calls, storedCalls []ToolCall) {
	for index := range calls {
		call := &calls[index]
		if call.IdempotencyKey != "" {
			continue
		}
		oldCall := false
		for _, stored := range storedCalls {
			if stored.ID == call.ID {
				oldCall = true
				break
			}
		}
		if oldCall {
			// Never infer a historical key from today's template. An old
			// call without a frozen key remains explicitly unreconciled.
			continue
		}
		tool, found := orchestrator.registry.byName[call.Name]
		if !found || tool.generation == nil || tool.generation.IdempotencyKeyTemplate == "" {
			continue
		}
		call.IdempotencyKey = strings.NewReplacer(
			"{session_id}", sessionID,
			"{call_id}", call.ID,
		).Replace(tool.generation.IdempotencyKeyTemplate)
	}
}

// perTurnToolCallIDs 把新解析出的工具调用 ID 改写为按 assistant 消息
// 与轮次唯一 的稳定形式。DSML 模板与部分网关给出的调用 ID 只在单轮内
// 唯一甚至为空，而幂等键模板按会话+调用 ID 组键，直接使用会导致同会话
// 第二轮生成撞幂等冲突或静默回放旧任务。恢复路径复用已落库的 ID，
// 不受影响。多工具轮（B4 有界循环）以 round 区分不同执行轮次的调用。
func perTurnToolCallIDs(
	assistantMessageID string,
	calls []agents.ToolCall,
	round int,
) []agents.ToolCall {
	for index := range calls {
		if round == 0 {
			calls[index].ID = fmt.Sprintf(
				"%s_tool_%d",
				assistantMessageID,
				index,
			)
			continue
		}
		calls[index].ID = fmt.Sprintf(
			"%s_r%d_tool_%d",
			assistantMessageID,
			round,
			index,
		)
	}
	return calls
}

// maxRoundFromCalls 从已落库的工具调用 ID（{msg} 或 {msg}_r{round}_tool_{n}）
// 解析出现过的最大轮次；无轮次段或解析失败按 0 处理。
func maxRoundFromCalls(calls []ToolCall) int {
	maxRound := 0
	for _, call := range calls {
		index := strings.LastIndex(call.ID, "_r")
		if index < 0 {
			continue
		}
		rest := call.ID[index+2:]
		end := strings.Index(rest, "_tool_")
		if end <= 0 {
			continue
		}
		if round, err := strconv.Atoi(rest[:end]); err == nil && round > maxRound {
			maxRound = round
		}
	}
	return maxRound
}

func messageCallsToAgent(calls []ToolCall) []agents.ToolCall {
	result := make([]agents.ToolCall, 0, len(calls))
	for _, call := range calls {
		result = append(result, agents.ToolCall{
			ID:        call.ID,
			Name:      call.Name,
			Arguments: append(json.RawMessage(nil), call.Arguments...),
		})
	}
	return result
}

// mergeDurableToolCalls reads the persisted assistant message and merges the
// estimated credits and approval source of each accepted paid step into the
// in-memory calls. A failed
// read is logged and leaves the in-memory calls as they are.
func (orchestrator *Orchestrator) mergeDurableToolCalls(
	ctx context.Context,
	command ProcessTurn,
	assistant *Message,
) {
	messages, err := orchestrator.service.ListMessages(
		context.WithoutCancel(ctx), command.OwnerID, command.SessionID)
	if err != nil {
		orchestrator.logger.Warn(
			"合并已持久化的付费步骤绑定前读取消息失败",
			"session_id", command.SessionID,
			"error", err,
		)
		return
	}
	for _, stored := range messages {
		if stored.ID != command.AssistantMessageID {
			continue
		}
		for index := range assistant.ToolCalls {
			for _, call := range stored.ToolCalls {
				if assistant.ToolCalls[index].ID != call.ID {
					continue
				}
				// Only the step info: the task ID of the final result still
				// comes from the tool result, as before.
				if assistant.ToolCalls[index].EstimatedCredits == 0 {
					assistant.ToolCalls[index].EstimatedCredits = call.EstimatedCredits
					assistant.ToolCalls[index].NativeQuote = call.NativeQuote
				}
				if assistant.ToolCalls[index].ApprovalSource == "" {
					assistant.ToolCalls[index].ApprovalSource = call.ApprovalSource
				}
			}
		}
		return
	}
}

// mergeStoredToolCall copies the durable binding of an accepted paid step from
// the persisted call into an in-memory one without overwriting a value the
// in-memory call already holds.
func mergeStoredToolCall(target *ToolCall, stored ToolCall) {
	if target.TaskID == "" {
		target.TaskID = stored.TaskID
	}
	if target.IdempotencyKey == "" {
		target.IdempotencyKey = stored.IdempotencyKey
	}
	if target.EstimatedCredits == 0 {
		target.EstimatedCredits = stored.EstimatedCredits
		target.NativeQuote = stored.NativeQuote
	}
	if target.ApprovalSource == "" {
		target.ApprovalSource = stored.ApprovalSource
	}
}

// withErrorReference appends the Schema's error reference with the assistant
// message ID; the failure log line carries the same assistant_message_id.
func (orchestrator *Orchestrator) withErrorReference(ctx context.Context, text, messageID string) string {
	template := orchestrator.text(ctx, orchestrator.schema.Presentation.Messages.ErrorReference)
	if template == "" || messageID == "" {
		return text
	}
	return text + strings.ReplaceAll(template, "{id}", messageID)
}

// agentFailureMessage is what the person reads when the Agent request failed:
// a deployment without Agent settings says the Agent is not configured, a
// refusal of a request with pictures says the model may not read images
// (#253); anything else stays the unknown error, whose ID is in the log.
func (orchestrator *Orchestrator) agentFailureMessage(ctx context.Context, err error) string {
	messages := orchestrator.schema.Presentation.Messages
	if errors.Is(err, agents.ErrNotConfigured) && messages.AgentNotConfigured != "" {
		return orchestrator.text(ctx, messages.AgentNotConfigured)
	}
	if errors.Is(err, agents.ErrImagesRejected) && messages.AgentImagesUnsupported != "" {
		return orchestrator.text(ctx, messages.AgentImagesUnsupported)
	}
	return orchestrator.text(ctx, messages.UnknownError)
}

// text is a Schema text in the UI locale of the turn or request in ctx.
func (orchestrator *Orchestrator) text(ctx context.Context, text string) string {
	return orchestrator.texts.TextIn(ctx, text)
}

// replyLanguagePrompt asks the Agent to answer in the turn's UI locale. The
// default locale needs no instruction: the prompts are written in it.
func (orchestrator *Orchestrator) replyLanguagePrompt(ctx context.Context) string {
	locale := catalog.LocaleFrom(ctx)
	template := orchestrator.schema.Agent.ReplyLanguageTemplate
	if template == "" || !orchestrator.texts.Translates(locale) {
		return ""
	}
	language := orchestrator.texts.LocaleLabel(locale)
	return "\n\n" + strings.ReplaceAll(orchestrator.text(ctx, template), "{language}", language)
}
