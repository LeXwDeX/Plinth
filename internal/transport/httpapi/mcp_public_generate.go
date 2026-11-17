package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/mcppublic"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// The tools of the external MCP service that start a Task (#332):
// generate_image, remove_background, create_model and process_model.
//
// Each call works on one asset (a chat session the service keeps for a name)
// and takes the same road every other entry takes to a paid step, so nothing
// about admission is decided here:
//
//  1. the input is frozen by the same preparation the browser's prepare route
//     and the Agent use (style lock, production purpose, the kernel's
//     normalization and validation of every parameter);
//  2. a grant of source access_key is issued for the frozen input and decided in
//     place by the access-key policy: 2D is approved, a 3D step follows the
//     owner's MCP step mode, and a step only a person could approve (the repeat
//     of a charged attempt whose result was not stored, #294) is denied with its
//     own reason;
//  3. the Task is created by the generation kernel (SubmitPrepared), which
//     checks the grant against the frozen input and binds it to the Task.
//
// The call returns at once with the Task's ID; polling is get_task's.
//
// An idempotency key names one request. The Task's key and its grant's key are
// the Schema's prefix plus the caller's key; the grant's operation ID is a hash
// of the key and the call's content, so a retry of the same call finds its
// grant by key and continues it (a Task that exists is returned as it is, never
// started, counted or charged twice), while the same key with other content is
// a conflict.

// mcpGrantKeys is the read of a grant by its idempotency key.
type mcpGrantKeys interface {
	GrantByKey(ctx context.Context, owner, key string) (executiongrant.Grant, bool, error)
}

// mcpStartRequest is a start call as the tool understands it.
type mcpStartRequest struct {
	capability generation.Capability
	prompt     *string
	images     []string
	service    string
	parameters map[string]any
	reasons    map[string]any
	// key is the caller's idempotency_key, asset and project name where the
	// call works.
	key     string
	asset   string
	project string
	// sourceTask and sourceAsset name the model a processing step takes.
	sourceTask  string
	sourceAsset string
}

// mcpProblemOutcome is a business failure of a call, with what the status block
// should be about.
func mcpProblemOutcome(code, message string, subject mcppublic.Subject) mcpOutcome {
	return mcpOutcome{problem: &mcpProblem{Code: code, Message: message}, subject: subject}
}

// mcpFill words a Schema message in the request's language with its holders.
func (s *Server) mcpFill(locale, template string, pairs ...string) string {
	return strings.NewReplacer(pairs...).Replace(s.mcpText(locale, template))
}

func (s *Server) mcpMessageProblem(call *mcpCall, code, template string, subject mcppublic.Subject, pairs ...string) *mcpOutcome {
	outcome := mcpProblemOutcome(code, s.mcpFill(call.locale, template, pairs...), subject)
	return &outcome
}

func (s *Server) mcpStartArguments(call *mcpCall) (mcpStartRequest, *mcpOutcome) {
	request := mcpStartRequest{
		key: mcpStringArgument(call, "idempotency_key"), asset: mcpStringArgument(call, "asset"),
		project: mcpStringArgument(call, "project"), service: mcpStringArgument(call, "service"),
		sourceTask: mcpStringArgument(call, "source_task_id"), sourceAsset: mcpStringArgument(call, "source_asset_id"),
	}
	if _, present := call.arguments["prompt"]; present {
		prompt := mcpStringArgument(call, "prompt")
		request.prompt = &prompt
	}
	if list, ok := call.arguments["images"].([]any); ok {
		for _, item := range list {
			request.images = append(request.images, fmt.Sprint(item))
		}
	}
	if image := mcpStringArgument(call, "image"); image != "" {
		request.images = []string{image}
	}
	request.parameters, _ = call.arguments["parameters"].(map[string]any)
	request.reasons, _ = call.arguments["parameter_reasons"].(map[string]any)
	invalid := func(detail string) (mcpStartRequest, *mcpOutcome) {
		outcome := s.mcpInvalidArguments(call.locale, detail)
		return mcpStartRequest{}, &outcome
	}
	switch call.tool.declared.Name {
	case catalog.MCPToolGenerateImage:
		request.capability = generation.TextToImage
		if len(request.images) > 0 {
			request.capability = generation.ImageToImage
		}
	case catalog.MCPToolRemoveBackground:
		request.capability = schemacontract.BackgroundRemove
	case catalog.MCPToolCreateModel:
		switch {
		case len(request.images) > 0:
			request.capability = generation.ImageTo3D
		case request.prompt != nil:
			request.capability = generation.TextTo3D
		default:
			return invalid("$: give a prompt (text-to-3D) or images (image-to-3D)")
		}
	case catalog.MCPToolProcessModel:
		request.capability = generation.Capability(mcpStringArgument(call, "operation"))
	default:
		return mcpStartRequest{}, nil
	}
	if !slices.Contains(call.tool.declared.Capabilities, request.capability) {
		return invalid("$: this tool does not offer " + string(request.capability))
	}
	if name, value := mcpReasonProblem(request.reasons); name != "" {
		return invalid("$.parameter_reasons." + name + ": " + value)
	}
	return request, nil
}

