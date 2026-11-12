package generationtool

// The persisted 3D self-check contract (#246) on the tool side: every real
// read-only review call appends evidence to the record, the counters enforce
// the Schema's limits, and record_self_check writes the Agent's conclusion.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// selfCheckStatusBlock is the counters every review tool result carries.
type selfCheckStatusBlock struct {
	Status           string `json:"status"`
	StatusLabel      string `json:"status_label,omitempty"`
	RoundsUsed       int    `json:"rounds_used"`
	RoundsLimit      int    `json:"rounds_limit"`
	RendersInRound   int    `json:"renders_in_round"`
	RendersLimit     int    `json:"renders_limit"`
	CorrectionsUsed  int    `json:"corrections_used"`
	CorrectionsLimit int    `json:"corrections_limit"`
	// Message states the Schema's own fact about this call: which evidence
	// was recorded and how many rounds, renders or corrections remain.
	Message string `json:"message,omitempty"`
}

func (executor *Executor) selfCheckBlock(record chat.SelfCheckRecord) *selfCheckStatusBlock {
	if executor.selfChecks == nil {
		return nil
	}
	counters := executor.selfChecks.Counts(record)
	return &selfCheckStatusBlock{
		Status:           counters.StatusID,
		StatusLabel:      counters.StatusLabel,
		RoundsUsed:       counters.RoundsUsed,
		RoundsLimit:      counters.RoundsLimit,
		RendersInRound:   counters.RendersInRound,
		RendersLimit:     counters.RendersLimit,
		CorrectionsUsed:  counters.CorrectionsUsed,
		CorrectionsLimit: counters.CorrectionsLimit,
	}
}

// selfCheckRecord reads the evidence counters of one result. No record means an
// empty record: unchecked, zero rounds.
func (executor *Executor) selfCheckRecord(ctx context.Context, command chat.ToolCommand, taskID string) chat.SelfCheckRecord {
	if executor.selfChecks == nil || taskID == "" {
		return chat.SelfCheckRecord{TaskID: taskID}
	}
	record, found, err := executor.selfChecks.Get(ctx, command.OwnerID, command.SessionID, taskID)
	if err != nil {
		executor.logger.Warn("self-check record not readable", "task_id", taskID, "error", err)
		return chat.SelfCheckRecord{TaskID: taskID}
	}
	if !found {
		return chat.SelfCheckRecord{TaskID: taskID}
	}
	return record
}

// appendSelfCheckCheck stores one real call's evidence. A storage failure is
// logged and left out of the result: it must not lose the rendered pictures or
// the inspection the Agent already paid attention to.
func (executor *Executor) appendSelfCheckCheck(ctx context.Context, command chat.ToolCommand,
	source reviewedModel, check chat.SelfCheckCheck) *selfCheckStatusBlock {
	if executor.selfChecks == nil {
		return nil
	}
	if check.At.IsZero() {
		check.At = executor.clock().UTC()
	}
	record, err := executor.selfChecks.AppendCheck(ctx, command.OwnerID, command.SessionID,
		string(source.task.ID), string(source.asset.ID), check)
	if err != nil {
		executor.logger.Warn("self-check evidence not stored",
			"task_id", source.task.ID, "kind", check.Kind, "error", err)
		return nil
	}
	block := executor.selfCheckBlock(record)
	if block != nil {
		block.Message = executor.checkRecordedMessage(command, record, check.Kind)
	}
	return block
}

// checkRecordedMessage words the stored evidence and the headroom the Schema
// leaves after it. The remaining texts are the Schema's own copy, so the
// Agent reads the limits from the same declaration that enforces them.
func (executor *Executor) checkRecordedMessage(command chat.ToolCommand, record chat.SelfCheckRecord,
	kind string) string {
	texts := executor.selfChecks.Schema().Texts
	counters := executor.selfChecks.Counts(record)
	parts := []string{executor.fillText(command, texts.CheckRecorded,
		"{kind}", kind, "{round}", strconv.Itoa(counters.RoundsUsed))}
	switch kind {
	case chat.SelfCheckKindInspect:
		if remaining := counters.RoundsLimit - counters.RoundsUsed; remaining > 0 {
			parts = append(parts, executor.fillText(command, texts.RoundsRemaining,
				"{remaining}", strconv.Itoa(remaining), "{limit}", strconv.Itoa(counters.RoundsLimit)))
		}
	default:
		if remaining := counters.RendersLimit - counters.RendersInRound; remaining > 0 {
			parts = append(parts, executor.fillText(command, texts.RendersRemaining,
				"{remaining}", strconv.Itoa(remaining), "{limit}", strconv.Itoa(counters.RendersLimit)))
		}
	}
	return strings.Join(parts, " ")
}

