// Package agenttools 提供 AGENTS 模式的多工具执行器：在既有生成工具之上
// 增加 set_params（写请求参数 store）与 list_offerings（schema→agent 投影），
// 并在生成调用前把 store 中的权威请求参数合并进工具入参。
package agenttools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
)

const (
	ToolSetParams         = "set_params"
	ToolListOfferings     = "list_offerings"
	ToolGetTask           = "get_task"
	ToolObserveTask       = "observe_task"
	ToolGetProcessingPlan = "get_processing_plan"
	ToolRequestConsent    = "request_consent"
	ToolCreateThreeView   = "create_three_view"
	// ToolListSessionModels lets the Agent find the session's finished 3D
	// models that the processing tools can take as a source.
	ToolListSessionModels = "list_session_models"
	// ToolUpdateSessionNotes replaces the session's settings summary.
	ToolUpdateSessionNotes = "update_session_notes"
	// ToolFindHistoryImages lets the Agent look through the session's
	// pictures when the user refers to images the turn does not carry (#279).
	ToolFindHistoryImages = "find_history_images"
	// ToolRecoverTaskResult fetches a failed Task's accepted upstream result
	// again at no charge (#295).
	ToolRecoverTaskResult = "recover_task_result"
)

// 同意状态键与决定值（agent 状态 store 与 harness consent 端点共用词汇）。
const (
	ConsentPendingKey  = agentparams.ConsentPendingKey
	ConsentDecisionKey = agentparams.ConsentDecisionKey
	ConsentApprovedKey = agentparams.ConsentApprovedKey
	// ConsentSnapshotKey 保存批准时的生成目标快照（模型+参数哈希，
	// 服务端在创建待批准项时写入）。生成执行前必须与当前选择一致，
	// 防止批准后被 set_params 或换模型改写计费范围（AGT-05）。
	ConsentSnapshotKey = agentparams.ConsentSnapshotKey

	ConsentDecisionPending  = "pending"
	ConsentDecisionApproved = "approved"
	ConsentDecisionDenied   = "denied"
)

type Config struct {
	Offerings func() []generation.Offering
	// Context carries the attachment handle syntax and its refusal wording;
	// create_three_view accepts handles beside history indices.
	Context    catalog.ChatContextSchema
	Params     *agentparams.Store
	Generate   chat.ToolExecutor
	Projection func() json.RawMessage
	// HistoryLookup bounds and words find_history_images (Product Schema
	// chat.context); a policy without a bound leaves the tool unavailable.
	HistoryLookup chat.HistoryLookupPolicy
	// ReadTask 读取 owner 自己的公开 Task 事实（状态、进度、公开失败与
	// 已落盘 Asset），不得包含上游 remote ID、Opaque 或取证字段。
	ReadTask func(ctx context.Context, ownerID, taskID string) (json.RawMessage, error)
	// RecoverTask starts (or returns) the no-charge refetch of a failed Task's
	// accepted upstream result for the command's own owner and session (#295)
	// and returns the result the Agent reads.
	RecoverTask func(ctx context.Context, ownerID, sessionID, taskID string) (json.RawMessage, error)
	// ListModels returns the owner's succeeded 3D tasks of this session that
	// processing tools can use as a source (platform IDs and public facts
	// only, no upstream IDs or URLs).
	ListModels         func(ctx context.Context, ownerID, sessionID string) (json.RawMessage, error)
	ReadProcessingPlan func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	// UpdateNotes replaces the settings summary of the command's own session
	// (owner and session come from the server-held command, never from the
	// arguments) and returns the stored text. It fails with
	// chat.ErrSessionNotesTooLong above the Schema bound.
	UpdateNotes func(ctx context.Context, ownerID, sessionID, notes string) (string, error)
	// Consent 创建待批准项。snapshot 是服务端计算的生成目标快照
	//（当前模型+合并参数哈希），与 pending 一并存入会话状态；
	// 生成执行前校验一致性（AGT-05）。
	Consent func(command chat.ToolCommand, request json.RawMessage, snapshot json.RawMessage) error
	// Preflight 在同意门禁之前执行无需用户交互即可判定的校验
	//（如"未选择生成模型"），返回面向 LLM 的失败文案；空串表示
	// 通过。批准不再被一次注定失败的调用白白消耗（AGT-04）。
	Preflight func(command chat.ToolCommand) string
	// ConsentState 读取会话的同意状态；ConsentClear 在一次付费生成
	// 消费掉批准后清空同意状态（一次性语义）。二者必须同时提供。
	// ConsentWaitTimeout 界定 request_consent 挂起等待用户决定的最长
	// 时长（来自 Product Schema chat.agent.consent_*，禁止硬编码）。
	ConsentState       func(scope string) map[string]json.RawMessage
	ConsentClear       func(scope string)
	ConsentClaim       func(scope, callID, idempotencyKey string, snapshot json.RawMessage) (taskID string, allowed bool, err error)
	ConsentAccepted    func(scope, callID, taskID string) error
	ConsentRelease     func(scope, callID, idempotencyKey string) error
	ConsentWaitTimeout time.Duration
	// TaskObservationTimeout bounds recovery when the accepted Task cannot be
	// read. Application assembly supplies the Schema request timeout.
	TaskObservationTimeout time.Duration
	// MaxGenerationCallsPerTurn 限定单轮付费生成次数（Schema 声明；
	// <=0 表示不设限，仅测试允许）。
	MaxGenerationCallsPerTurn int
	// Consent*Content 是 request_consent 与付费硬门禁回给 LLM 的文案
	// （Product Schema chat.agent.consent_*，B16 迁入 Schema，禁止硬编码）。
	ConsentApprovedContent  string
	ConsentDeniedContent    string
	ConsentWaitingContent   string
	ConsentBlockedContent   string
	ConsentReconcileContent string
	ConsentRequiredContent  string
	ConsentLimitContent     string
}