// mcpReasonProblem finds a parameter_reasons entry that is not a non-empty text.
func mcpReasonProblem(reasons map[string]any) (name, problem string) {
	for key, value := range reasons {
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" || len(text) > 4096 {
			return key, "must be a text of 1 to 4096 bytes"
		}
	}
	return "", ""
}

// mcpOperationID is the operation identity of a call: the key and what the call
// asks for, the asset by its normalized name. The same key with the same content
// is one operation; any change of content is another, which the grant found by
// the key then reports as a conflict.
func mcpOperationID(tool string, arguments map[string]any, assetKey, key string) string {
	content := make(map[string]any, len(arguments))
	for name, value := range arguments {
		if name != "idempotency_key" {
			content[name] = value
		}
	}
	if _, named := content["asset"]; named {
		content["asset"] = assetKey
	}
	encoded, _ := json.Marshal(map[string]any{"tool": tool, "key": key, "arguments": content})
	sum := sha256.Sum256(encoded)
	return "mcp:" + hex.EncodeToString(sum[:16])
}

// mcpStartTask is the common road of the four tools that start a Task.
func (s *Server) mcpStartTask(call *mcpCall) (mcpOutcome, error) {
	service := s.application.MCPPublic
	request, stopped := s.mcpStartArguments(call)
	if stopped != nil {
		return *stopped, nil
	}
	assetKey := ""
	if request.asset != "" {
		key, _, err := mcppublic.NormalizeAssetName(request.asset, service.Assets.NameMaxLength)
		if err != nil {
			return *s.mcpMessageProblem(call, "asset_name_invalid", service.Messages.AssetNameInvalid, mcppublic.Subject{},
				"{max}", strconv.Itoa(service.Assets.NameMaxLength)), nil
		}
		assetKey = key
	}
	key := service.Generation.IdempotencyKeyPrefix + request.key
	operation := mcpOperationID(call.tool.declared.Name, call.arguments, assetKey, key)

	// A retry of a call that already has its grant continues that grant.
	earlier, resumed, stopped, err := s.mcpEarlierGrant(call, key, operation)
	if err != nil || stopped != nil {
		if stopped != nil {
			return *stopped, nil
		}
		return mcpOutcome{}, err
	}
	var (
		asset    mcppublic.NamedAsset
		prepared generation.PreparedInput
		offering generation.Offering
		defaults = request.service == ""
	)
	if resumed {
		if earlier.Status == executiongrant.Bound && earlier.TaskID != "" {
			return s.mcpReplayedTask(call, earlier)
		}
		var found bool
		if asset, found, err = s.mcpAssets.Describe(call.ctx, string(call.owner), earlier.SessionID); err != nil || !found {
			if err != nil {
				return mcpOutcome{}, err
			}
			return *s.mcpMessageProblem(call, "idempotency_conflict", service.Messages.IdempotencyConflict, mcppublic.Subject{}), nil
		}
		subject := mcppublic.Subject{AssetID: asset.SessionID, AssetTitle: asset.Title}
		if prepared, err = s.generation.GetPreparedInput(call.ctx, call.owner, generation.PreparedInputID(earlier.PreparedInputID)); err != nil {
			if generation.IsCode(err, generation.ErrNotFound) || generation.IsCode(err, generation.ErrConflict) {
				return *s.mcpMessageProblem(call, "idempotency_conflict", service.Messages.IdempotencyConflict, subject), nil
			}
			return mcpOutcome{}, err
		}
		var known bool
		if offering, known = s.generation.Offering(prepared.OfferingID); !known {
			return *s.mcpMessageProblem(call, "idempotency_conflict", service.Messages.IdempotencyConflict, subject), nil
		}
	} else {
		var outcome *mcpOutcome
		if asset, prepared, offering, outcome, err = s.mcpFreeze(call, request, defaults); err != nil || outcome != nil {
			if outcome != nil {
				return *outcome, nil
			}
			return mcpOutcome{}, err
		}
	}
	subject := mcppublic.Subject{AssetID: asset.SessionID, AssetTitle: asset.Title}
	counted := s.executionGrants.Policy.Steps != nil && s.executionGrants.Policy.Steps.Counts(string(prepared.Capability))

	grant, prepared, outcome, err := s.mcpGrant(call, prepared, key, operation, resumed, subject)
	if err != nil || outcome != nil {
		if outcome != nil {
			return *outcome, nil
		}
		return mcpOutcome{}, err
	}
	// The grant is approved: create the Task. The submission is not abandoned
	// with the request, so a caller that hangs up cannot leave it half done.
	accepted, err := s.generation.SubmitPrepared(context.WithoutCancel(call.ctx), generation.SubmitPreparedCommand{
		OwnerID: call.owner, SessionID: prepared.SessionID, PreparedInputID: prepared.ID,
		IdempotencyKey: key, OperationID: operation, AuthorizationRef: grant.RequestID,
	})
	if err != nil {
		return s.mcpSubmitFailure(call, err, grant, subject)
	}
	task, err := s.generation.GetSummaryScoped(call.ctx, generation.OwnTasks(call.owner), accepted.TaskID)
	if err != nil {
		return mcpOutcome{}, err
	}
	s.mcpAuditTask(call, task, accepted.Replayed)
	// What a step of each service of the asset costs, now with this one among them.
	subject.Costs = s.mcpStepCosts(call.ctx, call.owner, asset.SessionID)
	note := ""
	if prepared.ReferenceLimit != nil {
		limit := *prepared.ReferenceLimit
		limit.Model = s.mcpText(call.locale, limit.Model)
		note = limit.Note(s.mcpText(call.locale, limit.Template))
	}
	return s.mcpStartedOutcome(call, task, asset, offering, defaults, counted, accepted.Replayed, note, subject), nil
}