// selfCheckRoundRefusal refuses another inspect round above the Schema's
// per-result limit. The record writer stays available so the Agent can close
// the record truthfully.
func (executor *Executor) selfCheckRoundRefusal(ctx context.Context, command chat.ToolCommand,
	source reviewedModel) *chat.ToolResult {
	if executor.selfChecks == nil {
		return nil
	}
	record := executor.selfCheckRecord(ctx, command, string(source.task.ID))
	counters := executor.selfChecks.Counts(record)
	if counters.RoundsLimit <= 0 || counters.RoundsUsed < counters.RoundsLimit {
		return nil
	}
	refusal := executor.modelReviewRefusal(command, executor.localized(command, executor.selfChecks.Schema().Texts.LimitReached))
	return &refusal
}

// selfCheckRenderRefusal refuses another render, comparison or reference
// comparison in the current round. The same reply's own render-call bound is
// checked separately and may hit first.
func (executor *Executor) selfCheckRenderRefusal(ctx context.Context, command chat.ToolCommand,
	source reviewedModel) *chat.ToolResult {
	if executor.selfChecks == nil {
		return nil
	}
	record := executor.selfCheckRecord(ctx, command, string(source.task.ID))
	counters := executor.selfChecks.Counts(record)
	if counters.RendersLimit <= 0 || counters.RendersInRound < counters.RendersLimit {
		return nil
	}
	refusal := executor.modelReviewRefusal(command, executor.localized(command, executor.selfChecks.Schema().Texts.LimitReached))
	return &refusal
}