type Executor struct {
	offerings              func() []generation.Offering
	context                catalog.ChatContextSchema
	skillRuntime           *skillrun.Runtime
	skillReferences        SkillReferenceResolver
	skillOfferings         SkillOfferingResolver
	skillRecipes           map[string]string
	skillTurnStopped       func(context.Context, string, string, string) (bool, error)
	threeView              catalog.ChatThreeViewSchema
	params                 *agentparams.Store
	generate               chat.ToolExecutor
	preflight              func(command chat.ToolCommand) string
	projection             func() json.RawMessage
	historyLookup          chat.HistoryLookupPolicy
	readTask               func(ctx context.Context, ownerID, taskID string) (json.RawMessage, error)
	recoverTask            func(ctx context.Context, ownerID, sessionID, taskID string) (json.RawMessage, error)
	listModels             func(ctx context.Context, ownerID, sessionID string) (json.RawMessage, error)
	readProcessingPlan     func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	updateNotes            func(ctx context.Context, ownerID, sessionID, notes string) (string, error)
	consent                func(command chat.ToolCommand, request json.RawMessage, snapshot json.RawMessage) error
	consentState           func(scope string) map[string]json.RawMessage
	consentClear           func(scope string)
	consentClaim           func(scope, callID, idempotencyKey string, snapshot json.RawMessage) (string, bool, error)
	consentAccepted        func(scope, callID, taskID string) error
	consentRelease         func(scope, callID, idempotencyKey string) error
	consentWaitTimeout     time.Duration
	taskObservationTimeout time.Duration

	// 每轮生成次数统计：scope → (轮次 assistant 消息 ID, 已用次数)。
	// 同一会话同时只有一个活动轮次，按 scope 覆盖即可。
	usageMu       sync.Mutex
	usage         map[string]generationUsage
	maxGeneration int

	// consentContents 承载 Schema 声明的同意门禁文案。
	consentContents consentContents
}

type SkillReferenceResolver interface {
	Resolve(context.Context, generation.OwnerID, []string) ([]generation.AssetID, error)
}

type SkillOfferingResolver interface {
	Offerings(generation.Capability) []generation.Offering
	OfferingAvailable(generation.OfferingID) bool
}

// SetSkillRuntime is called during application assembly, before the executor
// can receive any tool call. The recipe revision is read from Product Schema.
func (e *Executor) SetSkillRuntime(runtime *skillrun.Runtime, references SkillReferenceResolver,
	offerings SkillOfferingResolver, recipes []catalog.ChatSkillRecipeSchema,
	turnStopped func(context.Context, string, string, string) (bool, error)) error {
	if runtime == nil || references == nil || offerings == nil || turnStopped == nil {
		return errors.New("agent tools: incomplete skill dependencies")
	}
	e.skillRuntime, e.skillReferences, e.skillOfferings = runtime, references, offerings
	e.skillTurnStopped = turnStopped
	e.skillRecipes = make(map[string]string, len(recipes))
	for _, recipe := range recipes {
		e.skillRecipes[recipe.ID] = recipe.Revision
	}
	return nil
}

// SetThreeView gives the three-view tool the Schema's texts. It is called
// during application assembly, like SetSkillRuntime.
func (e *Executor) SetThreeView(schema catalog.ChatThreeViewSchema) { e.threeView = schema }

