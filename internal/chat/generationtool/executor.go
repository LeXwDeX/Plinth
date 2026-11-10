package generationtool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
	"github.com/LeXwDeX/Plinth/internal/imageinspect"
	"github.com/LeXwDeX/Plinth/internal/processing"
)

type Gateway interface {
	Offerings(generation.Capability) []generation.Offering
	OfferingAvailable(generation.OfferingID) bool
	Submit(
		context.Context,
		generation.SubmitCommand,
	) (generation.Accepted, error)
	Get(
		context.Context,
		generation.OwnerID,
		generation.TaskID,
	) (generation.Task, error)
}

type ReferenceResolver interface {
	Resolve(
		context.Context,
		generation.OwnerID,
		[]string,
	) ([]generation.AssetID, error)
}

// ImageReviewer reviews one picture before it becomes an image-to-3D input
// (#245). It reads only the owner's picture and returns deterministic checks.
type ImageReviewer interface {
	ReviewAttachment(context.Context, string, chat.Attachment, string, string) (imageinspect.Report, []imageinspect.Check, error)
}

// PictureReader opens one picture of the command's owner for the reference
// comparison (#246): a chat handle's stored asset or a frozen input asset. The
// returned bytes are bounded by the caller.
type PictureReader interface {
	OpenPicture(context.Context, string, chat.Attachment, string) ([]byte, error)
}

// ModelSource is the view of a three-view group that a 3D model made from the
// group uses.
type ModelSource struct {
	// Available is true once that view finished.
	Available bool
	// TaskID is empty when the person replaced the view with an upload; the
	// asset is then an input asset.
	TaskID  string
	AssetID string
}

// ThreeViews tells whether a Task is one view of a three-view group of the
// session. A model made from a group uses its front view only, so the
// reference picture is replaced by that view before the input is frozen.
type ThreeViews interface {
	ModelSource(ctx context.Context, owner, session, taskID string) (source ModelSource, member bool, err error)
	// GroupViews lists the finished views of one of the owner's groups in
	// the recipe's order (#65); ErrThreeViewGroupNotFound for a foreign or
	// missing group.
	GroupViews(ctx context.Context, owner, session, runID string) ([]GroupView, error)
}

// GroupView is one finished view of a three-view group: its view key, and the
// Task and asset of the picture that stands for it. TaskID is empty for a
// picture the person uploaded in place of the generated one; AssetID is then
// an input asset.
type GroupView struct {
	View    string
	TaskID  string
	AssetID string
}

// ErrThreeViewGroupNotFound: the named group is not one of the owner's in the session.
var ErrThreeViewGroupNotFound = errors.New("generation tool: three-view group not found")

type Config struct {
	HarnessResolver *resolve.Service
	Harness         harness.Store

	Gateway Gateway
	// Tasks reads the session's Tasks for the recharge guard (#294). Without
	// it the guard is off.
	Tasks       SessionTasks
	Processing  *processing.Service
	PlanCatalog processing.Catalog
	// Choices stores the in-chat choice cards (#321). Without it a processing
	// tool keeps the first compatible Offering.
	Choices       chat.ChoiceStore
	ThreeViews    ThreeViews
	Operations    OperationStore
	Grants        *executiongrant.Service
	StopRequested func(context.Context, string, string, string) (bool, error)
	References    ReferenceResolver
	// ModelAssets opens a saved model file for the read-only review tools
	// (#103); InputAssets stores their rendered pictures as the owner's input
	// assets. Both stay nil when the tools are not configured.
	ModelAssets ModelAssetGateway
	InputAssets ModelRenderStore
	// ImageReview checks an image-to-3D input before submission (#245). Nil
	// disables the gate.
	ImageReview ImageReviewer
	// PictureReader opens a session picture for compare_model_reference (#246).
	// Nil leaves that tool unavailable.
	PictureReader PictureReader
	// SelfChecks is the persisted 3D self-check record (#246). Nil leaves the
	// evidence trail, the limits and record_self_check unavailable.
	SelfChecks *chat.SelfCheckService
	// InputAssetRoute is the Schema's input-asset item route ({assetID}); a
	// three-view view the person replaced with an upload is named by it (#65).
	InputAssetRoute string
	Chat            catalog.ChatSchema
	// CanvasArtboard gives the Schema tables that turn a ratio-only output
	// size into pixels (ratio_pixels, ratio_pixel_tiers) for the size notes.
	CanvasArtboard catalog.CanvasArtboardSchema
	// Texts gives the copy of the turn's UI locale for tool results. Nil
	// keeps the Schema's default-locale copy.
	Texts *catalog.TextLocalizer
	// ModelDelivery orders the conversion routes of a tool that declares a
	// delivery format argument, exactly as the format coverage orders them.
	ModelDelivery catalog.ModelDeliverySchema
	Skills        catalog.SkillCatalogSchema
	TaskStates    []catalog.TaskStatePresentation
	Clock         func() time.Time
	Sleep         func(context.Context, time.Duration) error

	// OfferingParameters reads per-offering overrides only for a new input.
	OfferingParameters       func(ownerID, sessionID, offeringID string) map[string]json.RawMessage
	OfferingParameterReasons func(ownerID, sessionID, offeringID string) map[string]generation.ParameterReason

	// Logger records each refused processing step with its call and reason
	// (#321 A, #233). Nil discards.
	Logger *slog.Logger
}

type Executor struct {
	harnessResolver *resolve.Service
	harness         harness.Store

	plans         *processing.Service
	planCatalog   processing.Catalog
	choices       chat.ChoiceStore
	threeViews    ThreeViews
	gateway       Gateway
	tasks         SessionTasks
	operations    OperationStore
	grants        *executiongrant.Service
	stopRequested func(context.Context, string, string, string) (bool, error)
	references    ReferenceResolver
	modelAssets   ModelAssetGateway
	inputAssets   ModelRenderStore
	imageReview   ImageReviewer
	pictureReader PictureReader
	selfChecks    *chat.SelfCheckService
	inputAsset    string
	chat          catalog.ChatSchema
	texts         *catalog.TextLocalizer
	modelDelivery catalog.ModelDeliverySchema
	skills        map[string]catalog.SkillSchema
	publicStates  map[generation.TaskState]string
	clock         func() time.Time
	sleep         func(context.Context, time.Duration) error

	// modelReview holds the parsed-model cache and render concurrency bound
	// of the read-only review tools (#103).
	modelReview *modelReviewState

	// canvasArtboard holds the Schema tables that turn a ratio into pixels.
	canvasArtboard catalog.CanvasArtboardSchema

	offeringParameters       func(string, string, string) map[string]json.RawMessage
	offeringParameterReasons func(string, string, string) map[string]generation.ParameterReason

	logger        *slog.Logger
	modelFormats  ModelFormatStatus
	modelVersions ModelVersions

	// approvalLocks serialize approval-card creation per session.
	approvalLocks [32]sync.Mutex
}

