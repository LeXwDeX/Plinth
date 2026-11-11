package generationtool

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
)

type reviseProcessingArguments struct {
	PlanID              string          `json:"plan_id"`
	ExpectedPlanVersion int64           `json:"expected_plan_version"`
	Operation           string          `json:"operation"`
	StepIndex           int             `json:"step_index"`
	ContingencyID       string          `json:"contingency_id"`
	Capability          string          `json:"capability"`
	OfferingID          string          `json:"offering_id"`
	Parameters          json.RawMessage `json:"parameters"`
	Reason              string          `json:"reason"`
}

// ReviseProcessingPlan is the Agent's controlled plan revision (#251). It
// never submits or approves a step: it moves the plan's steps within the
// approval the person already gave, and the normal per-step grant machinery
// still decides each paid attempt.
func (executor *Executor) ReviseProcessingPlan(ctx context.Context, command chat.ToolCommand) (chat.ToolResult, error) {
	config, found := executor.planToolConfig()
	if !found {
		return chat.ToolResult{NoSubmission: true, Error: "processing plan tool schema unavailable"}, nil
	}
	if executor.plans == nil {
		return executor.reject(command, config.Messages.Unavailable)
	}
	var arguments reviseProcessingArguments
	decoder := json.NewDecoder(strings.NewReader(string(command.Call.Arguments)))
	decoder.DisallowUnknownFields()
	if !json.Valid(command.Call.Arguments) || decoder.Decode(&arguments) != nil ||
		decoder.Decode(new(any)) != io.EOF || arguments.PlanID == "" || arguments.ExpectedPlanVersion <= 0 ||
		arguments.StepIndex < 0 || strings.TrimSpace(arguments.Reason) == "" {
		return executor.reject(command, executor.fillText(command, config.Messages.InvalidInput, "{detail}", "arguments"))
	}
	plan, err := executor.plans.Get(ctx, generation.OwnerID(command.OwnerID), arguments.PlanID)
	if errors.Is(err, processing.ErrMissing) || (err == nil && plan.SessionID != command.SessionID) {
		return executor.reject(command, config.Plan.NotFoundContent)
	}
	if err != nil {
		return chat.ToolResult{NoSubmission: true}, err
	}
	var contingency *budget.PlanContingency
	if arguments.Operation == processing.OperationInsert {
		contingency = executor.approvedContingency(ctx, command, arguments.ContingencyID)
		if contingency == nil {
			return executor.revisionRefusal(command, config, config.Messages.ContingencyUnknown)
		}
	}
	revised, err := executor.plans.Revise(ctx, processing.ReviseCommand{
		Harness:             turnHarnessBinding(command),
		OwnerID:             generation.OwnerID(command.OwnerID),
		SessionID:           command.SessionID,
		PlanID:              arguments.PlanID,
		ExpectedPlanVersion: arguments.ExpectedPlanVersion,
		Operation:           arguments.Operation,
		StepIndex:           arguments.StepIndex,
		ContingencyID:       arguments.ContingencyID,
		Contingency:         contingency,
		Capability:          generation.Capability(arguments.Capability),
		OfferingID:          generation.OfferingID(arguments.OfferingID),
		Parameters:          arguments.Parameters,
		Reason:              arguments.Reason,
	})
	switch {
	case errors.Is(err, processing.ErrRevisionOutsidePlan):
		return executor.approvalRequiredRefusal(command, config)
	case errors.Is(err, processing.ErrRevisionNotStarted):
		return executor.revisionRefusal(command, config, config.Messages.RevisionNotStarted)
	case errors.Is(err, processing.ErrRevisionRetryPerson):
		return executor.revisionDecisionRefusal(command, config, config.Messages.RevisionRetryPerson)
	case errors.Is(err, processing.ErrRevisionCheckpointBlocked):
		return executor.revisionRefusal(command, config, config.Messages.RevisionCheckpointBlocked)
	case errors.Is(err, processing.ErrConflict):
		return executor.revisionRefusal(command, config, "The plan changed; read it again with get_processing_plan.")
	case errors.Is(err, processing.ErrInvalid):
		return executor.reject(command, executor.fillText(command, config.Messages.InvalidInput, "{detail}", "revision"))
	case err != nil:
		return chat.ToolResult{NoSubmission: true}, err
	}
	return executor.revisedPlanResult(ctx, command, config, revised)
}