// BoundTaskForCall exposes only a server-verified historical binding for
// recovery. It never asks the generator to prepare or submit a new Task.
func (e *Executor) BoundTaskForCall(ctx context.Context, command chat.ToolCommand) (string, error) {
	if command.Call.Name == ToolCreateThreeView {
		identity, err := e.TaskIdentityForCall(ctx, command)
		return identity.TaskID, err
	}
	reader, ok := e.generate.(interface {
		BoundTaskForCall(context.Context, chat.ToolCommand) (string, error)
	})
	if !ok {
		return "", nil
	}
	return reader.BoundTaskForCall(ctx, command)
}

// DetachedOperationForCall reports whether a generation call's approval card
// left its turn (see generationtool.Executor.DetachedOperationForCall).
func (e *Executor) DetachedOperationForCall(ctx context.Context, command chat.ToolCommand) (bool, error) {
	reader, ok := e.generate.(interface {
		DetachedOperationForCall(context.Context, chat.ToolCommand) (bool, error)
	})
	if !ok {
		return false, nil
	}
	return reader.DetachedOperationForCall(ctx, command)
}

type generationUsage struct {
	turn  string
	count int
	calls map[string]struct{}
}

func New(config Config) (*Executor, error) {
	if config.Params == nil || config.Generate == nil {
		return nil, errors.New("agent tools: incomplete dependencies")
	}
	if (config.ConsentState == nil) != (config.ConsentClear == nil) {
		return nil, errors.New(
			"agent tools: consent state and clear must be provided together",
		)
	}
	if (config.ConsentClaim == nil) != (config.ConsentAccepted == nil) {
		return nil, errors.New("agent tools: consent claim and accepted must be provided together")
	}
	if config.ConsentClaim != nil && config.ConsentRelease == nil {
		return nil, errors.New("agent tools: durable consent requires release for pre-submit rejection")
	}
	if config.ConsentClaim != nil && config.ConsentState == nil {
		return nil, errors.New("agent tools: durable consent requires state reader")
	}
	return &Executor{
		offerings:              config.Offerings,
		context:                config.Context,
		params:                 config.Params,
		generate:               config.Generate,
		projection:             config.Projection,
		historyLookup:          config.HistoryLookup,
		preflight:              config.Preflight,
		readTask:               config.ReadTask,
		recoverTask:            config.RecoverTask,
		listModels:             config.ListModels,
		readProcessingPlan:     config.ReadProcessingPlan,
		updateNotes:            config.UpdateNotes,
		consent:                config.Consent,
		consentState:           config.ConsentState,
		consentClear:           config.ConsentClear,
		consentClaim:           config.ConsentClaim,
		consentAccepted:        config.ConsentAccepted,
		consentRelease:         config.ConsentRelease,
		consentWaitTimeout:     config.ConsentWaitTimeout,
		taskObservationTimeout: config.TaskObservationTimeout,
		usage:                  map[string]generationUsage{},
		maxGeneration:          config.MaxGenerationCallsPerTurn,
		consentContents: consentContents{
			approved:  config.ConsentApprovedContent,
			denied:    config.ConsentDeniedContent,
			waiting:   config.ConsentWaitingContent,
			blocked:   config.ConsentBlockedContent,
			reconcile: config.ConsentReconcileContent,
			required:  config.ConsentRequiredContent,
			limit:     config.ConsentLimitContent,
		},
	}, nil
}

func (e *Executor) Execute(
	ctx context.Context,
	command chat.ToolCommand,
	observe func(chat.ToolObservation) error,
) (chat.ToolResult, error) {
	switch command.Call.Name {
	case ToolSetParams:
		return e.setParams(command), nil
	case ToolListOfferings:
		return e.listOfferings(), nil
	case ToolGetTask, ToolObserveTask:
		return e.readTaskState(ctx, command), nil
	case ToolRecoverTaskResult:
		return e.recoverTaskResult(ctx, command), nil
	case ToolListSessionModels:
		return e.listSessionModels(ctx, command), nil
	case ToolGetProcessingPlan:
		if e.readProcessingPlan == nil {
			return chat.ToolResult{Error: "processing plan reader unavailable", NoSubmission: true}, nil
		}
		return e.readProcessingPlan(ctx, command)
	case ToolUpdateSessionNotes:
		return e.updateSessionNotes(ctx, command), nil
	case ToolFindHistoryImages:
		return e.findHistoryImages(command), nil
	case ToolRequestConsent:
		return e.requestConsent(ctx, command), nil
	case ToolCreateThreeView:
		return e.createThreeView(ctx, command, observe)
	default:
		// The orchestrator supplies this server-side definition after registry
		// admission. Direct internal callers without a definition are retained
		// for compatibility; a supplied definition must match the call.
		if command.Tool.Name != "" && (command.Tool.Name != command.Call.Name || !command.Tool.Capability.Valid()) {
			return chat.ToolResult{Error: "unknown tool"}, nil
		}
		return e.generateWithParams(ctx, command, observe)
	}
}