// mcpEarlierGrant finds the grant a retry of this call already has. A grant that
// carries the key but not this operation, or a Task made by another entry under
// the key, is a conflict.
func (s *Server) mcpEarlierGrant(call *mcpCall, key, operation string) (executiongrant.Grant, bool, *mcpOutcome, error) {
	service := s.application.MCPPublic
	conflict := s.mcpMessageProblem(call, "idempotency_conflict", service.Messages.IdempotencyConflict, mcppublic.Subject{})
	reader, ok := s.executionGrants.Grants.(mcpGrantKeys)
	if !ok {
		return executiongrant.Grant{}, false, nil, errors.New("mcp: the grant store cannot be read by key")
	}
	grant, found, err := reader.GrantByKey(call.ctx, string(call.owner), key)
	if err != nil {
		return executiongrant.Grant{}, false, nil, err
	}
	if found {
		if grant.Source != executiongrant.AccessKey || grant.OperationID != operation {
			return executiongrant.Grant{}, false, conflict, nil
		}
		return grant, true, nil, nil
	}
	if _, err := s.generation.LookupByIdempotency(call.ctx, call.owner, key); err == nil {
		return executiongrant.Grant{}, false, conflict, nil
	} else if !generation.IsCode(err, generation.ErrNotFound) {
		return executiongrant.Grant{}, false, nil, err
	}
	return executiongrant.Grant{}, false, nil, nil
}

// mcpReplayedTask answers a retry whose Task exists: the Task, as it was
// started, with nothing started, counted or charged again.
func (s *Server) mcpReplayedTask(call *mcpCall, grant executiongrant.Grant) (mcpOutcome, error) {
	asset, found, err := s.mcpAssets.Describe(call.ctx, string(call.owner), grant.SessionID)
	if err != nil {
		return mcpOutcome{}, err
	}
	if !found {
		return *s.mcpMessageProblem(call, "idempotency_conflict", s.application.MCPPublic.Messages.IdempotencyConflict, mcppublic.Subject{}), nil
	}
	task, err := s.generation.GetSummaryScoped(call.ctx, generation.OwnTasks(call.owner), generation.TaskID(grant.TaskID))
	if generation.IsCode(err, generation.ErrNotFound) {
		// The Task was deleted since: the key now names nothing a retry can return.
		return *s.mcpMessageProblem(call, "idempotency_conflict", s.application.MCPPublic.Messages.IdempotencyConflict,
			mcppublic.Subject{AssetID: asset.SessionID, AssetTitle: asset.Title}), nil
	}
	if err != nil {
		return mcpOutcome{}, err
	}
	offering, known := s.generation.Offering(task.Offering.ID)
	if !known {
		offering = task.Offering
	}
	subject := mcppublic.Subject{AssetID: asset.SessionID, AssetTitle: asset.Title, Costs: s.mcpStepCosts(call.ctx, call.owner, asset.SessionID)}
	counted := s.executionGrants.Policy.Steps != nil && s.executionGrants.Policy.Steps.Counts(string(task.Capability))
	s.mcpAuditTask(call, task, true)
	return s.mcpStartedOutcome(call, task, asset, offering, mcpStringArgument(call, "service") == "", counted, true, "", subject), nil
}

