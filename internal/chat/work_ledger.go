package chat

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// The session production ledger (#248): a read-only projection of the public
// Tasks of one session, the Agent tool calls that asked for them and the
// read-only self-check summaries persisted with those calls. It writes no new
// task state and copies no generation state machine: everything here is read
// from internal/generation's public Task facts, the chat repository's messages
// and the session's pick.
//
// WorkLedgerSummary renders the block that is added to the system prompt every
// turn. It is computed from those facts, never from the compacted transcript,
// so context compaction cannot lose a step, a prompt or a self-check. The
// block is framed by Schema markers and its texts say it is material, not
// instructions; markers in the rendered text are removed first, like the
// session notes.
//
// Prompt texts and user content in the ledger never reach a log line. The only
// Agent-facing carriers are the system prompt and the get_production_step
// tool result.

// ErrWorkLedgerUnavailable reports a service assembled without the ledger
// policy or the session Task source.
var ErrWorkLedgerUnavailable = errors.New("chat: work ledger unavailable")

// SessionTaskSource reads the session's public Tasks, newest first, bounded to
// the requested limit. The application supplies it with
// generation.Module.List(OwnTasks(owner), ListQuery{SessionID: session,
// IncludeBranchRefs: true, Limit: maximum_steps}); a branch session therefore
// reads its ancestors' Tasks too, while owner scope never widens.
//
// The list projection is reduced on purpose: it carries the prompt and the
// result assets, not the full frozen Offering or input. The ledger summary
// works with it; get_production_step reads the one requested step in full
// through SessionTaskDetail.
type SessionTaskSource func(ctx context.Context, ownerID, sessionID string) (generation.TaskPage, error)

// SessionTaskDetail reads one public Task of the owner in full (the frozen
// Offering, every declared parameter and the whole input). The caller only
// names a Task the session list returned, so session scope is already
// established; this read adds no grant. Nil leaves the tool without full
// parameters.
type SessionTaskDetail func(ctx context.Context, ownerID, sessionID, taskID string) (generation.Task, error)

// WorkDecision states.
const (
	WorkDecisionAdopted  = "adopted"
	WorkDecisionRejected = "rejected"
)

// WorkDecision is one persisted decision about a production step. The ledger
// derives "adopted" from the session pick; "rejected" and its reason come from
// the optional decision source (#241). A step without any source entry has no
// decision.
type WorkDecision struct {
	Step   string `json:"step,omitempty"`
	TaskID string `json:"task_id,omitempty"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// WorkDecisionReader is the optional source of rejected directions (#241).
// Without one the ledger's rejected list stays empty.
type WorkDecisionReader interface {
	WorkDecisions(ctx context.Context, ownerID, sessionID string) ([]WorkDecision, error)
}

// WorkParameter is one effective declared parameter of a step and where its
// value came from. Source is agent, default, style_lock or unknown; sources
// are read-time facts, never guesses about who wrote a value.
type WorkParameter struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Source string `json:"source"`
}

// WorkReference names an input or result of a step by its session handle. It
// never carries a URL, an upstream ID or an asset ID: a reference parameter is
// projected to the handle the history already gave it (or to the step handle
// for a source Task).
type WorkReference struct {
	Handle  string `json:"handle,omitempty"`
	Kind    string `json:"kind,omitempty"`
	Purpose string `json:"purpose,omitempty"`
	Role    string `json:"role,omitempty"`
}

// WorkLeftover is one failed inspect_model check: what failed, the measured
// value, the budget and the declared fix hint.
type WorkLeftover struct {
	Check   string `json:"check"`
	Actual  string `json:"actual,omitempty"`
	Budget  string `json:"budget,omitempty"`
	FixHint string `json:"fix_hint,omitempty"`
}

// WorkSelfCheck is the read-only self-check conclusion stored with a tool call
// and attributed to the step it inspected.
type WorkSelfCheck struct {
	Tool        string         `json:"tool"`
	Passed      int            `json:"passed"`
	Failed      int            `json:"failed"`
	Leftovers   []WorkLeftover `json:"leftovers,omitempty"`
	FailedViews []string       `json:"failed_views,omitempty"`
}

// WorkStep is one step of the ledger: a public Task with its frozen input and
// result, the historical Agent call and its self-check conclusions.
type WorkStep struct {
	Step             string          `json:"step"`
	TaskID           string          `json:"task_id"`
	Capability       string          `json:"capability"`
	Offering         string          `json:"offering"`
	Model            string          `json:"model"`
	Provider         string          `json:"provider"`
	State            string          `json:"state"`
	Progress         int             `json:"progress"`
	FailureClass     string          `json:"failure_class,omitempty"`
	EstimatedCredits int             `json:"estimated_credits,omitempty"`
	RawPrompt        string          `json:"raw_prompt"`
	FinalPrompt      string          `json:"final_prompt"`
	NegativePrompt   string          `json:"negative_prompt,omitempty"`
	Parameters       []WorkParameter `json:"parameters,omitempty"`
	Inputs           []WorkReference `json:"inputs,omitempty"`
	Results          []WorkReference `json:"results,omitempty"`
	SelfChecks       []WorkSelfCheck `json:"self_check"`
	Decision         string          `json:"decision,omitempty"`
	DecisionReason   string          `json:"decision_reason,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
}

