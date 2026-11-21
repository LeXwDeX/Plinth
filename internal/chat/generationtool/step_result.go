package generationtool

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// stepStandingState is one tool call's 3D step evidence (#330 step 3B): the
// capability this call really runs and the standing a step-cap refusal was
// decided on. The submission path records both, so the text the Agent already
// received and the object it reads come from one read. Without the record the
// object falls back to the Catalog's step mapping and a fresh read.
type stepStandingState struct {
	capability schemacontract.Capability
	summary    *executiongrant.StepSummary
}

type stepStandingStateKey struct{}

// withStepStandingState gives one Execute call its private evidence record.
func withStepStandingState(ctx context.Context) (context.Context, *stepStandingState) {
	state := &stepStandingState{}
	return context.WithValue(ctx, stepStandingStateKey{}, state), state
}

func stepStandingStateOf(ctx context.Context) *stepStandingState {
	state, _ := ctx.Value(stepStandingStateKey{}).(*stepStandingState)
	return state
}

// recordStepCapability remembers the capability the submission path resolved.
// A processing tool whose capability depends on its step argument must report
// the step's capability, not the tool's main one.
func recordStepCapability(ctx context.Context, capability schemacontract.Capability) {
	if state := stepStandingStateOf(ctx); state != nil {
		state.capability = capability
	}
}

// recordStepSummary remembers the standing a refusal was decided on, so the
// steps object repeats the numbers the refusal text already used instead of a
// second read that could disagree.
func recordStepSummary(ctx context.Context, summary executiongrant.StepSummary) {
	if state := stepStandingStateOf(ctx); state != nil {
		state.summary = &summary
	}
}

// attachStepStanding adds the asset's 3D step standing to a generation tool
// result (#330 step 3B), beside the budget object. It appears only where the
// result has a step context: the capability this call really runs is one the
// Schema counts as a 3D step, and the session's approval mode derives the
// internal steps mode. A 2D tool, another approval mode and a standing that
// cannot be read add nothing: the object never carries a guessed zero.
//
// The object belongs to the receipt the model reads. That includes a pure
// failure with no agent content of its own: the orchestrator hands the model
// the Error text in place of missing content, so the same failure receipt is
// built with the object inside. A wait (a suspension or a task still being
// awaited) has no receipt yet, and a tool whose Schema declares no result key
// for it, content that is not a JSON object and a result that already owns the
// key (the plan tool's step array) are left as they are.
func (executor *Executor) attachStepStanding(
	ctx context.Context,
	command chat.ToolCommand,
	result *chat.ToolResult,
	state *stepStandingState,
) {
	key := command.Tool.ResultFields.Steps
	if key == "" || result == nil || executor.stepPolicy == nil || executor.grants == nil ||
		result.Suspend || result.AwaitTask {
		return
	}
	capability, known := command.Tool.Capability, true
	if state != nil && state.capability != "" {
		capability = state.capability
	} else if command.Tool.Processing != nil && command.Tool.Processing.StepArgument != "" {
		// A tool whose step argument selects the capability must resolve that
		// step; without it the tool's main capability would misreport the call.
		capability, known = stepCapabilityOf(command)
	}
	if !known || !executor.stepCounts(capability) {
		return
	}
	var payload map[string]json.RawMessage
	switch {
	case len(result.AgentContent) > 0:
		if err := json.Unmarshal([]byte(result.AgentContent), &payload); err != nil {
			return
		}
	case result.Error != "":
		// A pure failure: without content the orchestrator would hand the model
		// only the error text. Build the same failure receipt the rejection
		// results use, with the standing beside it.
		success, failure := command.Tool.ResultFields.Success, command.Tool.ResultFields.Error
		if success == "" || failure == "" {
			return
		}
		message, err := json.Marshal(result.Error)
		if err != nil {
			return
		}
		payload = map[string]json.RawMessage{success: json.RawMessage("false"), failure: message}
	default:
		return
	}
	if _, exists := payload[key]; exists {
		return
	}
	var summary executiongrant.StepSummary
	if state != nil && state.summary != nil {
		summary = *state.summary
	} else {
		read, err := executor.grants.StepStatus(ctx, command.OwnerID, command.SessionID)
		if err != nil {
			// A session the steps mode does not govern answers ErrInvalid: it
			// has no step context, which is not a failure. Any other read
			// failure is logged and leaves the object out rather than guessing
			// a number.
			if !errors.Is(err, executiongrant.ErrInvalid) && executor.logger != nil {
				executor.logger.WarnContext(ctx, "读取 3D 步数失败，工具结果不带 steps 对象",
					"owner_id", command.OwnerID, "session_id", command.SessionID, "error", err)
			}
			return
		}
		summary = read
	}
	encoded, err := json.Marshal(stepStandingResult(summary))
	if err != nil {
		return
	}
	payload[key] = encoded
	content, err := json.Marshal(payload)
	if err != nil {
		return
	}
	result.AgentContent = string(content)
}

// stepCapabilityOf resolves the capability a processing call really runs when
// the submission path did not record it. A tool whose step argument selects one
// of several capabilities (process_model) takes the Catalog's step mapping; a
// uv-unwrap step must not inherit the standing of the tool's main capability.
func stepCapabilityOf(command chat.ToolCommand) (schemacontract.Capability, bool) {
	processing := command.Tool.Processing
	if processing == nil || processing.StepArgument == "" {
		return "", false
	}
	var arguments map[string]json.RawMessage
	if json.Unmarshal(command.Call.Arguments, &arguments) != nil {
		return "", false
	}
	var step string
	if json.Unmarshal(arguments[processing.StepArgument], &step) != nil {
		return "", false
	}
	capability, known := processing.Steps[step]
	return capability, known
}

// stepCounts reports whether the Schema counts the tool's capability as a 3D
// step. 2D and platform-only capabilities are never counted, so their results
// carry no step standing.
func (executor *Executor) stepCounts(capability schemacontract.Capability) bool {
	return capability != "" && slices.Contains(executor.stepPolicy.CountedCapabilities, capability)
}

// stepStandingResult is the steps object of one result. Every mode reports the
// mode and the used steps; only the cap mode reports a limit, so cap and
// remaining are absent under agent and unlimited instead of naming a cap the
// platform never applies (#330 step 3B). The property names come from the
// Catalog Schema (catalog.StepResult*), which validation pins.
func stepStandingResult(summary executiongrant.StepSummary) map[string]any {
	standing := map[string]any{
		catalog.StepResultMode: summary.Mode,
		catalog.StepResultUsed: summary.Used,
	}
	if summary.Mode == approvalpolicy.StepModeCap {
		standing[catalog.StepResultCap] = summary.Limit()
		standing[catalog.StepResultRemaining] = summary.Remaining()
	}
	return standing
}