// mcpFreeze is the first call of a request: it names the service, builds and
// checks the input, finds or makes the asset and freezes the input there.
func (s *Server) mcpFreeze(call *mcpCall, request mcpStartRequest, defaults bool) (
	mcppublic.NamedAsset, generation.PreparedInput, generation.Offering, *mcpOutcome, error,
) {
	service := s.application.MCPPublic
	owner := string(call.owner)
	fail := func(outcome *mcpOutcome) (mcppublic.NamedAsset, generation.PreparedInput, generation.Offering, *mcpOutcome, error) {
		return mcppublic.NamedAsset{}, generation.PreparedInput{}, generation.Offering{}, outcome, nil
	}
	processing := call.tool.declared.Name == catalog.MCPToolProcessModel
	// A processing step takes a model an asset made, so the asset has to exist;
	// every other step makes the asset the first time its name is used.
	resolve := func() (mcppublic.NamedAsset, *mcpOutcome, error) {
		asset, err := s.mcpAssets.Resolve(call.ctx, mcppublic.AssetRequest{
			OwnerID: owner, OwnerName: call.ownerName, Locale: call.locale, Name: request.asset,
			ProjectID: request.project, FindOnly: processing,
		})
		switch {
		case err == nil:
			return asset, nil, nil
		case errors.Is(err, mcppublic.ErrInvalidAssetName):
			return asset, s.mcpMessageProblem(call, "asset_name_invalid", service.Messages.AssetNameInvalid, mcppublic.Subject{},
				"{max}", strconv.Itoa(service.Assets.NameMaxLength)), nil
		case errors.Is(err, mcppublic.ErrProjectMismatch):
			return asset, s.mcpMessageProblem(call, "asset_project_mismatch", service.Messages.AssetProjectMismatch, mcppublic.Subject{}), nil
		case errors.Is(err, chat.ErrProjectNotFound):
			return asset, s.mcpMessageProblem(call, "project_not_found", service.Messages.ProjectNotFound, mcppublic.Subject{}), nil
		case errors.Is(err, mcppublic.ErrAssetNotFound):
			return asset, s.mcpMessageProblem(call, "source_not_found", service.Messages.SourceNotFound, mcppublic.Subject{}), nil
		}
		return asset, nil, err
	}

	var (
		offering generation.Offering
		binding  *generation.ModelSourceBinding
		asset    mcppublic.NamedAsset
	)
	if processing {
		var outcome *mcpOutcome
		var err error
		if asset, outcome, err = resolve(); outcome != nil || err != nil {
			return mcppublic.NamedAsset{}, generation.PreparedInput{}, generation.Offering{}, outcome, err
		}
		subject := mcppublic.Subject{AssetID: asset.SessionID, AssetTitle: asset.Title}
		source, outcome, err := s.mcpProcessingSource(call, request, asset.SessionID, subject)
		if outcome != nil || err != nil {
			return mcppublic.NamedAsset{}, generation.PreparedInput{}, generation.Offering{}, outcome, err
		}
		if offering, binding, outcome = s.mcpProcessingOffering(call, request, source, subject); outcome != nil {
			return fail(outcome)
		}
	} else {
		var outcome *mcpOutcome
		if offering, outcome = s.mcpOffering(call, request); outcome != nil {
			return fail(outcome)
		}
	}
	input, outcome := s.mcpBuildInput(call, offering, request, binding)
	if outcome != nil {
		return fail(outcome)
	}
	if !processing {
		var outcome *mcpOutcome
		var err error
		if asset, outcome, err = resolve(); outcome != nil || err != nil {
			return mcppublic.NamedAsset{}, generation.PreparedInput{}, generation.Offering{}, outcome, err
		}
	}
	subject := mcppublic.Subject{AssetID: asset.SessionID, AssetTitle: asset.Title}
	reasons := map[string]string{}
	for name, value := range request.reasons {
		reasons[name], _ = value.(string)
	}
	prepared, failure := s.prepareDirectInput(call.ctx, call.owner, directPrepare{
		SessionID: asset.SessionID, Capability: request.capability, OfferingID: offering.ID, Input: input,
		ParameterReasons: reasons,
	})
	if failure != nil {
		outcome, err := s.mcpDirectFailure(call, failure, subject)
		return mcppublic.NamedAsset{}, generation.PreparedInput{}, generation.Offering{}, outcome, err
	}
	return asset, prepared, offering, nil, nil
}

// mcpDirectFailure words a refused preparation for the calling Agent, or hands
// back the failure the caller cannot act on.
func (s *Server) mcpDirectFailure(call *mcpCall, failure *directFailure, subject mcppublic.Subject) (*mcpOutcome, error) {
	switch failure.kind {
	case directProjectReferences:
		text := ""
		if s.application.Project != nil {
			text = s.application.Project.Messages.ReferenceInvalid
		}
		if text == "" {
			text = s.application.MCPPublic.Messages.InputRefused
		}
		outcome := mcpProblemOutcome("input_refused", s.mcpText(call.locale, text), subject)
		return &outcome, nil
	case directStyleReference:
		outcome := mcpProblemOutcome("input_refused",
			s.mcpText(call.locale, s.application.Chat.Presentation.Messages.StyleLockReferenceUnavailable), subject)
		return &outcome, nil
	case directRefused:
		return s.mcpDomainProblem(call, failure.err, subject)
	}
	if failure.err == nil {
		return nil, errors.New("mcp: the input could not be prepared")
	}
	return nil, failure.err
}