// WorkLedger is the projection of one session.
type WorkLedger struct {
	Steps []WorkStep `json:"steps"`
	// Rejected is the "do not do again" list, newest first.
	Rejected []WorkDecision `json:"rejected,omitempty"`
	// CurrentTaskID is the step whose result is the session's picked object, ""
	// when nothing is picked.
	CurrentTaskID string `json:"-"`
	// TotalSteps counts the session's Tasks even when the projection is bounded.
	TotalSteps int `json:"total_steps"`
	// StepOffset is how many older steps were left out by the bound; the step
	// numbers of the returned list start after it.
	StepOffset int `json:"-"`
}

// SetSessionTaskSource supplies the ledger's only task read entry. It is
// called during application assembly, before any turn runs.
func (service *Service) SetSessionTaskSource(source SessionTaskSource) {
	service.workLedgerTasks = source
}

// SetSessionTaskDetail supplies the full-task read that get_production_step
// uses for the one requested step.
func (service *Service) SetSessionTaskDetail(detail SessionTaskDetail) {
	service.workLedgerDetail = detail
}

// SetDecisionReader supplies the optional rejected-direction source (#241).
func (service *Service) SetDecisionReader(reader WorkDecisionReader) {
	service.workDecisions = reader
}

// workLedgerEnabled reports whether the Schema declared the ledger and the
// assembly supplied its task source.
func (service *Service) workLedgerEnabled() bool {
	return service.workLedger.MaximumSteps > 0 && service.workLedgerTasks != nil
}

// WorkLedger projects the owner's session. It reads the public Tasks through
// the injected source, the persisted messages (tool arguments and summaries)
// and the session pick; it never writes.
func (service *Service) WorkLedger(ctx context.Context, ownerID, sessionID string) (WorkLedger, error) {
	return service.readWorkLedger(ctx, ownerID, sessionID, "")
}

// workLedger projects the session; detailTaskID asks for that one step to be
// read in full (the get_production_step path), while every other step stays on
// the reduced list projection.
func (service *Service) readWorkLedger(ctx context.Context, ownerID, sessionID, detailTaskID string) (WorkLedger, error) {
	if !service.workLedgerEnabled() {
		return WorkLedger{}, ErrWorkLedgerUnavailable
	}
	owned, err := service.repository.OwnsSession(ctx, ownerID, sessionID)
	if err != nil {
		return WorkLedger{}, err
	}
	if !owned {
		return WorkLedger{}, ErrSessionNotFound
	}
	page, err := service.workLedgerTasks(ctx, ownerID, sessionID)
	if err != nil {
		return WorkLedger{}, err
	}
	if detailTaskID != "" && service.workLedgerDetail != nil {
		for index := range page.Tasks {
			if string(page.Tasks[index].ID) != detailTaskID {
				continue
			}
			full, err := service.workLedgerDetail(ctx, ownerID, sessionID, detailTaskID)
			if err != nil {
				return WorkLedger{}, err
			}
			page.Tasks[index] = full
			break
		}
	}
	history, err := service.repository.ListMessages(ctx, ownerID, sessionID)
	if err != nil {
		return WorkLedger{}, err
	}
	styleLock, _, err := service.repository.StyleLock(ctx, ownerID, sessionID)
	if err != nil {
		return WorkLedger{}, err
	}
	decisions := []WorkDecision(nil)
	if service.workDecisions != nil {
		decisions, _ = service.workDecisions.WorkDecisions(ctx, ownerID, sessionID)
	}
	pick, _, err := service.SessionPick(ctx, ownerID, sessionID)
	if err != nil {
		return WorkLedger{}, err
	}
	return service.projectWorkLedger(page, history, styleLock, decisions, pick), nil
}