// RecordSelfCheck executes record_self_check: it resolves the owner's finished
// model, applies the Schema's conclusion rules and persists the conclusion.
// Owner and session come from the server-held command; the arguments only name
// the model and the words.
func (executor *Executor) RecordSelfCheck(ctx context.Context, command chat.ToolCommand) (chat.ToolResult, error) {
	if executor.selfChecks == nil {
		return chat.ToolResult{Error: "self-check record unavailable", NoSubmission: true}, nil
	}
	review := executor.chat.ModelReview
	texts := executor.selfChecks.Schema().Texts
	var arguments struct {
		Model     string   `json:"model"`
		Status    string   `json:"status"`
		Summary   string   `json:"summary"`
		Findings  []string `json:"findings"`
		Fixes     []string `json:"fixes"`
		Remaining []string `json:"remaining"`
		Waiver    string   `json:"waiver"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(command.Call.Arguments)))
	decoder.DisallowUnknownFields()
	if len(command.Call.Arguments) == 0 || decoder.Decode(&arguments) != nil ||
		decoder.Decode(new(any)) != io.EOF {
		return executor.modelReviewRefusal(command, executor.fillText(command, texts.RecordInvalidArguments,
			"{detail}", "arguments must match the declared self-check object")), nil
	}
	if strings.TrimSpace(arguments.Status) == "" {
		return executor.modelReviewRefusal(command, executor.fillText(command, texts.RecordInvalidArguments,
			"{detail}", "status is required")), nil
	}
	source, refusal, err := executor.resolveReviewedModel(ctx, command, review,
		strings.TrimSpace(arguments.Model), "")
	if err != nil {
		return chat.ToolResult{}, err
	}
	if refusal != nil {
		return executor.modelReviewRefusal(command, executor.fillText(command, texts.RecordUnknownTask,
			"{model}", displayModelToken(arguments.Model))), nil
	}
	record, err := executor.selfChecks.RecordConclusion(ctx, command.OwnerID, command.SessionID,
		string(source.task.ID), string(source.asset.ID), chat.SelfCheckConclusion{
			Status: arguments.Status, Summary: arguments.Summary,
			Findings: arguments.Findings, Fixes: arguments.Fixes, Remaining: arguments.Remaining,
			Waiver: arguments.Waiver,
		})
	if err != nil {
		var validation *chat.SelfCheckConclusionError
		if errors.As(err, &validation) {
			return executor.selfCheckConclusionRefusal(command, validation), nil
		}
		return chat.ToolResult{Error: "self-check record not saved", NoSubmission: true}, err
	}
	counters := executor.selfChecks.Counts(record)
	label := source.task.Offering.ModelName
	if label == "" {
		label = source.task.Offering.ModelID
	}
	message := executor.localized(command, texts.RecordSaved)
	if conclusion := record.Conclusion; conclusion != nil && conclusion.PurposeSource == modelReviewUsageSourceDefault &&
		strings.TrimSpace(texts.RecordDefaultPurpose) != "" {
		message += " " + executor.fillText(command, texts.RecordDefaultPurpose, "{purpose}", conclusion.PurposeLabel)
	}
	result := map[string]any{
		"ok":           true,
		"message":      message,
		"task_id":      string(source.task.ID),
		"asset_id":     string(source.asset.ID),
		"model":        label,
		"status":       counters.StatusID,
		"status_role":  counters.StatusRole,
		"status_label": counters.StatusLabel,
		"self_check":   executor.selfCheckBlock(record),
		"checks":       record.Checks,
		"conclusion":   record.Conclusion,
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return chat.ToolResult{Error: "self-check record not readable", NoSubmission: true}, nil
	}
	// The persisted summary carries the conclusion's role and remaining items,
	// so the session work ledger (#248) can project this claim onto its step.
	var remaining []string
	if record.Conclusion != nil {
		remaining = record.Conclusion.Remaining
	}
	summary, _ := json.Marshal(map[string]any{
		"tool": "record_self_check", "task_id": string(source.task.ID),
		"status": counters.StatusID, "status_role": counters.StatusRole,
		"rounds_used": counters.RoundsUsed, "corrections_used": counters.CorrectionsUsed,
		"remaining": remaining,
	})
	return chat.ToolResult{AgentContent: string(encoded), NoSubmission: true, Summary: summary}, nil
}

func (executor *Executor) selfCheckConclusionRefusal(command chat.ToolCommand,
	validation *chat.SelfCheckConclusionError) chat.ToolResult {
	texts := executor.selfChecks.Schema().Texts
	switch validation.Kind {
	case "unknown_status":
		return executor.modelReviewRefusal(command, executor.fillText(command, texts.RecordUnknownStatus,
			"{status}", validation.Status, "{statuses}", strings.Join(executor.selfChecks.StatusIDs(), ", ")))
	case "not_writable":
		return executor.modelReviewRefusal(command, executor.fillText(command, texts.RecordNotWritable,
			"{status}", validation.Status, "{statuses}", strings.Join(executor.selfChecks.WritableStatuses(), ", ")))
	case "needs_check":
		return executor.modelReviewRefusal(command, executor.localized(command, texts.RecordNeedsCheck))
	case "usage_failed":
		return executor.modelReviewRefusal(command, executor.fillText(command, texts.RecordUsageFailed,
			"{checks}", validation.Field))
	case "uncheckable_unproven":
		return executor.modelReviewRefusal(command, executor.fillText(command, texts.RecordUncheckableUnproven,
			"{status}", validation.Status))
	case "remaining_required":
		return executor.modelReviewRefusal(command, executor.localized(command, texts.RecordRemainingRequired))
	case "remaining_forbidden":
		return executor.modelReviewRefusal(command, executor.localized(command, texts.RecordRemainingForbidden))
	case "too_long":
		return executor.modelReviewRefusal(command, executor.fillText(command, texts.RecordTooLong,
			"{field}", validation.Field, "{length}", strconv.Itoa(validation.Length),
			"{maximum}", strconv.Itoa(validation.Maximum)))
	default:
		return executor.modelReviewRefusal(command, executor.localized(command, texts.RecordInvalidArguments))
	}
}

// attachOperationCorrection counts the paid processing step this operation
// really submitted against the result it changes and adds the self-check
// counters to the tool result. The operation is the idempotency key: a
// restarted replay, a bound-grant observation or a detached approval that
// settles the same operation can never count it twice. The step is never
// refused by the correction limit: the Schema's text asks the Agent to report
// the remaining issue truthfully instead.
func (executor *Executor) attachOperationCorrection(ctx context.Context, command chat.ToolCommand,
	op Operation, result chat.ToolResult) chat.ToolResult {
	if executor.selfChecks == nil || op.CorrectionTaskID == "" {
		return result
	}
	// The Task is already accepted; a stopping turn must not lose its evidence.
	record, found, err := executor.selfChecks.IncrementCorrectionsOnce(context.WithoutCancel(ctx),
		command.OwnerID, command.SessionID, op.CorrectionTaskID, CorrectionKey(op))
	if err != nil {
		executor.logger.Warn("self-check correction not counted", "task_id", op.CorrectionTaskID, "error", err)
		return result
	}
	if !found {
		return result
	}
	block := executor.selfCheckBlock(record)
	note, remaining := executor.correctionNote(command, executor.selfChecks.Counts(record))
	if block != nil {
		block.Message = remaining
	}
	return executor.addSelfCheckBlock(result, block, note)
}

// correctionNote words what the correction counters leave: the Schema's limit
// reached note when nothing remains, otherwise how many corrections remain.
func (executor *Executor) correctionNote(command chat.ToolCommand,
	counters chat.SelfCheckCounters) (note, remaining string) {
	if counters.CorrectionsLimit <= 0 {
		return "", ""
	}
	texts := executor.selfChecks.Schema().Texts
	if counters.CorrectionsUsed >= counters.CorrectionsLimit {
		return executor.localized(command, texts.LimitReached), ""
	}
	remaining = executor.fillText(command, texts.CorrectionsRemaining,
		"{remaining}", strconv.Itoa(counters.CorrectionsLimit-counters.CorrectionsUsed),
		"{limit}", strconv.Itoa(counters.CorrectionsLimit))
	return "", remaining
}

// attachCorrectionReceipt adds the current self-check counters to the receipt
// of a paid processing step that finished after its submission. The count was
// taken when the step was accepted; this path must not increment it again.
func (executor *Executor) attachCorrectionReceipt(ctx context.Context, command chat.ToolCommand,
	result chat.ToolResult) chat.ToolResult {
	if executor.selfChecks == nil || command.Tool.Processing == nil || result.AgentContent == "" {
		return result
	}
	var arguments map[string]json.RawMessage
	if json.Unmarshal(command.Call.Arguments, &arguments) != nil || arguments == nil {
		return result
	}
	source, _, rejection, err := executor.resolveProcessingSource(ctx, command, arguments)
	if err != nil || rejection != nil || source.ID == "" {
		return result
	}
	record, found, err := executor.selfChecks.Get(ctx, command.OwnerID, command.SessionID, string(source.ID))
	if err != nil || !found {
		return result
	}
	block := executor.selfCheckBlock(record)
	note, remaining := executor.correctionNote(command, executor.selfChecks.Counts(record))
	if block != nil {
		block.Message = remaining
	}
	return executor.addSelfCheckBlock(result, block, note)
}

// addSelfCheckBlock merges the counters into an existing JSON receipt. A
// receipt that is not a JSON object is left as it is; the step itself already
// happened and must not be lost because its presentation could not be edited.
func (executor *Executor) addSelfCheckBlock(result chat.ToolResult, block *selfCheckStatusBlock,
	note string) chat.ToolResult {
	if block == nil || result.AgentContent == "" {
		return result
	}
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(result.AgentContent), &payload); err != nil {
		return result
	}
	payload["self_check"] = block
	if note != "" {
		payload["self_check_note"] = note
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return result
	}
	result.AgentContent = string(encoded)
	return result
}

func displayModelToken(token string) string {
	if token = strings.TrimSpace(token); token != "" {
		return token
	}
	return "the selected model"
}
