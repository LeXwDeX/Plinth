// Package agenttools 提供 AGENTS 模式的多工具执行器：在既有生成工具之上
// 增加 set_params（写请求参数 store）与 list_offerings（schema→agent 投影），
// 并在生成调用前把 store 中的权威请求参数合并进工具入参。
package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
	"github.com/LeXwDeX/Plinth/internal/chat/references"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
	"github.com/LeXwDeX/Plinth/internal/threeview"
)

const (
	ToolSetParams         = "set_params"
	ToolListOfferings     = "list_offerings"
	ToolGetTask           = "get_task"
	ToolGetProcessingPlan = "get_processing_plan"
	// ToolReviseProcessingPlan applies one controlled revision to an
	// existing processing plan within the approval already given (#251).
	ToolReviseProcessingPlan = "revise_processing_plan"
	ToolCreateThreeView      = "create_three_view"
	// ToolListSessionModels lets the Agent find the session's finished 3D
	// models that the processing tools can take as a source.
	ToolListSessionModels = "list_session_models"
	// ToolUpdateSessionNotes replaces the session's settings summary.
	ToolUpdateSessionNotes = "update_session_notes"
	// ToolUpdateProductionBrief replaces the session's production brief (#103).
	ToolUpdateProductionBrief = "update_production_brief"
	// ToolAskChoice opens a Schema-declared generic choice card (#103).
	ToolAskChoice = "ask_choice"
	// ToolFindHistoryImages lets the Agent look through the session's
	// pictures when the user refers to images the turn does not carry (#279).
	ToolFindHistoryImages = "find_history_images"
	// ToolRecoverTaskResult fetches a failed Task's accepted upstream result
	// again at no charge (#295).
	ToolRecoverTaskResult = "recover_task_result"
	// ToolInspectImage reports deterministic facts about one picture of the
	// session without creating a Task (#245).
	ToolInspectImage = "inspect_image"
	// ToolCreateSelectionMask builds a local selection mask for one picture and
	// stores it as the owner's input asset (#244).
	ToolCreateSelectionMask = "create_selection_mask"
	// ToolCropImageRegions cuts normalized regions out of one picture and
	// stores every crop as the owner's list_crop input asset (#331).
	ToolCropImageRegions = "crop_image_regions"
	// ToolInspectModel reports the structure of a finished 3D model without
	// creating a Task (#103).
	ToolInspectModel = "inspect_model"
	// ToolRenderModelViews renders still views of a finished 3D model and
	// attaches them to the reply (#103).
	ToolRenderModelViews = "render_model_views"
	// ToolCompareModels renders two finished 3D models side by side (#103).
	ToolCompareModels = "compare_models"
	// ToolCompareModelReference renders a finished model beside its reference
	// picture and reports deterministic subject metrics (#246).
	ToolCompareModelReference = "compare_model_reference"
	// ToolRecordSelfCheck writes the Agent's conclusion into a result's
	// persisted self-check record (#246).
	ToolRecordSelfCheck = "record_self_check"
	// ToolGetProductionStep reads one step of the session's work ledger in
	// full by @stepN or Task ID (#248).
	ToolGetProductionStep = "get_production_step"
	// ToolProposeProductionPlan proposes the budget-mode production plan the
	// person approves once (#103).
	ToolProposeProductionPlan = "propose_production_plan"
	// ToolProposeAssetList proposes one draft production list of a project for
	// the person to confirm (#327).
	ToolProposeAssetList = "propose_asset_list"
	// ToolSubmitAssetListItem reports the outcome of the production-list item
	// whose sub-session runs this turn (#328).
	ToolSubmitAssetListItem = "submit_asset_list_item"
	// ToolGetAssetList reads the current state of a production list: every
	// item's state, artifacts and self-check conclusion (#329f).
	ToolGetAssetList = "get_asset_list"
	// ToolSetVisualAnchor writes a session visual anchor after the person
	// confirmed it on a choice card (#250).
	ToolSetVisualAnchor = "set_visual_anchor"
	// ToolListProjectAssets lists the project assets frozen with this turn,
	// with the @projectN handles the Agent may pass to a generation tool (#240).
	ToolListProjectAssets = "list_project_assets"
)

