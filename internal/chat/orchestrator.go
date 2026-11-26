package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/inlinepictures"
	"github.com/LeXwDeX/Plinth/internal/agentscope"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat/dsml"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/moderation"
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
	// PriorGenerationCalls is the turn's max_generation_calls_per_turn count
	// before this call, rebuilt from the units the turn checkpoint keeps
	// (TurnCheckpoint.GenerationUnits, #665).
	PriorGenerationCalls int
	// ConsentWaitDeadline 非零表示本轮已因等待同意而挂起过：工具在
	// 决定未出且已过该时刻时应返回等待文案，而不是再次登记挂起。
	ConsentWaitDeadline time.Time
	Call                agents.ToolCall
	Tool                catalog.ChatToolSchema
	History             []Message
	// Turn holds what this Agent reply produced before this call: its
	// pictures, its tool calls with their bound Tasks and the attachments
	// (with handles) already given to them (#25). A later step of the same
	// reply names those results like any history object.
	Turn    Message
	Options TurnOptions
	// EarlierCalls are the tool calls this Agent reply ran before this one, in
	// order (a resumed reply included). A tool with a per-reply bound replays
	// them instead of keeping state of its own.
	EarlierCalls []ToolCall
	// ToolAvailable reports whether a named tool is declared and available in
	// this turn's Agent scope (#103). ask_choice uses it to refuse a card whose
	// answer would call a tool the turn cannot run. Nil means no check.
	ToolAvailable func(name string) bool
	// ChoiceScope is set when this turn answers a direction card on the card
	// (#549): a paid step must be the chosen option's step. The generation
	// tools check the capability the call resolves to against it. Nil means
	// the turn is not scoped by a card.
	ChoiceScope *ChoiceScope
	// ExplicitParameters preserves caller inputs before legacy defaults merge.
	// Non-nil enables offering overrides without changing the command hash.
	ExplicitParameters map[string]json.RawMessage `json:"-"`
}

// ReferenceHistory is the history a reference is looked up in: the messages
// before this reply and, when it produced anything yet, this reply's results.
// Reading "the person's latest message" still uses History, which ends with it.
func (command ToolCommand) ReferenceHistory() []Message {
	if len(command.Turn.ImageURLs) == 0 && len(command.Turn.Attachments) == 0 && len(command.Turn.ToolCalls) == 0 {
		return command.History
	}
	return append(append(make([]Message, 0, len(command.History)+1), command.History...), command.Turn)
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
	// RetryAfter, with NoSubmission, says the generation kernel refused the
	// submission for lack of admission room and when to try again. The
	// result still carries the failure receipt: the orchestrator queues the
	// call for a retry while the Schema request timeout allows, and hands
	// that receipt to the model only once it ran out.
	RetryAfter time.Duration `json:"-"`
	// GenerationUnits is what this call added to the turn's
	// max_generation_calls_per_turn count (a processing plan its paid steps,
	// an applied insert or retry one, a generation that proceeded to submit
	// one; 0 when it was not counted). The orchestrator keeps it on the turn
	// checkpoint so a later call after a restart starts from the same count
	// (#665).
	GenerationUnits int `json:"-"`
	// Suspend 表示该工具已登记等待用户输入（AGT-12）：调用方应中断
	// 本批剩余工具并向编排器返回挂起信号，而不是在 worker 内阻塞。
	Suspend   bool
	AwaitTask bool
	WaitUntil time.Time
	// Pictures are session pictures a tool asks to show the Agent (#279): a
	// read-only lookup, or a generation result the Agent should inspect in the
	// same turn (#245). The orchestrator reads each for the turn's owner and,
	// when the Agent reads images, shows it after the tool results; a picture it
	// cannot read is named as unreadable instead.
	Pictures []ToolPicture `json:"-"`
	// Summary is a compact result the frontend can show as one line (#103).
	// It is persisted with the tool call and never sent to the Agent.
	Summary json.RawMessage `json:"-"`
	// PresentedEntries, with ImageURLs, names the array of the JSON result
	// whose entries describe those pictures one to one and in order (#552).
	// Once every picture of the call is a handled attachment of the reply,
	// each entry gets its own handle.
	PresentedEntries string `json:"-"`
	// UnpresentedContent, with ImageURLs, is the result the Agent reads
	// instead of AgentContent when not every picture of the call became a
	// handled attachment of the reply (#552): a tool whose work only counts
	// once the person sees it never reports success for a picture the person
	// cannot see or name.
	UnpresentedContent string `json:"-"`
}

// ToolPicture is one picture a tool shows the Agent, by handle and platform URL.
// Fresh marks a picture this turn's generation just produced (#245): it is
// framed as a result to inspect, not as a historical lookup.
//
// Purpose is the vision role the picture plays (#247): "result" and "render"
// are this turn's work, "history" a lookup, "crop" an inspect_image view. It
// selects the role's place quota and detail. Detail overrides the role's
// detail when set (a crop carries the crop policy's), and Label is a complete
// label for a view the default templates cannot name (a crop region).
type ToolPicture struct {
	Handle  string
	URL     string
	Fresh   bool
	Purpose string
	Detail  string
	Label   string
}

// Tool picture purposes (#247).
const (
	ToolPictureResult  = "result"
	ToolPictureRender  = "render"
	ToolPictureHistory = "history"
	ToolPictureCrop    = "crop"
)

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

// ErrTurnCheckpointUnsaved 表示检查点没有写进存储，但原因不是另一个写入方
// 赢得了比较交换（#429）：编码失败、存储拒绝或锁冲突都算。进程内没有
// 任何东西会重试这次写入，所以轮次以 failed 收尾，而不是挂起等重启。
// 真正的版本冲突仍是 ErrTurnCheckpointConflict，轮次留给那个写入方。
var ErrTurnCheckpointUnsaved = errors.New("chat orchestrator: turn checkpoint not saved")

// ErrTurnSubmissionUnconfirmed 表示付费调用的提交结果一直无法确认（#516）：
// 自第一次因此挂起起已超过 Schema 的 Agent 请求超时，回合不再挂起重放，
// 以 failed 收尾。可能已受理的任务仍归生成内核，不取消、不重新提交。
var ErrTurnSubmissionUnconfirmed = errors.New("chat orchestrator: paid submission unconfirmed past the request timeout")

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
	// Project is application.project (#240): the project block and citation
	// template. Nil keeps the block out (a Catalog without projects).
	Project *catalog.ProjectSchema
	Model   string
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
	// VisionAssetInline 把风格锁的资产 ID 转成 data URI（#247）；nil 时风格
	// 锁参考图只保留文字说明，不占视觉预算。
	VisionAssetInline func(
		ctx context.Context,
		owner generation.OwnerID,
		assetID string,
	) (string, bool)
	// VisionAnchorInline 把视觉锚的参考图转成 data URI（#250）。锚点参考图
	// 可能是 input asset id、input asset URL 或生成结果的规范 Task 资产
	// URL，校验按 owner 与会话进行；nil 时锚点图片只保留文字标签，并在溢出
	// 说明里点名。
	VisionAnchorInline func(
		ctx context.Context,
		owner generation.OwnerID,
		sessionID string,
		reference string,
	) (string, bool)
	// SelectionPreview composes the server-side preview of the turn's frozen
	// selection mask (#244): the artboard snapshot dimmed outside the selected
	// region and tinted inside it, as a data URI. It reports false when the
	// pictures cannot be read; the turn keeps the mask's text description.
	SelectionPreview func(
		ctx context.Context,
		ownerID string,
		pictureURL string,
		maskAssetID string,
	) (string, bool)
	// Pictures encodes every picture the Agent may see into the format
	// chat.agent.request_pictures declares before the vision budget counts
	// its bytes (#555); nil keeps the pictures as they were inlined.
	Pictures *inlinepictures.Encoder
	// TaskCanceler 供停止轮次时取消该轮已绑定的生成任务（尽力而为，
	// 失败只记录日志，不阻塞停止语义）。
	TaskCanceler func(ctx context.Context, ownerID, taskID string) error
	// ToolNote is what a turn adds to a generation tool's description, from
	// the turn's options (#329 E': how many reference pictures the picked model
	// uses). Nil, or "", leaves the declared description as it is.
	ToolNote func(tool catalog.ChatToolSchema, options TurnOptions) string
	// SelfCheckTasks lists the session's finished 3D results for the
	// self-check prompt (#246). Nil keeps the block out.
	SelfCheckTasks func(context.Context, string, string) ([]SelfCheckTask, error)
	// Capabilities are application.capabilities (#330): the names a self-check
	// remedy uses for the fix's capabilities. Nil falls back to the ids.
	Capabilities []catalog.CapabilityPresentation
	// TaskLookup and TaskGet read kernel facts without current Catalog checks.
	// They let a stopped turn find paid submissions whose response or binding
	// was lost before the assistant message could be updated.
	TaskLookup func(ctx context.Context, ownerID, idempotencyKey string) (generation.Task, error)
	TaskGet    func(ctx context.Context, ownerID, taskID string) (generation.Task, error)
	// ToolAllowed 声明 capability 是否属于当前会话领域（B14：2D 会话
	// 不收 3D 工具）。nil 时不过滤（测试）。
	ToolAllowed func(capability, domain string) bool
	// OfferingAllowed reports whether an enabled, available Offering with the
	// model_id serves the capability; it narrows offering_argument choices
	// (#189). Nil offers the declared choices (tests).
	OfferingAllowed func(capability, modelID string) bool
	// Offerings lists the runtime Offerings of a capability in Catalog order.
	// With a tool's model_choices it derives the model enum from the enabled,
	// available Offerings (#249), and the craft brief lists their knowledge.
	// Nil keeps a historical Catalog's declared enum and literal brief.
	Offerings func(capability string) []generation.Offering
	// Texts gives the copy of the turn's UI locale. Nil keeps the Schema's
	// default-locale copy.
	Texts *catalog.TextLocalizer
	// AgentProviderName is the chat model connection's display name; it names
	// the upstream in the text of a model call the upstream refused with an
	// HTTP error status. Empty keeps the generic failure texts.
	AgentProviderName string
	// StepPolicy is application.step_policy (#330): the Agent's 3D step
	// segment. Nil keeps the segment out of every prompt.
	StepPolicy *catalog.StepPolicySchema
	// StepUsage reads a session's 3D step standing under its owner's setting.
	// Nil, or an error, writes the standing as unknown for the turn.
	StepUsage func(ctx context.Context, ownerID, sessionID string) (executiongrant.StepSummary, error)
	// StepBalances reads the 3D providers' balances through the shared cache.
	// Nil writes them as unknown.
	StepBalances func(ctx context.Context) []StepBalance
	// StepCosts reads the approximate per-step costs of the services the asset
	// used. Nil writes them as unknown.
	StepCosts func(ctx context.Context, ownerID, sessionID string) []StepCost
	// SessionParameters reads the parameter values the session holds per
	// Offering (#564). Nil keeps the block out of every prompt.
	SessionParameters func(ownerID, sessionID string) []SessionParameterGroup
	// ThreeViewGroups lists the session's three-view groups with their run IDs
	// and view states (#630). Nil keeps the groups block out of every prompt.
	ThreeViewGroups func(ctx context.Context, ownerID, sessionID string) ([]ThreeViewGroupFact, error)
}

type Orchestrator struct {
	configuration func(context.Context, string) (AgentConfigurationSnapshot, error)
	service       *Service
	schema        catalog.ChatSchema
	project       *catalog.ProjectSchema
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
	// visionAssetInline 把风格锁的资产 ID（不是 URL）转成 data URI；nil 表示
	// 风格锁参考图不进入视觉上下文（#247）。
	visionAssetInline func(
		ctx context.Context,
		owner generation.OwnerID,
		assetID string,
	) (string, bool)
	// visionAnchorInline 把视觉锚的参考图（资产 ID 或平台 URL）转成 data
	// URI；nil 表示锚点图片只保留文字标签（#250）。
	visionAnchorInline func(
		ctx context.Context,
		owner generation.OwnerID,
		sessionID string,
		reference string,
	) (string, bool)
	selectionPreview func(
		ctx context.Context,
		ownerID string,
		pictureURL string,
		maskAssetID string,
	) (string, bool)
	pictures       *inlinepictures.Encoder
	taskCanceler   func(ctx context.Context, ownerID, taskID string) error
	toolNote       func(tool catalog.ChatToolSchema, options TurnOptions) string
	selfCheckTasks func(context.Context, string, string) ([]SelfCheckTask, error)
	capabilities   []catalog.CapabilityPresentation
	taskLookup     func(ctx context.Context, ownerID, idempotencyKey string) (generation.Task, error)
	taskGet        func(ctx context.Context, ownerID, taskID string) (generation.Task, error)
	texts          *catalog.TextLocalizer
	agentProvider  string
	// stepPolicy, stepUsage, stepBalances and stepCosts render the Agent's
	// 「3D 步数」segment (#330); a nil policy keeps the segment out.
	stepPolicy   *catalog.StepPolicySchema
	stepUsage    func(ctx context.Context, ownerID, sessionID string) (executiongrant.StepSummary, error)
	stepBalances func(ctx context.Context) []StepBalance
	stepCosts    func(ctx context.Context, ownerID, sessionID string) []StepCost
	// sessionParameters reads the session's parameter values per Offering
	// (#564); nil keeps the block out of every prompt.
	sessionParameters func(ownerID, sessionID string) []SessionParameterGroup
	// threeViewGroups reads the session's three-view groups (#630); nil keeps
	// the groups block out of every prompt.
	threeViewGroups func(ctx context.Context, ownerID, sessionID string) ([]ThreeViewGroupFact, error)
	queue           chan ProcessTurn
	queueMu         sync.Mutex
	// titleWaiters counts background title summaries in flight.
	titleWaiters sync.WaitGroup
	queued       map[string]struct{}

	// activeMu 保护活动轮次的取消句柄与用户取消标记：
	// key = turnKey(ownerID, sessionID)，同会话同时只有一个活动轮次。
	activeMu     sync.Mutex
	activeCancel map[string]context.CancelFunc
	// activeDone is closed when the active turn of the key has left Process,
	// so deleting a session can wait for its turn; activeAssistant names that
	// turn (#384).
	activeDone      map[string]chan struct{}
	activeAssistant map[string]string
	canceledTurn    map[string]struct{}

	// consentWaits 是因等待同意而挂起的轮次登记表（AGT-12）：
	// key = turnKey(ownerID, sessionID)。挂起不占用 worker；唤醒
	// （用户决定/超时/停止）只允许一个来源生效——弹出即失效。
	suspendMu    sync.Mutex
	consentWaits map[string]*consentSuspend
	// wakePending holds a consent decision that arrived while the turn of the
	// key was still running and had not registered its wait yet (#665): the
	// assistant message ID it was for. suspendForConsent consumes it right
	// after registering, so the decision wakes the turn at once instead of
	// after consent_wait_timeout. A tool round drops it before its tools run:
	// a decision from before then was not for the consent those tools ask
	// for. Guarded by suspendMu.
	wakePending map[string]string

	// panicked are the turns whose processing panicked in this process
	// (#665). A worker does not run such a turn again: it only ends it as
	// failed, so a turn that keeps panicking cannot loop through the restart
	// recovery or the stranded-turn reclaim. Guarded by panickedMu.
	panickedMu sync.Mutex
	panicked   map[string]struct{}

	// stranded holds the running turns the last reclaim pass found held by no
	// worker and no consent wait of this process (#516); a turn still
	// unheld on the next pass is enqueued again. See ReclaimStrandedTurns.
	strandedMu sync.Mutex
	stranded   map[string]struct{}

	// compacting serializes the context compactions of each session (#322).
	compactMu  sync.Mutex
	compacting map[string]*compactionLock
	// compactFailedUntil is when each session's automatic compaction may be
	// tried again after it failed (#665). Guarded by compactMu.
	compactFailedUntil map[string]time.Time

	// backgroundWarned is when a failing background-availability read of
	// each session was last logged as a warning (#137).
	backgroundWarnMu sync.Mutex
	backgroundWarned map[string]time.Time
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
	assistantMessageID string,
	cancel context.CancelFunc,
) {
	orchestrator.activeMu.Lock()
	defer orchestrator.activeMu.Unlock()
	orchestrator.activeCancel[key] = cancel
	orchestrator.activeDone[key] = make(chan struct{})
	orchestrator.activeAssistant[key] = assistantMessageID
}