func New(config Config) (*Executor, error) {
	if config.Gateway == nil || config.References == nil {
		return nil, errors.New("generation tool: incomplete dependencies")
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	if config.Sleep == nil {
		config.Sleep = sleepContext
	}
	if config.Logger == nil {
		config.Logger = slog.New(slog.DiscardHandler)
	}
	states := make(map[generation.TaskState]string, len(config.TaskStates))
	for _, state := range config.TaskStates {
		states[generation.TaskState(state.InternalState)] = state.PublicState
	}
	skills := make(map[string]catalog.SkillSchema, len(config.Skills.Skills))
	for _, skill := range config.Skills.Skills {
		skills[skill.ID] = skill
	}
	return &Executor{
		harnessResolver: config.HarnessResolver,
		harness:         config.Harness,

		plans:         config.Processing,
		planCatalog:   config.PlanCatalog,
		choices:       config.Choices,
		threeViews:    config.ThreeViews,
		gateway:       config.Gateway,
		tasks:         config.Tasks,
		operations:    config.Operations,
		grants:        config.Grants,
		stopRequested: config.StopRequested,
		references:    config.References,
		modelAssets:   config.ModelAssets,
		inputAssets:   config.InputAssets,
		imageReview:   config.ImageReview,
		pictureReader: config.PictureReader,
		selfChecks:    config.SelfChecks,
		inputAsset:    config.InputAssetRoute,
		chat:          config.Chat,
		texts:         config.Texts,
		modelDelivery: config.ModelDelivery,
		skills:        skills,
		publicStates:  states,
		clock:         config.Clock,
		sleep:         config.Sleep,
		modelReview:   newModelReviewState(config.Chat.ModelReview),

		canvasArtboard: config.CanvasArtboard,

		offeringParameters:       config.OfferingParameters,
		offeringParameterReasons: config.OfferingParameterReasons,
		logger:                   config.Logger,
	}, nil
}

type preparedGateway interface {
	PrepareInput(context.Context, generation.PrepareInputCommand) (generation.PreparedInput, error)
	GetPreparedInput(context.Context, generation.OwnerID, generation.PreparedInputID) (generation.PreparedInput, error)
	SubmitPrepared(context.Context, generation.SubmitPreparedCommand) (generation.Accepted, error)
}

// SetThreeViews wires the three-view groups during application assembly,
// before the executor receives any call; they depend on services that are
// built after it.
func (executor *Executor) SetThreeViews(threeViews ThreeViews) { executor.threeViews = threeViews }

// SetProcessing wires the same plan service used by the workspace before any
// tool call. Chat only creates and reads plans; it never advances their steps.
func (executor *Executor) SetProcessing(plans *processing.Service, catalog processing.Catalog) {
	executor.plans, executor.planCatalog = plans, catalog
}

func (executor *Executor) UsesExecutionGrants() bool {
	_, prepared := executor.gateway.(preparedGateway)
	return prepared && executor.operations != nil && executor.grants != nil &&
		executor.grants.Grants != nil && executor.stopRequested != nil
}

func (executor *Executor) ExistingOperation(ctx context.Context, command chat.ToolCommand) (bool, error) {
	if command.Tool.Processing != nil && command.Tool.Processing.Plan != nil && executor.plans != nil {
		plan, err := executor.plans.Get(ctx, generation.OwnerID(command.OwnerID), processing.PlanID(generation.OwnerID(command.OwnerID), ProcessingPlanKey(command)))
		if errors.Is(err, processing.ErrMissing) {
			return false, nil
		}
		return err == nil && plan.SessionID == command.SessionID, err
	}
	if !executor.UsesExecutionGrants() {
		return false, nil
	}
	_, err := executor.operations.LoadAgentOperation(ctx, command.OwnerID, command.SessionID,
		command.AssistantMessageID, command.Call.ID)
	if errors.Is(err, ErrOperationNotFound) {
		return false, nil
	}
	return err == nil, err
}

// BoundTaskForCall is a recovery-only lookup. It never prepares or submits a
// new operation, so a historical Task remains observable after its Offering is
// disabled. Only the immutable call identity and server-held grant are used.
func (executor *Executor) BoundTaskForCall(ctx context.Context, command chat.ToolCommand) (string, error) {
	if !executor.UsesExecutionGrants() {
		return "", nil
	}
	op, err := executor.operations.LoadAgentOperation(ctx, command.OwnerID, command.SessionID,
		command.AssistantMessageID, command.Call.ID)
	if errors.Is(err, ErrOperationNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if op.Harness != turnHarnessBinding(command) || op.OwnerID != command.OwnerID || op.SessionID != command.SessionID ||
		op.AssistantMessageID != command.AssistantMessageID || op.CallID != command.Call.ID ||
		op.ToolName != command.Call.Name || op.IdempotencyKey != command.IdempotencyKey() {
		return "", errors.Join(chat.ErrTurnCheckpointConflict, ErrOperationConflict)
	}
	if op.GrantRequestID == "" {
		return "", nil
	}
	grant, err := executor.grants.Grants.GetExecutionGrant(ctx, op.OwnerID, op.GrantRequestID)
	if err != nil {
		return "", err
	}
	if !agentGrantMatchesOperation(grant, op) {
		return "", errors.Join(chat.ErrTurnCheckpointConflict, ErrOperationConflict)
	}
	if op.Detached {
		// A detached operation's task is reported by the pending-approval
		// service, never by the turn that stopped waiting.
		return "", nil
	}
	if grant.Status == executiongrant.Bound {
		if grant.TaskID == "" {
			return "", errors.Join(chat.ErrTurnCheckpointConflict, ErrOperationConflict)
		}
		return grant.TaskID, nil
	}
	return "", nil
}

// DetachedOperationForCall reports whether the call's operation was handed to
// the pending-approval service. Its approval card waits for the person, not
// for the turn: a Stop of the turn must leave it, and the task a later approval
// creates under the call's frozen idempotency key, alone.
func (executor *Executor) DetachedOperationForCall(ctx context.Context, command chat.ToolCommand) (bool, error) {
	if !executor.UsesExecutionGrants() {
		return false, nil
	}
	op, err := executor.operations.LoadAgentOperation(ctx, command.OwnerID, command.SessionID,
		command.AssistantMessageID, command.Call.ID)
	if errors.Is(err, ErrOperationNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if op.Harness != turnHarnessBinding(command) || op.OwnerID != command.OwnerID || op.SessionID != command.SessionID ||
		op.AssistantMessageID != command.AssistantMessageID || op.CallID != command.Call.ID ||
		op.ToolName != command.Call.Name ||
		(command.FrozenIdempotencyKey != "" && op.IdempotencyKey != command.FrozenIdempotencyKey) {
		return false, errors.Join(chat.ErrTurnCheckpointConflict, ErrOperationConflict)
	}
	return op.Detached, nil
}

// SelectionError 在"人类未选择生成模型"时返回 Schema 声明的失败文案，
// 否则返回空串。它把模型必选校验前移（AGT-04）：同意门禁之前先做
// 不需要用户交互就能判定的预检，避免批准被一次注定失败的调用消费。
func (executor *Executor) SelectionError(command chat.ToolCommand) string {
	if activeHarness(command) {
		return ""
	}
	if executor.userModel(command) == "" && command.Tool.RequireUserModel && !executor.fallbackModelAllowed(command) {
		return executor.localized(command, command.Tool.ModelRequiredMessage)
	}
	return ""
}

func (executor *Executor) Execute(
	ctx context.Context,
	command chat.ToolCommand,
	observe func(chat.ToolObservation) error,
) (chat.ToolResult, error) {
	if command.Tool.Processing != nil && command.Tool.Processing.Plan != nil {
		if activeHarness(command) {
			return executor.proposeHarness(ctx, command)
		}
		if err := executor.checkCurrentHarness(ctx, command); err != nil {
			return executor.harnessRefusal(command, err)
		}
		return executor.executeProcessingPlan(ctx, command)
	}
	// A persisted binding is an observation of an already accepted task. The
	// current catalog, selected model, and reference URLs may have changed since
	// submission; none of them may prevent recovery or trigger another submit.
	if command.BoundTaskID != "" {
		if executor.UsesExecutionGrants() {
			original, err := executor.BoundTaskForCall(ctx, command)
			if err != nil {
				return chat.ToolResult{NoSubmission: true}, err
			}
			if original != command.BoundTaskID {
				return chat.ToolResult{NoSubmission: true}, ErrOperationConflict
			}
		}
		return executor.observeBoundTask(ctx, command, observe)
	}
	if executor.UsesExecutionGrants() {
		stored, err := executor.operations.LoadAgentOperation(ctx, command.OwnerID, command.SessionID,
			command.AssistantMessageID, command.Call.ID)
		if err == nil {
			return executor.resumeAgentOperation(ctx, command, stored, observe, nil, nil)
		}
		if !errors.Is(err, ErrOperationNotFound) {
			// A failed read cannot prove an earlier submission did not commit.
			return chat.ToolResult{}, err
		}
	}
	if activeHarness(command) {
		return executor.executeHarnessOperation(ctx, command, observe)
	}
	if err := executor.checkCurrentHarness(ctx, command); err != nil {
		return executor.harnessRefusal(command, err)
	}
	if command.Tool.Processing != nil {
		return executor.executeProcessing(ctx, command, observe)
	}
	var arguments map[string]json.RawMessage
	if err := json.Unmarshal(command.Call.Arguments, &arguments); err != nil {
		arguments = map[string]json.RawMessage{}
	}
	prompt := rawString(
		arguments[executor.chat.Context.ToolPromptArgument],
	)
	trace := &promptTrace{Original: prompt, Final: prompt}
	if executor.canvasTurn(command) && command.Tool.CanvasSource == canvasSourceRefuse {
		// The person chose the canvas; a tool that reads no image would drop
		// it. Nothing is submitted and the Agent is told which tools fit.
		result := executor.failure(command, "", executor.canvasRefusal(command))
		result.NoSubmission = true
		return result, nil
	}
	var turnReferenceURLs []string
	// A multiview step (#65) takes its pictures and views from the person's
	// structured request or the named three-view group, never from handles.
	multiview, refusal, err := executor.multiviewSelection(ctx, command, arguments)
	if err != nil {
		result := executor.executionFailure(command, "")
		result.NoSubmission = true
		return result, err
	}
	if refusal != "" {
		result := executor.failure(command, "", refusal)
		result.NoSubmission = true
		return result, nil
	}
	if multiview != nil {
		for _, name := range []string{executor.chat.ThreeView.Multiview.ViewsArgument,
			executor.chat.Context.ReferenceHandlesArgument, executor.chat.Context.ToolReferenceArgument} {
			delete(arguments, name)
		}
		if multiview.fromRequest && command.Tool.ModelArgument != "" {
			model, _ := json.Marshal(executor.multiviewModelID(command, multiview.model))
			arguments[command.Tool.ModelArgument] = model
		}
		turnReferenceURLs = multiview.URLs
	} else if command.Tool.AcceptReferenceImages == nil || *command.Tool.AcceptReferenceImages {
		turnReferenceURLs, err = executor.selectReferences(command, arguments)
		if err != nil {
			if result, refused := executor.refuseReference(command, err); refused {
				return result, nil
			}
			result := executor.executionFailure(command, "")
			result.NoSubmission = true
			return result, err
		}
	}
	// #250: the required turn inputs lead the list (the artboard snapshot and
	// the pictures marked "input"); identity anchors follow, then the style
	// pictures, then the turn's other ordinary references. The order is also
	// the trimming priority.
	identityReferenceURLs, identityPurposes, anchorHandles, anchorRefusal, anchorErr := executor.anchorReferenceURLs(command, arguments)
	if anchorErr != nil && anchorRefusal == "" {
		anchorRefusal = executor.anchorInvalid(command, "锚点参考图不可用")
	}
	if anchorRefusal != "" {
		result := executor.failure(command, "", anchorRefusal)
		result.NoSubmission = true
		return result, nil
	}
	styleReferenceURLs, stylePurposes, styleReferenceErr := executor.styleReferenceURLs(command)
	if styleReferenceErr != nil {
		result := executor.failure(command, "", executor.styleLockUnavailableMessage(command))
		result.NoSubmission = true
		return result, nil
	}
	// A style anchor is auto-attached without an argument, so its handles join
	// the identity anchors' for the unavailable-picture refusal (#250). The
	// person's style lock keeps its historical wording.
	anchorHandles = mergeReferenceLabels(anchorHandles, executor.styleAnchorLabels(command))
	serverPurposes := mergeReferencePurposes(identityPurposes, stylePurposes)
	// A historical turn without anchors or a required input keeps its list
	// byte for byte (a duplicate URL stays ambiguous, a three-view selection
	// keeps its repeats). An anchored turn de-duplicates so the same picture is
	// not sent twice, and the counts follow the final list so a trimmed suffix
	// is reported by the right class.
	groups := executor.referenceGroupsFor(command, identityReferenceURLs, styleReferenceURLs, turnReferenceURLs)
	referenceURLs := groups.URLs
	if command.Tool.ReferencePurposesArgument != "" {
		for handle, purpose := range command.Options.AttachmentPurposes {
			if purpose != "input" {
				continue
			}
			resolved, resolveErr := executor.handlePolicy().ResolveHandles(command.ReferenceHistory(), command.Options.Selection, []string{handle}, chat.AttachmentKindImage)
			position := -1
			if resolveErr == nil && len(resolved) == 1 && resolved[0].URL != "" {
				position = slices.Index(referenceURLs, resolved[0].URL)
			}
			if position < 0 || slices.Contains(referenceURLs[position+1:], resolved[0].URL) || (executor.canvasTurn(command) && position != 0) {
				message := strings.ReplaceAll(executor.chat.Context.ReferencePurposeInvalidTemplate, "{handle}", handle)
				result := executor.failure(command, "", message)
				result.NoSubmission = true
				return result, nil
			}
		}
	}
	capability := command.Tool.FallbackCapability
	if len(referenceURLs) > 0 {
		capability = command.Tool.Capability
	}
	var offering generation.Offering
	if selectsOfferingByArgument(command.Tool) {
		// #189: the Agent names the Offering (a template, or the source Task
		// whose pairing picks the build); the Offering's own capability runs.
		selected, refusal, err := executor.selectArgumentOffering(ctx, command, arguments)
		if err != nil {
			result := executor.executionFailure(command, "")
			result.NoSubmission = true
			return result, err
		}
		if refusal != "" {
			result := executor.failure(command, "", refusal)
			result.NoSubmission = true
			return result, nil
		}
		offering, capability = selected, selected.Capability
	} else if command.Tool.ModelArgument != "" {
		// #103: the person's explicit pick, the Agent's model argument, the
		// session's production purpose and the defaults decide, in that order.
		choice, err := executor.chooseOffering(command, capability, arguments,
			modelInputFor(command, referenceURLs, multiview))
		if err != nil {
			result := executor.executionFailure(command, "")
			result.NoSubmission = true
			return result, err
		}
		if choice.Refusal != "" {
			return executor.modelChoiceFailure(command, choice.Refusal), nil
		}
		offering = choice.Offering
	} else {
		model := executor.userModel(command)
		if model == "" && command.Tool.RequireUserModel && !executor.fallbackModelAllowed(command) {
			result := executor.failure(command, "", command.Tool.ModelRequiredMessage)
			result.NoSubmission = true
			return result, nil
		}
		offering, err = executor.selectOffering(
			capability,
			command.Tool,
			model,
		)
		if err != nil {
			result := executor.executionFailure(command, "")
			result.NoSubmission = true
			return result, err
		}
	}
	// #244: a frozen selection mask must be bound to a mask parameter, never
	// quietly dropped into a whole-image redraw. A model the person or the
	// Agent named is refused; only an unchosen model may move to a mask-capable
	// Offering, and the result names that switch.
	offering, maskRefusal := executor.ensureMaskOffering(command, capability,
		modelInputFor(command, referenceURLs, multiview), offering)
	if maskRefusal != "" {
		result := executor.failure(command, "", maskRefusal)
		result.NoSubmission = true
		return result, nil
	}
	if multiview != nil {
		group := !multiview.fromRequest || (command.Options.Multiview != nil && command.Options.Multiview.Group != "")
		if refusal := executor.fitMultiviewViews(command, offering, multiview, group); refusal != "" {
			result := executor.failure(command, "", refusal)
			result.NoSubmission = true
			return result, nil
		}
		if declaresReferenceViews(offering) {
			// An order-only Offering has no view-label parameter: the pictures
			// already arrive in the declared view order (#243).
			views, _ := json.Marshal(multiview.Views)
			arguments[executor.chat.ThreeView.Multiview.ViewsArgument] = views
		}
		groups = executor.referenceGroupsFor(command, identityReferenceURLs, styleReferenceURLs, multiview.URLs)
		referenceURLs = groups.URLs
	}
	if groups.Identity+groups.Style > 0 {
		if _, supported := semanticParameter(offering, generation.SemanticReferenceImage); !supported {
			result := executor.failure(command, "", executor.styleLockUnavailableMessage(command))
			result.NoSubmission = true
			return result, nil
		}
	}
	// references records a cut of the pictures to fewer than the turn gave.
	var references referenceUse
	var selection referenceSelection
	var finalReferences []referenceCandidate
	if offering.ReferenceSelectionPolicy == "preserve" {
		for _, parameter := range offering.Parameters {
			if parameter.Semantic == generation.SemanticReferenceImage && parameter.MaxItems > 0 && len(referenceURLs) > parameter.MaxItems {
				// Keep every selected view: refuse the step with the Schema's
				// reason instead of cutting the list (#329), so the Agent can
				// tell the person how many fit. Without the text the old
				// diagnostic error is returned as before.
				refusal := referenceLimitExceededError(offering, parameter, len(referenceURLs))
				if refusal == nil {
					refusal = &generation.Error{Code: generation.ErrInvalidInput, Operation: "reference-selection", Cause: fmt.Errorf("parameter %q permits at most %d images", parameter.Name, parameter.MaxItems)}
				}
				if result, refused := executor.inputRefusal(command, refusal); refused {
					return result, nil
				}
				return chat.ToolResult{NoSubmission: true}, refusal
			}
		}
	} else {
		if capability == generation.ImageTo3D {
			var refusal string
			selection, refusal, err = executor.modelSourceReferences(ctx, command, referenceURLs)
			if err != nil {
				result := executor.executionFailure(command, "")
				result.NoSubmission = true
				return result, err
			}
			if refusal != "" {
				result := executor.failure(command, "", refusal)
				result.NoSubmission = true
				return result, nil
			}
			referenceURLs = selection.urls()
		}
		given := len(referenceURLs)
		referenceURLs = truncateReferences(referenceURLs, offering)
		if len(referenceURLs) < given {
			identityUsed, styleUsed, turnUsed := groups.countClasses(len(referenceURLs))
			references = referenceUse{
				Used: len(referenceURLs), Given: given,
				IdentityGiven: groups.Identity, IdentityUsed: identityUsed,
				StyleGiven: groups.Style, StyleUsed: styleUsed,
				TurnGiven: groups.Turn, TurnUsed: turnUsed,
			}
			references.StyleTrimmed = styleUsed < groups.Style
			// The model limit cuts the tail: ordinary references first, then
			// style pictures. Everything it cut is reported to the Agent by its
			// server-visible handle (#250).
			if dropped := groups.droppedTurnURLs(len(referenceURLs)); len(dropped) > 0 {
				references.DroppedTurn = executor.referenceHandleLabels(command, dropped)
				references.DroppedTurnCount = len(dropped)
			}
			if dropped := groups.droppedStyleURLs(len(referenceURLs)); len(dropped) > 0 {
				references.DroppedStyle = executor.styleReferenceLabels(command, dropped)
				references.DroppedStyleCount = len(dropped)
			}
		}
		if capability == generation.ImageTo3D {
			if len(selection.Candidates) < len(referenceURLs) {
				return chat.ToolResult{NoSubmission: true}, errors.New("generation tool: reference selection does not match the frozen URLs")
			}
			finalReferences = selection.Candidates[:len(referenceURLs)]
		}
	}
	if capability == generation.ImageTo3D {
		// Style-lock pictures guide the look, not the geometry: they are not
		// 3D inputs and must not be judged by the input checklist. They sit
		// between the identity anchors and the ordinary references, so filter
		// by class instead of assuming a suffix.
		gateReferences := make([]string, 0, len(referenceURLs))
		for index, reference := range referenceURLs {
			if index < len(groups.Classes) && groups.Classes[index] == referenceClassStyle {
				continue
			}
			gateReferences = append(gateReferences, reference)
		}
		override, refusal := executor.review3DInput(ctx, command, gateReferences)
		if refusal != "" {
			result := executor.failure(command, "", refusal)
			result.NoSubmission = true
			return result, nil
		}
		if override != "" {
			executor.logger.Info("image-to-3D input gate overridden",
				"owner_id", command.OwnerID, "session_id", command.SessionID,
				"call_id", command.Call.ID, "reason", override)
		}
	}

	purposes, refusal := executor.referencePurposes(command, arguments, referenceURLs, serverPurposes)
	if refusal != "" {
		result := executor.failure(command, "", refusal)
		result.NoSubmission = true
		return result, nil
	}
	switch {
	case purposes != "":
		before := prompt
		prompt = promptWithPurposes(command, purposes, prompt)
		if prompt != before {
			trace.add(promptLayerPurposesTemplate, command.Tool.PromptWithReferencePurposesTemplate)
		}
	case len(referenceURLs) > 0:
		before := prompt
		prompt = executor.synthesizePrompt(command, prompt, referenceURLs)
		if prompt != before {
			layer := promptLayerReferenceTemplate
			template := command.Tool.PromptWithReferenceTemplate
			if executor.previousPrompt(command, referenceURLs[0]) != "" {
				layer = promptLayerPreviousPromptTemplate
				template = command.Tool.PromptWithPreviousTemplate
			}
			trace.add(layer, template)
		}
	}
	skills, skillExecution := executor.promptSkills(command)
	// A tool-binding Skill directs the Agent to its tool (see the orchestrator's
	// directive); it carries no style prompt for a generation tool to apply.
	if bound, exists := skills[command.Options.SkillID]; exists && bound.Tool != "" {
		command.Options.SkillID = ""
	}
	if command.Options.SkillID != "" {
		if _, exists := skills[command.Options.SkillID]; !exists || !slices.Contains(offering.Features, executor.chat.Skills.Feature) {
			return chat.ToolResult{Error: "selected skill is unavailable", NoSubmission: true}, nil
		}
	}
	skillOverrides := map[generation.ParameterSemantic]json.RawMessage(nil)
	if application, applied := executor.promptSkillApplication(command, offering, capability, skills, skillExecution); applied {
		fields := application.Fields
		prompt = strings.NewReplacer(
			fields.UserInput, prompt,
			fields.SkillPrompt, application.Prompt,
		).Replace(application.Template)
		skillOverrides = application.Overrides
		trace.add(promptLayerSkillTemplate, application.Template)
	}
	beforeStyleLock := prompt
	prompt = executor.applyStyleLockPrompt(command, offering, prompt)
	if prompt != beforeStyleLock {
		trace.add(promptLayerStyleLock, strings.TrimPrefix(prompt, beforeStyleLock))
	}
	if value, applied := defaultNegativePromptValue(command, offering); applied {
		trace.add(promptLayerNegativePrompt, value)
	}
	prompt, truncated, maximum, refusalResult, refused := executor.applyPromptLength(command, offering, prompt)
	trace.Truncated = truncated
	trace.MaxLength = maximum
	trace.Final = prompt
	if refused {
		return refusalResult, nil
	}
	input, referenceIDs, err := executor.buildInput(
		ctx,
		command,
		offering,
		arguments,
		prompt,
		referenceURLs,
		skillOverrides,
		anchorHandles,
	)
	if err != nil {
		if result, refused := executor.refuseReference(command, err); refused {
			return result, nil
		}
		if result, refused := executor.inputRefusal(command, err); refused {
			return result, nil
		}
		result := executor.executionFailure(command, "")
		result.NoSubmission = true
		return result, err
	}
	idempotencyKey := command.IdempotencyKey()
	if executor.UsesExecutionGrants() {
		return executor.prepareAgentOperation(ctx, command, capability, offering, input, prompt, idempotencyKey,
			references, selection, finalReferences, referenceIDs, "", observe, trace)
	}
	if offering.NativePricing != nil {
		return chat.ToolResult{NoSubmission: true}, errors.New("native pricing requires prepared authorization")
	}
	// 提交不随轮次取消而中断：半途放弃会把"结果不明"的付费提交留给
	// 上游；完整提交后绑定会落库，停止路径据此取消在飞任务（AGT-06）。
	accepted, err := executor.gateway.Submit(
		context.WithoutCancel(ctx),
		generation.SubmitCommand{
			OwnerID:        generation.OwnerID(command.OwnerID),
			SessionID:      command.SessionID,
			Capability:     capability,
			OfferingID:     offering.ID,
			Input:          input,
			IdempotencyKey: idempotencyKey,
		},
	)
	if err != nil {
		if result, refused := executor.inputRefusal(command, err); refused {
			return result, nil
		}
		result := executor.executionFailure(command, "")
		result.NoSubmission = knownNotSubmitted(err)
		result.RetryAfter = admissionRetryAfter(err)
		return result, err
	}
	if observe != nil {
		if err := observe(chat.ToolObservation{
			Registered:       true,
			TaskID:           string(accepted.TaskID),
			Capability:       string(capability),
			Prompt:           prompt,
			AIModel:          offering.ModelID,
			EstimatedCredits: offering.EstimateTotalCredits(input),
		}); err != nil {
			return executor.awaitTask(command, accepted.TaskID, executor.taskWaitDeadline(command, offering), err)
		}
	}
	var fact *ReferenceSourceFact
	if capability == generation.ImageTo3D && offering.ReferenceSelectionPolicy != "preserve" {
		fact = referenceFact(selection, finalReferences)
	}
	info := stepInfo{Credits: offering.EstimateTotalCredits(input), CreditsMetered: offering.CreditsMetered,
		ExtraCredits:  offering.ExtraCreditDetail(input).Credits,
		ReferenceNote: executor.stepReferenceNote(command, offering, fact, references, input)}
	info.PromptFinal, info.PromptNote = executor.promptReport(command, command.Tool, offering, trace)
	return executor.wait(ctx, command, offering, accepted.TaskID, observe, info)
}

// stepInfo is what the Agent and the chat projection learn about one accepted
// step besides its Task: the estimated credits and who approved it.
type stepInfo struct {
	Credits        int
	NativeQuote    *generation.NativeQuote
	CreditsMetered bool
	// ExtraCredits is the part of Credits that pays for extra options (extra
	// export formats); 0 when the step has none.
	ExtraCredits int
	Approval     string
	// Formats are the model file formats a finished 3D step delivered; they
	// are reported only for tools whose Schema names a result key for them.
	Formats []string
	// Model is the name of the model the step ran on; Fallback is true when
	// the Schema's fallback chose it because the user picked none.
	Model    string
	Fallback bool
	// ModelChoice names who chose the 3D model (#103) when the tool declares
	// a result key for it.
	ModelChoice *modelChoice
	// ModelNote says the person's pick was a retired model and the step ran
	// on its successor (#329 follow-up). Empty when it does not apply.
	ModelNote string
	// MaskNote says the Agent's own mask argument was dropped because the turn
	// carried no frozen selection mask (#244). Empty when it does not apply.
	MaskNote string
	// ReferenceNote says which of the references a step really used, such as
	// "only the front view of a three-view group". Empty when it does not apply.
	ReferenceNote string
	// SizeNote says, on a canvas turn, that the output size followed the
	// artboard or which nearest legal size was used (#272); else that it
	// followed the one reference picture (#296).
	SizeNote string
	// PurposeBudgetNote names the step's values over the session purpose's
	// budget (#129). Empty when every bounded value fits.
	PurposeBudgetNote string
	// Budget is the production plan that authorized the step (#103); nil when
	// no plan did (another mode, or a browser-started step).
	Budget *budget.Summary
	// PromptFinal is the exact prompt the server submitted when a layer
	// changed it (#249); PromptNote names the layers and any cut. Both are
	// reported only through the tool's declared result keys.
	PromptFinal string
	PromptNote  map[string]any
}

// budgetResult is the budget part of a paid tool result: the plan the step
// belongs to (its ID and title) and what it still holds after this step, per
// credit unit (#120, #135). The Agent uses it to decide whether the next
// planned step fits in its own unit, without guessing from earlier numbers or
// adding different units.
func budgetResult(summary budget.Summary) map[string]any {
	remaining := make([]budget.Amount, 0, len(summary.Units))
	reserved := make([]budget.Amount, 0, len(summary.Units))
	for _, unit := range summary.Units {
		remaining = append(remaining, unit.Amount(unit.Remaining))
		reserved = append(reserved, unit.Amount(unit.Reserved))
	}
	return map[string]any{
		"id":        summary.ID,
		"title":     summary.Plan.Title,
		"remaining": remaining,
		"reserved":  reserved,
	}
}

const (
	approvalByPolicy = "policy"
	approvalByHuman  = "human"
)

// approvalSource names who approved a grant. An empty grant (recovery paths
// that never loaded one) has no known source. A step an approved production
// plan approved (actor budget:<id>) was approved by the session's approval
// policy, not by a person clicking its card (#135); its result also carries
// the plan's budget object.
func approvalSource(grant executiongrant.Grant) string {
	switch {
	case grant.RequestID == "":
		return ""
	case grant.ApprovedByPolicy(), grant.ApprovedByBudget():
		return approvalByPolicy
	default:
		return approvalByHuman
	}
}

func taskCredits(task generation.Task) int {
	if task.NativeQuote != nil {
		return 0
	}
	return task.Offering.EstimateTotalCredits(task.Input)
}

// taskStep is what the Agent learns about an accepted task's price: the
// estimate and the extra-credit part of it.
func taskStep(task generation.Task, approval string) stepInfo {
	return stepInfo{NativeQuote: task.NativeQuote, Credits: taskCredits(task), CreditsMetered: task.Offering.CreditsMetered, Approval: approval,
		ExtraCredits: task.Offering.ExtraCreditDetail(task.Input).Credits}
}

// admissionRetryAfter is the wait the generation kernel asks for when it
// refused a submission because the in-flight Task admission is full. The
// submission did not happen, so the orchestrator may queue the call and submit
// it again with the same frozen idempotency key. Zero for any other error.
func admissionRetryAfter(err error) time.Duration {
	return generation.AdmissionRetryAfter(err)
}

func knownNotSubmitted(err error) bool {
	if errors.Is(err, generation.ErrConversionInProgress) {
		// The repository refused before writing anything: an overlapping
		// conversion is active, so the turn must not wait for a Task that
		// does not exist.
		return true
	}
	for _, code := range []generation.ErrorCode{
		generation.ErrInvalidCapability, generation.ErrInvalidInput,
		generation.ErrUnknownOffering, generation.ErrNoOffering,
		generation.ErrNotFound, generation.ErrForbidden,
		generation.ErrTooManyRequests,
	} {
		if generation.IsCode(err, code) {
			return true
		}
	}
	return false
}

func (executor *Executor) observeBoundTask(
	ctx context.Context,
	command chat.ToolCommand,
	observe func(chat.ToolObservation) error,
) (chat.ToolResult, error) {
	task, err := executor.gateway.Get(
		ctx, generation.OwnerID(command.OwnerID), generation.TaskID(command.BoundTaskID),
	)
	if err != nil {
		return executor.awaitTask(command, generation.TaskID(command.BoundTaskID),
			executor.taskWaitDeadline(command, generation.Offering{}), err)
	}
	if task.ID != generation.TaskID(command.BoundTaskID) ||
		task.OwnerID != generation.OwnerID(command.OwnerID) || task.SessionID != command.SessionID {
		return executor.executionFailure(command, command.BoundTaskID),
			errors.New("generation tool: bound task belongs to another session")
	}
	if observe != nil {
		if err := observe(chat.ToolObservation{
			Registered:  true,
			TaskID:      command.BoundTaskID,
			Capability:  string(task.Capability),
			AIModel:     task.Offering.ModelID,
			NativeQuote: task.NativeQuote, EstimatedCredits: taskCredits(task),
		}); err != nil {
			return executor.awaitTask(command, task.ID, executor.taskWaitDeadline(command, task.Offering), err)
		}
	}
	info := taskStep(task, "")
	if executor.UsesExecutionGrants() {
		if op, err := executor.operations.LoadAgentOperation(ctx, command.OwnerID, command.SessionID,
			command.AssistantMessageID, command.Call.ID); err == nil && op.ToolName == command.Call.Name &&
			op.CallID == command.Call.ID && op.AssistantMessageID == command.AssistantMessageID &&
			op.Harness == turnHarnessBinding(command) && op.IdempotencyKey == command.IdempotencyKey() {
			if prepared, err := executor.gateway.(preparedGateway).GetPreparedInput(ctx,
				generation.OwnerID(op.OwnerID), op.PreparedInputID); err == nil {
				info.ReferenceNote = executor.operationReferenceNote(command, op, prepared, task.Offering)
				trace := executor.promptTraceFromPrepared(ctx, command, task.Offering, prepared.NormalizedInput)
				info.PromptFinal, info.PromptNote = executor.promptReport(command, command.Tool, task.Offering, trace)
			}
			// The grant that admitted the Task still says who approved it; a
			// read failure only leaves the source unknown, as before.
			if grant, err := executor.grants.Grants.GetExecutionGrant(ctx, op.OwnerID, op.GrantRequestID); err == nil {
				info.Approval = approvalSource(grant)
			}
			if summary, found, err := executor.grants.BudgetForGrant(ctx, op.OwnerID, op.GrantRequestID); err == nil && found {
				info.Budget = &summary
			}
		}
	}
	result, err := executor.wait(ctx, command, task.Offering, task.ID, observe, info)
	// #246: a paid correction accepted while its Task was still running gets
	// its self-check counters added to the receipt that is built here.
	return executor.attachCorrectionReceipt(ctx, command, result), err
}

// multiviewModelID accepts a structured request's model as either the model ID
// the tool's enum names or the Offering ID, like every other model choice, and
// returns the model ID.
func (executor *Executor) multiviewModelID(command chat.ToolCommand, model string) string {
	for _, offering := range executor.gateway.Offerings(generation.Capability(command.Tool.Capability)) {
		if string(offering.ID) == model && offering.ModelID != "" {
			return offering.ModelID
		}
	}
	return model
}

func (executor *Executor) selectOffering(
	capability generation.Capability,
	tool catalog.ChatToolSchema,
	model string,
) (generation.Offering, error) {
	offerings := executor.gateway.Offerings(capability)
	if tool.RequireUniqueOffering {
		var selected generation.Offering
		count := 0
		for _, offering := range offerings {
			if offering.Capability == capability && offering.Enabled && executor.gateway.OfferingAvailable(offering.ID) {
				selected = offering
				count++
			}
		}
		if count != 1 {
			return generation.Offering{}, fmt.Errorf("generation tool: expected one available offering for %q, got %d", capability, count)
		}
		return selected, nil
	}
	if model == "" && !tool.IgnoreUserModel {
		// No user choice for this domain (a Schema-allowed fallback, or a tool
		// that needs none): the default, else recommended, else first
		// available Offering.
		if offering, found := preferredOffering(offerings, executor.gateway.OfferingAvailable); found {
			return offering, nil
		}
	}
	for _, offering := range offerings {
		if !offering.Enabled ||
			!executor.gateway.OfferingAvailable(offering.ID) {
			continue
		}
		// IgnoreUserModel 声明工具与用户所选模型无关（如抠图）；
		// 用户模型属于其他 capability 时不能把工具过滤成无 Offering。
		if tool.IgnoreUserModel ||
			model == "" ||
			model == offering.ModelID ||
			model == string(offering.ID) {
			return offering, nil
		}
	}
	return generation.Offering{}, fmt.Errorf(
		"generation tool: no available offering for %q",
		model,
	)
}

// toolDomain is the workspace domain a tool belongs to. A tool that spans
// several domains follows the domain in view.
func toolDomain(command chat.ToolCommand) string {
	if len(command.Tool.Execution.Domains) == 1 {
		return command.Tool.Execution.Domains[0]
	}
	return command.Options.CreativeDomain
}

// userModel is the model the user chose for the domain of the tool being run.
func (executor *Executor) userModel(command chat.ToolCommand) string {
	return command.Options.ModelFor(toolDomain(command))
}

// retiredModelNote is the Schema note for a step that ran on the successor
// of the retired model the person picked for the tool's domain (#329
// follow-up): the service moved the pick when the turn started.
func (executor *Executor) retiredModelNote(command chat.ToolCommand, offering generation.Offering) string {
	template := executor.localized(command, executor.chat.Presentation.Messages.RetiredModelFallback)
	if template == "" || command.Tool.ResultFields.ModelNote == "" || command.Tool.IgnoreUserModel {
		return ""
	}
	switched, ok := command.Options.RetiredModelFor(toolDomain(command))
	if !ok || offering.ModelID != switched.To || executor.userModel(command) != switched.To {
		return ""
	}
	to := executor.localized(command, offering.ModelName)
	if to == "" {
		to = executor.localized(command, switched.ToName)
	}
	return strings.NewReplacer("{from}", executor.localized(command, switched.FromName), "{to}", to).Replace(template)
}

// fallbackModelAllowed reports whether the Schema lets this call run without
// a user-chosen model: the tool opted in, and the user is working in the other
// workspace domain (so they never had the chance to choose one for this one).
func (executor *Executor) fallbackModelAllowed(command chat.ToolCommand) bool {
	domain := toolDomain(command)
	return command.Tool.CrossDomainModelFallback && domain != "" &&
		command.Options.CreativeDomain != "" && domain != command.Options.CreativeDomain
}

// usedFallbackModel reports whether a step ran on the Schema's fallback model.
func (executor *Executor) usedFallbackModel(command chat.ToolCommand) bool {
	return command.Tool.CrossDomainModelFallback && executor.userModel(command) == "" && executor.fallbackModelAllowed(command)
}

// preferredOffering picks the Offering to use when the user chose none: an
// available default first, then a recommended one, then the first available,
// in catalog order.
func preferredOffering(offerings []generation.Offering, available func(generation.OfferingID) bool) (generation.Offering, bool) {
	var recommended, first *generation.Offering
	for index := range offerings {
		offering := &offerings[index]
		if !offering.Enabled || !available(offering.ID) {
			continue
		}
		if offering.Default {
			return *offering, true
		}
		if recommended == nil && offering.Recommended {
			recommended = offering
		}
		if first == nil {
			first = offering
		}
	}
	switch {
	case recommended != nil:
		return *recommended, true
	case first != nil:
		return *first, true
	}
	return generation.Offering{}, false
}

func (executor *Executor) selectReferences(
	command chat.ToolCommand,
	arguments map[string]json.RawMessage,
) ([]string, error) {
	references, err := executor.selectReferencesBy(command, arguments, executor.chat.Context.ToolReferenceArgument)
	if err != nil || !executor.canvasTurn(command) || command.Tool.CanvasSource != canvasSourceBind {
		return references, err
	}
	// #266: the artboard snapshot the person sent is the turn's source. It
	// leads the references whether or not the Agent named it; pictures the
	// Agent or the person added come after it.
	bound := selectedImageURLs(command)
	for _, reference := range references {
		if !slices.Contains(bound, reference) {
			bound = append(bound, reference)
		}
	}
	return bound, nil
}

const (
	canvasSourceBind   = "bind"
	canvasSourceRefuse = "refuse"
)

// canvasTurn reports whether the person chose the artboard snapshot as the
// turn's source; the service verified the snapshot when the turn started.
func (executor *Executor) canvasTurn(command chat.ToolCommand) bool {
	source := executor.chat.Context.CanvasInputSource
	return source != "" && command.Options.InputSource == source
}

func (executor *Executor) canvasRefusal(command chat.ToolCommand) string {
	var names []string
	for _, tool := range executor.chat.Tools {
		if tool.CanvasSource == canvasSourceBind {
			names = append(names, tool.Name)
		}
	}
	return strings.ReplaceAll(executor.localized(command, executor.chat.Presentation.Messages.CanvasSourceToolRefused),
		"{tools}", strings.Join(names, ", "))
}

// selectReferencesBy resolves 1-indexed historical chat images named by the
// given tool argument, the images of the current user turn, or (for a tool
// that defaults to it) the workspace selection. selectReferences puts a canvas
// snapshot the person chose ahead of all of these.
func (executor *Executor) selectReferencesBy(
	command chat.ToolCommand,
	arguments map[string]json.RawMessage,
	argument string,
) ([]string, error) {
	var indices []int
	var explicitSelection bool
	var handled []string
	if len(command.Options.ReferenceImageIndices) > 0 {
		explicitSelection = true
		indices = append(
			[]int(nil),
			command.Options.ReferenceImageIndices...,
		)
	} else {
		tokens, err := executor.handleTokens(command, arguments, executor.chat.Context.ReferenceHandlesArgument)
		if err != nil {
			return nil, err
		}
		if len(tokens) > 0 {
			if handled, err = executor.resolveImageHandles(command, tokens); err != nil {
				return nil, err
			}
			explicitSelection = true
		}
		if rawIndices, named := arguments[argument]; named {
			// Indices keep working beside handles.
			if err := json.Unmarshal(rawIndices, &indices); err != nil || (indices == nil && len(handled) == 0) {
				return nil, &referenceRefusal{message: executor.localized(command, executor.chat.Presentation.Messages.InvalidReferenceIndices)}
			}
			explicitSelection = true
		}
	}
	var all []string
	for _, message := range command.ReferenceHistory() {
		all = append(all, message.ImageURLs...)
	}
	if explicitSelection {
		selected := append([]string(nil), handled...)
		for _, index := range indices {
			if index <= 0 || index > len(all) {
				message := strings.NewReplacer(
					"{index}", fmt.Sprint(index),
					"{count}", fmt.Sprint(len(all)),
				).Replace(executor.localized(command, executor.chat.Presentation.Messages.ReferenceIndexOutOfRangeTemplate))
				return nil, &referenceRefusal{message: message}
			}
			selected = append(selected, all[index-1])
		}
		return selected, nil
	}
	// Only images in the current user turn are implicit references. Older images
	// must be selected by index, so a fresh text request stays text-to-image.
	if len(command.History) > 0 {
		latest := command.History[len(command.History)-1]
		if latest.Role == executor.chat.Roles.User && len(latest.ImageURLs) > 0 {
			return append([]string(nil), latest.ImageURLs...), nil
		}
	}
	if command.Tool.DefaultToSelection {
		// "This image": a tool that needs a picture uses the selected one.
		return selectedImageURLs(command), nil
	}
	return nil, nil
}

func (executor *Executor) synthesizePrompt(
	command chat.ToolCommand,
	prompt string,
	referenceURLs []string,
) string {
	if len(referenceURLs) == 0 {
		return prompt
	}
	previous := executor.previousPrompt(command, referenceURLs[0])
	if previous != "" {
		return strings.NewReplacer(
			"{previous_prompt}", previous,
			"{prompt}", prompt,
		).Replace(command.Tool.PromptWithPreviousTemplate)
	}
	return strings.ReplaceAll(
		command.Tool.PromptWithReferenceTemplate,
		"{prompt}",
		prompt,
	)
}

// previousPrompt is the prompt of the call of this tool whose result is the
// picture being edited (the first reference). Only that call's own result
// carries its prompt forward (#272): an unrelated input, such as the artboard
// snapshot of a canvas turn, an upload or another tool's picture, is edited
// with the reference template and never inherits the last call's instructions.
func (executor *Executor) previousPrompt(command chat.ToolCommand, edited string) string {
	source := executor.referenceSourceTask(command, edited)
	if source == "" {
		return ""
	}
	history := command.ReferenceHistory()
	for messageIndex := len(history) - 1; messageIndex >= 0; messageIndex-- {
		message := history[messageIndex]
		for callIndex := len(message.ToolCalls) - 1; callIndex >= 0; callIndex-- {
			call := message.ToolCalls[callIndex]
			if call.Name != command.Tool.Name || call.TaskID != source {
				continue
			}
			var arguments map[string]json.RawMessage
			if json.Unmarshal(call.Arguments, &arguments) == nil {
				if prompt := rawString(
					arguments[executor.chat.Context.ToolPromptArgument],
				); prompt != "" {
					return prompt
				}
			}
		}
	}
	return ""
}

// referenceSourceTask is the Task that generated a reference picture: read
// from a Task asset URL, else from the generated attachment that shows it in
// the history or the selection. Empty for anything else (uploads, the
// artboard snapshot, captures).
func (executor *Executor) referenceSourceTask(command chat.ToolCommand, reference string) string {
	if taskID, generated := executor.taskOfAssetURL(reference); generated {
		return taskID
	}
	for _, message := range command.ReferenceHistory() {
		for _, attachment := range message.Attachments {
			if attachment.Role == chat.AttachmentRoleGenerated && attachment.Kind == chat.AttachmentKindImage &&
				attachment.TaskID != "" && chat.AttachmentURL(message, attachment) == reference {
				return attachment.TaskID
			}
		}
	}
	for _, attachment := range command.Options.Selection {
		if attachment.Role == chat.AttachmentRoleGenerated && attachment.Kind == chat.AttachmentKindImage &&
			attachment.TaskID != "" && attachment.URL == reference {
			return attachment.TaskID
		}
	}
	return ""
}

func (executor *Executor) buildInput(
	ctx context.Context,
	command chat.ToolCommand,
	offering generation.Offering,
	arguments map[string]json.RawMessage,
	prompt string,
	referenceURLs []string,
	skillOverrides map[generation.ParameterSemantic]json.RawMessage,
	anchorHandles map[string]string,
) (json.RawMessage, []generation.AssetID, error) {
	allowed := make(
		map[string]generation.Parameter,
		len(offering.Parameters),
	)
	var promptParameter string
	var referenceParameter string
	semanticParameters := make(
		map[generation.ParameterSemantic]string,
		len(offering.Parameters),
	)
	for _, parameter := range offering.Parameters {
		allowed[parameter.Name] = parameter
		if parameter.Semantic != "" {
			semanticParameters[parameter.Semantic] = parameter.Name
		}
		switch parameter.Semantic {
		case generation.SemanticPrompt:
			promptParameter = parameter.Name
		case generation.SemanticReferenceImage:
			referenceParameter = parameter.Name
		}
	}
	input := map[string]any{}
	for parameter, encoded := range command.Tool.DefaultParameters {
		if _, exists := allowed[parameter]; !exists {
			continue
		}
		var value any
		if err := json.Unmarshal(encoded, &value); err != nil {
			return nil, nil, err
		}
		input[parameter] = value
	}
	// #103: the production purpose's public values sit above the Offering's
	// defaults and below the person's panel and the Agent's own values, which
	// are applied next.
	profileValues := map[string]json.RawMessage{}
	if _, values := executor.profileParameterValues(command, offering); len(values) > 0 {
		for name, encoded := range values {
			var value any
			if err := json.Unmarshal(encoded, &value); err != nil {
				return nil, nil, err
			}
			input[name] = value
			profileValues[name] = encoded
		}
	}
	parameters := make(map[string]json.RawMessage, len(command.Options.Parameters))
	for name, value := range command.Options.Parameters {
		if parameter, declared := allowed[name]; declared && turnParameterAllowed(offering, parameter) {
			parameters[name] = value
		}
	}
	if executor.offeringParameters != nil {
		for name, value := range executor.offeringParameters(command.OwnerID, command.SessionID, string(offering.ID)) {
			if parameter, declared := allowed[name]; declared && offering.AgentParameterAllowed(parameter) {
				parameters[name] = value
			}
		}
	}
	explicit := command.ExplicitParameters
	if explicit == nil {
		explicit = command.Options.Parameters
	}
	for name, value := range explicit {
		if parameter, declared := allowed[name]; declared && turnParameterAllowed(offering, parameter) {
			parameters[name] = value
		}
	}
	// chosen are the parameters some layer set (the turn, the session, the
	// Agent, a Skill); a tool default or the Offering's default is not a
	// choice. A size restored to its default ("null") is not one either.
	chosen := map[string]bool{}
	for name, encoded := range parameters {
		if string(encoded) == "null" {
			delete(input, name)
			continue
		}
		parameter, declared := allowed[name]
		if !declared ||
			parameter.Semantic == generation.SemanticPrompt ||
			parameter.Semantic == generation.SemanticReferenceImage {
			continue
		}
		value, err := executor.parameterValue(
			ctx,
			generation.OwnerID(command.OwnerID),
			parameter,
			encoded,
		)
		if err != nil {
			return nil, nil, err
		}
		input[name] = value
		chosen[name] = true
	}
	// A purpose value survived the later layers (a panel "null" deleted it):
	// it counts as chosen, so the reference size rule leaves it alone.
	for name := range profileValues {
		if _, present := input[name]; present {
			chosen[name] = true
		}
	}
	var referenceAssets []generation.AssetID
	if promptParameter != "" {
		input[promptParameter] = prompt
	}
	if len(referenceURLs) > 0 && referenceParameter != "" {
		// A scalar reference consumes exactly the selected image; never choose
		// the first of several images or serialize an array into an asset field.
		if allowed[referenceParameter].Type == "asset" && len(referenceURLs) != 1 {
			return nil, nil, &generation.Error{Code: generation.ErrInvalidInput, Operation: "tool-reference"}
		}
		assets, err := executor.references.Resolve(
			ctx,
			generation.OwnerID(command.OwnerID),
			referenceURLs,
		)
		if err != nil {
			// A named anchor whose picture was deleted or expired is reported
			// with that anchor's handle (#250) instead of a generic generation
			// failure. Other reference failures keep their historical refusal.
			if refusal := executor.anchorReferenceRefusal(ctx, command, referenceURLs, anchorHandles); refusal != nil {
				return nil, nil, refusal
			}
			return nil, nil, err
		}
		referenceAssets = assets
		if allowed[referenceParameter].Type == "asset" {
			if len(assets) != 1 {
				return nil, nil, &generation.Error{Code: generation.ErrInvalidInput, Operation: "tool-reference"}
			}
			input[referenceParameter] = assets[0]
		} else {
			input[referenceParameter] = assets
		}
	}
	overrideValues := map[string]string{
		"aspect_ratio": command.Options.AspectRatio,
		"pose_mode":    command.Options.PoseMode,
		"size":         command.Options.Size,
		"image_size":   command.Options.ImageSize,
	}
	for semantic, parameter := range command.Tool.OverrideMappings {
		if _, exists := allowed[parameter]; !exists {
			continue
		}
		if value := overrideValues[semantic]; value != "" {
			input[parameter] = value
			chosen[parameter] = true
		}
	}
	for argument, semantic := range command.Tool.ArgumentMappings {
		if semantic == string(generation.SemanticPrompt) ||
			semantic == "reference-indices" {
			continue
		}
		parameter, exists := command.Tool.OverrideMappings[semantic]
		if !exists {
			continue
		}
		if raw, exists := arguments[argument]; exists {
			declared, ok := allowed[parameter]
			if !ok {
				// offering 未声明该参数（如当前模型不支持蒙版）时
				// 丢弃该参数，不能让整次生成失败。
				continue
			}
			var value any
			if err := json.Unmarshal(raw, &value); err == nil {
				if semantic == "source-task-from-image-handle" {
					if declared.Type != "task" || declared.Semantic != generation.SemanticSourceTask {
						return nil, nil, &generation.Error{Code: generation.ErrInvalidInput, Operation: "tool-source-task"}
					}
					token, isString := value.(string)
					if !isString || strings.TrimSpace(token) == "" {
						return nil, nil, &generation.Error{Code: generation.ErrInvalidInput, Operation: "tool-source-task"}
					}
					if _, _, handle := executor.handlePolicy().Parse(token); handle || executor.handlePolicy().IsSelectionAlias(token) {
						resolved, err := executor.handlePolicy().ResolveHandles(command.ReferenceHistory(), command.Options.Selection, []string{token}, chat.AttachmentKindImage)
						if err == nil && len(resolved) == 1 && resolved[0].Attachment.TaskID == "" {
							// The handle names a picture no Task made (an upload, a
							// capture): it cannot stand for a source Task (#25).
							err = &chat.UnknownHandleError{Handle: token, Reason: "kind"}
						}
						if err != nil || len(resolved) != 1 {
							return nil, nil, &generation.Error{Code: generation.ErrInvalidInput, Operation: "tool-source-task", Cause: executor.handleFailure(command, err)}
						}
						value = resolved[0].Attachment.TaskID
					}
				}
				if declared.Type == "asset" || declared.Type == "asset-array" {
					var reference string
					if err := json.Unmarshal(raw, &reference); err != nil || reference == "" {
						return nil, nil, &generation.Error{
							Code:      generation.ErrInvalidInput,
							Operation: "tool-argument",
							Cause:     fmt.Errorf("tool argument %q 必须是资产引用", argument),
						}
					}
					assets, err := executor.references.Resolve(
						ctx,
						generation.OwnerID(command.OwnerID),
						[]string{reference},
					)
					if err != nil {
						return nil, nil, err
					}
					if declared.Type == "asset" {
						value = assets[0]
					} else {
						value = assets
					}
				}
				input[parameter] = value
				chosen[parameter] = true
			}
		}
	}
	for semantic, encoded := range skillOverrides {
		// 装配期 catalog 已拒绝越界语义（ValidateSkillCatalogAgainst
		// ProductSchema）；执行期同源复核一次，避免绕过装配的调用方用
		// skill 覆盖系统注入或用户选定的值（来源/动作族）。
		if !semantic.SkillSettable() {
			return nil, nil, fmt.Errorf(
				"generation tool: skill override semantic %q 不可由 skill 覆盖",
				semantic,
			)
		}
		parameter, exists := semanticParameters[semantic]
		if !exists {
			continue
		}
		var value any
		if err := json.Unmarshal(encoded, &value); err != nil {
			return nil, nil, err
		}
		input[parameter] = value
		chosen[parameter] = true
	}
	// #139: the purpose's values were chosen before the later layers set the
	// step's combination (Tripo's smart low poly or quad). A purpose value no
	// later layer replaced is chosen again against the final input; one the
	// purpose no longer needs is left to normalization's default.
	if len(profileValues) > 0 {
		if err := executor.refreshPurposeValues(command, offering, input, profileValues, chosen); err != nil {
			return nil, nil, err
		}
	}
	// #296: outside a canvas turn, one reference picture sets the output size
	// that nobody chose.
	executor.applyReferenceOutputSize(ctx, command, offering, input, chosen, referenceAssets)
	// #272: on a canvas turn the artboard is the output frame; it is applied
	// last, over the turn's, the Agent's and a Skill's size settings.
	executor.applyCanvasOutputSize(command, offering, input)
	// #244: the frozen selection mask is the person's input, written into the
	// Offering's mask parameter after every other layer. An Agent-supplied
	// mask that reached the input above wins.
	bindSelectionMask(command, offering, input, chosen)
	encoded, err := json.Marshal(input)
	return encoded, referenceAssets, err
}

// parameterValue 解析对话面板提交的已声明参数。资产参数只在本模块
// 内解析 data URI（上传后返回归属当前用户的资产 ID）；其余字符串按资产
// ID 原样透传，由 generation.Submit 的归属、类型和大小校验兜底。
func (executor *Executor) parameterValue(
	ctx context.Context,
	owner generation.OwnerID,
	parameter generation.Parameter,
	encoded json.RawMessage,
) (any, error) {
	switch parameter.Type {
	case "asset":
		var value string
		if err := json.Unmarshal(encoded, &value); err != nil {
			return nil, fmt.Errorf(
				"generation tool: parameter %q must be a string",
				parameter.Name,
			)
		}
		if !strings.HasPrefix(value, "data:") {
			return value, nil
		}
		assets, err := executor.references.Resolve(ctx, owner, []string{value})
		if err != nil || len(assets) == 0 {
			return nil, fmt.Errorf(
				"generation tool: parameter %q asset rejected",
				parameter.Name,
			)
		}
		return string(assets[0]), nil
	case "asset-array":
		var values []string
		if err := json.Unmarshal(encoded, &values); err != nil {
			return nil, fmt.Errorf(
				"generation tool: parameter %q must be an array",
				parameter.Name,
			)
		}
		result := make([]string, 0, len(values))
		for _, value := range values {
			if !strings.HasPrefix(value, "data:") {
				result = append(result, value)
				continue
			}
			assets, err := executor.references.Resolve(
				ctx,
				owner,
				[]string{value},
			)
			if err != nil || len(assets) == 0 {
				return nil, fmt.Errorf(
					"generation tool: parameter %q asset rejected",
					parameter.Name,
				)
			}
			result = append(result, string(assets[0]))
		}
		return result, nil
	default:
		var value any
		if err := json.Unmarshal(encoded, &value); err != nil {
			return nil, fmt.Errorf(
				"generation tool: parameter %q has invalid JSON",
				parameter.Name,
			)
		}
		return value, nil
	}
}

func (executor *Executor) taskWaitDeadline(command chat.ToolCommand, offering generation.Offering) time.Time {
	if !command.TaskWaitDeadline.IsZero() {
		return command.TaskWaitDeadline
	}
	timeout := offering.Timeout
	if timeout <= 0 {
		// A historical Task may be unreadable before its Offering can be read.
		// Give that read a bounded recovery window using the Schema timeout.
		timeout = time.Duration(executor.chat.Agent.RequestTimeoutSeconds) * time.Second
	} else {
		// Upstream queue time does not count toward the job timeout (#136),
		// so the Task may run that much longer.
		timeout += offering.UpstreamQueueTimeout
	}
	return executor.clock().Add(timeout)
}

func (executor *Executor) awaitTask(command chat.ToolCommand, taskID generation.TaskID,
	deadline time.Time, err error) (chat.ToolResult, error) {
	if !executor.clock().Before(deadline) {
		return executor.generationFailure(command, string(taskID)), err
	}
	return chat.ToolResult{TaskID: string(taskID), AwaitTask: true, WaitUntil: deadline}, err
}

func (executor *Executor) wait(
	ctx context.Context,
	command chat.ToolCommand,
	offering generation.Offering,
	taskID generation.TaskID,
	observe func(chat.ToolObservation) error,
	info stepInfo,
) (chat.ToolResult, error) {
	deadline := executor.taskWaitDeadline(command, offering)
	if info.Model == "" {
		info.Model = offering.ModelName
		if info.Model == "" {
			info.Model = offering.ModelID
		}
	}
	info.Fallback = executor.usedFallbackModel(command)
	info.ModelNote = executor.retiredModelNote(command, offering)
	if switchNote := executor.maskModelSwitchNote(command, offering); switchNote != "" {
		info.ModelNote = strings.TrimSpace(strings.Join([]string{info.ModelNote, switchNote}, "\n"))
	}
	info.MaskNote = executor.maskNotAppliedNote(command, offering)
	{
		task, err := executor.gateway.Get(
			ctx,
			generation.OwnerID(command.OwnerID),
			taskID,
		)
		if err != nil {
			return executor.awaitTask(command, taskID, deadline, err)
		}
		info.ModelChoice = executor.modelChoiceOf(command, task.Offering, task.Input)
		info.PurposeBudgetNote = executor.purposeBudgetNote(command, task.Offering, task.Input)
		status := executor.publicStates[task.State]
		observation := chat.ToolObservation{
			TaskID:   string(taskID),
			Status:   status,
			Progress: task.Progress,
		}
		if task.State == generation.TaskSucceeded {
			observation.ImageURLs = executor.assetURLs(
				command.Tool,
				task,
			)
			if command.Tool.ResultFields.ModelFormats != "" {
				info.Formats = executor.publicModelFormats(task)
			}
			info.SizeNote = executor.canvasSizeNote(command, task.Offering, task.Input)
			if info.SizeNote == "" {
				info.SizeNote = executor.referenceSizeNote(ctx, command, task.Offering, task.Input)
			}
		}
		if task.Failure != nil {
			observation.Error = executor.failureMessage(command, task.Offering.CreditsMetered, task.Failure)
		}
		if observe != nil {
			if err := observe(observation); err != nil {
				return executor.awaitTask(command, taskID, deadline, err)
			}
		}
		switch task.State {
		case generation.TaskSucceeded:
			return executor.success(
				command.Tool,
				string(taskID),
				observation.ImageURLs,
				info,
			), nil
		case generation.TaskFailed, generation.TaskCanceled:
			detail := observation.Error
			if detail == "" {
				detail = executor.localized(command, executor.chat.Presentation.Messages.GenerationFailed)
			}
			// An ordinary failed Task carries the same actionable category and
			// suggested action as the plan and task views (#251), so the Agent's
			// primary result never leaves it with a generic "failed" text.
			return executor.taskFailure(
				command,
				task,
				detail,
			), errors.New(detail)
		}
		if !executor.clock().Before(deadline) {
			detail := executor.localized(command, executor.chat.Presentation.Messages.GenerationFailed)
			return executor.generationFailure(
				command,
				string(taskID),
			), errors.New(detail)
		}
		return chat.ToolResult{TaskID: string(taskID), AwaitTask: true, WaitUntil: deadline}, nil
	}
}

func (executor *Executor) assetURLs(
	tool catalog.ChatToolSchema,
	task generation.Task,
) []string {
	return ResultAssetURLs(executor.chat, tool, task)
}

// gateInput is one reference picture with the attachment that identifies its
// stored bytes. An attachment with no IDs is still carried for a data URI.
type gateInput struct {
	attachment chat.Attachment
	url        string
}

// review3DInput runs the deterministic 3D-input checklist over the final
// reference pictures (#245). It returns the override reason (when the call
// supplied one) and a refusal message. A picture whose bytes cannot be read is
// treated as unknown and does not block the step.
func (executor *Executor) review3DInput(ctx context.Context, command chat.ToolCommand, urls []string) (string, string) {
	if executor.imageReview == nil {
		return "", ""
	}
	override := inputOverrideReason(command.Call.Arguments, executor.chat.ImageReview.OverrideArgument)
	var reasons []string
	for _, item := range gateInputs(command, urls) {
		_, checks, err := executor.imageReview.ReviewAttachment(ctx, command.OwnerID, item.attachment, item.url, "3d_input")
		if err != nil {
			continue
		}
		for _, check := range checks {
			if check.Status != imageinspect.StatusFail {
				continue
			}
			handle := item.attachment.Handle
			if handle == "" {
				handle = "input picture"
			}
			reasons = append(reasons, fmt.Sprintf("%s %s: %s", handle, check.Name, check.Actual))
		}
	}
	if len(reasons) == 0 || strings.TrimSpace(override) != "" {
		return override, ""
	}
	refusal := strings.NewReplacer(
		"{reasons}", strings.Join(reasons, "; "),
		"{override_argument}", executor.chat.ImageReview.OverrideArgument,
	).Replace(executor.chat.ImageReview.RefusedTemplate)
	return override, refusal
}

func inputOverrideReason(arguments json.RawMessage, name string) string {
	if name == "" {
		return ""
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(arguments, &values) != nil {
		return ""
	}
	var reason string
	if json.Unmarshal(values[name], &reason) != nil {
		return ""
	}
	return strings.TrimSpace(reason)
}

// gateInputs pairs each final reference URL with the attachment that names its
// stored asset. Pictures outside this session's verified attachments are still
// listed; the reviewer then reports them as unreadable and the gate skips them.
func gateInputs(command chat.ToolCommand, urls []string) []gateInput {
	byURL := map[string]chat.Attachment{}
	add := func(message chat.Message) {
		for _, attachment := range message.Attachments {
			if attachment.Kind != chat.AttachmentKindImage {
				continue
			}
			if attachment.ImageSlot > 0 && attachment.ImageSlot <= len(message.ImageURLs) {
				byURL[message.ImageURLs[attachment.ImageSlot-1]] = attachment
			}
			if attachment.URL != "" {
				byURL[attachment.URL] = attachment
			}
		}
	}
	for _, message := range command.ReferenceHistory() {
		add(message)
	}
	for _, attachment := range command.Options.Selection {
		if attachment.Kind != chat.AttachmentKindImage {
			continue
		}
		if attachment.URL != "" {
			byURL[attachment.URL] = attachment
		}
	}
	inputs := make([]gateInput, 0, len(urls))
	for _, url := range urls {
		inputs = append(inputs, gateInput{attachment: byURL[url], url: url})
	}
	return inputs
}

func (executor *Executor) success(
	tool catalog.ChatToolSchema,
	taskID string,
	imageURLs []string,
	info stepInfo,
) chat.ToolResult {
	result := map[string]any{
		tool.ResultFields.Success:   true,
		tool.ResultFields.TaskID:    taskID,
		tool.ResultFields.ImageURLs: imageURLs,
	}
	if info.NativeQuote != nil {
		result["native_quote"] = info.NativeQuote
	}
	if key := tool.ResultFields.EstimatedCredits; info.NativeQuote == nil && key != "" &&
		(info.Credits > 0 || (info.CreditsMetered && info.Credits == 0)) {
		result[key] = info.Credits
	}
	if key := tool.ResultFields.ExtraCredits; info.NativeQuote == nil && key != "" && info.ExtraCredits > 0 {
		result[key] = info.ExtraCredits
	}
	if key := tool.ResultFields.Approval; key != "" && info.Approval != "" {
		result[key] = info.Approval
	}
	if key := tool.ResultFields.ModelFormats; key != "" && len(info.Formats) > 0 {
		result[key] = info.Formats
	}
	if key := tool.ResultFields.Model; key != "" && info.Model != "" {
		result[key] = info.Model
	}
	if key := tool.ResultFields.ReferenceNote; key != "" && info.ReferenceNote != "" {
		result[key] = info.ReferenceNote
	}
	if key := tool.ResultFields.SizeNote; key != "" && info.SizeNote != "" {
		result[key] = info.SizeNote
	}
	if key := tool.ResultFields.ModelFallback; key != "" && info.Fallback {
		result[key] = true
	}
	if key := tool.ResultFields.ModelChoice; key != "" && info.ModelChoice != nil {
		result[key] = info.ModelChoice
	}
	if key := tool.ResultFields.ModelNote; key != "" && info.ModelNote != "" {
		result[key] = info.ModelNote
	}
	if key := tool.ResultFields.MaskNote; key != "" && info.MaskNote != "" {
		result[key] = info.MaskNote
	}
	if key := tool.ResultFields.PurposeBudgetNote; key != "" && info.PurposeBudgetNote != "" {
		result[key] = info.PurposeBudgetNote
	}
	if key := tool.ResultFields.FinalPrompt; key != "" && info.PromptFinal != "" {
		result[key] = info.PromptFinal
	}
	if key := tool.ResultFields.PromptNote; key != "" && len(info.PromptNote) > 0 {
		result[key] = info.PromptNote
	}
	if info.Budget != nil {
		if key := tool.ResultFields.Budget; key != "" {
			result[key] = budgetResult(*info.Budget)
		}
	}
	content, _ := json.Marshal(result)
	return chat.ToolResult{
		TaskID:       taskID,
		ImageURLs:    imageURLs,
		AgentContent: string(content),
		Pictures:     resultPictures(tool, imageURLs),
	}
}

// resultPictures returns the pictures of a 2D generation result the Agent
// should see in the same turn (#245). 3D results are reviewed through
// inspect_model and render_model_views, not by looking at thumbnails. When a
// call produced more candidates than the Schema allows, evenly spaced ones are
// shown; every produced picture still keeps its handle in the result.
func resultPictures(tool catalog.ChatToolSchema, urls []string) []chat.ToolPicture {
	if len(urls) == 0 || !imageResultCapability(string(tool.Capability)) {
		return nil
	}
	// Every produced picture travels to the orchestrator (#247): when there
	// are more than the role's places, it composes a labelled contact sheet
	// instead of silently dropping candidates. max_result_pictures stays the
	// hard place cap.
	pictures := make([]chat.ToolPicture, 0, len(urls))
	for _, url := range urls {
		if url != "" {
			pictures = append(pictures, chat.ToolPicture{
				URL: url, Fresh: true, Purpose: chat.ToolPictureResult,
			})
		}
	}
	return pictures
}

func imageResultCapability(capability string) bool {
	switch capability {
	case "image-to-image", "background-removal", "image-to-multiview", "edit-multiview":
		return true
	default:
		return false
	}
}

func (executor *Executor) executionFailure(
	command chat.ToolCommand,
	taskID string,
) chat.ToolResult {
	return executor.failure(
		command,
		taskID,
		executor.chat.Presentation.Messages.GenerationError,
	)
}

// failureMessage is the public text of a failed Task in the turn's locale:
// the Schema's text for its failure class when one is declared, else the
// generic failure text. The metered classification itself stays canonical.
func (executor *Executor) failureMessage(
	command chat.ToolCommand,
	metered bool,
	failure *generation.Failure,
) string {
	return LocalizedFailureMessage(executor.chat, metered, failure, executor.localizeText(command))
}

func (executor *Executor) generationFailure(
	command chat.ToolCommand,
	taskID string,
) chat.ToolResult {
	return executor.failure(
		command,
		taskID,
		executor.chat.Presentation.Messages.GenerationFailed,
	)
}

// failure writes the Schema's public text in the turn's frozen locale. It
// changes presentation only: Task status, failure class and metered checks
// keep the canonical Schema text.
func (executor *Executor) failure(
	command chat.ToolCommand,
	taskID string,
	publicMessage string,
) chat.ToolResult {
	return executor.failureWithReport(command, taskID, publicMessage, nil)
}

// taskFailure is a failed Task's result. Besides the public message it names
// the actionable category, next action and advice (#251); the internal class
// and the upstream text stay server-side.
func (executor *Executor) taskFailure(
	command chat.ToolCommand,
	task generation.Task,
	publicMessage string,
) chat.ToolResult {
	var report *generation.FailureReport
	if task.Failure != nil {
		classified := generation.ClassifyFailure(task.Failure.Class, task.Failure.Retryable, task.Failure.Ambiguous)
		report = &classified
	}
	return executor.failureWithReport(command, string(task.ID), publicMessage, report)
}

func (executor *Executor) failureWithReport(
	command chat.ToolCommand,
	taskID string,
	publicMessage string,
	report *generation.FailureReport,
) chat.ToolResult {
	tool := command.Tool
	publicMessage = executor.localized(command, publicMessage)
	payload := map[string]any{
		tool.ResultFields.Success: false,
		tool.ResultFields.TaskID:  taskID,
		tool.ResultFields.Error:   publicMessage,
	}
	if report != nil {
		payload["failure_category"] = string(report.Category)
		payload["next_action"] = string(report.Action)
		payload["failure_advice"] = executor.processingFailureAdvice(*report)
		payload["retryable"] = report.Retryable
		payload["user_decision_required"] = report.UserDecisionRequired
	}
	content, _ := json.Marshal(payload)
	return chat.ToolResult{
		TaskID:       taskID,
		Error:        publicMessage,
		AgentContent: string(content),
	}
}

// localized returns text in the turn's frozen UI locale. Only Schema copy
// changes; an unknown text or a nil localizer keeps it as is.
func (executor *Executor) localized(command chat.ToolCommand, text string) string {
	if executor.texts == nil || text == "" {
		return text
	}
	return executor.texts.Text(command.Options.Locale, text)
}

// localizeText is the mapper handed to helpers that fill a Schema template:
// the template and each Schema value are translated before interpolation.
// Nil keeps the default-locale copy.
func (executor *Executor) localizeText(command chat.ToolCommand) func(string) string {
	if executor.texts == nil {
		return nil
	}
	return func(text string) string { return executor.texts.Text(command.Options.Locale, text) }
}

func truncateReferences(
	urls []string,
	offering generation.Offering,
) []string {
	for _, parameter := range offering.Parameters {
		if parameter.Semantic == generation.SemanticReferenceImage && parameter.Type == "asset-array" &&
			parameter.MaxItems > 0 &&
			len(urls) > parameter.MaxItems {
			return append([]string(nil), urls[:parameter.MaxItems]...)
		}
	}
	return urls
}

func rawString(encoded json.RawMessage) string {
	var value string
	_ = json.Unmarshal(encoded, &value)
	return value
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
