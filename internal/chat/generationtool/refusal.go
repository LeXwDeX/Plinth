package generationtool

import "github.com/LeXwDeX/Plinth/internal/chat"

// A call refused before anything was submitted answers the Agent with Schema
// text, and the server logs the refusal under one of these codes (#378). The
// log carries no prompt, no argument and no Agent text: to whoever reads it
// for a turn that "did nothing" it says which call was refused and why.
const (
	refusalModelUnavailable   = "model_choice_unavailable"
	refusalModelConflict      = "model_choice_conflict"
	refusalModelRequired      = "model_required"
	refusalOfferingChoice     = "offering_choice"
	refusalConceptSketchLimit = "concept_sketch_limit"
	refusalCanvasSource       = "canvas_source"
	refusalMultiviewSelection = "multiview_selection"
	refusalMultiviewViews     = "multiview_views"
	refusalVisualAnchor       = "visual_anchor"
	refusalStyleLock          = "style_lock_unavailable"
	refusalReferencePurpose   = "reference_purpose"
	refusalMask               = "mask"
	refusalModelSource        = "model_source"
	refusalInputGate          = "input_gate"
	refusalStepCap            = "step_cap"
	refusalPromptTooLong      = "prompt_too_long"
)

// logRefusal records a refused call as a warning. Handwritten Executors of
// the tests have no logger.
func (executor *Executor) logRefusal(command chat.ToolCommand, code string) {
	if executor.logger == nil {
		return
	}
	executor.logger.Warn("agent tool call refused",
		"tool", command.Tool.Name, "call_id", command.Call.ID, "owner_id", command.OwnerID,
		"session_id", command.SessionID, "assistant_message_id", command.AssistantMessageID,
		"code", code)
}

// refusal is the result of a call refused before any submission: the Schema's
// public text for the Agent, logged under code.
func (executor *Executor) refusal(command chat.ToolCommand, code, publicMessage string) chat.ToolResult {
	executor.logRefusal(command, code)
	result := executor.failure(command, "", publicMessage)
	result.NoSubmission = true
	return result
}