// projectWorkLedger builds the read model. It is pure apart from the values
// its arguments carry, so unit tests can exercise ordering and attribution
// without a repository.
func (service *Service) projectWorkLedger(
	page generation.TaskPage,
	history []Message,
	styleLock StyleLock,
	decisions []WorkDecision,
	pick Attachment,
) WorkLedger {
	tasks := append([]generation.Task(nil), page.Tasks...)
	sort.SliceStable(tasks, func(left, right int) bool {
		if tasks[left].CreatedAt.Equal(tasks[right].CreatedAt) {
			return tasks[left].ID < tasks[right].ID
		}
		return tasks[left].CreatedAt.Before(tasks[right].CreatedAt)
	})
	offset := max(0, page.Total-len(tasks))
	prefix := service.workLedger.StepPrefix
	if prefix == "" {
		prefix = "@step"
	}

	calls := workCallsByTask(history, service.assistantRole)
	attachmentsByAsset := map[string]Attachment{}
	attachmentsByTask := map[string]Attachment{}
	resultAttachments := map[string][]Attachment{}
	for _, message := range history {
		for _, attachment := range message.Attachments {
			if attachment.Handle == "" {
				continue
			}
			if attachment.AssetID != "" {
				attachmentsByAsset[attachment.AssetID] = attachment
			}
			if attachment.TaskID != "" {
				attachmentsByTask[attachment.TaskID] = attachment
				resultAttachments[attachment.TaskID] = append(resultAttachments[attachment.TaskID], attachment)
			}
		}
	}
	stepHandles := make(map[string]string, len(tasks))
	for index := range tasks {
		stepHandles[string(tasks[index].ID)] = prefix + strconv.Itoa(offset+index+1)
	}
	selfChecks := workSelfChecks(history, service.assistantRole, attachmentsByAsset, attachmentsByTask, stepHandles)
	adopted := workAdoptedTask(pick, attachmentsByAsset, attachmentsByTask)
	rejected := workRejectedDecisions(decisions)
	rejectedByTask := map[string]WorkDecision{}
	for _, decision := range rejected {
		if decision.TaskID != "" {
			rejectedByTask[decision.TaskID] = decision
		}
	}

	ledger := WorkLedger{
		Steps:         make([]WorkStep, 0, len(tasks)),
		Rejected:      rejected,
		CurrentTaskID: adopted,
		TotalSteps:    max(page.Total, len(tasks)),
		StepOffset:    offset,
	}
	for index := range tasks {
		task := tasks[index]
		stepHandle := stepHandles[string(task.ID)]
		call := calls[string(task.ID)]
		step := WorkStep{
			Step:             stepHandle,
			TaskID:           string(task.ID),
			Capability:       string(task.Capability),
			Offering:         string(task.Offering.ID),
			Model:            task.Offering.ModelName,
			Provider:         task.Offering.ProviderName,
			State:            string(task.State),
			Progress:         task.Progress,
			EstimatedCredits: task.Offering.EstimatedCredits,
			CreatedAt:        task.CreatedAt,
		}
		if step.Model == "" {
			step.Model = task.Offering.ModelID
		}
		if task.Failure != nil {
			step.FailureClass = string(task.Failure.Class)
		}
		if call != nil {
			step.EstimatedCredits = call.EstimatedCredits
			step.RawPrompt = workPromptFromArguments(call.Arguments, service.promptArgument())
		}
		step.FinalPrompt, step.NegativePrompt = workFrozenPrompts(task)
		styleText := ""
		if styleLock.Enabled {
			styleText = styleLock.Text
		}
		step.Parameters, step.Inputs = workParameters(task, call, styleText, stepHandles, attachmentsByAsset, attachmentsByTask)
		step.Results = workResults(task, resultAttachments)
		step.SelfChecks = selfChecks[string(task.ID)]
		if adopted != "" && adopted == string(task.ID) {
			step.Decision = WorkDecisionAdopted
		}
		if decision, exists := rejectedByTask[string(task.ID)]; exists {
			step.Decision = WorkDecisionRejected
			step.DecisionReason = decision.Reason
		}
		ledger.Steps = append(ledger.Steps, step)
	}
	return ledger
}

// promptArgument is the declared argument that carries the Agent's prompt.
func (service *Service) promptArgument() string {
	if service.placeholderHealing.PromptArgument != "" {
		return service.placeholderHealing.PromptArgument
	}
	return "prompt"
}

// workCallsByTask indexes the historical tool calls by the Task they bound.
func workCallsByTask(history []Message, assistantRole string) map[string]*ToolCall {
	calls := map[string]*ToolCall{}
	for messageIndex := range history {
		message := &history[messageIndex]
		if message.Role != assistantRole {
			continue
		}
		for callIndex := range message.ToolCalls {
			call := &message.ToolCalls[callIndex]
			if call.TaskID != "" {
				calls[call.TaskID] = call
			}
		}
	}
	return calls
}