// mcpDomainProblem words a refusal of the generation kernel for the calling
// Agent: what the caller can fix is a business error with the Schema's text,
// anything else is a failure of the service. Only a reason the kernel marks as
// meant for the requester is quoted; every other detail stays in the log.
func (s *Server) mcpDomainProblem(call *mcpCall, err error, subject mcppublic.Subject) (*mcpOutcome, error) {
	service := s.application.MCPPublic
	var domain *generation.Error
	if !errors.As(err, &domain) {
		return nil, err
	}
	// A picture ID that is not one of the caller's (an upload of their own, or a
	// picture output of their own tasks) is the one input refusal worth naming:
	// the kernel's input resolution refuses it without a reason of its own.
	if domain.Code == generation.ErrInvalidInput && domain.Operation == "resolve_input_assets" &&
		generation.UserFacingError(err) == nil {
		return s.mcpMessageProblem(call, "image_not_found", service.Messages.ImageNotFound, subject), nil
	}
	switch domain.Code {
	case generation.ErrInvalidInput, generation.ErrInvalidCapability, generation.ErrUnknownOffering, generation.ErrNoOffering,
		generation.ErrInvalidModelInput, generation.ErrUnsupportedModelInput, generation.ErrModelInputLimit, generation.ErrNotFound:
		message := s.mcpText(call.locale, service.Messages.InputRefused)
		if reason := generation.UserFacingError(err).LocalizedDetail(func(text string) string { return s.mcpText(call.locale, text) }); reason != "" {
			message = reason
		}
		s.logger.Info("MCP 生成：输入被生成内核拒绝", "owner_id", string(call.owner), "tool", call.tool.declared.Name,
			"code", string(domain.Code), "operation", domain.Operation, "error", err)
		outcome := mcpProblemOutcome("input_refused", message, subject)
		return &outcome, nil
	case generation.ErrIdempotencyConflict, generation.ErrConflict:
		return s.mcpMessageProblem(call, "idempotency_conflict", service.Messages.IdempotencyConflict, subject), nil
	}
	return nil, err
}

// mcpSubmitFailure answers a refused or failed submission. A full in-flight
// admission wrote nothing: the caller retries with the same key, which finds
// the approved grant and submits it.
func (s *Server) mcpSubmitFailure(call *mcpCall, err error, grant executiongrant.Grant, subject mcppublic.Subject) (mcpOutcome, error) {
	service := s.application.MCPPublic
	wait := generation.AdmissionRetryAfter(err)
	if wait == 0 && generationtool.KnownNotSubmitted(err) {
		// Refused for good before any Task was written: the approved grant
		// would otherwise hold a 3D step of the asset forever, as the Agent
		// path's abandonBudgetReservation prevents. A retry with the same key
		// finds the denied grant and has it issued and decided again.
		if releaseErr := s.executionGrants.AbandonBudgetReservation(context.WithoutCancel(call.ctx), string(call.owner), grant.RequestID); releaseErr != nil {
			s.logger.Warn("MCP 生成：释放被拒提交的授权失败", "owner_id", string(call.owner), "request_id", grant.RequestID, "error", releaseErr)
		}
	}
	if wait > 0 {
		seconds := int(wait.Round(time.Second) / time.Second)
		outcome := mcpProblemOutcome("too_many_in_flight",
			s.mcpFill(call.locale, service.Messages.TooManyInFlight, "{seconds}", strconv.Itoa(seconds)), subject)
		outcome.problem.RetryAfterSeconds = &seconds
		return outcome, nil
	}
	if outcome, failure := s.mcpDomainProblem(call, err, subject); outcome != nil || failure != nil {
		if outcome != nil {
			return *outcome, nil
		}
		return mcpOutcome{}, failure
	}
	return mcpOutcome{}, err
}

// mcpGrant issues and decides the grant of the frozen input and reports why a
// step is not taken. It returns the approved grant and the frozen input the
// grant is for: a call of the same request that prepared the grant a moment
// earlier decides which input that is.
func (s *Server) mcpGrant(call *mcpCall, prepared generation.PreparedInput, key, operation string, resumed bool,
	subject mcppublic.Subject) (executiongrant.Grant, generation.PreparedInput, *mcpOutcome, error) {
	service := s.application.MCPPublic
	owner := string(call.owner)
	conflict := func() (executiongrant.Grant, generation.PreparedInput, *mcpOutcome, error) {
		return executiongrant.Grant{}, generation.PreparedInput{},
			s.mcpMessageProblem(call, "idempotency_conflict", service.Messages.IdempotencyConflict, subject), nil
	}
	grant, err := s.executionGrants.Prepare(call.ctx, executiongrant.PrepareCommand{
		Principal: executiongrant.Principal{OwnerID: owner, ActorID: "access_key:" + call.keyID, Source: executiongrant.AccessKey},
		SessionID: prepared.SessionID, OfferingID: string(prepared.OfferingID), OfferingRevision: prepared.OfferingRevision,
		PreparedInputID: string(prepared.ID), PreparedInputHash: prepared.InputHash,
		IdempotencyKey: key, OperationID: operation, AccessKeyID: call.keyID,
	})
	if errors.Is(err, executiongrant.ErrConflict) && !resumed {
		// A call with the same key and content won the preparation a moment
		// ago: its grant, and the input it froze, are this request's too.
		reader := s.executionGrants.Grants.(mcpGrantKeys)
		won, found, lookupErr := reader.GrantByKey(call.ctx, owner, key)
		if lookupErr != nil {
			return executiongrant.Grant{}, generation.PreparedInput{}, nil, lookupErr
		}
		if !found || won.Source != executiongrant.AccessKey || won.OperationID != operation {
			return conflict()
		}
		winner, err := s.generation.GetPreparedInput(call.ctx, call.owner, generation.PreparedInputID(won.PreparedInputID))
		if err != nil {
			return conflict()
		}
		grant, prepared = won, winner
	} else if errors.Is(err, executiongrant.ErrConflict) {
		return conflict()
	} else if err != nil {
		return executiongrant.Grant{}, generation.PreparedInput{}, nil, err
	}
	decided, outcome, err := s.mcpDecide(call, grant, prepared, subject)
	return decided, prepared, outcome, err
}