func (e *Executor) setParams(command chat.ToolCommand) chat.ToolResult {
	var args struct {
		Params     map[string]json.RawMessage `json:"params"`
		OfferingID string                     `json:"offering_id"`
		Reasons    map[string]string          `json:"reasons"`
	}
	if len(command.Call.Arguments) > 0 {
		if err := json.Unmarshal(command.Call.Arguments, &args); err != nil {
			return chat.ToolResult{Error: "invalid set_params arguments"}
		}
	}
	if e.offerings != nil {
		if args.OfferingID == "" {
			return chat.ToolResult{Error: "set_params requires the exact offering_id", NoSubmission: true}
		}
		var selected generation.Offering
		for _, o := range e.offerings() {
			if string(o.ID) == args.OfferingID {
				selected = o
				break
			}
		}
		if selected.ID == "" || !selected.Enabled {
			return chat.ToolResult{Error: "parameter offering unavailable", NoSubmission: true}
		}
		normalized, err := catalog.ValidateParameterPatch(selected, args.Params, true, args.Reasons)
		if err != nil {
			return chat.ToolResult{Error: err.Error(), NoSubmission: true}
		}
		args.Params = normalized
	}
	if args.OfferingID == "" {
		e.params.Set(e.scopeKey(command), args.Params)
	} else {
		if err := e.params.SetAgentOffering(e.scopeKey(command), args.OfferingID, args.Params, args.Reasons); err != nil {
			return chat.ToolResult{Error: "parameter persistence failed", NoSubmission: true}
		}
	}
	return chat.ToolResult{AgentContent: "parameters updated"}
}

func (e *Executor) listOfferings() chat.ToolResult {
	if e.projection == nil {
		return chat.ToolResult{Error: "offerings projection unavailable"}
	}
	return chat.ToolResult{AgentContent: string(e.projection())}
}

// readTaskState 只读查询，不把被查询 Task 绑定到本次工具调用：TaskID
// 绑定语义保留给真正提交 Task 的生成工具，避免跨 session 查询伪造归属。
func (e *Executor) readTaskState(
	ctx context.Context,
	command chat.ToolCommand,
) chat.ToolResult {
	if e.readTask == nil {
		return chat.ToolResult{Error: "task reader unavailable"}
	}
	var args struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(command.Call.Arguments, &args); err != nil || args.TaskID == "" {
		return chat.ToolResult{Error: "invalid task_id"}
	}
	state, err := e.readTask(ctx, command.OwnerID, args.TaskID)
	if err != nil {
		return chat.ToolResult{Error: "task not readable"}
	}
	return chat.ToolResult{AgentContent: string(state)}
}

// recoverTaskResult starts the no-charge refetch of a failed Task's accepted
// upstream result. Owner and session come from the server-held command; the
// arguments only name the Task. It never submits a generation, so it does not
// count toward the paid-generation limit or bind the call to a Task.
func (e *Executor) recoverTaskResult(ctx context.Context, command chat.ToolCommand) chat.ToolResult {
	if e.recoverTask == nil {
		return chat.ToolResult{Error: "task recovery unavailable", NoSubmission: true}
	}
	var args struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(command.Call.Arguments, &args); err != nil || args.TaskID == "" {
		return chat.ToolResult{Error: "invalid task_id", NoSubmission: true}
	}
	result, err := e.recoverTask(ctx, command.OwnerID, command.SessionID, args.TaskID)
	if err != nil {
		return chat.ToolResult{Error: "task not readable", NoSubmission: true}
	}
	return chat.ToolResult{AgentContent: string(result), NoSubmission: true}
}

// listSessionModels is a read-only view of this owner's models in this
// session; the scope comes from the server-held command, never the arguments.
func (e *Executor) listSessionModels(ctx context.Context, command chat.ToolCommand) chat.ToolResult {
	if e.listModels == nil {
		return chat.ToolResult{Error: "model listing unavailable"}
	}
	models, err := e.listModels(ctx, command.OwnerID, command.SessionID)
	if err != nil {
		return chat.ToolResult{Error: "models not readable"}
	}
	return chat.ToolResult{AgentContent: string(models)}
}