type Config struct {
	Offerings func() []generation.Offering
	// Skills is the Schema's Skill declaration: use_skill reads its directory
	// contract and its system Skill IDs (#548).
	Skills catalog.ChatSkillsSchema
	// Context carries the attachment handle syntax and its refusal wording;
	// create_three_view accepts handles beside history indices.
	Context  catalog.ChatContextSchema
	Params   *agentparams.Store
	Generate chat.ToolExecutor
	// Projection is the list_offerings view (#382): capability and model narrow
	// it ("" for both: every capability with ids only); a request without the
	// new arguments still reads the ids-only default.
	Projection func(capability, model string) json.RawMessage
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
	// ReviseProcessingPlan applies one controlled plan revision (#251); it
	// never submits a paid step by itself.
	ReviseProcessingPlan func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	// InspectImage is the read-only image inspection tool (#245). It reads one
	// picture of the command's owner and session, never creates a Task and never
	// spends credits.
	InspectImage func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	// CreateSelectionMask is the read-only local mask tool (#244). It reads one
	// picture of the command's owner and session, stores a PNG mask as that
	// owner's input asset and never creates a Task or spends credits.
	CreateSelectionMask func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	// CropImageRegions is the read-only crop tool (#331). It cuts normalized
	// regions out of one owned picture at original resolution and stores each
	// crop as a list_crop input asset; no Task, no step and no credits.
	CropImageRegions func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	// InspectModel, RenderModelViews and CompareModels are the read-only model
	// review tools (#103). They read only the command's owner and session,
	// never create a Task and never spend credits.
	InspectModel     func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	RenderModelViews func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	CompareModels    func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	// CompareModelReference and RecordSelfCheck are the persisted self-check
	// contract tools (#246). Both are owner/session scoped and never create a
	// Task or spend credits.
	CompareModelReference func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	RecordSelfCheck       func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	// ReadProductionStep reads one step of the session's work ledger (#248).
	// It is read-only: no Task is created, no credit is spent and the session
	// is not changed.
	ReadProductionStep func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	// UpdateNotes replaces the settings summary of the command's own session
	// (owner and session come from the server-held command, never from the
	// arguments) and returns the stored text. It fails with
	// chat.ErrSessionNotesTooLong above the Schema bound.
	UpdateNotes func(ctx context.Context, ownerID, sessionID, notes string) (string, error)
	// UpdateBrief replaces the production brief of the command's own session
	// (#103). It fails with a *chat.BriefError that names the refused field.
	UpdateBrief func(ctx context.Context, ownerID, sessionID string, brief map[string]json.RawMessage) error
	// AskChoice opens a generic card of a declared kind (#103). The service
	// builds the options from model3d_usage.profiles.
	AskChoice func(ctx context.Context, command chat.ToolCommand, kind catalog.ChatChoiceKindSchema,
		arguments chat.AskChoiceArguments) (chat.ToolResult, error)
	// ProposePlan validates the Agent's production plan against the Schema,
	// re-quotes every step from the runtime Catalog and stores one pending
	// plan card (#103).
	ProposePlan func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	// ProposeAssetList validates the Agent's production list against the
	// Schema, resolves its reference handles and stores one draft list for the
	// person to confirm (#327).
	ProposeAssetList func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	// SubmitAssetListItem reports the current production-list item as done or
	// failed (#328). Only an item's own sub-session is accepted; the service
	// verifies the frozen AssetListItemID and the task outcome.
	SubmitAssetListItem func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	// ReadAssetList reads the current state of a production list (#329f): every
	// item's state, artifacts and self-check conclusion. Nil leaves the tool
	// unavailable.
	ReadAssetList func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	// AssetListUnavailable is the Schema's "tool {tool} is not available in
	// this turn" copy, set when the Catalog keeps application.asset_list.execution
	// off (#494). Only submit_asset_list_item answers it instead of running:
	// without a run no item reaches working, so the call could not finish. It
	// also covers a turn frozen while the tool was still offered. Propose and
	// read need no run and dispatch as before. {tool} is filled with the called
	// name; empty leaves the tool dispatched as before.
	AssetListUnavailable string
	// WriteVisualAnchor applies one explicitly confirmed visual-anchor write
	// (#250). The service owns the owner/session checks and the bounds; this
	// tool cannot touch the user's style lock.
	WriteVisualAnchor func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	// Project is application.project (#240): the list_project_assets copy,
	// handle prefix and list bound. Nil leaves the tool unavailable.
	Project *catalog.ProjectSchema
	// ChoiceKinds are chat.choice.kinds from the Product Schema; the Agent
	// names one by id and supplies options only for a kind whose option source
	// is agent (#239).
	ChoiceKinds []catalog.ChatChoiceKindSchema
	// ChoiceOptionInvalid words a refused direction-card option list
	// (chat.presentation.messages.choice_option_invalid, #239).
	ChoiceOptionInvalid string
	// ChoiceOpenInReply words the refusal of a second card while a card this
	// reply opened is still pending
	// (chat.presentation.messages.choice_open_in_reply, #615).
	ChoiceOpenInReply string
	// BriefSavedMessage and BriefInvalidTemplate word the update tool's result
	// (model3d_usage.brief.saved_message / invalid_template); BriefMessages
	// words its field refusals (model3d_usage.brief.messages).
	BriefSavedMessage    string
	BriefInvalidTemplate string
	BriefMessages        catalog.ChatProductionBriefMessages
	// Preflight 在准备付费操作（创建待批准项）之前执行无需用户交互即可
	// 判定的校验（如"未选择生成模型"），返回面向 LLM 的失败文案；空串表示
	// 通过。批准卡不再为一次注定失败的调用而出现（AGT-04）。
	Preflight func(command chat.ToolCommand) string
	// SubmissionGate 是第一层付费闸门（#328 §2.5.5）：生成类工具在提交
	// 前检查清单 run/项状态与在场，返回非空文案即拒绝。fresh 与已 prepared
	// 的重放都经过这里；已绑定的 Task 属观察而非新提交，不经过。
	// nil 表示未装配自动制作，行为不变。
	SubmissionGate func(ctx context.Context, command chat.ToolCommand) string
	// TaskObservationTimeout bounds recovery when the accepted Task cannot be
	// read. Application assembly supplies the Schema request timeout.
	TaskObservationTimeout time.Duration
	// MaxGenerationCallsPerTurn 限定单轮付费生成次数（Schema 声明；
	// <=0 表示不设限，仅测试允许）。
	MaxGenerationCallsPerTurn int
	// ConsentLimitContent is what the Agent reads when the turn's generation
	// call limit is reached; ConsentReconcileContent is the failure text of a
	// three-view view whose submission outcome stayed unknown past its wait
	// (Product Schema chat.agent.consent_limit_content and
	// consent_reconcile_content).
	ConsentLimitContent     string
	ConsentReconcileContent string
}