// mcpDecide decides a pending grant with the access-key policy. A grant that is
// already decided keeps its decision: an approved one goes on to its Task, and a
// denied one was re-issued by Prepare and is decided again, so a cap the person
// raised since lets the same retry through.
func (s *Server) mcpDecide(call *mcpCall, grant executiongrant.Grant, prepared generation.PreparedInput, subject mcppublic.Subject) (executiongrant.Grant, *mcpOutcome, error) {
	service := s.application.MCPPublic
	switch grant.Status {
	case executiongrant.Approved, executiongrant.Bound:
		// Bound: a call of the same request created the Task a moment ago; the
		// kernel replays it for this one.
		return grant, nil, nil
	case executiongrant.Claimed:
		return executiongrant.Grant{}, s.mcpMessageProblem(call, "in_progress", service.Messages.InProgress, subject), nil
	}
	// The repeat of a charged attempt whose result was not stored is the one
	// step the Agent path also never lets its policy approve.
	kinds := append([]generation.Capability(nil), call.tool.declared.Capabilities...)
	kinds = append(kinds, prepared.Capability)
	unstored, err := generationtool.UnstoredChargedTasks(call.ctx, s.generation, s.application.Chat, call.owner, prepared.SessionID, kinds)
	if err != nil {
		return executiongrant.Grant{}, nil, err
	}
	result, err := s.executionGrants.ApproveForAccessKey(call.ctx, string(call.owner), grant.RequestID,
		executiongrant.AccessKeyStep{NeedsPerson: len(unstored) > 0})
	switch {
	case err == nil:
		return result.Grant, nil, nil
	case errors.Is(err, executiongrant.ErrStepCapReached):
		used, limit := 0, 0
		if result.Steps != nil {
			used, limit = result.Steps.Used, result.Steps.Cap
		}
		text := s.application.StepPolicy.Messages.CapReached
		message := strings.NewReplacer("{used}", strconv.Itoa(used), "{cap}", strconv.Itoa(limit)).Replace(s.mcpText(call.locale, text))
		return executiongrant.Grant{}, ptrOutcome(mcpProblemOutcome("step_cap_reached", message, subject)), nil
	case errors.Is(err, executiongrant.ErrAccessKeyNeedsPerson):
		task := ""
		if len(unstored) > 0 {
			task = string(unstored[0])
		}
		return executiongrant.Grant{}, s.mcpMessageProblem(call, "needs_person", service.Messages.NeedsPerson, subject, "{task_id}", task), nil
	}
	return executiongrant.Grant{}, nil, err
}

func ptrOutcome(outcome mcpOutcome) *mcpOutcome { return &outcome }

// mcpStartedOutcome is the answer of a call that has its Task.
func (s *Server) mcpStartedOutcome(call *mcpCall, task generation.Task, asset mcppublic.NamedAsset, offering generation.Offering,
	defaulted, counted, replayed bool, note string, subject mcppublic.Subject) mcpOutcome {
	view, _ := s.mcpTaskView(call.locale, task)
	var name *string
	if asset.Name != "" {
		name = &asset.Name
	}
	var reference *string
	if note != "" {
		reference = &note
	}
	return mcpOutcome{
		data: map[string]any{
			"task_id": string(task.ID), "state": view.State, "state_label": view.StateLabel, "terminal": view.Terminal,
			"replayed":           replayed,
			"asset":              map[string]any{"id": asset.SessionID, "name": name, "title": asset.Title},
			"service":            map[string]any{"id": string(offering.ID), "provider": offering.ProviderID, "model": offering.ModelID, "capability": string(task.Capability), "defaulted": defaulted},
			"step_counted":       counted,
			"poll_after_seconds": view.PollAfterSeconds,
			"reference_note":     reference,
		},
		subject: subject,
	}
}