// workPromptFromArguments reads the Agent's original prompt from the frozen
// call arguments. An argument object that is not an object yields no prompt.
func workPromptFromArguments(arguments []byte, name string) string {
	if len(arguments) == 0 {
		return ""
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(arguments, &values) != nil {
		return ""
	}
	raw, present := values[name]
	if !present {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return ""
	}
	return text
}

// workFrozenPrompts reads the final prompt and negative prompt from the Task's
// frozen input, by the Offering's declared semantics. This is what was really
// sent upstream (style lock and template rewrites included); the Agent's
// original wording lives only in the historical call arguments.
func workFrozenPrompts(task generation.Task) (prompt, negative string) {
	var input map[string]json.RawMessage
	if len(task.Input) == 0 || json.Unmarshal(task.Input, &input) != nil {
		return "", ""
	}
	for _, parameter := range task.Offering.Parameters {
		raw, present := input[parameter.Name]
		if !present || len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var text string
		if json.Unmarshal(raw, &text) != nil {
			continue
		}
		switch parameter.Semantic {
		case generation.SemanticPrompt:
			if prompt == "" {
				prompt = text
			}
		case generation.SemanticNegativePrompt:
			if negative == "" {
				negative = text
			}
		}
	}
	return prompt, negative
}

// workParameters projects every declared, non-secret parameter with its
// effective value and read-time source, and every reference parameter as a
// handle plus its declared purpose. Mask, source-URL, source-asset and hidden
// parameters never leave their raw values.
func workParameters(
	task generation.Task,
	call *ToolCall,
	styleText string,
	stepHandles map[string]string,
	attachmentsByAsset map[string]Attachment,
	attachmentsByTask map[string]Attachment,
) ([]WorkParameter, []WorkReference) {
	var input map[string]json.RawMessage
	if len(task.Input) > 0 {
		_ = json.Unmarshal(task.Input, &input)
	}
	var arguments map[string]json.RawMessage
	if call != nil && len(call.Arguments) > 0 {
		_ = json.Unmarshal(call.Arguments, &arguments)
	}
	var parameters []WorkParameter
	var inputs []WorkReference
	for _, parameter := range task.Offering.Parameters {
		raw, present := input[parameter.Name]
		if !present || len(raw) == 0 || string(raw) == "null" {
			continue
		}
		if parameter.Presentation != nil && parameter.Presentation.Surface == "hidden" {
			continue
		}
		if parameter.Semantic == generation.SemanticPrompt || parameter.Semantic == generation.SemanticNegativePrompt {
			continue
		}
		switch {
		case parameter.Type == "asset" || parameter.Type == "asset-array":
			inputs = append(inputs, workAssetReferences(raw, parameter, attachmentsByAsset)...)
			continue
		case parameter.Type == "task":
			inputs = append(inputs, workTaskReferences(raw, parameter, stepHandles, attachmentsByTask)...)
			continue
		case parameter.Semantic == generation.SemanticMask:
			inputs = append(inputs, WorkReference{Kind: "mask", Purpose: string(parameter.Semantic)})
			continue
		case parameter.Semantic == generation.SemanticSourceURL || parameter.Semantic == generation.SemanticSourceAsset:
			// A remote address or an internal asset ID is never projected.
			continue
		case strings.HasSuffix(parameter.Name, "_url"):
			continue
		}
		parameters = append(parameters, WorkParameter{
			Name:   parameter.Name,
			Value:  workValueText(raw, parameter.Type),
			Source: workParameterSource(parameter, raw, arguments, styleText),
		})
	}
	return parameters, inputs
}

// workParameterSource reports where an effective value came from, in the
// declared order: the Agent's call, the Offering default, the style lock text,
// otherwise unknown. It never guesses a person wrote a value.
func workParameterSource(
	parameter generation.Parameter,
	value json.RawMessage,
	arguments map[string]json.RawMessage,
	styleText string,
) string {
	if _, exists := arguments[parameter.Name]; exists {
		return "agent"
	}
	if len(parameter.Default) > 0 && workSameJSON(value, parameter.Default) {
		return "default"
	}
	if strings.TrimSpace(styleText) != "" {
		var text string
		if json.Unmarshal(value, &text) == nil && strings.Contains(text, styleText) {
			return "style_lock"
		}
	}
	return "unknown"
}

func workSameJSON(left, right json.RawMessage) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	leftEncoded, leftErr := json.Marshal(leftValue)
	rightEncoded, rightErr := json.Marshal(rightValue)
	return leftErr == nil && rightErr == nil && string(leftEncoded) == string(rightEncoded)
}

// workValueText renders a scalar or small value for the Agent. Objects and
// arrays are compact JSON, bounded so one parameter cannot flood the result.
func workValueText(raw json.RawMessage, parameterType string) string {
	var text string
	if parameterType == "string" && json.Unmarshal(raw, &text) == nil {
		return text
	}
	var scalar any
	if json.Unmarshal(raw, &scalar) == nil {
		encoded, err := json.Marshal(scalar)
		if err == nil {
			return truncateRunes(string(encoded), 400)
		}
	}
	return truncateRunes(string(raw), 400)
}

func workAssetReferences(raw json.RawMessage, parameter generation.Parameter, attachments map[string]Attachment) []WorkReference {
	var ids []string
	if parameter.Type == "asset-array" {
		if json.Unmarshal(raw, &ids) != nil {
			return nil
		}
	} else {
		var id string
		if json.Unmarshal(raw, &id) != nil {
			return nil
		}
		ids = []string{id}
	}
	references := make([]WorkReference, 0, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			continue
		}
		reference := WorkReference{Kind: attachmentKindOf(parameter), Purpose: string(parameter.Semantic)}
		if attachment, exists := attachments[id]; exists {
			reference.Handle = attachment.Handle
			if attachment.Kind != "" {
				reference.Kind = attachment.Kind
			}
		}
		references = append(references, reference)
	}
	return references
}