// findHistoryImages is a read-only view of this session's pictures (#279).
// The history and selection are the ones the server loaded for the turn's
// owner and session; the arguments only choose among them. The reply's
// earlier lookup calls are replayed so the Schema bound counts each picture
// once per reply, across tool rounds and restarts.
func (e *Executor) findHistoryImages(command chat.ToolCommand) chat.ToolResult {
	if !e.historyLookup.Enabled() {
		return chat.ToolResult{Error: "history image lookup unavailable"}
	}
	var earlier [][]byte
	for _, call := range command.EarlierCalls {
		if call.Name == ToolFindHistoryImages {
			earlier = append(earlier, call.Arguments)
		}
	}
	result, err := e.historyLookup.Lookup(command.History, command.Options.Selection, earlier, command.Call.Arguments)
	if err != nil {
		var unknown *chat.UnknownHandleError
		if errors.As(err, &unknown) {
			template := e.context.UnknownHandleTemplate
			if unknown.Reason == "kind" {
				template = e.context.WrongHandleKindTemplate
			}
			return chat.ToolResult{Error: strings.ReplaceAll(template, "{handle}", unknown.Handle)}
		}
		return chat.ToolResult{Error: "invalid arguments"}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return chat.ToolResult{Error: "history images not readable"}
	}
	return chat.ToolResult{AgentContent: string(encoded), Pictures: result.Pictures}
}

// updateSessionNotes replaces the settings summary. An over-long text is
// refused with the Schema's message so the Agent can shorten it; nothing is
// cut silently.
func (e *Executor) updateSessionNotes(ctx context.Context, command chat.ToolCommand) chat.ToolResult {
	if e.updateNotes == nil {
		return chat.ToolResult{Error: "session notes unavailable"}
	}
	var args struct {
		Notes *string `json:"notes"`
	}
	if err := json.Unmarshal(command.Call.Arguments, &args); err != nil || args.Notes == nil {
		return chat.ToolResult{Error: "invalid notes"}
	}
	if _, err := e.updateNotes(ctx, command.OwnerID, command.SessionID, *args.Notes); err != nil {
		var tooLong *chat.SessionNotesTooLongError
		if errors.As(err, &tooLong) {
			return chat.ToolResult{Error: strings.NewReplacer(
				"{length}", fmt.Sprint(tooLong.Length),
				"{maximum}", fmt.Sprint(tooLong.Maximum),
			).Replace(e.context.SessionNotesTooLongTemplate)}
		}
		return chat.ToolResult{Error: "session notes not saved"}
	}
	return chat.ToolResult{AgentContent: e.context.SessionNotesSavedMessage}
}

func (e *Executor) requestConsent(
	ctx context.Context,
	command chat.ToolCommand,
) chat.ToolResult {
	if exact, ok := e.generate.(interface{ UsesExecutionGrants() bool }); ok && exact.UsesExecutionGrants() {
		// The actual operation has not yet been constructed. Describe the
		// required next step; never create or wait on a model/params approval.
		return chat.ToolResult{AgentContent: e.consentContents.required, NoSubmission: true}
	}
	if e.consent == nil {
		return chat.ToolResult{Error: "consent unavailable"}
	}
	scope := e.scopeKey(command)
	// AGT-12 挂起恢复重放：决定已写入（用户在挂起期间批准/拒绝）时
	// 直接返回对应结果，不重写待决状态、不再挂起。批准只对快照目标
	// 有效（AGT-05）：快照与当前生成目标不一致（如批准后切换模型）
	// 时，旧决定对本目标无效，继续走重写待决状态与快照的新授权流程，
	// 否则旧批准与本工具的 AGT-05 门禁互锁，生成永远被拦。
	if e.consentState != nil {
		state := e.consentState(scope)
		if raw := state[agentparams.ConsentClaimKey]; len(raw) > 0 {
			var claim struct {
				TaskID string `json:"task_id"`
			}
			if json.Unmarshal(raw, &claim) != nil || claim.TaskID == "" {
				// An unknown prior submission must be reconciled before a new
				// approval can overwrite its durable reservation.
				return chat.ToolResult{AgentContent: e.consentContents.reconcile}
			}
		}
		switch consentDecision(state) {
		case ConsentDecisionApproved:
			if (e.consentClaim == nil || len(state[ConsentSnapshotKey]) > 0) && e.consentSnapshotMatches(scope, command) {
				return chat.ToolResult{AgentContent: e.consentContents.approved}
			}
		case ConsentDecisionDenied:
			if e.consentSnapshotMatches(scope, command) {
				return chat.ToolResult{AgentContent: e.consentContents.denied}
			}
		}
	}
	// 恢复重放且已超过最长挂起时长仍无决定：返回等待文案收束本轮，
	// 与旧阻塞超时语义一致（生成工具在批准前不会执行）。
	if !command.ConsentWaitDeadline.IsZero() &&
		!time.Now().Before(command.ConsentWaitDeadline) {
		return chat.ToolResult{AgentContent: e.consentContents.waiting}
	}
	// 服务端计算生成目标快照（AGT-05）：以当前所选模型与合并参数
	// 为准，不信任 LLM 自报内容。
	options := e.mergedOptions(e.scopeKey(command), command)
	snapshot, snapshotErr := json.Marshal(struct {
		AIModel    string `json:"ai_model"`
		ParamsHash string `json:"params_hash"`
	}{
		AIModel:    options.AIModel,
		ParamsHash: paramsHash(options.Parameters),
	})
	if snapshotErr != nil {
		snapshot = nil
	}
	if err := e.consent(command, command.Call.Arguments, snapshot); err != nil {
		return chat.ToolResult{Error: "consent request failed"}
	}
	if e.consentState == nil || e.consentWaitTimeout <= 0 {
		return chat.ToolResult{
			AgentContent: "consent requested, awaiting approval",
		}
	}
	// AGT-12：不再在 worker 内阻塞轮询——登记挂起由编排器释放
	// worker，用户决定或超时唤醒后按 resumingPlan 路径重放续跑。
	return chat.ToolResult{Suspend: true}
}