// planToolConfig finds the processing plan tool's Schema block: the result
// content and messages are shared with get_processing_plan.
func (executor *Executor) planToolConfig() (*catalog.ChatToolProcessingSchema, bool) {
	for _, candidate := range executor.chat.Tools {
		if candidate.Processing != nil && candidate.Processing.Plan != nil {
			return candidate.Processing, true
		}
	}
	return nil, false
}

// approvedContingency resolves the contingency from the session's approved
// production plan. The Agent names an id; it can never invent a repair.
func (executor *Executor) approvedContingency(ctx context.Context, command chat.ToolCommand,
	id string) *budget.PlanContingency {
	if executor.grants == nil || id == "" {
		return nil
	}
	summary, found, err := executor.grants.BudgetSummary(ctx, command.OwnerID, command.SessionID)
	if err != nil || !found || summary.State != budget.StateActive {
		return nil
	}
	contingency, declared := summary.Plan.Contingency(id)
	if !declared {
		return nil
	}
	return &contingency
}

func (executor *Executor) revisionRefusal(command chat.ToolCommand, config *catalog.ChatToolProcessingSchema, text string) (chat.ToolResult, error) {
	if text == "" {
		text = "the revision was refused"
	}
	content, _ := json.Marshal(map[string]any{
		"success": false, "error": executor.localized(command, text), "next_action": "ask_user",
	})
	return chat.ToolResult{AgentContent: string(content), Error: text, NoSubmission: true}, nil
}

// revisionDecisionRefusal refuses a retry whose result is ambiguous: the
// Provider may already have charged it, so the person, not the Agent, decides
// whether to charge again.
func (executor *Executor) revisionDecisionRefusal(command chat.ToolCommand, config *catalog.ChatToolProcessingSchema, text string) (chat.ToolResult, error) {
	if text == "" {
		text = "The retry may already have been charged; the person must decide."
	}
	content, _ := json.Marshal(map[string]any{
		"success": false, "error": executor.localized(command, text),
		"next_action": "ask_user", "user_decision_required": true,
	})
	return chat.ToolResult{AgentContent: string(content), Error: text, NoSubmission: true}, nil
}

func (executor *Executor) approvalRequiredRefusal(command chat.ToolCommand, config *catalog.ChatToolProcessingSchema) (chat.ToolResult, error) {
	text := config.Messages.RevisionOutsidePlan
	if text == "" {
		text = "This revision is outside the approved plan or budget; ask the person."
	}
	content, _ := json.Marshal(map[string]any{
		"success": false, "approval_required": true,
		"error": executor.localized(command, text), "next_action": "ask_user",
	})
	return chat.ToolResult{AgentContent: string(content), Error: text, NoSubmission: true}, nil
}

// revisedPlanResult is the ordinary plan result plus the revision note.
func (executor *Executor) revisedPlanResult(ctx context.Context, command chat.ToolCommand,
	config *catalog.ChatToolProcessingSchema, plan processing.Plan) (chat.ToolResult, error) {
	result, err := executor.processingPlanResult(ctx, command, config, plan)
	if err != nil {
		return result, err
	}
	var payload map[string]any
	if json.Unmarshal([]byte(result.AgentContent), &payload) == nil {
		note := config.Messages.RevisionApplied
		if note == "" {
			note = executor.chat.Processing.Failures.RevisionApplied
		}
		if note == "" {
			note = "The revision was applied."
		}
		payload["message"] = executor.localized(command, note)
		payload["success"] = true
		if content, marshalErr := json.Marshal(payload); marshalErr == nil {
			result.AgentContent = string(content)
		}
	}
	return result, nil
}