// mcpAuditTask writes the same audit event the browser writes when a Task is
// created, marked with the entry and the access key.
func (s *Server) mcpAuditTask(call *mcpCall, task generation.Task, replayed bool) {
	audit, marks := s.application.Runtime.TaskAudit, s.application.MCPPublic.Audit
	client, _ := call.ctx.Value(mcpClientKey{}).(mcpClient)
	s.writeAuditEvent(call.ctx, administration.AuditEvent{
		Action: audit.CreateAction, ActorID: string(call.owner), ActorName: call.ownerName,
		ResourceType: audit.ResourceType, ResourceID: string(task.ID),
		Detail: map[string]any{
			audit.DetailFields.Capability: task.Capability, audit.DetailFields.OfferingID: task.Offering.ID,
			audit.DetailFields.Replayed: replayed, marks.EntryField: marks.EntryValue, marks.AccessKeyField: call.keyID,
		},
		IPAddress: client.address, UserAgent: client.userAgent,
	})
}

// ---- the service, the input and the source ---------------------------------------

// mcpOffering names the service of a call that has no source: the one asked
// for, or the default of the capability (for several pictures of one model, a
// service that takes several views). An unavailable service is refused, never
// replaced.
func (s *Server) mcpOffering(call *mcpCall, request mcpStartRequest) (generation.Offering, *mcpOutcome) {
	service := s.application.MCPPublic
	if request.service != "" {
		return s.mcpNamedOffering(call, request)
	}
	if request.capability == generation.ImageTo3D && len(request.images) > 1 {
		var fallback *generation.Offering
		for _, candidate := range s.generation.Offerings(request.capability) {
			if !candidate.Enabled || !s.generation.OfferingAvailable(candidate.ID) || !generationtool.TakesSeveralViews(candidate) {
				continue
			}
			if candidate.Default {
				return candidate, nil
			}
			if fallback == nil {
				found := candidate
				fallback = &found
			}
		}
		if fallback != nil {
			return *fallback, nil
		}
		return generation.Offering{}, s.mcpMessageProblem(call, "no_service", service.Messages.NoService, mcppublic.Subject{})
	}
	id, err := s.generation.DefaultOfferingID(request.capability)
	if err != nil {
		return generation.Offering{}, s.mcpMessageProblem(call, "no_service", service.Messages.NoService, mcppublic.Subject{})
	}
	offering, _ := s.generation.Offering(id)
	return offering, nil
}

// mcpNamedOffering checks a service the caller named.
func (s *Server) mcpNamedOffering(call *mcpCall, request mcpStartRequest) (generation.Offering, *mcpOutcome) {
	service := s.application.MCPPublic
	offering, found := s.generation.Offering(generation.OfferingID(request.service))
	if !found || !offering.Enabled || offering.Capability != request.capability {
		return generation.Offering{}, s.mcpMessageProblem(call, "service_mismatch", service.Messages.ServiceMismatch, mcppublic.Subject{},
			"{service}", request.service)
	}
	if !s.generation.OfferingAvailable(offering.ID) {
		return generation.Offering{}, s.mcpMessageProblem(call, "service_unavailable", service.Messages.ServiceUnavailable, mcppublic.Subject{},
			"{service}", request.service)
	}
	return offering, nil
}

// mcpProcessingSource finds the model a processing step takes: a succeeded task
// of this very asset (or one a branch of it inherits) that saved a model file.
func (s *Server) mcpProcessingSource(call *mcpCall, request mcpStartRequest, session string, subject mcppublic.Subject) (generation.Task, *mcpOutcome, error) {
	service := s.application.MCPPublic
	missing := func() (generation.Task, *mcpOutcome, error) {
		return generation.Task{}, s.mcpMessageProblem(call, "source_not_found", service.Messages.SourceNotFound, subject), nil
	}
	task, err := s.generation.Get(call.ctx, call.owner, generation.TaskID(request.sourceTask))
	if generation.IsCode(err, generation.ErrNotFound) || generation.IsCode(err, generation.ErrForbidden) {
		return missing()
	}
	if err != nil {
		return generation.Task{}, nil, err
	}
	if task.OwnerID != call.owner || task.ID != generation.TaskID(request.sourceTask) {
		return missing()
	}
	if task.SessionID != session {
		inSession, err := s.generation.TaskInSession(call.ctx, call.owner, session, task.ID)
		if err != nil {
			return generation.Task{}, nil, err
		}
		if !inSession {
			return missing()
		}
	}
	saved, named := false, request.sourceAsset == ""
	for _, asset := range task.Assets {
		if asset.Role == "model" && asset.StorageKey != "" {
			saved = true
			named = named || string(asset.ID) == request.sourceAsset
		}
	}
	if !named {
		return missing()
	}
	if task.State != generation.TaskSucceeded || !saved {
		return generation.Task{}, s.mcpMessageProblem(call, "source_not_ready", service.Messages.SourceNotReady, subject), nil
	}
	return task, nil, nil
}