// consentDecision 解析同意状态：consent_pending 不存在视为无待批准项；
// 存在时以 consent_decision 为准（兼容只写 consent_approved 的旧状态）。
func consentDecision(state map[string]json.RawMessage) string {
	if state == nil || state[ConsentPendingKey] == nil {
		return ""
	}
	var decision string
	if raw := state[ConsentDecisionKey]; raw != nil {
		_ = json.Unmarshal(raw, &decision)
	}
	switch decision {
	case ConsentDecisionApproved, ConsentDecisionDenied:
		return decision
	}
	var approved bool
	if raw := state[ConsentApprovedKey]; raw != nil {
		_ = json.Unmarshal(raw, &approved)
	}
	if approved {
		return ConsentDecisionApproved
	}
	return ConsentDecisionPending
}

func (e *Executor) generateWithParams(
	ctx context.Context,
	command chat.ToolCommand,
	observe func(chat.ToolObservation) error,
) (chat.ToolResult, error) {
	scope := e.scopeKey(command)
	// A previously accepted Task is observation, not a new paid operation.
	if command.BoundTaskID != "" {
		return e.generate.Execute(ctx, command, observe)
	}
	// Exact execution grants are prepared by the generation tool from the full
	// normalized input. A legacy request_consent decision is only conversational
	// intent and must never be claimed as authorization for that operation.
	if exact, ok := e.generate.(interface{ UsesExecutionGrants() bool }); ok && exact.UsesExecutionGrants() {
		alreadyPrepared := false
		if reader, ok := e.generate.(interface {
			ExistingOperation(context.Context, chat.ToolCommand) (bool, error)
		}); ok {
			var err error
			alreadyPrepared, err = reader.ExistingOperation(ctx, command)
			if err != nil {
				return chat.ToolResult{}, err
			}
		}
		if !alreadyPrepared && e.preflight != nil {
			if message := e.preflight(command); message != "" {
				return chat.ToolResult{AgentContent: message, NoSubmission: true}, nil
			}
		}
		if !alreadyPrepared && e.generationLimitReachedForCall(scope, command.AssistantMessageID,
			command.Call.ID, command.PriorGenerationCalls) {
			return chat.ToolResult{AgentContent: e.consentContents.limit, NoSubmission: true}, nil
		}
		command.ExplicitParameters = explicitParameters(command)
		command.Options = e.mergedOptions(scope, command)
		return e.generate.Execute(ctx, command, observe)
	}
	if e.consentClaim != nil {
		// A crash may leave an accepted Task without its chat binding. A
		// recovery-only lookup must run before current-model preflight and
		// per-turn limits; it cannot reserve a new approval.
		taskID, _, err := e.consentClaim(scope, command.Call.ID, command.IdempotencyKey(), nil)
		if err != nil {
			return chat.ToolResult{Error: "consent recovery unavailable"}, err
		}
		if taskID != "" {
			command.BoundTaskID = taskID
			return e.generate.Execute(ctx, command, observe)
		}
	}
	// 预检（AGT-04）：不需要用户交互就能判定的失败（未选模型等）
	// 在同意门禁之前直接返回，批准不再被无效调用消耗。
	if e.preflight != nil {
		if message := e.preflight(command); message != "" {
			return chat.ToolResult{AgentContent: message}, nil
		}
	}
	// 硬门禁一：付费生成必须持有批准。无论 LLM 是否自行调用
	// request_consent，服务端都以 store 状态为准；待批准/已拒绝/
	// 从未请求一律不执行。
	approved := false
	command.ExplicitParameters = explicitParameters(command)
	options := e.mergedOptions(scope, command)
	if e.consentClaim != nil {
		state := e.consentState(scope)
		if consentDecision(state) != ConsentDecisionApproved ||
			len(state[ConsentSnapshotKey]) == 0 ||
			!e.consentSnapshotMatches(scope, command) {
			return chat.ToolResult{AgentContent: e.consentContents.required}, nil
		}
		// Check the turn limit before creating a durable claim. An existing
		// accepted Task already took the recovery branch above.
		if e.generationLimitReached(scope, command.AssistantMessageID, command.PriorGenerationCalls) {
			return chat.ToolResult{AgentContent: e.consentContents.limit}, nil
		}
		snapshot, err := consentSnapshot(options)
		if err != nil {
			return chat.ToolResult{Error: "consent snapshot unavailable"}, err
		}
		taskID, allowed, err := e.consentClaim(scope, command.Call.ID, command.IdempotencyKey(), snapshot)
		if err != nil {
			return chat.ToolResult{Error: "consent storage unavailable"}, err
		}
		if !allowed {
			return chat.ToolResult{AgentContent: e.consentContents.required}, nil
		}
		if taskID != "" {
			command.BoundTaskID = taskID
			command.Options = options
			return e.generate.Execute(ctx, command, observe)
		}
		approved = true
	} else if e.consentState != nil {
		if decision := consentDecision(e.consentState(scope)); decision != ConsentDecisionApproved {
			return chat.ToolResult{AgentContent: e.consentContents.required}, nil
		}
		// 硬门禁二：批准与生成目标绑定（AGT-05）。当前模型或参数
		// 与批准快照不一致时必须重新请求同意；原批准保持有效。
		if !e.consentSnapshotMatches(scope, command) {
			return chat.ToolResult{AgentContent: e.consentContents.required}, nil
		}
		approved = true
	}
	// 硬门禁三：单轮生成次数上限（Schema 声明），防止一次回复里
	// 连发大量付费工具调用。
	if e.consentClaim == nil && e.generationLimitReached(scope, command.AssistantMessageID, command.PriorGenerationCalls) {
		return chat.ToolResult{AgentContent: e.consentContents.limit}, nil
	}
	command.Options = options
	accepted := false
	recordAccepted := func(taskID string) error {
		if accepted || taskID == "" || !approved {
			return nil
		}
		if e.consentAccepted != nil {
			if err := e.consentAccepted(scope, command.Call.ID, taskID); err != nil {
				return err
			}
		} else if e.consentClear != nil {
			e.consentClear(scope)
		}
		accepted = true
		return nil
	}
	result, err := e.generate.Execute(ctx, command, func(observation chat.ToolObservation) error {
		if observation.Registered {
			if err := recordAccepted(observation.TaskID); err != nil {
				return err
			}
		}
		if observe != nil {
			return observe(observation)
		}
		return nil
	})
	if result.NoSubmission && e.consentRelease != nil {
		if releaseErr := e.consentRelease(scope, command.Call.ID, command.IdempotencyKey()); releaseErr != nil {
			return result, releaseErr
		}
	}
	if result.TaskID != "" {
		if acceptErr := recordAccepted(result.TaskID); acceptErr != nil {
			return result, acceptErr
		}
	}
	return result, err
}