func workTaskReferences(
	raw json.RawMessage,
	parameter generation.Parameter,
	stepHandles map[string]string,
	attachmentsByTask map[string]Attachment,
) []WorkReference {
	var ids []string
	if parameter.Type == "task-array" {
		if json.Unmarshal(raw, &ids) != nil {
			return nil
		}
	} else {
		var id string
		if json.Unmarshal(raw, &id) != nil {
			return nil
		}
		ids = []string{id}
	}
	references := make([]WorkReference, 0, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			continue
		}
		reference := WorkReference{Kind: "model", Purpose: string(parameter.Semantic)}
		if handle, exists := stepHandles[id]; exists {
			reference.Handle = handle
		} else if attachment, exists := attachmentsByTask[id]; exists {
			reference.Handle = attachment.Handle
		}
		references = append(references, reference)
	}
	return references
}

func attachmentKindOf(parameter generation.Parameter) string {
	for _, mediaType := range parameter.MediaTypes {
		if strings.HasPrefix(strings.ToLower(mediaType), "model/") {
			return "model"
		}
	}
	return "image"
}

// workResults projects the Task's assets to the handles the session already
// gave them. A result without a handle keeps its role and is not invented.
func workResults(task generation.Task, attachments map[string][]Attachment) []WorkReference {
	results := make([]WorkReference, 0, len(task.Assets))
	for _, asset := range task.Assets {
		reference := WorkReference{Role: asset.Role}
		if asset.Variant != "" {
			reference.Role += "/" + asset.Variant
		}
		if strings.HasPrefix(strings.ToLower(asset.MediaType), "model/") {
			reference.Kind = "model"
		} else if strings.HasPrefix(strings.ToLower(asset.MediaType), "image/") {
			reference.Kind = "image"
		}
		for _, attachment := range attachments[string(task.ID)] {
			if attachment.Handle == "" {
				continue
			}
			if attachment.AssetID == "" || attachment.AssetID == string(asset.ID) {
				reference.Handle = attachment.Handle
				break
			}
		}
		results = append(results, reference)
	}
	return results
}

// workSelfChecks attributes the persisted read-only tool summaries to the
// steps they inspected. inspect_model contributes its usage-check failures and
// fix hints; inspect_image contributes pass/fail counts; render and compare
// contribute their failed views.
func workSelfChecks(
	history []Message,
	assistantRole string,
	attachmentsByAsset map[string]Attachment,
	attachmentsByTask map[string]Attachment,
	stepHandles map[string]string,
) map[string][]WorkSelfCheck {
	byTask := map[string]map[string]WorkSelfCheck{}
	order := map[string][]string{}
	update := func(taskID string, check WorkSelfCheck) {
		if taskID == "" {
			return
		}
		checks, exists := byTask[taskID]
		if !exists {
			checks = map[string]WorkSelfCheck{}
			byTask[taskID] = checks
		}
		if _, seen := checks[check.Tool]; !seen {
			order[taskID] = append(order[taskID], check.Tool)
		}
		checks[check.Tool] = check
	}
	for _, message := range history {
		if message.Role != assistantRole {
			continue
		}
		for _, call := range message.ToolCalls {
			if len(call.Summary) == 0 {
				continue
			}
			var summary struct {
				Tool    string   `json:"tool"`
				Model   string   `json:"model"`
				Models  []string `json:"models"`
				TaskID  string   `json:"task_id"`
				TaskIDs []string `json:"task_ids"`
				Handle  string   `json:"handle"`
				Checks  struct {
					Passed int `json:"passed"`
					Failed int `json:"failed"`
				} `json:"checks"`
				Views       []string `json:"views"`
				FailedViews []string `json:"failed_views"`
				Usage       *struct {
					Checks []struct {
						Check   string         `json:"check"`
						Status  string         `json:"status"`
						Actual  any            `json:"actual"`
						Budget  map[string]any `json:"budget"`
						FixHint string         `json:"fix_hint"`
					} `json:"checks"`
				} `json:"usage"`
			}
			if json.Unmarshal(call.Summary, &summary) != nil {
				continue
			}
			switch summary.Tool {
			case "inspect":
				if summary.Usage == nil {
					continue
				}
				check := WorkSelfCheck{Tool: "inspect_model"}
				for _, item := range summary.Usage.Checks {
					if item.Status == "fail" {
						check.Failed++
						check.Leftovers = append(check.Leftovers, WorkLeftover{
							Check:   item.Check,
							Actual:  workAnyText(item.Actual),
							Budget:  workAnyText(item.Budget),
							FixHint: item.FixHint,
						})
					} else {
						check.Passed++
					}
				}
				taskID := summary.TaskID
				if taskID == "" {
					taskID = workTaskOfToken(summary.Model, attachmentsByTask, stepHandles)
				}
				update(taskID, check)
			case "inspect_image":
				taskID := workTaskOfHandle(summary.Handle, attachmentsByAsset, attachmentsByTask)
				update(taskID, WorkSelfCheck{
					Tool: "inspect_image", Passed: summary.Checks.Passed, Failed: summary.Checks.Failed,
				})
			case "render":
				taskID := summary.TaskID
				if taskID == "" {
					taskID = workTaskOfToken(summary.Model, attachmentsByTask, stepHandles)
				}
				update(taskID, WorkSelfCheck{
					Tool: "render_model_views", Passed: len(summary.Views),
					Failed: len(summary.FailedViews), FailedViews: summary.FailedViews,
				})
			case "compare":
				taskIDs := summary.TaskIDs
				if len(taskIDs) == 0 {
					for _, model := range summary.Models {
						taskIDs = append(taskIDs, workTaskOfToken(model, attachmentsByTask, stepHandles))
					}
				}
				for _, taskID := range taskIDs {
					update(taskID, WorkSelfCheck{
						Tool: "compare_models", Passed: len(summary.Views),
						Failed: len(summary.FailedViews), FailedViews: summary.FailedViews,
					})
				}
			}
		}
	}
	result := make(map[string][]WorkSelfCheck, len(byTask))
	for taskID, checks := range byTask {
		list := make([]WorkSelfCheck, 0, len(order[taskID]))
		for _, tool := range order[taskID] {
			list = append(list, checks[tool])
		}
		result[taskID] = list
	}
	return result
}