type Executor struct {
	skills                catalog.ChatSkillsSchema
	offerings             func() []generation.Offering
	context               catalog.ChatContextSchema
	skillRuntime          *skillrun.Runtime
	skillReferences       SkillReferenceResolver
	skillOfferings        SkillOfferingResolver
	skillRecipes          map[string]string
	skillTurnStopped      func(context.Context, string, string, string) (bool, error)
	threeView             catalog.ChatThreeViewSchema
	threeViewService      *threeview.Service
	params                *agentparams.Store
	generate              chat.ToolExecutor
	preflight             func(command chat.ToolCommand) string
	submissionGate        func(ctx context.Context, command chat.ToolCommand) string
	projection            func(capability, model string) json.RawMessage
	historyLookup         chat.HistoryLookupPolicy
	readTask              func(ctx context.Context, ownerID, taskID string) (json.RawMessage, error)
	recoverTask           func(ctx context.Context, ownerID, sessionID, taskID string) (json.RawMessage, error)
	listModels            func(ctx context.Context, ownerID, sessionID string) (json.RawMessage, error)
	readProcessingPlan    func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	reviseProcessingPlan  func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	inspectImage          func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	createSelectionMask   func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	cropImageRegions      func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	inspectModel          func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	renderModelViews      func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	compareModels         func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	compareModelReference func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	recordSelfCheck       func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	readProductionStep    func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	updateNotes           func(ctx context.Context, ownerID, sessionID, notes string) (string, error)
	updateBrief           func(ctx context.Context, ownerID, sessionID string, brief map[string]json.RawMessage) error
	askChoice             func(ctx context.Context, command chat.ToolCommand, kind catalog.ChatChoiceKindSchema,
		arguments chat.AskChoiceArguments) (chat.ToolResult, error)
	proposePlan            func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	proposeAssetList       func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	submitAssetListItem    func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	readAssetList          func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	assetListUnavailable   string
	writeVisualAnchor      func(context.Context, chat.ToolCommand) (chat.ToolResult, error)
	project                *catalog.ProjectSchema
	choiceKinds            map[string]catalog.ChatChoiceKindSchema
	choiceOptionInvalid    string
	choiceOpenInReply      string
	briefSavedMessage      string
	briefInvalidTemplate   string
	briefMessages          catalog.ChatProductionBriefMessages
	taskObservationTimeout time.Duration

	// 每轮生成次数统计：scope → (轮次 assistant 消息 ID, 已用次数)。
	// 同一会话同时只有一个活动轮次，按 scope 覆盖即可。
	usageMu       sync.Mutex
	usage         map[string]generationUsage
	maxGeneration int

	// limitContent and reconcileContent are the Schema's texts for a reached
	// generation call limit and an unknown three-view submission outcome.
	limitContent     string
	reconcileContent string
}