func (e *Executor) generationLimitReachedForCall(scope, turn, callID string, prior int) bool {
	if e.maxGeneration <= 0 {
		return false
	}
	e.usageMu.Lock()
	defer e.usageMu.Unlock()
	current := e.usage[scope]
	if current.turn != turn {
		current = generationUsage{turn: turn, calls: map[string]struct{}{}}
	}
	if _, seen := current.calls[callID]; seen {
		return false
	}
	if current.count < prior {
		current.count = prior
	}
	if current.count >= e.maxGeneration {
		e.usage[scope] = current
		return true
	}
	current.count++
	if current.calls == nil {
		current.calls = map[string]struct{}{}
	}
	current.calls[callID] = struct{}{}
	e.usage[scope] = current
	return false
}

func consentSnapshot(options chat.TurnOptions) (json.RawMessage, error) {
	return json.Marshal(struct {
		AIModel    string `json:"ai_model"`
		ParamsHash string `json:"params_hash"`
	}{AIModel: options.AIModel, ParamsHash: paramsHash(options.Parameters)})
}

// consentStateKeys 是会话状态里属于同意门禁的键。paramsStore 与
// agent 状态 store 共用同一持久化表（B10），params 侧必须排除它们：
// 既防止同意状态被当作生成参数合并进请求，也保证批准快照的参数
// 哈希在"同意写入前后"保持一致（AGT-05 回归）。
var consentStateKeys = map[string]struct{}{
	ConsentPendingKey:           {},
	ConsentDecisionKey:          {},
	ConsentApprovedKey:          {},
	ConsentSnapshotKey:          {},
	agentparams.ConsentClaimKey: {},
	agentparams.AgentTaskIDKey:  {},
}