// workAnyText renders a JSON value for a self-check leftover. It is bounded so
// one measured value cannot flood the line.
func workAnyText(value any) string {
	if value == nil {
		return ""
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return truncateRunes(string(encoded), 200)
}

// workTaskOfToken resolves a self-check target named by a model token: a step
// handle, a Task ID or an attachment handle.
func workTaskOfToken(token string, attachmentsByTask map[string]Attachment, stepHandles map[string]string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	if _, exists := stepHandles[token]; exists {
		return token
	}
	for taskID, handle := range stepHandles {
		if handle == token {
			return taskID
		}
	}
	if attachment, exists := attachmentsByTask[token]; exists {
		return attachment.TaskID
	}
	// A task ID is the remaining accepted spelling.
	if _, exists := stepHandles[token]; exists {
		return token
	}
	return token
}

// workTaskOfHandle resolves an attachment handle to the Task that produced it.
func workTaskOfHandle(handle string, attachmentsByAsset map[string]Attachment, attachmentsByTask map[string]Attachment) string {
	handle = strings.TrimSpace(handle)
	if handle == "" {
		return ""
	}
	for _, attachment := range attachmentsByTask {
		if attachment.Handle == handle {
			return attachment.TaskID
		}
	}
	for _, attachment := range attachmentsByAsset {
		if attachment.Handle == handle {
			return attachment.TaskID
		}
	}
	return ""
}

// workAdoptedTask reads the session pick into the Task it belongs to.
func workAdoptedTask(pick Attachment, attachmentsByAsset, attachmentsByTask map[string]Attachment) string {
	if pick.TaskID != "" {
		return pick.TaskID
	}
	if pick.AssetID != "" {
		if attachment, exists := attachmentsByAsset[pick.AssetID]; exists && attachment.TaskID != "" {
			return attachment.TaskID
		}
	}
	return ""
}

func workRejectedDecisions(decisions []WorkDecision) []WorkDecision {
	rejected := make([]WorkDecision, 0, len(decisions))
	for _, decision := range decisions {
		decision.State = strings.TrimSpace(decision.State)
		if decision.State != WorkDecisionRejected {
			continue
		}
		rejected = append(rejected, decision)
	}
	return rejected
}

// workLedgerSummary renders the system-prompt block. mention is the newest
// user message; it only raises the priority of steps it names. previews=false
// is the compaction variant: handles, states, self-checks and decisions stay,
// full prompt previews do not.
func (service *Service) workLedgerSummary(
	ctx context.Context,
	ownerID, sessionID, mention string,
	previews bool,
) string {
	ledger, err := service.WorkLedger(ctx, ownerID, sessionID)
	if err != nil || len(ledger.Steps) == 0 {
		return ""
	}
	policy := service.workLedger
	ordered := workLedgerPriority(ledger, mention)
	var lines []string
	truncated := 0
	used := 0
	for _, step := range ordered {
		if len(lines) >= policy.MaximumSteps {
			truncated++
			continue
		}
		line := service.workStepLine(step, previews)
		if line == "" {
			continue
		}
		if policy.EntryMaximumCharacters > 0 && utf8.RuneCountInString(line) > policy.EntryMaximumCharacters {
			line = truncateRunes(line, policy.EntryMaximumCharacters)
		}
		if policy.SummaryMaximumCharacters > 0 && used+utf8.RuneCountInString(line)+1 > policy.SummaryMaximumCharacters {
			truncated++
			continue
		}
		lines = append(lines, line)
		used += utf8.RuneCountInString(line) + 1
	}
	if len(lines) == 0 {
		return ""
	}
	for index := range lines {
		lines[index] = withoutMarkers(lines[index], policy.OpenMarker, policy.CloseMarker)
	}
	stepsText := strings.Join(lines, "\n")
	if truncated > 0 && policy.TruncatedNote != "" {
		note := withoutMarkers(strings.ReplaceAll(policy.TruncatedNote, "{count}", strconv.Itoa(truncated)),
			policy.OpenMarker, policy.CloseMarker)
		stepsText += "\n" + note
	}
	rejected := withoutMarkers(service.workRejectedBlock(ledger.Rejected),
		policy.OpenMarker, policy.CloseMarker)
	// One pass: substituted text is never scanned again, so a ledger line that
	// spells "{close}" stays literal. The projected text already has the frame
	// markers removed, so it cannot close the frame early either.
	return strings.NewReplacer(
		"{open}", policy.OpenMarker,
		"{steps}", stepsText,
		"{rejected}", rejected,
		"{close}", policy.CloseMarker,
	).Replace(policy.SummaryTemplate)
}

// workLedgerPriority orders steps by the Issue's priority: the session's
// picked object, steps the newest user message names, rejected directions,
// then the most recent. Ties inside a class are newest first.
func workLedgerPriority(ledger WorkLedger, mention string) []WorkStep {
	steps := append([]WorkStep(nil), ledger.Steps...)
	sort.SliceStable(steps, func(left, right int) bool {
		return steps[left].CreatedAt.After(steps[right].CreatedAt)
	})
	mentioned := func(step WorkStep) bool {
		if strings.TrimSpace(mention) == "" {
			return false
		}
		if step.Step != "" && strings.Contains(mention, step.Step) {
			return true
		}
		if step.TaskID != "" && strings.Contains(mention, step.TaskID) {
			return true
		}
		for _, result := range step.Results {
			if result.Handle != "" && strings.Contains(mention, result.Handle) {
				return true
			}
		}
		return false
	}
	ordered := make([]WorkStep, 0, len(steps))
	seen := make(map[string]bool, len(steps))
	appendStep := func(step WorkStep) {
		key := step.TaskID
		if key == "" {
			key = step.Step
		}
		if seen[key] {
			return
		}
		seen[key] = true
		ordered = append(ordered, step)
	}
	for _, step := range steps {
		if step.TaskID != "" && step.TaskID == ledger.CurrentTaskID {
			appendStep(step)
		}
	}
	for _, step := range steps {
		if mentioned(step) {
			appendStep(step)
		}
	}
	for _, step := range steps {
		if step.Decision == WorkDecisionRejected {
			appendStep(step)
		}
	}
	for _, step := range steps {
		appendStep(step)
	}
	return ordered
}

// workStepLine renders one ledger line from the Schema template.
func (service *Service) workStepLine(step WorkStep, previews bool) string {
	policy := service.workLedger
	prompt := ""
	if !previews {
		prompt = "（用 get_production_step 取回）"
	}
	if previews && step.FinalPrompt != "" {
		preview := step.FinalPrompt
		if policy.PromptPreviewCharacters > 0 {
			preview = truncateRunes(preview, policy.PromptPreviewCharacters)
			if utf8.RuneCountInString(step.FinalPrompt) > policy.PromptPreviewCharacters {
				preview += "…"
			}
		}
		prompt = strings.ReplaceAll(policy.PromptLineTemplate, "{preview}", withoutMarkers(preview, policy.OpenMarker, policy.CloseMarker))
	}
	selfCheck := service.workSelfCheckLine(step.SelfChecks)
	decision := step.Decision
	if decision == "" {
		decision = "none"
	}
	if step.DecisionReason != "" {
		decision += ": " + step.DecisionReason
	}
	return strings.NewReplacer(
		"{step}", step.Step,
		"{task_id}", step.TaskID,
		"{capability}", step.Capability,
		"{model}", step.Model,
		"{status}", step.State,
		"{prompt}", prompt,
		"{self_check}", selfCheck,
		"{decision}", decision,
	).Replace(policy.StepLineTemplate)
}

// workSelfCheckLine renders all self-check conclusions of a step.
func (service *Service) workSelfCheckLine(checks []WorkSelfCheck) string {
	policy := service.workLedger
	if len(checks) == 0 {
		return "none"
	}
	lines := make([]string, 0, len(checks))
	for _, check := range checks {
		leftovers := ""
		if len(check.Leftovers) > 0 {
			items := make([]string, 0, len(check.Leftovers))
			for _, leftover := range check.Leftovers {
				item := leftover.Check
				if leftover.FixHint != "" {
					item += "(" + leftover.FixHint + ")"
				}
				items = append(items, item)
			}
			leftovers = strings.ReplaceAll(policy.LeftoverTemplate, "{checks}", strings.Join(items, "、"))
		}
		if len(check.FailedViews) > 0 {
			leftovers += "（失败视角：" + strings.Join(check.FailedViews, "、") + "）"
		}
		line := strings.NewReplacer(
			"{tool}", check.Tool,
			"{passed}", strconv.Itoa(check.Passed),
			"{failed}", strconv.Itoa(check.Failed),
			"{leftovers}", leftovers,
		).Replace(policy.SelfCheckLineTemplate)
		lines = append(lines, line)
	}
	return strings.Join(lines, "；")
}

// workRejectedBlock renders the directions the person rejected.
func (service *Service) workRejectedBlock(decisions []WorkDecision) string {
	policy := service.workLedger.Rejected
	if len(decisions) == 0 {
		return ""
	}
	if policy.MaximumItems > 0 && len(decisions) > policy.MaximumItems {
		decisions = decisions[:policy.MaximumItems]
	}
	lines := make([]string, 0, len(decisions))
	for _, decision := range decisions {
		step := decision.Step
		if step == "" {
			step = decision.TaskID
		}
		lines = append(lines, strings.NewReplacer(
			"{step}", step,
			"{reason}", decision.Reason,
		).Replace(policy.LineTemplate))
	}
	return strings.ReplaceAll(policy.ContextTemplate, "{items}", strings.Join(lines, "\n"))
}

// WorkLedgerSummary renders the Agent-facing ledger block with prompt
// previews for the owner's session. It is exported for the HTTP-free callers
// and tests; the orchestrator uses the same renderer every turn.
func (service *Service) WorkLedgerSummary(ctx context.Context, ownerID, sessionID, mention string) string {
	return service.workLedgerSummary(ctx, ownerID, sessionID, mention, true)
}

// workLedgerPrompt is the per-turn system-prompt block. The newest user
// message only raises the priority of the steps it names; the ledger itself
// comes from stored Tasks and messages.
func (orchestrator *Orchestrator) workLedgerPrompt(
	ctx context.Context,
	ownerID, sessionID string,
	history []Message,
) string {
	if orchestrator.service == nil || sessionID == "" {
		return ""
	}
	mention := ""
	for index := len(history) - 1; index >= 0; index-- {
		message := history[index]
		if message.Role == orchestrator.schema.Roles.User && orchestrator.contextMessage(message) {
			mention = message.Content
			break
		}
	}
	return orchestrator.service.workLedgerSummary(ctx, ownerID, sessionID, mention, true)
}

// workAnchorsPrompt is the per-turn style anchor block.
func (orchestrator *Orchestrator) workAnchorsPrompt(ctx context.Context, ownerID, sessionID string) string {
	if orchestrator.service == nil || sessionID == "" {
		return ""
	}
	return orchestrator.service.workAnchorsSummary(ctx, ownerID, sessionID)
}

// ReadProductionStep backs get_production_step: it resolves one step by handle
// or Task ID for the command's own owner and session and returns the complete
// record plus one-line summary. It is read-only and never logs the record.
func (service *Service) ReadProductionStep(ctx context.Context, command ToolCommand) (ToolResult, error) {
	if !service.workLedgerEnabled() {
		return ToolResult{Error: service.workLedger.NotReadableTemplate, NoSubmission: true}, nil
	}
	var arguments struct {
		Step string `json:"step"`
	}
	if json.Unmarshal(command.Call.Arguments, &arguments) != nil {
		return ToolResult{Error: service.workLedger.UnknownStepTemplate, NoSubmission: true}, nil
	}
	token := strings.TrimSpace(arguments.Step)
	if token == "" {
		return ToolResult{Error: strings.ReplaceAll(service.workLedger.UnknownStepTemplate, "{step}", token), NoSubmission: true}, nil
	}
	ledger, err := service.WorkLedger(ctx, command.OwnerID, command.SessionID)
	if err != nil {
		return ToolResult{Error: service.workLedger.NotReadableTemplate, NoSubmission: true}, nil
	}
	var found *WorkStep
	for index := range ledger.Steps {
		step := &ledger.Steps[index]
		if step.Step == token || step.TaskID == token {
			found = step
			break
		}
	}
	if found == nil {
		return ToolResult{
			Error:        strings.ReplaceAll(service.workLedger.UnknownStepTemplate, "{step}", truncateRunes(token, 200)),
			NoSubmission: true,
		}, nil
	}
	// The list projection carries only the prompt; the full record (every
	// declared parameter, source and input reference) comes from the one
	// owner-scoped Task read.
	if service.workLedgerDetail != nil {
		detailed, err := service.readWorkLedger(ctx, command.OwnerID, command.SessionID, found.TaskID)
		if err != nil {
			return ToolResult{Error: service.workLedger.NotReadableTemplate, NoSubmission: true}, nil
		}
		for index := range detailed.Steps {
			if detailed.Steps[index].TaskID == found.TaskID {
				found = &detailed.Steps[index]
				ledger = detailed
				break
			}
		}
	}
	content, err := json.Marshal(map[string]any{
		"ok":          true,
		"step":        found,
		"total_steps": ledger.TotalSteps,
	})
	if err != nil {
		return ToolResult{Error: service.workLedger.NotReadableTemplate, NoSubmission: true}, nil
	}
	summary, _ := json.Marshal(map[string]any{
		"tool": "get_production_step", "step": found.Step, "task_id": found.TaskID,
		"state": found.State, "decision": found.Decision, "self_check": found.SelfChecks,
	})
	return ToolResult{AgentContent: string(content), Summary: summary, NoSubmission: true}, nil
}
