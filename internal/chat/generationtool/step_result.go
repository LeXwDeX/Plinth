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

// attachStepStanding adds the asset's 3D step standing to a generation tool
// result (#330 step 3B), beside the budget object. It appears only where the
// result has a step context: the tool's capability is one the Schema counts as
// a 3D step, and the session's approval mode derives the internal steps mode.
// A 2D tool, another approval mode and a standing that cannot be read add
// nothing: the object never carries a guessed zero. A result without agent
// content (a task wait, a suspension) and a tool whose Schema declares no
// result key for it are left as they are.
func (executor *Executor) attachStepStanding(
	ctx context.Context,
	command chat.ToolCommand,
	result *chat.ToolResult,
) {
	key := command.Tool.ResultFields.Steps
	if key == "" || result == nil || len(result.AgentContent) == 0 ||
		executor.stepPolicy == nil || executor.grants == nil {
		return
	}
	if !executor.stepCounts(command.Tool.Capability) {
		return
	}
	summary, err := executor.grants.StepStatus(ctx, command.OwnerID, command.SessionID)
	if err != nil {
		// A session the steps mode does not govern answers ErrInvalid: it has
		// no step context, which is not a failure. Any other read failure is
		// logged and leaves the object out rather than guessing a number.
		if !errors.Is(err, executiongrant.ErrInvalid) && executor.logger != nil {
			executor.logger.WarnContext(ctx, "读取 3D 步数失败，工具结果不带 steps 对象",
				"owner_id", command.OwnerID, "session_id", command.SessionID, "error", err)
		}
		return
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(result.AgentContent), &payload); err != nil {
		return
	}
	if _, exists := payload[key]; exists {
		return
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
		standing[catalog.StepResultCap] = summary.Cap
		standing[catalog.StepResultRemaining] = max(summary.Cap-summary.Used, 0)
	}
	return standing
}