// explicitParameters retains provenance without changing Options or its hash.
func explicitParameters(command chat.ToolCommand) map[string]json.RawMessage {
	values := command.ExplicitParameters
	if values == nil {
		values = command.Options.Parameters
	}
	out := make(map[string]json.RawMessage, len(values))
	for key, value := range values {
		out[key] = append(json.RawMessage(nil), value...)
	}
	return out
}

// mergedOptions 返回存储参数与工具参数合并后的命令选项（与生成执行
// 的合并规则一致），同时用于批准快照哈希。
func (e *Executor) mergedOptions(scope string, command chat.ToolCommand) chat.TurnOptions {
	merged := make(map[string]json.RawMessage, len(command.Options.Parameters))
	for name, value := range command.Options.Parameters {
		merged[name] = append(json.RawMessage(nil), value...)
	}
	for name, value := range e.params.Get(scope) {
		if _, blocked := consentStateKeys[name]; blocked {
			continue
		}
		if _, exists := merged[name]; !exists {
			merged[name] = append(json.RawMessage(nil), value...)
		}
	}
	options := command.Options
	options.Parameters = merged
	return options
}

// consentSnapshotMatches 校验当前生成目标（模型+合并参数哈希）与
// 批准时服务端写入的快照一致。快照缺失（旧状态或测试桩）视为匹配；
// strict 校验只约束新写入的快照。
func (e *Executor) consentSnapshotMatches(scope string, command chat.ToolCommand) bool {
	state := e.consentState(scope)
	raw := state[ConsentSnapshotKey]
	if len(raw) == 0 {
		return true
	}
	var snapshot struct {
		AIModel    string `json:"ai_model"`
		ParamsHash string `json:"params_hash"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return false
	}
	if snapshot.AIModel != command.Options.AIModel {
		return false
	}
	return snapshot.ParamsHash == paramsHash(e.mergedOptions(scope, command).Parameters)
}

func paramsHash(params map[string]json.RawMessage) string {
	if len(params) == 0 {
		return ""
	}
	keys := make([]string, 0, len(params))
	for name := range params {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	hasher := sha256.New()
	for _, name := range keys {
		_, _ = hasher.Write([]byte(name))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write(params[name])
		_, _ = hasher.Write([]byte{0})
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

// generationLimitReached 报告本轮生成次数是否已达上限；未达限时
// 记一次占用。轮次以 assistant 消息 ID 界定，同会话新轮次自动重置。
func (e *Executor) generationLimitReached(scope, turn string, prior int) bool {
	if e.maxGeneration <= 0 {
		return false
	}
	e.usageMu.Lock()
	defer e.usageMu.Unlock()
	current := e.usage[scope]
	if current.turn != turn {
		current = generationUsage{turn: turn}
	}
	if current.count < prior {
		current.count = prior
	}
	if current.count >= e.maxGeneration {
		e.usage[scope] = current
		return true
	}
	current.count++
	e.usage[scope] = current
	return false
}

// scopeKey 组合 owner+session 作为 store/consent 键，保证按用户隔离。
func (e *Executor) scopeKey(command chat.ToolCommand) string {
	return agentparams.ScopeKey(command.OwnerID, command.SessionID)
}

// consentContents 聚合 Product Schema chat.agent.consent_* 声明的
// 同意门禁文案（原代码常量已迁入 Schema，B16）。
type consentContents struct {
	approved  string
	denied    string
	waiting   string
	blocked   string
	reconcile string
	required  string
	limit     string
}