type SkillReferenceResolver interface {
	Resolve(context.Context, references.ReferenceScope, []string) ([]generation.AssetID, error)
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

// SetThreeViewService gives the three-view tool the budget continuation and
// the automatic consistency check (#243). Without it the tool keeps the
// per-view behavior: only the front view starts.
func (e *Executor) SetThreeViewService(service *threeview.Service) { e.threeViewService = service }

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
	choiceKinds := make(map[string]catalog.ChatChoiceKindSchema, len(config.ChoiceKinds))
	for _, kind := range config.ChoiceKinds {
		choiceKinds[kind.ID] = kind
	}
	return &Executor{
		skills:                 config.Skills,
		choiceKinds:            choiceKinds,
		choiceOptionInvalid:    config.ChoiceOptionInvalid,
		choiceOpenInReply:      config.ChoiceOpenInReply,
		briefSavedMessage:      config.BriefSavedMessage,
		briefInvalidTemplate:   config.BriefInvalidTemplate,
		briefMessages:          config.BriefMessages,
		offerings:              config.Offerings,
		context:                config.Context,
		params:                 config.Params,
		generate:               config.Generate,
		projection:             config.Projection,
		historyLookup:          config.HistoryLookup,
		preflight:              config.Preflight,
		submissionGate:         config.SubmissionGate,
		readTask:               config.ReadTask,
		recoverTask:            config.RecoverTask,
		listModels:             config.ListModels,
		readProcessingPlan:     config.ReadProcessingPlan,
		reviseProcessingPlan:   config.ReviseProcessingPlan,
		inspectImage:           config.InspectImage,
		createSelectionMask:    config.CreateSelectionMask,
		cropImageRegions:       config.CropImageRegions,
		inspectModel:           config.InspectModel,
		renderModelViews:       config.RenderModelViews,
		compareModels:          config.CompareModels,
		compareModelReference:  config.CompareModelReference,
		recordSelfCheck:        config.RecordSelfCheck,
		readProductionStep:     config.ReadProductionStep,
		updateNotes:            config.UpdateNotes,
		updateBrief:            config.UpdateBrief,
		askChoice:              config.AskChoice,
		proposePlan:            config.ProposePlan,
		proposeAssetList:       config.ProposeAssetList,
		submitAssetListItem:    config.SubmitAssetListItem,
		readAssetList:          config.ReadAssetList,
		assetListUnavailable:   config.AssetListUnavailable,
		writeVisualAnchor:      config.WriteVisualAnchor,
		project:                config.Project,
		taskObservationTimeout: config.TaskObservationTimeout,
		usage:                  map[string]generationUsage{},
		maxGeneration:          config.MaxGenerationCallsPerTurn,
		limitContent:           config.ConsentLimitContent,
		reconcileContent:       config.ConsentReconcileContent,
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
		return e.listOfferings(command), nil
	case ToolGetTask:
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
	case ToolReviseProcessingPlan:
		if e.reviseProcessingPlan == nil {
			return chat.ToolResult{Error: "processing plan revision unavailable", NoSubmission: true}, nil
		}
		return e.reviseProcessingPlan(ctx, command)
	case ToolUpdateSessionNotes:
		return e.updateSessionNotes(ctx, command), nil
	case ToolUpdateProductionBrief:
		return e.updateProductionBrief(ctx, command), nil
	case ToolAskChoice:
		return e.askChoiceCard(ctx, command), nil
	case ToolSetVisualAnchor:
		if e.writeVisualAnchor == nil {
			return chat.ToolResult{Error: "visual anchors unavailable", NoSubmission: true}, nil
		}
		return e.writeVisualAnchor(ctx, command)
	case ToolListProjectAssets:
		return e.listProjectAssets(ctx, command), nil
	case ToolProposeProductionPlan:
		if e.proposePlan == nil {
			return chat.ToolResult{Error: "production plans unavailable", NoSubmission: true}, nil
		}
		return e.proposePlan(ctx, command)
	case ToolProposeAssetList, ToolSubmitAssetListItem, ToolGetAssetList:
		// #494: the Catalog keeps submit_asset_list_item declared but out of
		// the table while asset_list.execution is off. A turn frozen while it
		// was still offered can still name it; answer with the Schema's "not
		// available in this turn" copy, which names the way out, instead of
		// starting a completion nothing can advance. Propose and read need no
		// run, so they fall through to their dispatchers.
		if e.assetListUnavailable != "" && command.Call.Name == ToolSubmitAssetListItem {
			return chat.ToolResult{
				Error:        strings.ReplaceAll(e.assetListUnavailable, "{tool}", command.Call.Name),
				NoSubmission: true,
			}, nil
		}
		switch command.Call.Name {
		case ToolProposeAssetList:
			if e.proposeAssetList == nil {
				return chat.ToolResult{Error: "asset lists unavailable", NoSubmission: true}, nil
			}
			return e.proposeAssetList(ctx, command)
		case ToolSubmitAssetListItem:
			if e.submitAssetListItem == nil {
				return chat.ToolResult{Error: "asset list item reports unavailable", NoSubmission: true}, nil
			}
			return e.submitAssetListItem(ctx, command)
		default:
			if e.readAssetList == nil {
				return chat.ToolResult{Error: "asset lists unavailable", NoSubmission: true}, nil
			}
			return e.readAssetList(ctx, command)
		}
	case ToolFindHistoryImages:
		return e.findHistoryImages(command), nil
	case ToolInspectImage:
		if e.inspectImage == nil {
			return chat.ToolResult{Error: "image inspection unavailable", NoSubmission: true}, nil
		}
		return e.inspectImage(ctx, command)
	case ToolCreateSelectionMask:
		if e.createSelectionMask == nil {
			return chat.ToolResult{Error: "selection mask unavailable", NoSubmission: true}, nil
		}
		return e.createSelectionMask(ctx, command)
	case ToolCropImageRegions:
		if e.cropImageRegions == nil {
			return chat.ToolResult{Error: "image crop unavailable", NoSubmission: true}, nil
		}
		return e.cropImageRegions(ctx, command)
	case ToolGetProductionStep:
		if e.readProductionStep == nil {
			return chat.ToolResult{Error: "production ledger unavailable", NoSubmission: true}, nil
		}
		return e.readProductionStep(ctx, command)
	case ToolInspectModel, ToolRenderModelViews, ToolCompareModels:
		reader := e.inspectModel
		if command.Call.Name == ToolRenderModelViews {
			reader = e.renderModelViews
		} else if command.Call.Name == ToolCompareModels {
			reader = e.compareModels
		}
		if reader == nil {
			return chat.ToolResult{Error: "model review unavailable", NoSubmission: true}, nil
		}
		return reader(ctx, command)
	case ToolCompareModelReference:
		if e.compareModelReference == nil {
			return chat.ToolResult{Error: "reference comparison unavailable", NoSubmission: true}, nil
		}
		return e.compareModelReference(ctx, command)
	case ToolRecordSelfCheck:
		if e.recordSelfCheck == nil {
			return chat.ToolResult{Error: "self-check record unavailable", NoSubmission: true}, nil
		}
		return e.recordSelfCheck(ctx, command)
	case ToolCreateThreeView:
		result, err := e.createThreeView(ctx, command, observe)
		e.recordPickedSkill(command, &result)
		return result, err
	case ToolUseSkill:
		return e.useSkill(command), nil
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
		normalized, kept, err := catalog.NormalizeParameterPatch(selected, args.Params, true, args.Reasons)
		if err != nil {
			return chat.ToolResult{Error: err.Error(), NoSubmission: true}
		}
		// #402: a reason for a parameter that is not advised was ignored by
		// the validation. It is not stored either: the generation that freezes
		// this Offering's input would refuse a reason that no advised
		// parameter takes.
		args.Params, args.Reasons = normalized, kept
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

func (e *Executor) listOfferings(command chat.ToolCommand) chat.ToolResult {
	if e.projection == nil {
		return chat.ToolResult{Error: "offerings projection unavailable"}
	}
	// #382: the model asks for the one capability or the one model it is
	// choosing between; without an argument it gets the ids-only view, so a
	// round-trip does not carry every offering's knowledge and inputs.
	var args struct {
		Capability string `json:"capability"`
		Model      string `json:"model"`
	}
	if len(command.Call.Arguments) > 0 {
		if err := json.Unmarshal(command.Call.Arguments, &args); err != nil {
			return chat.ToolResult{Error: "invalid list_offerings arguments"}
		}
	}
	return chat.ToolResult{
		AgentContent: string(e.projection(args.Capability, args.Model)),
	}
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
	result, err := e.historyLookup.Lookup(command.ReferenceHistory(), command.Options.Selection, earlier, command.Call.Arguments)
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

// updateProductionBrief patches the session's production brief (#103). The
// arguments are the fields to change: only the fields the Schema declares,
// with the bounds it declares. A field omitted from the arguments keeps its
// stored value, so the choice-card continuation that replays only the purpose
// does not clear the rest of the brief (#103 review C2). A refused field comes
// back through the Schema's invalid_template so the Agent can correct it.
func (e *Executor) updateProductionBrief(ctx context.Context, command chat.ToolCommand) chat.ToolResult {
	if e.updateBrief == nil {
		return chat.ToolResult{Error: "production brief unavailable", NoSubmission: true}
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(command.Call.Arguments, &fields); err != nil || fields == nil {
		reason := e.briefMessages.NotObject
		if strings.TrimSpace(reason) == "" {
			reason = "参数必须是对象"
		}
		return chat.ToolResult{Error: e.briefInvalid(reason), NoSubmission: true}
	}
	if err := e.updateBrief(ctx, command.OwnerID, command.SessionID, fields); err != nil {
		if text := chat.ProductionBriefValidationErrorText(e.briefInvalidTemplate, err); text != "" {
			return chat.ToolResult{Error: text, NoSubmission: true}
		}
		if errors.Is(err, chat.ErrProductionBriefUnavailable) {
			return chat.ToolResult{Error: "production brief unavailable", NoSubmission: true}
		}
		return chat.ToolResult{Error: "production brief not saved", NoSubmission: true}
	}
	return chat.ToolResult{AgentContent: e.briefSavedMessage, NoSubmission: true}
}

func (e *Executor) briefInvalid(reason string) string {
	return strings.ReplaceAll(e.briefInvalidTemplate, "{reason}", reason)
}

// askChoiceCard opens a generic choice card declared by chat.choice.kinds
// (#103). The Agent names the kind only; the options and their arguments come
// from the Schema and the service. The card's answer would call its target
// tool, so that tool must be registered and available in this turn's scope; a
// kind with no target tool (#374) continues the conversation as text and has
// none to check.
// boundedChoiceToken reports whether an Agent-supplied direction option id is
// a bounded token the answer can pass to a tool argument: a letter or digit
// followed by at most 63 letters, digits, dots, underscores, colons, slashes
// or hyphens. Free text never becomes an option id (#239).
func boundedChoiceToken(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for index, char := range id {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9':
		case index > 0 && (char == '.' || char == '_' || char == ':' || char == '/' || char == '-'):
		default:
			return false
		}
	}
	return true
}

// choiceOptionSteps reads an option's step (#549): absent or null is none, an
// object is one, and a list is every step it names, so the service can refuse
// an option that does more than one thing in the Schema's words.
func choiceOptionSteps(raw json.RawMessage) ([]chat.ChoiceStep, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, true
	}
	if strings.HasPrefix(trimmed, "[") {
		var steps []chat.ChoiceStep
		if json.Unmarshal(raw, &steps) != nil {
			return nil, false
		}
		return steps, true
	}
	var step chat.ChoiceStep
	if json.Unmarshal(raw, &step) != nil {
		return nil, false
	}
	return []chat.ChoiceStep{step}, true
}

func (e *Executor) askChoiceCard(ctx context.Context, command chat.ToolCommand) chat.ToolResult {
	if e.askChoice == nil {
		return chat.ToolResult{Error: "choice cards unavailable", NoSubmission: true}
	}
	var arguments struct {
		Kind        string `json:"kind"`
		Question    string `json:"question"`
		Recommended string `json:"recommended"`
		Options     []struct {
			ID          string          `json:"id"`
			Label       string          `json:"label"`
			Description string          `json:"description"`
			Thumbnail   string          `json:"thumbnail"`
			Arguments   json.RawMessage `json:"arguments"`
			Action      *struct {
				Type        string `json:"type"`
				OperationID string `json:"operation_id"`
			} `json:"action"`
			// Step is one object (#549); a list is read whole so the service
			// can refuse an option that names more than one step.
			Step json.RawMessage `json:"step"`
		} `json:"options"`
		Proposal struct {
			Arguments json.RawMessage `json:"arguments"`
		} `json:"proposal"`
	}
	if err := json.Unmarshal(command.Call.Arguments, &arguments); err != nil || arguments.Kind == "" {
		return chat.ToolResult{Error: "invalid choice arguments", NoSubmission: true}
	}
	kind, declared := e.choiceKinds[arguments.Kind]
	if !declared {
		return chat.ToolResult{Error: "choice kind unavailable", NoSubmission: true}
	}
	if kind.ToolName != "" && command.ToolAvailable != nil && !command.ToolAvailable(kind.ToolName) {
		return chat.ToolResult{Error: "choice target tool unavailable", NoSubmission: true}
	}
	options := make([]chat.ChoiceOptionInput, 0, len(arguments.Options))
	for _, option := range arguments.Options {
		id := strings.TrimSpace(option.ID)
		label := strings.TrimSpace(option.Label)
		thumbnail := strings.TrimSpace(option.Thumbnail)
		if !boundedChoiceToken(id) || label == "" || len([]rune(label)) > 120 || len([]rune(option.Description)) > 300 ||
			len(thumbnail) > 40 {
			message := e.choiceOptionInvalid
			if message == "" {
				message = "invalid choice options"
			}
			return chat.ToolResult{Error: message, NoSubmission: true}
		}
		input := chat.ChoiceOptionInput{ID: id, Label: label, Description: strings.TrimSpace(option.Description),
			Thumbnail: thumbnail, Arguments: append(json.RawMessage(nil), option.Arguments...)}
		if option.Action != nil {
			input.Action = &chat.ChoiceAction{Type: strings.TrimSpace(option.Action.Type), OperationID: strings.TrimSpace(option.Action.OperationID)}
		}
		steps, valid := choiceOptionSteps(option.Step)
		if !valid {
			return chat.ToolResult{Error: "choice options invalid: step must be one object with tool and capability", NoSubmission: true}
		}
		input.Steps = steps
		options = append(options, input)
	}
	result, err := e.askChoice(ctx, command, kind, chat.AskChoiceArguments{
		Kind: arguments.Kind, Question: arguments.Question, Recommended: arguments.Recommended,
		Options: options, Proposal: arguments.Proposal.Arguments,
	})
	if err != nil {
		if errors.Is(err, chat.ErrSessionNotFound) {
			return chat.ToolResult{Error: "session not found", NoSubmission: true}
		}
		if errors.Is(err, chat.ErrChoiceOpenInReply) {
			message := e.choiceOpenInReply
			if message == "" {
				message = "choice card not opened: this reply already opened a card that is still pending; wait for the person's answer instead of calling ask_choice again"
			}
			return chat.ToolResult{Error: message, NoSubmission: true}
		}
		return chat.ToolResult{Error: "choice card not opened", NoSubmission: true}
	}
	return result
}

// submissionRefused runs the first paid-gate layer (#328). A non-empty Schema
// text refuses the call before any approval card or generation side effect.
func (e *Executor) submissionRefused(
	ctx context.Context,
	command chat.ToolCommand,
) (chat.ToolResult, bool) {
	if e.submissionGate == nil {
		return chat.ToolResult{}, false
	}
	message := e.submissionGate(ctx, command)
	if message == "" {
		return chat.ToolResult{}, false
	}
	return chat.ToolResult{AgentContent: message, NoSubmission: true}, true
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
	// The generation tool prepares the exact operation from the full normalized
	// input and asks for the decision by the session approval policy (#491);
	// nothing here claims, records or consumes a conversational consent. The
	// first paid-gate layer runs for a fresh operation and for its
	// already-prepared replay: both are one new submission here.
	if result, refused := e.submissionRefused(ctx, command); refused {
		return result, nil
	}
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
		return chat.ToolResult{AgentContent: e.limitContent, NoSubmission: true}, nil
	}
	command.ExplicitParameters = explicitParameters(command)
	command.Options = e.mergedOptions(scope, command)
	return e.generate.Execute(ctx, command, observe)
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
// 的合并规则一致）；参数存储本身不返回私有状态键。
func (e *Executor) mergedOptions(scope string, command chat.ToolCommand) chat.TurnOptions {
	merged := make(map[string]json.RawMessage, len(command.Options.Parameters))
	for name, value := range command.Options.Parameters {
		merged[name] = append(json.RawMessage(nil), value...)
	}
	for name, value := range e.params.Get(scope) {
		if _, exists := merged[name]; !exists {
			merged[name] = append(json.RawMessage(nil), value...)
		}
	}
	options := command.Options
	options.Parameters = merged
	return options
}

// scopeKey 组合 owner+session 作为参数存储和生成次数的键，保证按用户隔离。
func (e *Executor) scopeKey(command chat.ToolCommand) string {
	return agentparams.ScopeKey(command.OwnerID, command.SessionID)
}