func (orchestrator *Orchestrator) unregisterActiveTurn(key string) {
	orchestrator.activeMu.Lock()
	defer orchestrator.activeMu.Unlock()
	delete(orchestrator.activeCancel, key)
	delete(orchestrator.activeAssistant, key)
	// #665: the stop marker goes with the registration, under the same lock.
	// CancelTurn writes it only while the turn is registered, so a stop that
	// raced the end of the turn can never leave it to poison the next turn.
	delete(orchestrator.canceledTurn, key)
	if done := orchestrator.activeDone[key]; done != nil {
		close(done)
		delete(orchestrator.activeDone, key)
	}
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
// unexecuted is the checkpoint's list of calls answered without running a
// tool (#501): those are task-free whatever the registry says about them.
func (orchestrator *Orchestrator) reconcileStoppedTasks(
	ctx context.Context, ownerID, sessionID, assistantID string, calls []ToolCall, unexecuted []string, options TurnOptions,
) error {
	var combined error
	registry := orchestrator.turnRegistry(ctx)
	for index, call := range calls {
		tool, known := registry.byName[call.Name]
		if known && tool.generation != nil {
			// A generation call whose approval card was detached belongs to the
			// pending-approval service: the person may approve it after this
			// stop, and the task that creates carries the call's frozen key.
			// Stop must neither cancel that task nor settle the call.
			if reader, ok := orchestrator.tools.(interface {
				DetachedOperationForCall(context.Context, ToolCommand) (bool, error)
			}); ok {
				detached, err := reader.DetachedOperationForCall(ctx, ToolCommand{OwnerID: ownerID, SessionID: sessionID,
					AssistantMessageID: assistantID, Configuration: configurationFromContext(ctx),
					Call:        agents.ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments},
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
				AssistantMessageID: assistantID, Configuration: configurationFromContext(ctx),
				Call:        agents.ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments},
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
			// #501: the turn answered this call without running a tool (an
			// unknown or unavailable tool, a scope refusal, invalid
			// arguments), so it never submitted anything.
			if slices.Contains(unexecuted, call.ID) {
				continue
			}
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
					continue
				}
			case generation.IsCode(err, generation.ErrConflict):
				// A tombstoned key is permanently consumed, with no live Task
				// left to cancel.
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
	// #137: the person already chose to let this turn's Tasks keep running.
	// That choice committed before this stop (it requires no stop intent), so
	// the stop must not cancel them: the queued turn is finished by Process,
	// which ends it without cancelling.
	stored, err := orchestrator.service.GetTurn(ctx, ownerID, sessionID, turn.AssistantMessageID)
	if err != nil {
		return true, err
	}
	if stored.Checkpoint.Background {
		return true, nil
	}
	if stored.Checkpoint.Configuration != nil {
		// The turn's calls are reconciled against the registry it ran from:
		// its frozen Agent configuration, MCP presets included (#453). The
		// base registry does not know an MCP tool, so a read-only MCP call
		// would look unknown here and keep a stopped turn running.
		ctx = context.WithValue(ctx, configurationContextKey{}, stored.Checkpoint.Configuration)
	}
	turnKey := orchestrator.turnKey(ownerID, sessionID)
	cancel := orchestrator.markActiveTurnCanceled(turnKey, turn.AssistantMessageID)
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
			ownerID, sessionID, turn.AssistantMessageID, messages[index].ToolCalls,
			stored.Checkpoint.UnexecutedCalls, turn.Options); err != nil {
			orchestrator.logger.Warn("停止轮次即时核对任务失败，等待恢复重试", "error", err)
		}
		break
	}
	return true, nil
}

// markActiveTurnCanceled writes the stop marker of the key only while the
// stopped turn is the one registered as active, and returns its cancel
// function (#665). A turn that ended between RequestTurnStop and here has
// unregistered: no marker is written (the durable stop intent covers a turn
// that is only queued), so it cannot stop the next turn of the session.
func (orchestrator *Orchestrator) markActiveTurnCanceled(key, assistantMessageID string) context.CancelFunc {
	orchestrator.activeMu.Lock()
	defer orchestrator.activeMu.Unlock()
	cancel := orchestrator.activeCancel[key]
	if cancel == nil || orchestrator.activeAssistant[key] != assistantMessageID {
		return nil
	}
	orchestrator.canceledTurn[key] = struct{}{}
	return cancel
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
	// The durable checkpoint says which calls were answered without running
	// a tool (#501); a read failure keeps the turn open for a retry.
	stored, err := orchestrator.service.GetTurn(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID)
	if err != nil {
		return err
	}
	// 停止收尾兜底取消在飞任务：CancelTurn 扫描与工具提交之间的
	// TOCTOU 窗口里落库的绑定也在这里被取消（计费止损）。
	if err := orchestrator.reconcileStoppedTasks(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID,
		assistant.ToolCalls, stored.Checkpoint.UnexecutedCalls, command.Options); err != nil {
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
		// #665: the stream ends whether or not the stop text was stored, as
		// a failure's does, or its client would wait for an event that never
		// comes.
		orchestrator.appendDone(ctx, command.SessionID)
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
	registry.bindOfferingSources(config.OfferingAllowed, config.Offerings)
	orchestrator := &Orchestrator{
		service:            config.Service,
		schema:             config.Schema,
		project:            config.Project,
		model:              config.Model,
		dsmlFor:            config.DSMLFor,
		visionInline:       config.VisionInline,
		visionAssetInline:  config.VisionAssetInline,
		visionAnchorInline: config.VisionAnchorInline,
		selectionPreview:   config.SelectionPreview,
		pictures:           config.Pictures,
		agent:              config.Agent,
		tools:              config.Tools,
		logger:             config.Logger,
		registry:           registry,
		agentScope:         registry.scope,
		configuration:      config.Configuration,
		taskCanceler:       config.TaskCanceler,
		toolNote:           config.ToolNote,
		selfCheckTasks:     config.SelfCheckTasks,
		capabilities:       config.Capabilities,
		taskLookup:         config.TaskLookup,
		taskGet:            config.TaskGet,
		texts:              config.Texts,
		agentProvider:      config.AgentProviderName,
		stepPolicy:         config.StepPolicy,
		stepUsage:          config.StepUsage,
		stepBalances:       config.StepBalances,
		stepCosts:          config.StepCosts,
		sessionParameters:  config.SessionParameters,
		threeViewGroups:    config.ThreeViewGroups,
		queue:              make(chan ProcessTurn, config.Schema.Agent.QueueSize),
		queued:             map[string]struct{}{},
		activeCancel:       map[string]context.CancelFunc{},
		activeDone:         map[string]chan struct{}{},
		activeAssistant:    map[string]string{},
		canceledTurn:       map[string]struct{}{},
		consentWaits:       map[string]*consentSuspend{},
		wakePending:        map[string]string{},
		panicked:           map[string]struct{}{},
		stranded:           map[string]struct{}{},
		compacting:         map[string]*compactionLock{},
		compactFailedUntil: map[string]time.Time{},

		backgroundWarned: map[string]time.Time{},
	}
	// Deleting a session takes this runner's work off it first (#384).
	config.Service.addSessionQuiescer(orchestrator)
	return orchestrator, nil
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
		defer orchestrator.recoverGoroutine("consent-wait-timer", "session_id", command.SessionID)
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
	// #665: a decision that arrived before this registration left a wake
	// token; the wait fires at once instead of at its deadline. The token is
	// read under the same lock ResumeConsentWait writes it with.
	fireIn := time.Until(deadline)
	if woken := orchestrator.wakePending[key]; woken != "" {
		delete(orchestrator.wakePending, key)
		if woken == command.AssistantMessageID {
			fireIn = 0
		}
	}
	// timer 在持锁期间创建：entry 的发布与 timer 字段写入对
	// popConsentWait（同样持锁）可见，避免 timer 字段竞态。
	entry.timer = time.AfterFunc(fireIn, timeoutEnqueue)
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
//
// A decision that lands while the session's turn is still running, before it
// registered its wait (the tool already wrote the pending grant), is not lost
// (#665): it leaves a wake token for that turn, and suspendForConsent wakes
// the turn as soon as it registers.
func (orchestrator *Orchestrator) ResumeConsentWait(
	ownerID, sessionID string,
) bool {
	key := orchestrator.turnKey(ownerID, sessionID)
	orchestrator.suspendMu.Lock()
	entry := orchestrator.consentWaits[key]
	if entry == nil {
		orchestrator.activeMu.Lock()
		active := orchestrator.activeAssistant[key]
		orchestrator.activeMu.Unlock()
		if active != "" {
			orchestrator.wakePending[key] = active
		}
		orchestrator.suspendMu.Unlock()
		return false
	}
	orchestrator.suspendMu.Unlock()
	command, ok := orchestrator.popConsentWait(ownerID, sessionID)
	if !ok {
		return false
	}
	orchestrator.enqueueSuspendedResume(command)
	return true
}

// clearWakePending drops the wake token of the key when its turn leaves
// Process: a token is only for the turn that was running when it was left.
func (orchestrator *Orchestrator) clearWakePending(key string) {
	orchestrator.suspendMu.Lock()
	defer orchestrator.suspendMu.Unlock()
	delete(orchestrator.wakePending, key)
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
		defer orchestrator.recoverGoroutine("consent-wake-retry", "session_id", command.SessionID)
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
		command := ProcessTurn{
			OwnerID:            turn.OwnerID,
			SessionID:          turn.SessionID,
			AssistantMessageID: turn.AssistantMessageID,
			Options:            turn.Options,
		}
		// A turn whose session is gone is not resumed (#384). A failed read
		// proceeds as before: a recoverable turn is never dropped on a doubt.
		if orchestrator.sessionGone(runCtx, turn.OwnerID, turn.SessionID) {
			orchestrator.discardOrphanTurn(runCtx, command)
			continue
		}
		if err := orchestrator.enqueueRecovered(
			runCtx,
			command,
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
			orchestrator.dispatch(ctx, command)
		}
	}
}

// dispatch runs one dequeued command. A panic anywhere in it is recovered
// (#665): the worker keeps serving, the turn ends as failed with the Schema's
// error text instead of taking the process down, and a turn that panicked
// once is not run again by this process.
func (orchestrator *Orchestrator) dispatch(ctx context.Context, command ProcessTurn) {
	defer func() {
		if value := recover(); value != nil {
			orchestrator.notePanicked(command.AssistantMessageID)
			orchestrator.logger.Error("对话回合调度崩溃，轮次以失败结束",
				"session_id", command.SessionID,
				"assistant_message_id", command.AssistantMessageID,
				"error_id", command.AssistantMessageID,
				"panic", fmt.Sprint(value),
				"stack", string(debug.Stack()),
			)
			orchestrator.failPanickedTurn(context.WithoutCancel(ctx), command)
			orchestrator.unmarkQueued(command.AssistantMessageID)
		}
	}()
	if orchestrator.turnPanicked(command.AssistantMessageID) {
		// Never run it again. A turn whose ending is already written is left
		// as it is (#665): ending it again would rewrite its reply and append
		// error and done events to the session's stream, which may be the
		// next turn's by now. One whose first ending could not be written is
		// ended again.
		if orchestrator.panickedTurnEnded(context.WithoutCancel(ctx), command) {
			orchestrator.clearPanicked(command.AssistantMessageID)
		} else {
			orchestrator.failPanickedTurn(context.WithoutCancel(ctx), command)
		}
		orchestrator.unmarkQueued(command.AssistantMessageID)
		return
	}
	// A session being deleted has nothing for a worker to run (#384).
	if orchestrator.service.sessionDeleting(command.OwnerID, command.SessionID) {
		orchestrator.unmarkQueued(command.AssistantMessageID)
		return
	}
	dispatch, err := orchestrator.turnAwaitsDispatch(context.WithoutCancel(ctx), command)
	if err != nil || !dispatch {
		if err != nil {
			// The turn row goes with its session or message: gone is
			// not a failure.
			level := slog.LevelError
			if errors.Is(err, ErrMessageNotFound) {
				level = slog.LevelDebug
			}
			orchestrator.logger.Log(
				ctx, level,
				"读取 Chat Agent Turn 状态失败",
				"assistant_message_id", command.AssistantMessageID,
				"error", err,
			)
		}
		orchestrator.unmarkQueued(command.AssistantMessageID)
		return
	}
	if err := orchestrator.service.MarkTurnRunning(
		context.WithoutCancel(ctx),
		command.AssistantMessageID,
	); err != nil {
		// A turn gone, or one that finished since it was read (#665), is not
		// a failure: there is nothing to run.
		level := slog.LevelError
		if errors.Is(err, ErrMessageNotFound) || errors.Is(err, ErrTurnStateChanged) {
			level = slog.LevelDebug
		}
		orchestrator.logger.Log(
			ctx, level,
			"更新 Chat Agent Turn 状态失败",
			"assistant_message_id", command.AssistantMessageID,
			"error", err,
		)
		orchestrator.unmarkQueued(command.AssistantMessageID)
		return
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

// turnAwaitsDispatch reports whether a dequeued command still names a turn
// that needs processing. Run's startup recovery and the waiting scanner both
// enqueue queued turns from their own snapshots, and the in-flight dedup only
// holds while one copy runs, so a copy can arrive after the turn finished or
// was parked on a Task. Reopening it would set a terminal turn back to running
// and replay its persisted tool round: the tools run again and the Agent is
// called again (#90). A waiting turn is dispatched only to honour a stop; its
// normal wake moves it to queued first.
func (orchestrator *Orchestrator) turnAwaitsDispatch(
	ctx context.Context,
	command ProcessTurn,
) (bool, error) {
	turn, err := orchestrator.service.GetTurn(
		ctx,
		command.OwnerID,
		command.SessionID,
		command.AssistantMessageID,
	)
	if err != nil {
		return false, err
	}
	switch turn.State {
	case orchestrator.service.TurnQueuedState(), orchestrator.service.TurnRunningState():
		return true, nil
	case orchestrator.service.TurnWaitingTaskState():
		if turn.StopRequested {
			return true, nil
		}
	}
	orchestrator.logger.Debug(
		"跳过已不需处理的 Chat Agent 轮次命令",
		"assistant_message_id", command.AssistantMessageID,
		"state", turn.State,
	)
	return false, nil
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

// isQueued reports whether the turn's command is queued or being processed.
func (orchestrator *Orchestrator) isQueued(assistantMessageID string) bool {
	orchestrator.queueMu.Lock()
	defer orchestrator.queueMu.Unlock()
	_, exists := orchestrator.queued[assistantMessageID]
	return exists
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
	// A turn whose session or message was deleted under it ends without an
	// error (#384): whatever it failed on is gone with the rows it was writing.
	// This runs last, after the deferred finalizer below.
	defer func() {
		if processErr != nil && orchestrator.turnAbandoned(ctx, command) {
			processErr = nil
		}
	}()
	if orchestrator.service.sessionDeleting(command.OwnerID, command.SessionID) {
		return nil
	}
	messages, err := orchestrator.service.ListMessages(
		ctx,
		command.OwnerID,
		command.SessionID,
	)
	if errors.Is(err, ErrSessionNotFound) {
		// The turn of a session that is gone (a queued command, or a turn row
		// a restart finds): finish it so nothing offers it again.
		orchestrator.discardOrphanTurn(ctx, command)
		return nil
	}
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
	// The turn's frozen Agent configuration is installed before any ending of
	// the turn can run: a stop that arrives before the tool loop (or the
	// restart recovery of one) reconciles the calls against the registry the
	// turn ran from, MCP presets included (#453).
	if turn.Checkpoint.Configuration != nil {
		ctx = context.WithValue(ctx, configurationContextKey{}, turn.Checkpoint.Configuration)
	}
	// The selection is only ever the one the service verified and stored with
	// the turn; whatever the caller put in the command is not trusted.
	command.Options.Selection = turn.Options.Selection
	command.Options.AttachmentPurposes = turn.Options.AttachmentPurposes
	command.Options.SelectionPicked = turn.Options.SelectionPicked
	command.Options.InputSource = turn.Options.InputSource
	// The mask is the one the service measured and froze with the turn (#244);
	// a later paint on the stage never changes what this turn binds.
	command.Options.SelectionMask = turn.Options.SelectionMask
	command.Options.Multiview = turn.Options.Multiview
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
	command.Options.FollowUpReasoningEffort = turn.Options.FollowUpReasoningEffort
	// And so is the thinking contract the turn started with: a turn recovered
	// after a restart must keep the wire, the kind and the passback rule it ran
	// on, not the ones today's Catalog declares for its model (#377).
	command.Options.Thinking = turn.Options.Thinking
	// So are the production brief and the explicitly picked 3D model (#103):
	// the first run of a turn must honor the lock the service froze, or an
	// Agent-named model would replace the person's pick.
	command.Options.ProductionBrief = turn.Options.ProductionBrief
	command.Options.ExplicitModel3D = turn.Options.ExplicitModel3D
	command.Options.ExplicitModelImage = turn.Options.ExplicitModelImage
	command.Options.StyleLock = turn.Options.StyleLock
	command.Options.StyleLockSkipped = turn.Options.StyleLockSkipped
	command.Options.VisualAnchors = cloneVisualAnchors(turn.Options.VisualAnchors)
	// The project scope and its frozen snapshot come from the stored turn too:
	// they decide the project system prompt, @projectN handles and whether a
	// registered Task picture outside this session is readable (#240).
	copyFrozenProject(&command.Options, turn.Options)
	// A plan continuation is the service's mark, never the caller's (#134).
	command.Options.PlanContinuation = turn.Options.PlanContinuation
	// So are the production-list item marks of a server-started turn (#328).
	copyFrozenAssetListTurn(&command.Options, turn.Options)
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
	orchestrator.registerActiveTurn(turnKey, command.AssistantMessageID, cancelProcess)
	defer orchestrator.unregisterActiveTurn(turnKey)
	defer orchestrator.clearWakePending(turnKey)
	defer orchestrator.clearTurnCanceled(turnKey)
	defer cancelProcess()
	// Check after registering: a deletion that began before the registration
	// could not interrupt this turn, and one that begins from now on will.
	if orchestrator.service.sessionDeleting(command.OwnerID, command.SessionID) {
		return nil
	}
	failed := false
	// suspended=true 表示轮次已挂起等待用户输入（AGT-12）：返回时不
	// 收尾流与轮次，等唤醒方重新入队续跑。读写失败不是挂起：进程内没有
	// 任何重试，轮次会一直占着会话，见 endUnsaved（#378）。
	suspended := false
	stopFinalized := false
	// backgroundFinalized means the turn ended with its Tasks still running
	// (#137); a stop requested later must not cancel them.
	backgroundFinalized := false
	// emptyReply means the last model round answered with no text and no tool
	// call, also after the adapter layer's empty retry (#638): the turn ends
	// with the empty_response reason and says so, never as completed.
	emptyReply := false
	// inlineImageReply means a model round drew a picture itself instead of
	// calling a generation tool (#638). The Adapter cut that stream and nothing
	// sends it again (the picture is billed as a generated image); the turn
	// ends with the inline_image reason and says so.
	inlineImageReply := false
	finalizeStop := func() error {
		err := orchestrator.finalizeStoppedTurn(ctx, command, &assistant)
		if err == nil {
			stopFinalized = true
		}
		return err
	}
	// endUnsaved ends the turn as failed when something it has to keep cannot
	// be read or saved before it can go on (#378): the Agent configuration
	// snapshot, the round the model just planned, or - after the tools of the
	// round ran (#429) - the checkpoint that records their receipts. Nothing
	// of a round that never ran has run, so nothing paid is lost there; the
	// tool phase keeps the round's calls and their Task bindings on the reply,
	// and the Tasks themselves stay with the generation kernel. A stop or a
	// process shutdown keeps its own ending, and a write that lost to another
	// writer never gets here (the callers leave that turn to its writer).
	// Whatever else fails cannot be waited out: nothing in the live process
	// retries it, and a suspended turn would keep the session busy and the
	// stream open until the next restart. The person gets the failure text
	// with the error ID, and the stream ends with the events that close it.
	endUnsaved := func(stage string, cause error) error {
		if orchestrator.turnCanceled(turnKey) {
			return finalizeStop()
		}
		if ctx.Err() != nil {
			// 进程停机：保留可恢复轮次，交给 RecoverTurns 续跑。
			return nil
		}
		failed = true
		orchestrator.logger.Error(
			"对话回合无法读取或保存其状态，轮次以失败结束",
			"session_id", command.SessionID,
			"assistant_message_id", command.AssistantMessageID,
			"stage", stage,
			"error", cause,
		)
		// fail ends the stream with its done event also when the failure
		// text could not be stored.
		return orchestrator.fail(ctx, command.OwnerID, &assistant, orchestrator.agentFailureMessage(ctx, cause))
	}
	// endBackgroundFailed ends a turn whose background ending could not be
	// stored (#137/#429). The person already chose to let the Tasks run, so
	// nothing of the turn is cancelled and nothing is submitted again: the
	// reply keeps the waiting calls' Task bindings, the Tasks keep running in
	// the kernel, and the turn fails with the events that close its stream.
	// A shutdown is not a failure: the queued turn waits for the restart.
	endBackgroundFailed := func(cause error) error {
		// The failure write is the best remaining chance to mark the calls:
		// a later turn lists the Tasks from the reply and looks them up
		// instead of paying for them again. The bindings come from the
		// checkpoint the turn already wrote before it stopped waiting.
		for _, entry := range turn.Checkpoint.WaitingCalls() {
			if entry.TaskID == "" {
				continue
			}
			for index := range assistant.ToolCalls {
				call := &assistant.ToolCalls[index]
				if call.ID != entry.CallID {
					continue
				}
				if call.TaskID == "" {
					call.TaskID = entry.TaskID
				}
				if call.TaskID == entry.TaskID {
					call.Background = true
				}
			}
		}
		failed = true
		orchestrator.logger.Error(
			"后台继续的轮次无法收尾，轮次以失败结束",
			"session_id", command.SessionID,
			"assistant_message_id", command.AssistantMessageID,
			"error", cause,
		)
		// fail ends the stream with its done event also when the failure
		// text could not be stored.
		return orchestrator.fail(ctx, command.OwnerID, &assistant, orchestrator.agentFailureMessage(ctx, cause))
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
		// The session is being deleted: its stream and turn rows go with it.
		if orchestrator.service.sessionDeleting(command.OwnerID, command.SessionID) {
			return
		}
		requested, err := orchestrator.service.TurnStopRequested(
			context.WithoutCancel(ctx), command.OwnerID, command.SessionID, command.AssistantMessageID)
		if err != nil {
			processErr = errors.Join(processErr, err)
			return
		}
		if requested && !stopFinalized && !backgroundFinalized {
			if err := finalizeStop(); err != nil {
				processErr = errors.Join(processErr, err)
				return
			}
		}
		finishCtx := context.WithoutCancel(ctx)
		failedTurn := failed || processErr != nil
		finishErr := errors.Join(
			orchestrator.service.FinishStream(
				finishCtx,
				command.SessionID,
				command.AssistantMessageID,
				failedTurn,
			),
			orchestrator.service.FinishTurnWithReason(
				finishCtx,
				command.AssistantMessageID,
				orchestrator.turnEndReason(finishCtx, command, assistant, turnOutcome{
					failed:      failedTurn,
					background:  backgroundFinalized,
					stopped:     stopFinalized,
					empty:       emptyReply,
					inlineImage: inlineImageReply,
				}),
			),
		)
		if err := finishErr; processErr == nil && err != nil {
			processErr = err
		}
		// #645: the cards of an earlier reply the Agent did not map this
		// turn's typed message to are no longer open.
		if err := orchestrator.service.SupersedeStaleChoices(finishCtx, turn); err != nil && orchestrator.logger != nil {
			orchestrator.logger.Warn("收尾时失效旧选项卡失败", "session_id", command.SessionID,
				"assistant_message_id", command.AssistantMessageID, "error", err)
		}
	}()
	// #665: a panic in the turn (a tool, an executor, the request builder) is
	// recovered here, before the finalizer above runs: the reply gets the
	// Schema's error text with its error ID, the stream gets its done event,
	// and the turn ends as failed instead of taking the process down and being
	// recovered into the same panic on every restart.
	defer func() {
		value := recover()
		if value == nil {
			return
		}
		orchestrator.notePanicked(command.AssistantMessageID)
		suspended, failed = false, true
		orchestrator.logger.Error("对话回合执行崩溃，轮次以失败结束",
			"session_id", command.SessionID,
			"assistant_message_id", command.AssistantMessageID,
			"error_id", assistant.ID,
			"panic", fmt.Sprint(value),
			"stack", string(debug.Stack()),
		)
		cause := fmt.Errorf("%w: %v", errTurnPanicked, value)
		processErr = errors.Join(processErr, cause,
			orchestrator.fail(context.WithoutCancel(ctx), command.OwnerID, &assistant, ""))
	}()
	// #137: the person stopped waiting and let the Tasks run on. This comes
	// before the stop check: the choice committed before any later stop.
	if turn.Checkpoint.Background {
		// The choice is durable, so this turn no longer owns its Tasks: no
		// later stop cancels them, whether the ending below can be written or
		// not (#429).
		backgroundFinalized = true
		if err := orchestrator.finalizeBackgroundTurn(ctx, command, turn, &assistant); err != nil {
			if ctx.Err() != nil {
				// 进程停机：轮次保持排队（Background 已落库），重启后由
				// RecoverTurns 重新收尾。
				return err
			}
			return endBackgroundFailed(err)
		}
		return nil
	}
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
			// #378: the turn has done nothing yet, and nothing here retries the
			// read.
			return endUnsaved("读取 Agent 配置快照", snapshotErr)
		}
		snapshot.SkillExecution = orchestrator.schema.Skills.Execution
		turn.Checkpoint.Configuration = &snapshot
		version, saveErr := orchestrator.service.SaveTurnCheckpoint(processCtx, turn, turn.State, turn.Checkpoint, false)
		if saveErr != nil {
			if requested, checkErr := orchestrator.service.TurnStopRequested(context.WithoutCancel(ctx),
				command.OwnerID, command.SessionID, command.AssistantMessageID); checkErr == nil && requested {
				return finalizeStop()
			}
			if errors.Is(saveErr, ErrTurnCheckpointConflict) {
				// The write lost to another writer: the turn is that writer's
				// now (or the restart recovery's), not ours to finish.
				suspended = true
				return saveErr
			}
			return endUnsaved("保存 Agent 配置快照", saveErr)
		}
		turn.CheckpointVersion = version
	}
	if turn.Checkpoint.Configuration != nil {
		ctx = context.WithValue(ctx, configurationContextKey{}, turn.Checkpoint.Configuration)
		processCtx = context.WithValue(processCtx, configurationContextKey{}, turn.Checkpoint.Configuration)
	}
	// #413: the turn forks its tool registry once and shares it with the
	// request it builds and the tools it runs, so the Offerings, enum and
	// offered-tool lookups of one turn happen once. The fork lives in the
	// turn's contexts only; the Orchestrator's registry is never written.
	if registry, registryErr := orchestrator.registryForRequest(ctx); registryErr == nil {
		ctx = context.WithValue(ctx, turnRegistryContextKey{}, registry)
		processCtx = context.WithValue(processCtx, turnRegistryContextKey{}, registry)
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
		// The title only needs the first user message, so it is summarised
		// alongside the turn instead of after it (which can wait minutes on
		// generation tools).
		orchestrator.startFirstTurnTitle(command, history)
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
		completion, request, err = orchestrator.streamAgent(
			processCtx,
			command,
			request,
			func(delta agents.Delta) error {
				return round.receive(delta, streamFilter)
			},
		)
		if errors.Is(err, agents.ErrInlineImageOutput) && !orchestrator.turnCanceled(turnKey) && ctx.Err() == nil {
			// #638: not a failure of the turn, and not to be repeated: the
			// reply says the model drew a picture itself.
			orchestrator.noteInlineImage(command, err)
			inlineImageReply, completion, err = true, agents.Completion{}, nil
		}
		if err != nil {
			// #665: what the round already streamed stays in the reply, as
			// the person saw it, before the stop or failure text.
			streamed := round.streamed.String()
			if orchestrator.turnCanceled(turnKey) {
				if streamed != "" {
					assistant.Content = streamed
				}
				return finalizeStop()
			}
			if ctx.Err() != nil {
				// 进程停机：保留可恢复轮次，交给 RecoverTurns 续跑。
				return nil
			}
			if orchestrator.turnAbandoned(ctx, command) {
				// The session or the message was deleted under the turn.
				return nil
			}
			failed = true
			orchestrator.logger.Error(
				"Chat Agent 请求失败",
				"session_id", command.SessionID,
				"assistant_message_id", command.AssistantMessageID,
				"error", err,
			)
			detail := orchestrator.agentFailureMessage(ctx, err)
			if streamed != "" {
				detail = joinNarration(streamed, detail)
			}
			return orchestrator.fail(ctx, command.OwnerID, &assistant, detail)
		}
		orchestrator.recordUsage(ctx, command, request.Model, completion.Usage, request)
		emptyReply = !inlineImageReply && orchestrator.noteEmptyReply(command, completion, 0)
		if err := streamFilter.Flush(round.emitText); err != nil {
			if orchestrator.turnCanceled(turnKey) {
				return finalizeStop()
			}
			if ctx.Err() != nil {
				// 进程停机：保留可恢复轮次，交给 RecoverTurns 续跑。
				return nil
			}
			failed = true
			orchestrator.appendDone(ctx, command.SessionID)
			return err
		}
	}
	// B4 工具循环：模型（例如同意获批后）可在同一轮内继续调用
	// 工具，单轮调用个数不设上限（#553），轮次数受
	// chat.agent.max_tool_rounds_per_turn 约束（#665，重放的已落库计划
	// 不再计数），模型不再调用工具时本轮结束。每轮调用 ID 按轮次唯一，已执行轮的调用与 TaskID 绑定
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
	// 旧任务）。循环控制仍从 0 起：round=0 保留“首次赋值”语义。
	resumeRound := 0
	if len(assistant.ToolCalls) > 0 {
		resumeRound = maxRoundFromCalls(assistant.ToolCalls) + 1
	}
	startRound := 0
	if checkpointResuming {
		startRound = turn.Checkpoint.Round
		resumeRound = maxRoundFromCalls(assistant.ToolCalls) - startRound
	}
	// replayingPlan is set when round 0 replays a stored plan without a
	// checkpoint (#665): its calls were planned, and counted against
	// chat.agent.max_tool_rounds_per_turn, when the model wrote them. A new
	// round after it is round effectiveRound-1 of the turn: the call IDs of
	// that path skip one number past the stored rounds.
	replayingPlan := resumingPlan && !checkpointResuming
	// followUpErr is the follow-up request's failure that ended the loop, if
	// any; a reply left empty by it is told from it.
	var followUpErr error
	// malformedCalls are the calls of this round whose arguments the model
	// wrote as invalid JSON (#378); the checkpoint keeps them.
	var malformedCalls, invalidJSONCalls []string
	// cutReply is the finish reason of a round the upstream cut (#665): its
	// output token limit or its content filter. roundsCapped is set when the
	// model called tools past chat.agent.max_tool_rounds_per_turn.
	cutReply := ""
	roundsCapped := false
	// endUnsavedRound is endUnsaved for the round the model just planned. The
	// calls the round added (executed is how many the reply had before it)
	// never ran, so they leave the reply; a round resumed from the checkpoint
	// keeps its calls, whose Tasks may already be bound.
	endUnsavedRound := func(stage string, cause error, executed int) error {
		if !parsed {
			assistant.ToolCalls = assistant.ToolCalls[:executed]
		}
		return endUnsaved(stage, cause)
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
		if !checkpointResuming || round != startRound {
			// #378: the arguments are the model's raw JSON. They are checked
			// before they reach the reply, the checkpoint or the next request:
			// a call whose arguments are not valid JSON is not run, and the
			// model gets the error instead. A checkpoint being resumed was
			// vetted when it was written.
			completion.ToolCalls, malformedCalls, invalidJSONCalls = orchestrator.vetToolArguments(ctx, command, completion.ToolCalls)
			cutReply = completion.FinishReason
			if cutReply != "" && len(completion.ToolCalls) > 0 {
				// #665: a round the upstream cut is not whole. Its calls' arguments
				// may be cut short, and it may have meant more calls: none of them
				// runs, and the reply says it was cut.
				orchestrator.logger.Warn("对话模型这一轮被上游截断，本轮工具调用不执行",
					"session_id", command.SessionID,
					"assistant_message_id", command.AssistantMessageID,
					"finish_reason", cutReply,
					"calls", len(completion.ToolCalls),
				)
				completion.ToolCalls, malformedCalls, invalidJSONCalls = nil, nil, nil
			}
			planRound := effectiveRound
			if replayingPlan {
				planRound--
			}
			if limit := orchestrator.schema.Agent.MaxToolRoundsPerTurn; limit > 0 &&
				!(replayingPlan && round == 0) && planRound >= limit && len(completion.ToolCalls) > 0 {
				// #665: the model kept calling tools past the turn's bound; the
				// round past it does not run, and the reply says why it ended.
				orchestrator.logger.Warn("对话回合工具轮次达到上限，本轮到此为止",
					"session_id", command.SessionID,
					"assistant_message_id", command.AssistantMessageID,
					"rounds", planRound,
					"limit", limit,
				)
				completion.ToolCalls, malformedCalls, invalidJSONCalls = nil, nil, nil
				roundsCapped = true
			}
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
						assistant.ToolCalls[index].Summary = stored.Summary
						break
					}
				}
			}
		} else {
			assistant.ToolCalls = append(
				assistant.ToolCalls,
				agentCallsToMessages(completion.ToolCalls)...,
			)
			// #402: a later round's text is a new paragraph, not a
			// continuation of the sentence before it.
			assistant.Content = joinNarration(assistant.Content, completion.Content)
		}
		orchestrator.freezeToolCallKeys(ctx, command.SessionID, assistant.ToolCalls, storedCalls)
		if len(completion.ToolCalls) == 0 {
			break
		}
		// executedCalls is how many calls the reply ran before this round;
		// the round's own calls leave the reply if it cannot be saved.
		executedCalls := len(assistant.ToolCalls) - len(completion.ToolCalls)
		if !checkpointResuming || round != startRound {
			// The calls answered without running stay listed for the whole
			// turn (#501): a stop in a later round still reconciles them.
			turn.Checkpoint = TurnCheckpoint{Configuration: turn.Checkpoint.Configuration, Round: round, Assistant: assistant, Request: request, Completion: completion, MalformedCalls: malformedCalls, InvalidJSONCalls: invalidJSONCalls,
				UnexecutedCalls: turn.Checkpoint.UnexecutedCalls, GenerationUnits: turn.Checkpoint.GenerationUnits}
			version, saveErr := orchestrator.service.SaveTurnCheckpoint(context.WithoutCancel(ctx), turn,
				orchestrator.service.TurnRunningState(), turn.Checkpoint, false)
			if saveErr != nil {
				if errors.Is(saveErr, ErrTurnCheckpointConflict) {
					if requested, checkErr := orchestrator.service.TurnStopRequested(context.WithoutCancel(ctx), command.OwnerID, command.SessionID, command.AssistantMessageID); checkErr == nil && requested {
						return finalizeStop()
					}
					// The write lost to another writer: the turn is that
					// writer's now (or the restart recovery's), not ours to
					// finish.
					suspended = true
					return saveErr
				}
				// #378: any other failure cannot be waited out. A checkpoint
				// that cannot be encoded never can be, and nothing here
				// retries a refused write; suspended, the turn would keep the
				// session busy and the stream open until the next restart.
				return endUnsavedRound("保存回合检查点", saveErr, executedCalls)
			}
			turn.CheckpointVersion, turn.State = version, orchestrator.service.TurnRunningState()
		}
		if err := orchestrator.service.UpdateMessage(
			context.WithoutCancel(ctx),
			command.OwnerID,
			assistant,
		); err != nil {
			return endUnsavedRound("保存回合的工具计划", err, executedCalls)
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
			orchestrator.appendDone(ctx, command.SessionID)
			return err
		}
		// #665: a wake token left before this round's tools run came from a
		// decision on another card; the round's own consent request comes
		// from these tools, so only a decision after it may wake its wait.
		orchestrator.clearWakePending(turnKey)
		results := turnResults(assistant, boundTasks)
		// Tool pictures share the request's vision budget (#247): the base
		// request's pictures spend first, and every round of this turn keeps
		// spending from the same count.
		toolBudget := orchestrator.visionBudget(command.Options)
		toolSpend := orchestrator.requestVisionSpend(request)
		toolContent, imageURLs, taskBindings, toolErr :=
			orchestrator.executeTools(
				processCtx,
				command,
				history,
				completion.ToolCalls,
				boundTasks,
				assistant.ToolCalls,
				&turn,
				&results,
				toolBudget,
				&toolSpend,
			)
		// Handles given during the round stay with the reply, so the next
		// round and the final write keep the numbers the Agent was told (#25).
		assistant.Attachments = results.Attachments
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
			// AGT-12：挂起的工具已写入待决状态（生成工具已建批准卡），
			// 挂起轮次并立即释放 worker。assistant 计划（含工具调用）
			// 已在上方 UpdateMessage 落库，唤醒后按 resumingPlan 路径重放
			// 续跑。登记失败（suspendForConsent 返回 false）或登记后复查
			// 到取消标记，都表示停止与挂起竞态命中：丢弃登记并原地按
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
		if errors.Is(toolErr, ErrTurnCheckpointUnsaved) || errors.Is(toolErr, ErrTurnSubmissionUnconfirmed) {
			// #429: 工具执行阶段的检查点写入失败没有人重试，挂起会让会话一直
			// 忙、SSE 一直开着。本轮的调用可能已经提交了付费任务：先把内存里
			// 的绑定并回回复（消息上已持久化的绑定保持不变），再按 #378 的
			// 同一语义以 failed 结束。任务归生成内核，不会被取消，也不会被
			// 这次失败重新提交；failed 轮次不在 RecoverTurns 的续跑范围内。
			// #516: 付费提交结果超过请求超时仍无法确认时同样这样收尾。
			orchestrator.mergeDurableToolCalls(ctx, command, &assistant)
			for index := range assistant.ToolCalls {
				if assistant.ToolCalls[index].TaskID == "" {
					assistant.ToolCalls[index].TaskID =
						taskBindings[assistant.ToolCalls[index].ID]
				}
			}
			stage := "保存工具执行结果"
			if errors.Is(toolErr, ErrTurnSubmissionUnconfirmed) {
				stage = "确认付费提交"
			}
			return endUnsaved(stage, toolErr)
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
			if orchestrator.turnAbandoned(ctx, command) {
				// The session or the message was deleted under the turn.
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
		if len(toolContent) == 0 {
			break
		}
		followUp := orchestrator.followUpRequest(
			request,
			completion,
			toolContent,
		)
		// #638: a round after tool results sends the turn's follow-up level
		// (the person's choice, else the profile's tool_followup level); the
		// first round of the turn kept the turn's own level.
		if level := command.Options.FollowUpReasoningEffort; level != "" {
			followUp.ReasoningEffort = level
		}
		// 续跑流同样要过滤 DSML：模型可能在总结中继续发起调用。
		streamFilter := dsml.NewFilter(dsmlSchema)
		followUpRound := orchestrator.startThinkingRound(processCtx, command.SessionID)
		followUpRound.continueAfter(assistant.Content)
		next, sentFollowUp, streamErr := orchestrator.streamAgent(
			processCtx,
			command,
			followUp,
			func(delta agents.Delta) error {
				return followUpRound.receive(delta, streamFilter)
			},
		)
		if requestHasPictures(followUp) {
			// The upstream refused a picture of this context: later rounds
			// start from the request without it, so they do not hit it again.
			request = carryStrippedPictures(request, followUp, sentFollowUp)
		}
		followUp = sentFollowUp
		if streamErr != nil {
			// #665: the text this round already streamed stays in the reply,
			// as the person saw it; the stop or interruption note follows it.
			if streamed := followUpRound.streamed.String(); streamed != "" {
				assistant.Content = joinNarration(assistant.Content, streamed)
			}
			if orchestrator.turnCanceled(turnKey) {
				return finalizeStop()
			}
			if ctx.Err() != nil {
				// 进程停机：保留可恢复轮次，交给 RecoverTurns 续跑。
				return nil
			}
			if orchestrator.turnAbandoned(ctx, command) {
				// The session or the message was deleted under the turn.
				return nil
			}
			orchestrator.logger.Error(
				"对话工具续跑请求失败",
				"session_id", command.SessionID,
				"assistant_message_id", command.AssistantMessageID,
				"error", streamErr,
			)
			if errors.Is(streamErr, agents.ErrInlineImageOutput) {
				// #638: the reply so far stays; it says the model drew a
				// picture itself, and the turn is not failed.
				orchestrator.noteInlineImage(command, streamErr)
				inlineImageReply = true
			} else if len(assistant.ImageURLs) == 0 && toolErr == nil {
				failed = true
			}
			followUpErr = streamErr
			break
		}
		orchestrator.recordUsage(ctx, command, followUp.Model, next.Usage, followUp)
		emptyReply = orchestrator.noteEmptyReply(command, next, effectiveRound+1)
		if err := streamFilter.Flush(followUpRound.emitText); err != nil {
			if orchestrator.turnCanceled(turnKey) {
				return finalizeStop()
			}
			if ctx.Err() != nil {
				// 进程停机：保留可恢复轮次，交给 RecoverTurns 续跑。
				return nil
			}
			failed = true
			orchestrator.appendDone(ctx, command.SessionID)
			return err
		}
		completion = next
		checkpointResuming = false
		parsed = false
		// AGT-02：续跑请求累积——下一轮 followUp 基于本轮完整上下文，
		// 模型不再"失忆"重复已执行的工具调用。
		request = followUp
	}
	emptyText := orchestrator.text(ctx, orchestrator.schema.Presentation.Messages.AgentEmptyResponse)
	inlineText, _ := orchestrator.agentInlineImageMessage(ctx, agents.ErrInlineImageOutput)
	if note := orchestrator.endingNote(ctx, cutReply, roundsCapped); note != "" {
		// #665: a reply the upstream cut, or one stopped at the tool round
		// bound, keeps what it wrote and says why it ends there.
		emptyReply = false
		assistant.Content = joinNarration(assistant.Content, "\n\n"+note)
		if strings.TrimSpace(assistant.Content) == note {
			assistant.Content = note
		}
	}
	if inlineImageReply && inlineText != "" {
		note := orchestrator.withErrorReference(ctx, inlineText, assistant.ID)
		if assistant.Content == "" {
			assistant.Content = note
		} else {
			assistant.Content += "\n\n" + note
		}
	} else if emptyReply && emptyText != "" {
		// #638: the model's last round said nothing, also after its retry.
		// What the turn wrote and did before it stays; the reply says the
		// model stopped answering, so the person can regenerate it.
		note := orchestrator.withErrorReference(ctx, emptyText, assistant.ID)
		if assistant.Content == "" {
			assistant.Content = note
		} else {
			assistant.Content += "\n\n" + note
		}
	} else if assistant.Content == "" && len(assistant.ImageURLs) == 0 {
		assistant.Content = orchestrator.text(ctx, orchestrator.schema.Presentation.Messages.NoResult)
		// #120: a follow-up refused for a used-up balance or quota says so
		// instead of asking for a retry that cannot succeed.
		if message, exhausted := orchestrator.agentQuotaExhaustedMessage(ctx, followUpErr); exhausted {
			assistant.Content = message
		} else if message, refused := orchestrator.agentUpstreamHTTPMessage(ctx, followUpErr); refused {
			assistant.Content = message
		} else if message, broke := orchestrator.agentInterruptedMessage(ctx, followUpErr); broke {
			assistant.Content = message
		}
		if failed {
			assistant.Content = orchestrator.withErrorReference(ctx, assistant.Content, assistant.ID)
		}
	} else if message, broke := orchestrator.agentInterruptedMessage(ctx, followUpErr); broke {
		// #509: the follow-up broke off after its retries; the reply so far
		// stays and says it was cut, so the person knows to send it again.
		// A gateway that answered an error status is named with it (#547).
		if refused, named := orchestrator.agentUpstreamHTTPMessage(ctx, followUpErr); named {
			message = refused
		}
		assistant.Content += "\n\n" + orchestrator.withErrorReference(ctx, message, assistant.ID)
	}
	orchestrator.attachAssistantResults(context.WithoutCancel(ctx), command.OwnerID, &assistant)
	if err := orchestrator.service.UpdateMessage(
		context.WithoutCancel(ctx),
		command.OwnerID,
		assistant,
	); err != nil {
		failed = true
		orchestrator.appendDone(ctx, command.SessionID)
		return err
	}
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

// endingNote is the Schema note of a reply that ends early (#665): the round
// the upstream cut at its output limit or by its content filter, or the tool
// round bound. Empty when none applies or the Schema declares no text.
func (orchestrator *Orchestrator) endingNote(ctx context.Context, cutReply string, roundsCapped bool) string {
	messages := orchestrator.schema.Presentation.Messages
	switch {
	case roundsCapped && messages.ToolRoundsCapped != "":
		return strings.ReplaceAll(orchestrator.text(ctx, messages.ToolRoundsCapped),
			"{limit}", strconv.Itoa(orchestrator.schema.Agent.MaxToolRoundsPerTurn))
	case cutReply == agents.FinishLength:
		return orchestrator.text(ctx, messages.AgentReplyTruncated)
	case cutReply == agents.FinishContentFilter:
		return orchestrator.text(ctx, messages.AgentReplyFiltered)
	}
	return ""
}

// startFirstTurnTitle summarises the session title from the first user message
// in the background as soon as a new first turn starts. The turn never waits
// for it: the summary is a convenience that may fail without anyone noticing,
// and a failed or stopped turn does not cancel it. Only the
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
		defer orchestrator.recoverGoroutine("first-turn-title", "session_id", command.SessionID)
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
	// The title is written by the turn's own model and connection (#322), at
	// the level the model family declares for a title rather than the
	// session's: a title needs far less thinking than the turn it names
	// (#382).
	model := orchestrator.turnChatModel(command.Options)
	completion, err := orchestrator.agent.Complete(
		ctx,
		agents.Request{
			Model:           model.name,
			Connection:      model.connection,
			Temperature:     orchestrator.schema.Agent.Temperature,
			ReasoningEffort: orchestrator.turnEffort(model.name, catalog.ChatTurnEffortTitle),
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
	// Every block is a named, ordered section (#248): the order is code, and a
	// block that renders nothing is left out, so the same facts produce the
	// same prompt.
	basePrompt := orchestrator.schema.Agent.SystemPrompt
	domainContexts := orchestrator.schema.Agent.DomainContext
	if snapshot := configurationFromContext(ctx); snapshot != nil {
		basePrompt = snapshot.SystemPrompt
		domainContexts = snapshot.DomainContext
	}
	sections := &promptSectionList{}
	sections.add("base", basePrompt)
	if domainContext := domainContexts[options.CreativeDomain]; domainContext != "" {
		sections.add("domain", "\n\n"+domainContext)
	}
	registry, registryErr := orchestrator.registryForRequest(ctx)
	scope := orchestrator.agentScope.Normalize(options.AgentScope)
	if registryErr == nil {
		// A craft brief comes before the scope block: it teaches how to work in
		// a domain, while the scope only says which tools this turn offers. The
		// selection and the framed session notes stay at the end of the prompt.
		sections.add("craft", orchestrator.craftPrompt(registry, scope, options.CreativeDomain, options.ProductionBrief))
		sections.add("skill_directory", orchestrator.skillDirectoryPrompt(ctx, registry, scope, options))
		sections.add("concept_directions", orchestrator.conceptDirectionsPrompt(registry, scope))
		sections.add("session_parameters", orchestrator.sessionParametersPrompt(ownerID, sessionID))
		sections.add("candidates", orchestrator.candidatesPrompt(registry, scope))
		sections.add("scope", orchestrator.scopePrompt(registry, scope))
	}
	// The session's real approval mode follows the scope it must not be mixed
	// up with (#7).
	sections.add("approval", orchestrator.approvalModePrompt(ctx, ownerID, sessionID))
	// The 3D step facts follow the approval section and appear only in a
	// steps session (#330): the mode, the asset's used steps, the three
	// providers' balances and what one step costs.
	sections.add("step_policy", orchestrator.stepPolicyPrompt(ctx, ownerID, sessionID))
	// The scene-decomposition guidance (#331) appears only where the turn
	// carries a picture of a project whose deployment offers production
	// lists and propose_asset_list; it is server-injected like step_policy.
	indexedByMessage, lastUserMessage := orchestrator.indexHistoryImages(history)
	sections.add("asset_list_decompose", orchestrator.decomposePrompt(
		ctx, options, registry, registryErr, scope, indexedByMessage, lastUserMessage))
	if registryErr == nil {
		sections.add("skill", orchestrator.skillToolDirective(ctx, registry, options)+
			orchestrator.styleSkillDirective(ctx, options))
		sections.add("multiview", orchestrator.multiviewDirective(registry, options))
		sections.add("three_view_groups", orchestrator.threeViewGroupsPrompt(ctx, registry, options, ownerID, sessionID))
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
	sections.add("summary", orchestrator.summaryPrompt(latest))
	sections.add("compacted_handles", orchestrator.compactedHandlePrompt(history, indexedByMessage, summarized, lastUserMessage))
	// The ledger and the style anchor are projected from stored facts, not from
	// the message window, so compaction never changes them (#248). The style
	// lock text is read from the turn's frozen snapshot: the visual anchors
	// block states its state and picture count only, so one prompt never shows
	// two versions of the same user setting (#250).
	sections.add("work_ledger", orchestrator.workLedgerPrompt(ctx, ownerID, sessionID, history))
	sections.add("work_anchors", orchestrator.workAnchorsPrompt(ctx, ownerID, sessionID, options.StyleLock))
	sections.add("visual_anchors", orchestrator.visualAnchorsPrompt(options))
	sections.add("selection", orchestrator.selectionPrompt(options))
	sections.add("brief", orchestrator.briefPrompt(options))
	// The project block follows the brief and precedes the session settings;
	// its guide, conventions and @projectN assets are the frozen snapshot of
	// the turn, never the mutable project rows (#240).
	sections.add("project", orchestrator.projectPrompt(options))
	// The self-check state is stored session data, not a message-window
	// projection, so context compaction never changes it (#246). It sits
	// before the framed session notes, which stay the last settings block.
	sections.add("self_check", orchestrator.selfCheckPrompt(ctx, ownerID, sessionID, history))
	// The session's stored choices are also read from storage, not from the
	// message window, so compaction cannot lose them (#241). They precede the
	// settings summary, which stays the last settings block.
	sections.add("preferences", orchestrator.preferencesPrompt(ctx, ownerID, sessionID))
	sections.add("notes", orchestrator.notesPrompt(ctx, ownerID, sessionID))
	var choices []Choice
	if orchestrator.service != nil && sessionID != "" {
		choices, _ = orchestrator.service.ListChoices(ctx, ownerID, sessionID)
	}
	sections.add("choice", orchestrator.pendingChoicePrompt(choices))
	// Tasks the person stopped waiting for still run (#137): the Agent looks
	// them up instead of submitting the same paid generation again.
	sections.add("background_tasks", orchestrator.backgroundTasksPrompt(ctx, ownerID, sessionID, history))
	sections.add("harness", orchestrator.harnessPrompt(options.Harness, registry, scope))
	sections.add("reply_language", orchestrator.replyLanguagePrompt(ctx))
	systemPrompt := sections.render()
	request := agents.Request{
		Model:           orchestrator.turnModel(options),
		Connection:      orchestrator.turnChatModel(options).connection,
		Temperature:     orchestrator.schema.Agent.Temperature,
		ReasoningEffort: options.ReasoningEffort,
		// The frozen contract says which wire serves this turn and how its model
		// carries thinking, so the Adapter does not read today's profile for it
		// (#377).
		Thinking: options.Thinking,
		Messages: []agents.Message{{
			Role:    orchestrator.schema.Roles.System,
			Content: systemPrompt,
		}},
	}
	// The vision budget (#247) classifies the request's pictures by purpose.
	// The current turn's artboard snapshot is its primary input, so it spends
	// the canvas role's places before any history picture does (#16).
	budget := orchestrator.visionBudget(options)
	spend := visionSpend{}
	var canvasParts []agents.ContentPart
	var canvasShown map[int]bool
	if budget.Enabled && lastUserMessage > summarized && lastUserMessage >= 0 {
		canvasParts, canvasShown = orchestrator.canvasSnapshotParts(
			ctx, ownerID, history[lastUserMessage],
			budget.roleQuota(catalog.ChatVisionSourceCanvas))
		canvasTokens := budget.tokenCostFor(catalog.ChatVisionSourceCanvas)
		for _, part := range canvasParts {
			if part.Kind == orchestrator.schema.Context.ImagePartKind {
				spend.Images++
				spend.Tokens += canvasTokens
				spend.Bytes += int64(len(part.URL))
			}
		}
	}
	// #244: the frozen selection mask travels with a server-composed preview
	// picture, right after the newest user message, so the model sees exactly
	// which region the person painted. A composition failure keeps the mask's
	// text description and never blocks the turn.
	var selectionParts []agents.ContentPart
	if budget.Enabled && options.SelectionMask != nil && orchestrator.selectionPreview != nil &&
		lastUserMessage > summarized && lastUserMessage >= 0 &&
		history[lastUserMessage].Role == orchestrator.schema.Roles.User {
		if snapshot, found := canvasSnapshotAttachment(options); found {
			if url, ok := orchestrator.selectionPreview(ctx, ownerID, snapshot.URL,
				options.SelectionMask.MaskAssetID); ok {
				url = orchestrator.pictures.URL(url)
				selectionParts = append(selectionParts,
					agents.ContentPart{
						Kind:   orchestrator.schema.Context.ImagePartKind,
						URL:    url,
						Detail: orchestrator.schema.Context.CanvasImageDetail,
					},
					agents.ContentPart{
						Kind: orchestrator.schema.Context.TextPartKind,
						Text: orchestrator.schema.Context.SelectionMaskPreviewLabel,
					})
				// The preview is one more picture of the canvas role: it spends
				// the same role quota and the shared request budget as the
				// snapshot, so the vision plan can drop the lowest priority
				// pictures instead of silently exceeding the bound.
				spend.Images++
				spend.Tokens += budget.tokenCostFor(catalog.ChatVisionSourceCanvas)
				spend.Bytes += int64(len(url))
			}
		}
	}
	plan := orchestrator.planVision(
		ctx, ownerID, sessionID, history, indexedByMessage, summarized, lastUserMessage,
		options, spend,
	)
	// The newest generated pictures (a 3D result's thumbnail included) are shown
	// to the model too, so "the one you just made" can be looked at. They ride
	// with the latest user message: image parts are not valid in assistant turns.
	generatedParts := orchestrator.plannedGeneratedParts(plan)
	styleParts := orchestrator.plannedStyleParts(plan)
	overflowNote := budget.overflowText(plan.Dropped) + budget.skippedText(plan.Skipped)
	visionOffNote := ""
	if plan.Off {
		visionOffNote = budget.VisionOff
	}
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
			// A selected picture is already budgeted and inlined by the plan.
			if pick, picked := plan.pickFor(messageIndex, image.slot-1); picked &&
				message.Role == orchestrator.schema.Roles.User {
				if len(parts) == 0 && content != "" {
					parts = append(parts, agents.ContentPart{
						Kind: orchestrator.schema.Context.TextPartKind,
						Text: content,
					})
					content = ""
				}
				parts = append(parts, agents.ContentPart{
					Kind:   orchestrator.schema.Context.ImagePartKind,
					URL:    pick.URL,
					Detail: pick.Detail,
				})
				// The picture itself carries no name; its handle label does.
				if pick.Label != "" {
					parts = append(parts, agents.ContentPart{
						Kind: orchestrator.schema.Context.TextPartKind,
						Text: pick.Label,
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
		shown := map[int]bool(nil)
		if messageIndex == lastUserMessage && message.Role == orchestrator.schema.Roles.User {
			shown = canvasShown
		}
		appendText(orchestrator.unpicturedLabelsExcept(message, shown))
		if len(shown) > 0 {
			if len(parts) == 0 && content != "" {
				parts = append(parts, agents.ContentPart{
					Kind: orchestrator.schema.Context.TextPartKind,
					Text: content,
				})
				content = ""
			}
			parts = append(parts, canvasParts...)
		}
		if messageIndex == lastUserMessage && message.Role == orchestrator.schema.Roles.User {
			// A card answered on the card: the Agent reads the exact next call (#321).
			appendText(orchestrator.choiceAnswerText(choices, message.ID))
			// A plan approved on its card: the Agent runs it now (#134).
			if options.PlanContinuation != "" {
				appendText(orchestrator.schema.ApprovalPolicy.Budget.Continuation.AgentTemplate)
			}
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
		if messageIndex == lastUserMessage && len(styleParts) > 0 {
			if len(parts) == 0 && content != "" {
				parts = append(parts, agents.ContentPart{
					Kind: orchestrator.schema.Context.TextPartKind,
					Text: content,
				})
				content = ""
			}
			parts = append(parts, styleParts...)
		}
		if messageIndex == lastUserMessage && len(selectionParts) > 0 {
			if len(parts) == 0 && content != "" {
				parts = append(parts, agents.ContentPart{
					Kind: orchestrator.schema.Context.TextPartKind,
					Text: content,
				})
				content = ""
			}
			parts = append(parts, selectionParts...)
		}
		if messageIndex == lastUserMessage && message.Role == orchestrator.schema.Roles.User {
			if overflowNote != "" {
				appendText(overflowNote)
			}
			if visionOffNote != "" {
				appendText(visionOffNote)
			}
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
		// #413: describe hands out the request's shared tool list; the request
		// gets its own copy because the tool notes append to descriptions.
		request.Tools = append([]agents.Tool(nil), registry.describe(scope)...)
		// #382: the turn plan narrows that table once more, by what this turn
		// froze as runnable; never to nothing.
		request.Tools = registry.narrowToPlan(request.Tools, options.TurnPlan)
		orchestrator.noteTools(registry, request.Tools, options)
		request.ScopeWithheld = append([]string(nil), registry.withheld(scope)...)
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
//
// A tool call the model starts writing replaces the status once per round with
// the Schema preparing-tool status (#375): the model writes the call's
// arguments for a long while before the call can run, and without it the
// thinking status would stay on screen until the generating status of the run.
type thinkingRound struct {
	orchestrator *Orchestrator
	ctx          context.Context
	sessionID    string
	active       bool
	toolShown    bool
	// following and shown are set on a later round of a reply (#402): shown is
	// the reply's text up to the round's own text, so the round's first visible
	// text can start a new paragraph. Once that text has been sent, following
	// is false and shown is no longer kept.
	following bool
	shown     string
	// streamed is the round's visible text as the filter passed it on, before
	// the paragraph break (#665): a round that breaks off keeps it in the
	// reply with joinNarration, which puts the same break.
	streamed strings.Builder
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

// announceToolCall shows the preparing-tool status once per round, replacing
// the thinking status (#375). It is best effort and optional like announce: a
// Schema without the text shows nothing.
func (round *thinkingRound) announceToolCall() {
	status := round.orchestrator.schema.Presentation.Status.PreparingTool
	if round.toolShown || status == "" {
		return
	}
	round.toolShown = true
	round.active = false
	round.orchestrator.statusEvent(round.ctx, round.sessionID, status)
}

// receive handles one adapter delta: a reasoning signal (re)announces the
// status, a tool call signal replaces it, content goes through the round's
// DSML filter to emitText.
func (round *thinkingRound) receive(delta agents.Delta, filter *dsml.Filter) error {
	if delta.Thinking {
		round.announce()
	}
	if delta.ToolCalling {
		round.announceToolCall()
	}
	if delta.Content == "" {
		return nil
	}
	return filter.Push(delta.Content, round.emitText)
}

// continueAfter marks the round as a later round of a reply whose text so far
// is shown (#402). The round's first visible text is sent with the paragraph
// break joinNarration puts in the stored message.
func (round *thinkingRound) continueAfter(shown string) {
	round.following, round.shown = true, shown
}

// emitText appends visible text; text that is not blank ends the status.
func (round *thinkingRound) emitText(content string) error {
	round.streamed.WriteString(content)
	if strings.TrimSpace(content) != "" {
		round.active = false
	}
	if round.following {
		// The break goes before the first visible character and after any
		// whitespace the round opened with (joinNarration), so the events joined
		// are the stored text.
		visible := strings.TrimLeftFunc(content, unicode.IsSpace)
		leading := content[:len(content)-len(visible)]
		if visible == "" {
			round.shown += content
		} else {
			content = leading + narrationBreak(round.shown+leading) + visible
			round.following, round.shown = false, ""
		}
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
	err := orchestrator.service.UpdateMessage(
		context.WithoutCancel(ctx),
		ownerID,
		*assistant,
	)
	// #665: the stream ends whether or not the failure text was stored, or
	// its client would wait for an event that never comes.
	orchestrator.appendDone(ctx, assistant.SessionID)
	return err
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
	produced *Message,
	budget visionBudget,
	spend *visionSpend,
) ([]agents.Message, []string, map[string]string, error) {
	registry, registryErr := orchestrator.registryForRequest(ctx)
	if registryErr != nil {
		return nil, nil, nil, registryErr
	}
	results := append([]agents.Message(nil), turn.Checkpoint.Completed...)
	imageURLs := append([]string(nil), turn.Checkpoint.ImageURLs...)
	// A resumed round already ran some calls: their pictures are this reply's
	// results too, numbered as they were when the Agent read them.
	orchestrator.recordTurnResult(ctx, command.OwnerID, produced, "", "", turn.Checkpoint.ImageURLs)
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
			if errors.Is(err, ErrTurnCheckpointConflict) {
				// 写入输给另一个写入方：轮次归它（或重启恢复）收尾，不是
				// 我们该结束的。
				return err
			}
			// #429: 编码、锁、存储等其他失败没有人会重试。挂起只会让会话
			// 一直忙、SSE 一直开着，直到下一次重启；回合以 failed 结束，
			// 已提交的任务留在生成内核并保留消息上的绑定。
			return errors.Join(ErrTurnCheckpointUnsaved, err)
		}
		turn.CheckpointVersion, turn.State = version, state
		return nil
	}
	// 上一次挂起留下的等待项（旧单字段检查点读作一项）：带回各调用的任务
	// 绑定、等待截止和排队起点。本轮提交阶段新产生的在途项收在 inflight。
	waiting := turn.Checkpoint.WaitingCalls()
	turn.Checkpoint.SetWaiting(waiting)
	requestTimeout := time.Duration(orchestrator.schema.Agent.RequestTimeoutSeconds) * time.Second
	maxParallel := max(1, orchestrator.schema.Agent.MaxParallelGenerationSubmissions)
	// suspendUnconfirmed suspends the turn on a paid call whose submission
	// could be neither confirmed nor ruled out, so a replay looks for its Task
	// again. The first suspension is stored with the call; once the Schema
	// request timeout has passed since then the turn stops replaying and ends
	// failed (#516). A Task that was accepted stays with the kernel.
	suspendUnconfirmed := func(callID string) error {
		now := time.Now()
		since, found := turn.Checkpoint.unconfirmedSince(callID)
		if !found {
			since = now
			turn.Checkpoint.Unconfirmed = append(turn.Checkpoint.Unconfirmed,
				UnconfirmedCall{CallID: callID, Since: since})
			if err := persist(orchestrator.service.TurnRunningState()); err != nil {
				return err
			}
		}
		if requestTimeout > 0 && !now.Before(since.Add(requestTimeout)) {
			orchestrator.logger.Warn("付费调用的提交结果超时仍无法确认，回合不再重放",
				"session_id", command.SessionID, "assistant_message_id", command.AssistantMessageID,
				"call_id", callID, "unconfirmed_since", since)
			return ErrTurnSubmissionUnconfirmed
		}
		return ErrTurnSuspended
	}
	var inflight []WaitingCall
	// #549: what this turn may submit when it answers a direction card on the
	// card, read from storage every round so a resumed turn is scoped alike.
	choiceScope := orchestrator.turnChoiceScope(ctx, command.OwnerID, command.SessionID, history)
	// 提交阶段：按调用顺序逐个执行。付费步骤提交后若要等待任务，不再立即
	// 挂起轮次，记为在途并继续提交同批互不依赖的下一个付费步骤；本批提交
	// 完（或遇到必须按序处理的调用）后，由循环之后的等待阶段一次性挂起。
	for callIndex := len(results); callIndex < len(calls); callIndex++ {
		call := calls[callIndex]
		if slices.Contains(turn.Checkpoint.MalformedCalls, call.ID) {
			// #378: the model wrote this call's arguments as invalid JSON, so
			// the tool does not run; the error is its result and the model
			// writes the call again. This is an ordinary tool result, not a
			// failed tool, and it counts as a call of the reply like any
			// call. Like every call that is answered instead of
			// submitted, it waits its turn behind a call still in flight. It
			// was logged when the model wrote it (vetToolArguments).
			if len(inflight) > 0 {
				break
			}
			invalidJSON := slices.Contains(turn.Checkpoint.InvalidJSONCalls, call.ID)
			results = append(results, agents.Message{
				Role:       orchestrator.schema.Roles.Tool,
				Content:    orchestrator.toolArgumentsInvalidText(ctx, call, invalidJSON),
				ToolCallID: call.ID,
			})
			turn.Checkpoint.Completed = append(turn.Checkpoint.Completed, results[len(results)-1])
			turn.Checkpoint.markUnexecuted(call.ID)
			waiting = removeWaiting(waiting, call.ID)
			turn.Checkpoint.SetWaiting(waiting)
			if err := persist(orchestrator.service.TurnRunningState()); err != nil {
				return results, imageURLs, taskBindings, err
			}
			continue
		}
		frozenKey := ""
		for _, stored := range storedCalls {
			if stored.ID == call.ID {
				frozenKey = stored.IdempotencyKey
				break
			}
		}
		boundTaskID := boundTasks[call.ID]
		waitDeadline := time.Time{}
		entry, hasEntry := waitingEntry(waiting, call.ID)
		if hasEntry && entry.TaskID != "" {
			if boundTaskID == "" {
				boundTaskID = entry.TaskID
			}
			waitDeadline = entry.Deadline
		}
		priorGenerationCalls := turn.Checkpoint.priorGenerationUnits(call.ID, boundTasks, taskBindings)
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
					return results, imageURLs, taskBindings, suspendUnconfirmed(call.ID)
				}
				if candidate != "" {
					boundTaskID = candidate
					tool, found = registry.resolve(call.Name, true)
				}
			}
		}
		batching := len(inflight) > 0
		if batching && (!found || len(inflight) >= maxParallel ||
			orchestrator.scopeRefuses(registry, call.Name, command.Options.AgentScope, boundTaskID != "") ||
			!paidTool(tool.Execution) || dependsOnInflight(call, inflight)) {
			// 同批已有在途调用：只继续提交付费步骤，且不超过单批并发上限。
			// 其余调用（控制类、只读类、未知工具）保持顺序语义，留到在途
			// 调用的回执补齐之后再处理。
			break
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
			if boundTaskID == "" {
				turn.Checkpoint.markUnexecuted(call.ID)
			}
			waiting = removeWaiting(waiting, call.ID)
			turn.Checkpoint.SetWaiting(waiting)
			if err := persist(orchestrator.service.TurnRunningState()); err != nil {
				return results, imageURLs, taskBindings, err
			}
			continue
		}
		if !found {
			// #378: a name the model made up, or a tool this reply is not
			// offered. Like a scope refusal this is an ordinary tool result,
			// not a failed tool: the Agent reads it in the turn's locale and
			// picks another tool, and the turn is not marked failed.
			orchestrator.logger.Warn("模型调用了本回合不可用的工具，已把错误交还模型",
				"session_id", command.SessionID, "assistant_message_id", command.AssistantMessageID,
				"call_id", call.ID, "tool", call.Name)
			// Strict tool-call protocols require a result for every call, even
			// when the name was unknown or not allowed in this domain.
			results = append(results, agents.Message{
				Role:       orchestrator.schema.Roles.Tool,
				Content:    orchestrator.toolUnavailableText(ctx, call.Name),
				ToolCallID: call.ID,
			})
			turn.Checkpoint.Completed = append(turn.Checkpoint.Completed, results[len(results)-1])
			if boundTaskID == "" {
				turn.Checkpoint.markUnexecuted(call.ID)
			}
			waiting = removeWaiting(waiting, call.ID)
			turn.Checkpoint.SetWaiting(waiting)
			if err := persist(orchestrator.service.TurnRunningState()); err != nil {
				return results, imageURLs, taskBindings, err
			}
			continue
		}
		if boundTaskID == "" {
			// #549: the turn that answers a direction card on the card runs the
			// chosen option's one step, once. Any other paid step is answered,
			// not run: an ordinary tool result in the Schema's words, nothing
			// submitted and nothing charged, and the Agent reports and asks.
			if refusal := orchestrator.choiceStepRefusal(ctx, command, choiceScope, call, tool,
				earlierCalls(storedCalls, call.ID), taskBindings); refusal != "" {
				orchestrator.logger.Info("方向卡回合拒绝了未选择的付费步骤",
					"session_id", command.SessionID, "assistant_message_id", command.AssistantMessageID,
					"call_id", call.ID, "tool", call.Name, "choice_id", choiceScope.ChoiceID, "option_id", choiceScope.OptionID)
				results = append(results, agents.Message{
					Role:       orchestrator.schema.Roles.Tool,
					Content:    refusal,
					ToolCallID: call.ID,
				})
				turn.Checkpoint.Completed = append(turn.Checkpoint.Completed, results[len(results)-1])
				turn.Checkpoint.markUnexecuted(call.ID)
				waiting = removeWaiting(waiting, call.ID)
				turn.Checkpoint.SetWaiting(waiting)
				if err := persist(orchestrator.service.TurnRunningState()); err != nil {
					return results, imageURLs, taskBindings, err
				}
				continue
			}
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
				PriorGenerationCalls: priorGenerationCalls,
				ConsentWaitDeadline:  command.ConsentWaitDeadline,
				Call:                 call,
				Tool:                 tool,
				History:              history,
				Turn:                 copyMessage(*produced),
				Options:              command.Options,
				ChoiceScope:          choiceScope,
				EarlierCalls:         earlierCalls(storedCalls, call.ID),
				ToolAvailable: func(name string) bool {
					candidate, exists := registry.byName[name]
					return exists && registry.permits(candidate, false) &&
						!orchestrator.scopeRefuses(registry, name, command.Options.AgentScope, false)
				},
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
		// Before any branch below: a suspended, queued or discarded result
		// still counted against the turn (#665).
		turn.Checkpoint.recordGenerationUnits(call.ID, result.GenerationUnits)
		if err != nil && !result.NoSubmission && result.TaskID == "" && paidTool(tool.Execution) {
			// Task acceptance may have committed while the response or chat
			// binding was lost. Keep this call outside Checkpoint.Completed so
			// restart can read its durable grant/idempotency binding and observe
			// the original paid Task. Never plan a fresh paid call here. The
			// replays are bounded (#516): see suspendUnconfirmed.
			return results, imageURLs, taskBindings, suspendUnconfirmed(call.ID)
		}
		turn.Checkpoint.clearUnconfirmed(call.ID)
		if err != nil && result.AwaitTask && result.TaskID != "" {
			// The Task identity is known, but observation or chat binding failed.
			// Preserve the original Task for the waiting scanner to retry.
			err = nil
		}
		if err != nil && result.NoSubmission && result.RetryAfter > 0 && result.TaskID == "" {
			// 生成内核因在途准入已满拒绝了这次提交，确定没有提交：沿用同一冻结
			// 幂等键排队重试，总时长以 Schema 请求超时为上限（自第一次被拒起算）；
			// 超限后落到下面的失败回执。
			now := time.Now()
			since, firstRefusal := now, true
			if hasEntry && entry.TaskID == "" && !entry.QueuedSince.IsZero() {
				since, firstRefusal = entry.QueuedSince, false
			}
			if deadline := since.Add(requestTimeout); now.Before(deadline) {
				retryAt := now.Add(result.RetryAfter)
				if retryAt.After(deadline) {
					retryAt = deadline
				}
				if firstRefusal {
					orchestrator.statusEvent(ctx, command.SessionID, orchestrator.schema.Presentation.Status.QueuedSubmission)
				}
				inflight = append(inflight, WaitingCall{CallID: call.ID, Deadline: deadline,
					RetryAt: retryAt, QueuedSince: since})
				// 准入已满时后面的提交多半同样被拒，留到重试时再试。
				break
			}
			if result.AgentContent == "" && result.Error == "" {
				// 排队超限后这就是最终回执：工具没给文案时也不能交给模型空回执，
				// 退回通用生成失败文案（与生成工具的 executionFailure 一致）。
				result.AgentContent = orchestrator.schema.Presentation.Messages.GenerationError
			}
		}
		if result.Suspend {
			// AGT-12：等待用户决定的工具已登记状态；挂起返回并保留
			// 已完成部分的结果，未执行的调用在唤醒重放时重新执行。
			// 同批已在途的任务先把等待项落库，保留各自的截止时间。挂起调用
			// 本身计入的生成单位也落库（#665）：只在内存里的话，进程在等批准
			// 时重启，多步处理计划只按 1 次计入。
			if len(inflight) > 0 {
				turn.Checkpoint.SetWaiting(mergeWaiting(inflight, waiting, results))
			}
			if len(inflight) > 0 || result.GenerationUnits > 0 {
				if saveErr := persist(orchestrator.service.TurnRunningState()); saveErr != nil {
					return results, imageURLs, taskBindings, saveErr
				}
			}
			return results, imageURLs, taskBindings, ErrTurnSuspended
		}
		if result.AwaitTask {
			if result.TaskID == "" {
				return results, imageURLs, taskBindings, ErrTurnCheckpointConflict
			}
			taskBindings[call.ID] = result.TaskID
			if result.WaitUntil.IsZero() {
				// Older executors may fail observation without returning a deadline.
				// Preserve this call's durable deadline; bound a first read failure
				// with the Schema request timeout rather than waiting forever.
				result.WaitUntil = waitDeadline
				if result.WaitUntil.IsZero() {
					result.WaitUntil = time.Now().Add(requestTimeout)
				}
			}
			inflight = append(inflight, WaitingCall{CallID: call.ID, TaskID: result.TaskID, Deadline: result.WaitUntil})
			continue
		}
		if batching {
			// 前面还有在途调用，这条回执暂时排不进有序的 Completed。已有任务
			// 的调用记为在途（唤醒后由绑定任务重新观察得到同一回执）；没有
			// 任务的结果（拒绝、校验失败）丢弃，唤醒后按序重新得到。
			if result.TaskID != "" {
				deadline := waitDeadline
				if deadline.IsZero() {
					deadline = time.Now().Add(requestTimeout)
				}
				taskBindings[call.ID] = result.TaskID
				inflight = append(inflight, WaitingCall{CallID: call.ID, TaskID: result.TaskID, Deadline: deadline})
				continue
			}
			break
		}
		combined = errors.Join(combined, err)
		if err != nil {
			turn.Checkpoint.HadToolError = true
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
		// The reply had this many pictures before this call. recordTurnResult
		// appends the call's pictures after them, so each picture's attachment
		// slot is known even when verification skipped one.
		imagesBefore := len(produced.ImageURLs)
		handles := orchestrator.recordTurnResult(ctx, command.OwnerID, produced, call.ID, taskBindings[call.ID], result.ImageURLs)
		content = orchestrator.withPresentedHandles(content, result, *produced, imagesBefore, handles)
		// #665: the result text the model reads is bounded by the Schema.
		content = orchestrator.capToolResult(ctx, call.Name, content)
		// #246: the self-check record stores the pictures a check produced by
		// their attachment handles, once the reply has named them.
		orchestrator.attachSelfCheckHandles(ctx, command.OwnerID, command.SessionID, result.Summary, handles)
		// A read-only tool's compact summary travels with its call, so the
		// conversation can show one line per check (#103). The handles its
		// pictures received belong in the summary too, so the frontend binds
		// each picture to its view instead of guessing by position (#103
		// review F9). Keep it on the checkpoint as well: a later resume must
		// not lose it.
		orchestrator.recordToolSummary(produced, &turn.Checkpoint.Assistant, call.ID,
			orchestrator.withSummaryHandles(result.Summary, handles))
		reply := agents.Message{
			Role:       orchestrator.schema.Roles.Tool,
			Content:    content,
			ToolCallID: call.ID,
		}
		orchestrator.attachToolPictures(ctx, command.OwnerID, &reply,
			picturesWithHandles(*produced, imagesBefore, result.Pictures), budget, spend)
		results = append(results, reply)
		turn.Checkpoint.Completed = append(turn.Checkpoint.Completed, results[len(results)-1])
		turn.Checkpoint.ImageURLs = append(turn.Checkpoint.ImageURLs, result.ImageURLs...)
		waiting = removeWaiting(waiting, call.ID)
		turn.Checkpoint.SetWaiting(waiting)
		if err := persist(orchestrator.service.TurnRunningState()); err != nil {
			return results, imageURLs, taskBindings, err
		}
	}
	if len(inflight) > 0 {
		// 等待阶段：本批已提交完，一次性进入 waiting_task。扫描器在全部在途
		// 任务终态（或各自截止）后唤醒，回执按原调用顺序回填。
		turn.Checkpoint.SetWaiting(mergeWaiting(inflight, waiting, results))
		if err := persist(orchestrator.service.TurnWaitingTaskState()); err != nil {
			return results, imageURLs, taskBindings, err
		}
		return results, imageURLs, taskBindings, ErrTurnAwaitTask
	}
	return results, imageURLs, taskBindings, combined
}

// dependsOnInflight reports whether a call names the Task or the call ID of a
// step still in flight: it needs that step's result, so it is not submitted
// next to it but after the receipt exists. A handle a step's result will get
// is not known before the receipt, so a call using one is refused without
// submitting and is run again, in order, after the wait.
func dependsOnInflight(call agents.ToolCall, inflight []WaitingCall) bool {
	arguments := string(call.Arguments)
	for _, entry := range inflight {
		if entry.TaskID != "" && strings.Contains(arguments, entry.TaskID) ||
			strings.Contains(arguments, entry.CallID) {
			return true
		}
	}
	return false
}

func waitingEntry(list []WaitingCall, callID string) (WaitingCall, bool) {
	for _, entry := range list {
		if entry.CallID == callID {
			return entry, true
		}
	}
	return WaitingCall{}, false
}

func removeWaiting(list []WaitingCall, callID string) []WaitingCall {
	kept := make([]WaitingCall, 0, len(list))
	for _, entry := range list {
		if entry.CallID != callID {
			kept = append(kept, entry)
		}
	}
	return kept
}

// mergeWaiting is the waiting list to persist: the calls in flight of this
// pass plus earlier entries the pass did not reach, minus calls that already
// have their receipt.
func mergeWaiting(inflight, prior []WaitingCall, results []agents.Message) []WaitingCall {
	merged := append([]WaitingCall(nil), inflight...)
	answered := make(map[string]struct{}, len(results))
	for _, result := range results {
		answered[result.ToolCallID] = struct{}{}
	}
	for _, entry := range prior {
		if _, done := answered[entry.CallID]; done {
			continue
		}
		if _, present := waitingEntry(merged, entry.CallID); !present {
			merged = append(merged, entry)
		}
	}
	return merged
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
		Role:      orchestrator.schema.Roles.Assistant,
		Content:   completion.Content,
		ToolCalls: completion.ToolCalls,
		Reasoning: reasoningItems(base, completion),
	})
	messages = append(messages, orchestrator.withToolPictures(toolResults)...)
	// #377: only the reasoning of the request's own source goes back. Every item
	// names the connection, model, wire and kind it came from, so a model is
	// never handed another model's thinking — not even after a restart into a
	// changed Catalog. The assistant message itself always goes, so a vendor
	// that requires the field on a tool-call message keeps a legal request.
	filterReasoning(messages, agents.ForRequest(base))
	base.Messages = messages
	// B4 工具循环：续跑请求保留工具定义，模型可在同一轮内
	//（例如同意获批后）继续调用生成工具；调用次数不设上限（#553）。
	return base
}

// reasoningItems is what a reply's reasoning becomes on the assistant message
// of the same tool loop: one item marked with the source it came from, so that
// only the same connection, model, wire and kind is given it back (#377). A
// reply without reasoning carries none.
func reasoningItems(
	request agents.Request,
	completion agents.Completion,
) []agents.ReasoningItem {
	if completion.ReasoningContent == "" {
		return nil
	}
	source := agents.ForRequest(request)
	return []agents.ReasoningItem{{
		Connection: source.Connection,
		Model:      source.Model,
		Wire:       source.Wire,
		Kind:       source.Kind,
		Content:    completion.ReasoningContent,
	}}
}

// filterReasoning drops the reasoning items a request may not carry (#377):
// only items of the request's own source stay, so a model is never given
// another connection's, another model's, another wire's or another kind's
// thinking. Messages keep their place whatever is left of them.
func filterReasoning(messages []agents.Message, source agents.Source) {
	for index := range messages {
		var kept []agents.ReasoningItem
		for _, item := range messages[index].Reasoning {
			if item.Matches(source) {
				kept = append(kept, item)
			}
		}
		messages[index].Reasoning = kept
	}
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

// freezeToolCallKeys freezes each new call's paid-operation identity from the
// turn's own registry (#453), so a tool the Schema does not declare (an MCP
// preset tool of the frozen Agent configuration) is looked up in the same view
// the call will run from. A tool that is still unknown there gets no key, which
// is the explicit unreconciled state the stop path protects.
func (orchestrator *Orchestrator) freezeToolCallKeys(ctx context.Context, sessionID string, calls, storedCalls []ToolCall) {
	registry := orchestrator.turnRegistry(ctx)
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
		tool, found := registry.byName[call.Name]
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
				if len(assistant.ToolCalls[index].Summary) == 0 {
					assistant.ToolCalls[index].Summary = call.Summary
				}
			}
		}
		return
	}
}

// recordToolSummary stores one tool's compact result on the call in the message
// being built and on the checkpointed copy of that message.
func (orchestrator *Orchestrator) recordToolSummary(message, checkpoint *Message, callID string, summary json.RawMessage) {
	if len(summary) == 0 || callID == "" {
		return
	}
	set := func(calls []ToolCall) {
		for index := range calls {
			if calls[index].ID == callID {
				calls[index].Summary = summary
				return
			}
		}
	}
	if message != nil {
		set(message.ToolCalls)
	}
	if checkpoint != nil {
		set(checkpoint.ToolCalls)
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
	if len(target.Summary) == 0 {
		target.Summary = stored.Summary
	}
	target.Background = target.Background || stored.Background
}

// streamAgent streams one Agent request. When the upstream refused a request
// that carried pictures, it sends the request once more with the pictures
// replaced by a Schema note, so the Agent can answer knowing it could not see
// them instead of the turn failing:
//   - the upstream's content-safety review (#35): every picture;
//   - a picture the model read and refused as a bad picture (#497, #522): the
//     pictures the Schema's size bounds show to be unreadable if the request has
//     any, else every picture; a refusal that remains is answered once more with
//     every picture. The pictures stripped are remembered by the session, so no
//     later round or turn sends them again;
//   - a model that takes no picture input (#253, #522): every picture, for this
//     request only, because the next model the person picks may read them.
//
// It returns the request actually sent. The refusal comes before any delta, so
// nothing was streamed from the refused attempt. A refusal that is none of
// these (a refused tool declaration, a context too long) is returned as it is.
func (orchestrator *Orchestrator) streamAgent(
	ctx context.Context,
	command ProcessTurn,
	request agents.Request,
	emit func(agents.Delta) error,
) (agents.Completion, agents.Request, error) {
	completion, err := orchestrator.agent.Stream(ctx, request, emit)
	refusal := classifyPictureRefusal(err)
	if refusal == pictureRefusalNone || !requestHasPictures(request) {
		return completion, request, err
	}
	switch refusal {
	case pictureRefusalContent:
		if orchestrator.schema.Agent.ModeratedImageNote == "" {
			return completion, request, err
		}
		orchestrator.logger.Warn(
			"Agent 请求的图片被上游内容审核拦截，去掉图片后重试",
			"session_id", command.SessionID,
			"assistant_message_id", command.AssistantMessageID,
			"error", err,
		)
		stripped := orchestrator.withoutPictures(ctx, request, orchestrator.contentRejectedReasons(ctx, err))
		completion, err = orchestrator.agent.Stream(ctx, stripped, emit)
		return completion, stripped, err
	case pictureRefusalInvalid:
		return orchestrator.retryWithoutInvalidPictures(ctx, command, request, err, emit)
	case pictureRefusalUnsupported:
		if orchestrator.schema.Agent.ImagesUnsupportedNote == "" {
			return completion, request, err
		}
		orchestrator.logger.Warn(
			"Agent 模型不接受图片输入，去掉图片后重试（不记入会话）",
			"session_id", command.SessionID,
			"assistant_message_id", command.AssistantMessageID,
			"error", err,
		)
		stripped := orchestrator.replacePictures(
			request, orchestrator.text(ctx, orchestrator.schema.Agent.ImagesUnsupportedNote), nil)
		completion, err = orchestrator.agent.Stream(ctx, stripped, emit)
		return completion, stripped, err
	}
	return completion, request, err
}

// retryWithoutInvalidPictures answers an upstream refusal of a picture it read
// (#522). The refusal does not say which picture, so the pictures the Schema's
// bounds show to be unreadable go first when the request has some; a refusal
// that remains, or a request with none, strips every picture for this request.
// Only pictures known to be bad are remembered by the session: those the
// Schema's bounds name, or the one picture of a request that has only one. A
// request stripped of pictures it could not tell apart, or refused over their
// number, leaves the session's memory alone, and the Agent is told that the
// pictures were skipped without being judged. A failure of another kind ends
// the retry and is returned as it is.
func (orchestrator *Orchestrator) retryWithoutInvalidPictures(
	ctx context.Context,
	command ProcessTurn,
	request agents.Request,
	refused error,
	emit func(agents.Delta) error,
) (agents.Completion, agents.Request, error) {
	agent := orchestrator.schema.Agent
	if agent.InvalidImageNote == "" {
		return agents.Completion{}, request, refused
	}
	reasons := orchestrator.imageInvalidReasons(ctx, refused)
	confirmedNote := orchestrator.text(ctx, agent.InvalidImageNote) + reasons
	unsureNote := confirmedNote
	if agent.InvalidImageUnsureNote != "" {
		unsureNote = orchestrator.text(ctx, agent.InvalidImageUnsureNote) + reasons
	}
	every := requestPictureKeys(request)
	suspects := orchestrator.unreadablePictureKeys(request)
	// confirmed are the pictures known to be bad: the ones the Schema's bounds
	// show to be unreadable, or the only picture of the request.
	confirmed := suspects
	if len(every) == 1 {
		confirmed = map[string]bool{every[0]: true}
	}
	stages := []map[string]bool{nil}
	if len(suspects) > 0 && len(suspects) < len(every) {
		stages = []map[string]bool{suspects, nil}
	}
	var completion agents.Completion
	err := refused
	sent := request
	for _, stage := range stages {
		count := len(stage)
		note := confirmedNote
		if stage == nil {
			count = len(every)
			if len(confirmed) < len(every) {
				note = unsureNote
			}
		}
		orchestrator.logger.Warn(
			"Agent 请求的图片被上游模型拒绝读取，去掉图片后重试",
			"session_id", command.SessionID,
			"assistant_message_id", command.AssistantMessageID,
			"stripped_pictures", count,
			"error", err,
		)
		sent = orchestrator.replacePictures(sent, note, stage)
		completion, err = orchestrator.agent.Stream(ctx, sent, emit)
		if err == nil {
			orchestrator.rememberRefusedPictures(ctx, command, refusedPictureKeys(every, confirmed, refused))
			return completion, sent, nil
		}
		if classifyPictureRefusal(err) != pictureRefusalInvalid {
			break
		}
	}
	return completion, sent, err
}

// refusedPictureKeys are the keys of the pictures a request was answered
// without that the session should remember: the confirmed ones, in request
// order. A refusal over the number of pictures says nothing about any of them,
// so none is remembered.
func refusedPictureKeys(every []string, confirmed map[string]bool, refused error) []string {
	if slices.Contains(agents.ImageInvalidReasons(refused), "count") {
		return nil
	}
	var keys []string
	for _, key := range every {
		if confirmed[key] {
			keys = append(keys, key)
		}
	}
	return keys
}

// rememberRefusedPictures stores the pictures a request was answered without,
// so the session no longer sends them. A failed write is logged: the next
// request that carries them is refused and stripped again.
func (orchestrator *Orchestrator) rememberRefusedPictures(ctx context.Context, command ProcessTurn, keys []string) {
	maximum := orchestrator.visionInvalidPolicy().MaximumExcluded
	if orchestrator.service == nil || maximum <= 0 || len(keys) == 0 {
		return
	}
	stored, err := orchestrator.service.ExcludeVisionPictures(
		context.WithoutCancel(ctx), command.OwnerID, command.SessionID, keys, maximum)
	if err != nil {
		orchestrator.logger.Warn(
			"记录会话中被上游拒绝的图片失败",
			"session_id", command.SessionID,
			"pictures", len(keys),
			"error", err,
		)
		return
	}
	if !stored {
		orchestrator.logger.Warn(
			"会话没有保存被上游拒绝的图片记录（会话不存在或仓库不支持）",
			"session_id", command.SessionID,
			"pictures", len(keys),
		)
	}
}

// contentRejectedReasons is the Schema's reasons text for the reasons a
// content-safety refusal in err gave, in the turn's locale (#35).
func (orchestrator *Orchestrator) contentRejectedReasons(ctx context.Context, err error) string {
	messages := orchestrator.schema.Presentation.Messages
	return moderation.Explain(messages.ContentRejectedReasons, agents.ContentRejectedReasons(err),
		messages.ContentRejectedReasonLabels, messages.ContentRejectedReasonSeparator,
		func(text string) string { return orchestrator.text(ctx, text) })
}

// imageInvalidReasons is the Schema's reasons text for what a refusal in err
// objected to about the request's picture, in the turn's locale (#497): the
// tokens come from a closed vocabulary, never the upstream's own words.
func (orchestrator *Orchestrator) imageInvalidReasons(ctx context.Context, err error) string {
	messages := orchestrator.schema.Presentation.Messages
	return moderation.Explain(messages.AgentImageInvalidReasons, agents.ImageInvalidReasons(err),
		messages.AgentImageInvalidReasonLabels, messages.AgentImageInvalidReasonSeparator,
		func(text string) string { return orchestrator.text(ctx, text) })
}

// withoutPictures copies request with each picture part replaced by the
// Schema's moderated-image note in the request's locale, followed by the
// reasons the review gave, if any.
func (orchestrator *Orchestrator) withoutPictures(ctx context.Context, request agents.Request, reasons string) agents.Request {
	if orchestrator.schema.Agent.ModeratedImageNote == "" || !requestHasPictures(request) {
		return request
	}
	note := orchestrator.text(ctx, orchestrator.schema.Agent.ModeratedImageNote) + reasons
	messages := make([]agents.Message, len(request.Messages))
	for index, message := range request.Messages {
		messages[index] = message
		if len(message.Parts) == 0 {
			continue
		}
		parts := make([]agents.ContentPart, 0, len(message.Parts))
		for _, part := range message.Parts {
			if part.URL != "" {
				part = agents.ContentPart{Kind: orchestrator.schema.Context.TextPartKind, Text: note}
			}
			parts = append(parts, part)
		}
		messages[index].Parts = parts
	}
	request.Messages = messages
	return request
}

func requestHasPictures(request agents.Request) bool {
	for _, message := range request.Messages {
		for _, part := range message.Parts {
			if part.URL != "" {
				return true
			}
		}
	}
	return false
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
// (#253), a picture the model read and refused says the picture does not meet
// the model's limits and what it objected to (#497), an upstream
// content-safety refusal says the input tripped it (#35),
// a used-up upstream balance or key quota says so (#120), a picture the
// model drew itself says it was not kept, a request field the upstream
// refused names that field (#401: only the plain field name is shown, the
// upstream's own wording stays in the log), a transient failure the retries
// did not overcome says to send the message again (#509), unless the upstream
// answered an HTTP error status: that names the connection, the status and
// its reason in the Schema's upstream text, including after the retries;
// anything else stays the unknown error, whose ID is in the log.
func (orchestrator *Orchestrator) agentFailureMessage(ctx context.Context, err error) string {
	messages := orchestrator.schema.Presentation.Messages
	if errors.Is(err, agents.ErrNotConfigured) && messages.AgentNotConfigured != "" {
		return orchestrator.text(ctx, messages.AgentNotConfigured)
	}
	if errors.Is(err, ErrTurnSubmissionUnconfirmed) && messages.SubmissionUnconfirmed != "" {
		return orchestrator.text(ctx, messages.SubmissionUnconfirmed)
	}
	if message, exhausted := orchestrator.agentQuotaExhaustedMessage(ctx, err); exhausted {
		return message
	}
	if errors.Is(err, agents.ErrContentRejected) && messages.AgentContentRejected != "" {
		return orchestrator.text(ctx, messages.AgentContentRejected) + orchestrator.contentRejectedReasons(ctx, err)
	}
	if errors.Is(err, agents.ErrImagesRejected) && messages.AgentImagesUnsupported != "" {
		return orchestrator.text(ctx, messages.AgentImagesUnsupported)
	}
	if errors.Is(err, agents.ErrImageInvalid) && messages.AgentImageInvalid != "" {
		return orchestrator.text(ctx, messages.AgentImageInvalid) + orchestrator.imageInvalidReasons(ctx, err)
	}
	if errors.Is(err, agents.ErrInlineImageOutput) && messages.AgentInlineImageOutput != "" {
		return orchestrator.text(ctx, messages.AgentInlineImageOutput)
	}
	if param, rejected := agents.RequestRejectedParam(err); rejected && messages.AgentRequestRejected != "" {
		return strings.ReplaceAll(orchestrator.text(ctx, messages.AgentRequestRejected), "{param}", param)
	}
	if message, refused := orchestrator.agentUpstreamHTTPMessage(ctx, err); refused {
		return message
	}
	if message, broke := orchestrator.agentInterruptedMessage(ctx, err); broke {
		return message
	}
	return orchestrator.text(ctx, messages.UnknownError)
}

// agentUpstreamHTTPMessage is the Schema's upstream text for a chat model
// call the upstream answered with an HTTP error status (agents.StatusError):
// "{provider} 网关返回 {status}，原因：{reason}", the provider being the chat
// connection's display name. The upstream's own words stay in the log.
func (orchestrator *Orchestrator) agentUpstreamHTTPMessage(ctx context.Context, err error) (string, bool) {
	var status *agents.StatusError
	if err == nil || !errors.As(err, &status) {
		return "", false
	}
	message := orchestrator.schema.Presentation.Messages.UpstreamHTTPError.Message(
		orchestrator.agentProvider, status.Status, func(text string) string { return orchestrator.text(ctx, text) })
	return message, message != ""
}

// agentInterruptedMessage is the Schema's text for a chat model call that
// failed transiently after the adapter layer's retries, or broke off after it
// had started streaming (#509); false for any other error.
func (orchestrator *Orchestrator) agentInterruptedMessage(ctx context.Context, err error) (string, bool) {
	text := orchestrator.schema.Presentation.Messages.AgentUpstreamInterrupted
	if err == nil || text == "" || !errors.Is(err, agents.ErrTransient) {
		return "", false
	}
	return orchestrator.text(ctx, text), true
}

// agentInlineImageMessage is the Schema text for a chat model round that drew
// a picture itself instead of answering or calling a tool (#638); false for
// any other error.
func (orchestrator *Orchestrator) agentInlineImageMessage(ctx context.Context, err error) (string, bool) {
	text := orchestrator.schema.Presentation.Messages.AgentInlineImageOutput
	if text == "" || !errors.Is(err, agents.ErrInlineImageOutput) {
		return "", false
	}
	return orchestrator.text(ctx, text), true
}

// noteInlineImage logs a model round that drew a picture itself (#638): the
// Adapter cut its stream, and the turn ends on it without sending it again.
func (orchestrator *Orchestrator) noteInlineImage(command ProcessTurn, err error) {
	orchestrator.logger.Warn(
		"对话模型自己画了图（未调用生成工具），已断开该轮且不重发，回合以 inline_image 结束",
		"session_id", command.SessionID,
		"assistant_message_id", command.AssistantMessageID,
		"error", err,
	)
}

// noteEmptyReply reports a model round that answered with no text and no tool
// call, also after the adapter layer's empty retry (#638), and logs it: such
// a round ends the turn, and the turn must not pass as completed.
func (orchestrator *Orchestrator) noteEmptyReply(command ProcessTurn, completion agents.Completion, round int) bool {
	if !completion.Empty() {
		return false
	}
	orchestrator.logger.Warn(
		"对话模型本轮返回空回复（无文字、无工具调用），回合以 empty_response 结束",
		"session_id", command.SessionID,
		"assistant_message_id", command.AssistantMessageID,
		"round", round,
		"usage_reported", completion.Usage != nil,
	)
	return true
}

// agentQuotaExhaustedMessage is the Schema text for an Agent request the
// upstream refused because the account balance or key quota is used up
// (#120), and whether err is such a refusal.
func (orchestrator *Orchestrator) agentQuotaExhaustedMessage(ctx context.Context, err error) (string, bool) {
	text := orchestrator.schema.Presentation.Messages.AgentQuotaExhausted
	if text == "" || !errors.Is(err, agents.ErrQuotaExhausted) {
		return "", false
	}
	return orchestrator.text(ctx, text), true
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