// mcpProcessingOffering names the service of a processing step and how the
// source feeds it. Asked for by name, a service that cannot take the source is
// refused; otherwise the default of the capability among the available services
// that accept the source is used (the first of the Schema's order that is
// marked default, else the first).
func (s *Server) mcpProcessingOffering(call *mcpCall, request mcpStartRequest, source generation.Task, subject mcppublic.Subject) (
	generation.Offering, *generation.ModelSourceBinding, *mcpOutcome,
) {
	service := s.application.MCPPublic
	prefer := service.Generation.SourceVariantPreference
	bind := func(offering generation.Offering) *generation.ModelSourceBinding {
		binding, err := generation.BindModelSource(offering, source, generation.AssetID(request.sourceAsset), prefer)
		if err != nil {
			return nil
		}
		return &binding
	}
	if request.service != "" {
		offering, outcome := s.mcpNamedOffering(call, request)
		if outcome != nil {
			return generation.Offering{}, nil, outcome
		}
		if binding := bind(offering); binding != nil {
			return offering, binding, nil
		}
		return generation.Offering{}, nil, s.mcpMessageProblem(call, "source_incompatible", service.Messages.SourceIncompatible, subject)
	}
	var chosen *generation.Offering
	var chosenBinding *generation.ModelSourceBinding
	for _, candidate := range s.generation.Offerings(request.capability) {
		if !candidate.Enabled || !s.generation.OfferingAvailable(candidate.ID) {
			continue
		}
		binding := bind(candidate)
		if binding == nil {
			continue
		}
		if candidate.Default {
			return candidate, binding, nil
		}
		if chosen == nil {
			found := candidate
			chosen, chosenBinding = &found, binding
		}
	}
	if chosen == nil {
		return generation.Offering{}, nil, s.mcpMessageProblem(call, "source_incompatible", service.Messages.SourceIncompatible, subject)
	}
	return *chosen, chosenBinding, nil
}

// mcpBuildInput lays the call's arguments over the service's declared
// parameters. The caller's parameters must be ones the service lets a caller
// set: a fixed, hidden, read-only or file-taking parameter is refused by name,
// and one the service advises against changing needs its reason. Their values
// are not checked here: the kernel normalizes and validates every one when the
// input is frozen, the same way for every entry.
func (s *Server) mcpBuildInput(call *mcpCall, offering generation.Offering, request mcpStartRequest, binding *generation.ModelSourceBinding) (json.RawMessage, *mcpOutcome) {
	invalid := func(detail string) (json.RawMessage, *mcpOutcome) {
		outcome := s.mcpInvalidArguments(call.locale, detail)
		return nil, &outcome
	}
	declared := make(map[string]generation.Parameter, len(offering.Parameters))
	var promptName, referenceName string
	for _, parameter := range offering.Parameters {
		declared[parameter.Name] = parameter
		switch parameter.Semantic {
		case generation.SemanticPrompt:
			promptName = parameter.Name
		case generation.SemanticReferenceImage:
			referenceName = parameter.Name
		}
	}
	input := map[string]any{}
	names := make([]string, 0, len(request.parameters))
	for name := range request.parameters {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		parameter, known := declared[name]
		switch {
		case !known || !offering.AgentParameterAllowed(parameter):
			return invalid("$.parameters." + name + ": is not a parameter this service lets a caller set (see list_services)")
		case name == promptName:
			return invalid("$.parameters." + name + ": give the prompt in the prompt argument")
		}
		input[name] = request.parameters[name]
		if parameter.AdjustmentPolicy == "advised" && !sameParameterDefault(request.parameters[name], parameter.Default) {
			if reason, _ := request.reasons[name].(string); strings.TrimSpace(reason) == "" {
				return invalid("$.parameter_reasons." + name + ": this parameter is advised against changing; say why in parameter_reasons")
			}
		}
	}
	for name := range request.reasons {
		if parameter, known := declared[name]; !known || parameter.AdjustmentPolicy != "advised" {
			return invalid("$.parameter_reasons." + name + ": this parameter takes no reason (only an advised parameter does)")
		}
	}
	if request.prompt != nil {
		if promptName == "" {
			return invalid("$.prompt: this service takes no prompt")
		}
		input[promptName] = *request.prompt
	}
	if len(request.images) > 0 {
		if referenceName == "" {
			return invalid("$.images: this service takes no pictures")
		}
		if declared[referenceName].Type == "asset" {
			if len(request.images) != 1 {
				return invalid("$.images: this service takes exactly one picture")
			}
			input[referenceName] = request.images[0]
		} else {
			input[referenceName] = request.images
		}
	}
	if binding != nil {
		input[binding.Parameter] = binding.Value
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return invalid("$.parameters: cannot be encoded")
	}
	// An Offering that bills extra credits for some inputs needs the consent
	// flag. The server sets it, as for the Agent: the caller cannot name it, the
	// kernel measures what the frozen input then costs extra, and the access-key
	// policy decides that exact input.
	consented, err := generationtool.ConsentInput(offering, encoded)
	if err != nil {
		return invalid("$.parameters: cannot be encoded")
	}
	return consented, nil
}

// sameParameterDefault reports whether a caller's value is the declared default.
func sameParameterDefault(value any, declared json.RawMessage) bool {
	if len(declared) == 0 {
		return false
	}
	var fallback any
	if json.Unmarshal(declared, &fallback) != nil {
		return false
	}
	return reflect.DeepEqual(value, fallback)
}
