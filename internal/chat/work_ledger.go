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

// SessionTaskSource reads one page of the session's public Tasks, newest first,
// and reports the full count. The application supplies it with
// generation.Module.List(OwnTasks(owner), ListQuery{SessionID: session,
// IncludeBranchRefs: true, Offset: offset, Limit: limit}); a branch session
// therefore reads its ancestors' Tasks too, while owner scope never widens.
//
// The summary reads only the newest chat.work_ledger.maximum_steps Tasks, but
// the same source pages the complete set for a step the summary left out
// (get_production_step, and an old picked object or rejected direction that
// must still enter the summary).
//
// The list projection is reduced on purpose: it carries the semantic prompt and
// the result assets, not the full frozen Offering or input. The ledger summary
// works with it; get_production_step reads the one requested step in full
// through SessionTaskDetail.
type SessionTaskSource func(ctx context.Context, ownerID, sessionID string, offset, limit int) (generation.TaskPage, error)

// SessionTaskDetail reads one public Task of the owner in full (the frozen
// Offering, every declared parameter and the whole input). The caller only
// names a Task the session list returned, so session scope is already
// established; this read adds no grant. Nil leaves the tool without full
// parameters.
type SessionTaskDetail func(ctx context.Context, ownerID, sessionID, taskID string) (generation.Task, error)

// WorkParameterState is the last-writer state of one persisted Offering
// parameter (the existing workspace/Agent parameter store, not a new store).
// Value is compared with the Task's frozen input before the source is claimed:
// a state that no longer matches the step cannot name who wrote that step.
type WorkParameterState struct {
	Value  json.RawMessage
	Source string
}

// WorkParameterSource reads the persisted last-writer state of every parameter
// of one Offering for the session. It is supplied by the application from the
// existing agent parameter store; errors leave the source unknown and are
// reported as an omission.
type WorkParameterSource func(ctx context.Context, ownerID, sessionID, offeringID string) (map[string]WorkParameterState, error)

// WorkDecision states.
const (
	WorkDecisionAdopted  = "adopted"
	WorkDecisionRejected = "rejected"
)

// WorkDecision is one persisted decision about a production step. The ledger
// derives "adopted" from the session pick; "rejected" comes from the injected
// reader (see SessionDecisionReader) and, once #241 lands, from explicit
// rejection records. A step without any source entry has no decision.
type WorkDecision struct {
	Step   string `json:"step,omitempty"`
	TaskID string `json:"task_id,omitempty"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// WorkDecisionReader is the source of rejected directions. The application
// supplies SessionDecisionReader, which projects the results a later selection
// superseded from the existing turn records; #241 owns explicit rejection
// writes and can extend or replace it. Without one the ledger's rejected list
// stays empty.
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
// and attributed to the step it inspected. Passed counts only checks the tool
// judged as pass; Unknown counts checks the purpose did not ask for or the
// file could not judge. Unknown is never folded into Passed: an unjudged check
// must not read as a passed one.
type WorkSelfCheck struct {
	Tool        string         `json:"tool"`
	Passed      int            `json:"passed"`
	Unknown     int            `json:"unknown"`
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

// WorkOmission is one fact the projection could not read or attribute. Kind is
// a stable machine-readable tag; Subject names the non-sensitive subject (a
// tool name, an Offering ID or a bounded token); Detail is a bounded
// operator-facing explanation. Prompt text and user content never appear here.
type WorkOmission struct {
	Kind    string `json:"kind"`
	Subject string `json:"subject,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// WorkLedger is the projection of one session.
type WorkLedger struct {
	Steps []WorkStep `json:"steps"`
	// Rejected is the "do not do again" list, newest first. It is omitted when
	// the existing records name no rejected direction, never rendered as an
	// empty list.
	Rejected []WorkDecision `json:"rejected,omitempty"`
	// Omissions names every fact this projection could not read or attribute.
	// The summary renders a Schema note when the list is non-empty, so a failed
	// decision read is visible instead of silently empty.
	Omissions []WorkOmission `json:"omissions,omitempty"`
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

// SetParameterSource supplies the existing parameter store's last-writer state,
// so a parameter the person pinned is reported as "user" when the frozen value
// still matches it.
func (service *Service) SetParameterSource(source WorkParameterSource) {
	service.workParameterSource = source
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
	page, err := service.workLedgerTasks(ctx, ownerID, sessionID, 0, service.workLedger.MaximumSteps)
	if err != nil {
		return WorkLedger{}, err
	}
	facts, err := service.loadWorkLedgerFacts(ctx, ownerID, sessionID)
	if err != nil {
		return WorkLedger{}, err
	}
	steps, omissions := service.workLedgerSteps(ctx, ownerID, sessionID, page, facts)
	facts.omissions = appendWorkOmissions(facts.omissions, omissions...)
	parameterStates, stateOmissions := service.workParameterStates(ctx, ownerID, sessionID, steps)
	facts.omissions = appendWorkOmissions(facts.omissions, stateOmissions...)
	return service.projectWorkLedger(steps, max(page.Total, len(steps)), facts, parameterStates), nil
}

// workLedgerFacts are the stored facts every projection shares: the history,
// the style lock, the rejected directions, the pick and the read omissions.
type workLedgerFacts struct {
	history   []Message
	styleLock StyleLock
	decisions []WorkDecision
	pick      Attachment
	omissions []WorkOmission
}

// loadWorkLedgerFacts reads the session's stored facts once. A failed decision
// read becomes a diagnosable omission; it never silently reads as "no
// rejected directions".
func (service *Service) loadWorkLedgerFacts(ctx context.Context, ownerID, sessionID string) (workLedgerFacts, error) {
	history, err := service.repository.ListMessages(ctx, ownerID, sessionID)
	if err != nil {
		return workLedgerFacts{}, err
	}
	styleLock, _, err := service.repository.StyleLock(ctx, ownerID, sessionID)
	if err != nil {
		return workLedgerFacts{}, err
	}
	pick, _, err := service.SessionPick(ctx, ownerID, sessionID)
	if err != nil {
		return workLedgerFacts{}, err
	}
	facts := workLedgerFacts{history: history, styleLock: styleLock, pick: pick}
	if service.workDecisions != nil {
		decisions, readErr := service.workDecisions.WorkDecisions(ctx, ownerID, sessionID)
		if readErr != nil {
			facts.omissions = appendWorkOmissions(facts.omissions, WorkOmission{
				Kind:   "decisions_unreadable",
				Detail: truncateRunes(readErr.Error(), 200),
			})
		} else {
			facts.decisions = decisions
		}
	}
	return facts, nil
}

// workNumberedStep is one Task with its chronological step number, the number
// its @stepN handle uses.
type workNumberedStep struct {
	task generation.Task
	step int
}

// workExtraCandidate names a step outside the summary page that must still be
// visible: the picked object or a rejected direction.
type workExtraCandidate struct {
	taskID string
	step   int
	kind   string
}

// workLedgerSteps numbers the newest page and adds the steps outside it that
// the priority rules must still see. The summary itself stays bounded: these
// extras only make the picked and rejected steps resolvable and renderable.
func (service *Service) workLedgerSteps(
	ctx context.Context,
	ownerID, sessionID string,
	page generation.TaskPage,
	facts workLedgerFacts,
) ([]workNumberedStep, []WorkOmission) {
	tasks := append([]generation.Task(nil), page.Tasks...)
	sort.SliceStable(tasks, func(left, right int) bool {
		if tasks[left].CreatedAt.Equal(tasks[right].CreatedAt) {
			return tasks[left].ID < tasks[right].ID
		}
		return tasks[left].CreatedAt.Before(tasks[right].CreatedAt)
	})
	offset := max(0, page.Total-len(tasks))
	steps := make([]workNumberedStep, 0, len(tasks))
	inPage := make(map[string]bool, len(tasks))
	for index := range tasks {
		steps = append(steps, workNumberedStep{task: tasks[index], step: offset + index + 1})
		inPage[string(tasks[index].ID)] = true
	}
	rejected := workRejectedDecisions(facts.decisions)
	var candidates []workExtraCandidate
	adopted := workAdoptedTaskFromHistory(facts.pick, facts.history)
	if adopted != "" && !inPage[adopted] {
		candidates = append(candidates, workExtraCandidate{taskID: adopted, kind: "current_step_unreadable"})
	}
	limit := service.workLedger.Rejected.MaximumItems
	seenRejected := map[string]bool{}
	for _, decision := range rejected {
		if limit > 0 && len(seenRejected) >= limit {
			break
		}
		if decision.TaskID != "" {
			if inPage[decision.TaskID] || seenRejected[decision.TaskID] {
				continue
			}
			seenRejected[decision.TaskID] = true
			candidates = append(candidates, workExtraCandidate{taskID: decision.TaskID, kind: "rejected_step_unreadable"})
			continue
		}
		if number, ok := service.stepNumber(decision.Step); ok && number >= 1 && number <= page.Total {
			candidates = append(candidates, workExtraCandidate{step: number, kind: "rejected_step_unreadable"})
		}
	}
	seen := map[string]bool{}
	var omissions []WorkOmission
	for _, candidate := range candidates {
		var (
			task   generation.Task
			number int
			found  bool
			err    error
		)
		if candidate.taskID != "" {
			if seen[candidate.taskID] {
				continue
			}
			seen[candidate.taskID] = true
			task, number, found, err = service.findSessionTask(ctx, ownerID, sessionID, candidate.taskID, page)
		} else {
			task, number, found, err = service.findSessionStep(ctx, ownerID, sessionID, candidate.step, page)
			if found {
				seen[string(task.ID)] = true
			}
		}
		switch {
		case err != nil:
			omissions = appendWorkOmissions(omissions, WorkOmission{
				Kind:   candidate.kind,
				Detail: truncateRunes(err.Error(), 200),
			})
		case !found:
			omissions = appendWorkOmissions(omissions, WorkOmission{
				Kind:   candidate.kind,
				Detail: "the step is no longer in the session",
			})
		default:
			steps = append(steps, workNumberedStep{task: task, step: number})
		}
	}
	sort.SliceStable(steps, func(left, right int) bool { return steps[left].step < steps[right].step })
	return steps, omissions
}

// workLedgerLookupPageSize is one page of the complete-set step lookup. The
// generation list read caps a page at 100.
const workLedgerLookupPageSize = 100

// findSessionTask pages the session's complete Task set (newest first) until
// taskID is found, returning the Task and its chronological position. page is
// the summary page already read, so a step it contains needs no extra read.
func (service *Service) findSessionTask(
	ctx context.Context,
	ownerID, sessionID, taskID string,
	page generation.TaskPage,
) (generation.Task, int, bool, error) {
	for index := range page.Tasks {
		if string(page.Tasks[index].ID) == taskID {
			return page.Tasks[index], page.Total - index, true, nil
		}
	}
	total := page.Total
	for offset := 0; ; {
		next, err := service.workLedgerTasks(ctx, ownerID, sessionID, offset, workLedgerLookupPageSize)
		if err != nil {
			return generation.Task{}, 0, false, err
		}
		if offset == 0 {
			total = next.Total
		}
		for index := range next.Tasks {
			if string(next.Tasks[index].ID) == taskID {
				return next.Tasks[index], total - (offset + index), true, nil
			}
		}
		offset += len(next.Tasks)
		if len(next.Tasks) == 0 || offset >= total {
			return generation.Task{}, 0, false, nil
		}
	}
}

// findSessionStep reads the Task at one chronological step number from the
// complete session set. The summary page answers it without an extra read when
// the number falls inside that page.
func (service *Service) findSessionStep(
	ctx context.Context,
	ownerID, sessionID string,
	number int,
	page generation.TaskPage,
) (generation.Task, int, bool, error) {
	if number < 1 || number > page.Total {
		return generation.Task{}, 0, false, nil
	}
	index := page.Total - number
	if index < len(page.Tasks) {
		return page.Tasks[index], number, true, nil
	}
	next, err := service.workLedgerTasks(ctx, ownerID, sessionID, index, 1)
	if err != nil {
		return generation.Task{}, 0, false, err
	}
	if len(next.Tasks) == 0 {
		return generation.Task{}, 0, false, nil
	}
	return next.Tasks[0], number, true, nil
}

// stepPrefix is the Schema's step handle prefix.
func (service *Service) stepPrefix() string {
	if prefix := strings.TrimSpace(service.workLedger.StepPrefix); prefix != "" {
		return prefix
	}
	return "@step"
}

// stepNumber reads the canonical @stepN handle. Only the exact spelling the
// renderer writes is accepted; a display name or the selection alias is not a
// handle.
func (service *Service) stepNumber(token string) (int, bool) {
	prefix := service.stepPrefix()
	if !strings.HasPrefix(token, prefix) {
		return 0, false
	}
	digits := strings.TrimPrefix(token, prefix)
	number, err := strconv.Atoi(digits)
	if err != nil || number < 1 || strconv.Itoa(number) != digits {
		return 0, false
	}
	return number, true
}

// workParameterStates reads the persisted last-writer state of every Offering
// involved. A store it cannot read leaves the sources unknown and is reported
// as an omission.
func (service *Service) workParameterStates(
	ctx context.Context,
	ownerID, sessionID string,
	steps []workNumberedStep,
) (map[string]map[string]WorkParameterState, []WorkOmission) {
	states := map[string]map[string]WorkParameterState{}
	if service.workParameterSource == nil {
		return states, nil
	}
	seen := map[string]bool{}
	var omissions []WorkOmission
	for index := range steps {
		offeringID := string(steps[index].task.Offering.ID)
		if offeringID == "" || seen[offeringID] {
			continue
		}
		seen[offeringID] = true
		state, err := service.workParameterSource(ctx, ownerID, sessionID, offeringID)
		if err != nil {
			omissions = appendWorkOmissions(omissions, WorkOmission{
				Kind:    "parameter_source_unreadable",
				Subject: offeringID,
				Detail:  truncateRunes(err.Error(), 200),
			})
			continue
		}
		states[offeringID] = state
	}
	return states, omissions
}

// projectWorkLedger builds the read model from already-read facts. It performs
// no IO, so unit tests can exercise ordering and attribution directly.
func (service *Service) projectWorkLedger(
	steps []workNumberedStep,
	totalSteps int,
	facts workLedgerFacts,
	parameterStates map[string]map[string]WorkParameterState,
) WorkLedger {
	prefix := service.stepPrefix()
	ordered := append([]workNumberedStep(nil), steps...)
	sort.SliceStable(ordered, func(left, right int) bool {
		if ordered[left].step == ordered[right].step {
			return ordered[left].task.ID < ordered[right].task.ID
		}
		return ordered[left].step < ordered[right].step
	})
	unique := make([]workNumberedStep, 0, len(ordered))
	stepHandles := make(map[string]string, len(ordered))
	seen := make(map[string]bool, len(ordered))
	for _, item := range ordered {
		taskID := string(item.task.ID)
		if taskID == "" || seen[taskID] {
			continue
		}
		seen[taskID] = true
		unique = append(unique, item)
		stepHandles[taskID] = prefix + strconv.Itoa(item.step)
	}

	calls := workCallsByTask(facts.history, service.assistantRole)
	attachmentsByAsset, attachmentsByTask, resultAttachments := workHistoryAttachments(facts.history)
	selfChecks, selfOmissions := workSelfChecks(
		facts.history, service.assistantRole, attachmentsByAsset, attachmentsByTask, stepHandles)
	adopted := workAdoptedTask(facts.pick, attachmentsByAsset, attachmentsByTask)
	rejected := workRejectedDecisions(facts.decisions)
	for index := range rejected {
		if handle, exists := stepHandles[rejected[index].TaskID]; exists && rejected[index].Step == "" {
			rejected[index].Step = handle
		}
	}
	rejectedByTask := map[string]WorkDecision{}
	for _, decision := range rejected {
		if decision.TaskID != "" {
			rejectedByTask[decision.TaskID] = decision
		}
	}
	omissions := appendWorkOmissions(nil, facts.omissions...)
	omissions = appendWorkOmissions(omissions, selfOmissions...)

	stepOffset := 0
	if len(unique) > 0 {
		stepOffset = max(0, unique[0].step-1)
	}
	ledger := WorkLedger{
		Steps:         make([]WorkStep, 0, len(unique)),
		Rejected:      rejectedOrNil(rejected),
		Omissions:     omissionsOrNil(omissions),
		CurrentTaskID: adopted,
		TotalSteps:    max(totalSteps, len(unique)),
		StepOffset:    stepOffset,
	}
	styleText := ""
	if facts.styleLock.Enabled {
		styleText = facts.styleLock.Text
	}
	for index := range unique {
		task := unique[index].task
		taskID := string(task.ID)
		stepHandle := stepHandles[taskID]
		call := calls[taskID]
		step := WorkStep{
			Step:             stepHandle,
			TaskID:           taskID,
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
		step.Parameters, step.Inputs = workParameters(task, call, styleText, stepHandles,
			attachmentsByAsset, attachmentsByTask, parameterStates[string(task.Offering.ID)])
		step.Results = workResults(task, resultAttachments)
		step.SelfChecks = selfChecks[taskID]
		if adopted != "" && adopted == taskID {
			step.Decision = WorkDecisionAdopted
		}
		if decision, exists := rejectedByTask[taskID]; exists {
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
// call arguments. A single prompt argument is returned verbatim; a tool that
// carries one prompt per view (front_prompt, left_prompt, ...) has its named
// values listed instead, so the raw prompt is never silently empty. An argument
// object that is not an object yields no prompt.
func workPromptFromArguments(arguments []byte, name string) string {
	if len(arguments) == 0 {
		return ""
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(arguments, &values) != nil {
		return ""
	}
	if raw, present := values[name]; present {
		var text string
		if json.Unmarshal(raw, &text) == nil && strings.TrimSpace(text) != "" {
			return text
		}
	}
	names := make([]string, 0, 4)
	for key := range values {
		if key == "negative_prompt" || key == name || !strings.HasSuffix(key, "_prompt") {
			continue
		}
		var text string
		if json.Unmarshal(values[key], &text) != nil || strings.TrimSpace(text) == "" {
			continue
		}
		names = append(names, key)
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, key := range names {
		var text string
		_ = json.Unmarshal(values[key], &text)
		parts = append(parts, key+": "+text)
	}
	return strings.Join(parts, "; ")
}

// workPromptSlot reports whether a declared parameter carries prompt text. A
// tool may declare several prompts (front_prompt, left_prompt, ...) without the
// single semantic=prompt marker; the name suffix makes them prompt slots, not
// ordinary parameters.
func workPromptSlot(parameter generation.Parameter) bool {
	if parameter.Semantic == generation.SemanticNegativePrompt || parameter.Name == "negative_prompt" {
		return false
	}
	if parameter.Semantic == generation.SemanticPrompt {
		return true
	}
	return parameter.Name != "prompt" && strings.HasSuffix(strings.ToLower(parameter.Name), "_prompt")
}

// workFrozenPrompts reads the final prompt and negative prompt from the Task's
// frozen input, by the Offering's declared semantics. This is what was really
// sent upstream (style lock and template rewrites included); the Agent's
// original wording lives only in the historical call arguments. A tool with
// several prompt slots lists each value under its parameter name; a single
// prompt is returned verbatim.
func workFrozenPrompts(task generation.Task) (prompt, negative string) {
	var input map[string]json.RawMessage
	if len(task.Input) == 0 || json.Unmarshal(task.Input, &input) != nil {
		return "", ""
	}
	type promptValue struct {
		name string
		text string
	}
	var prompts []promptValue
	for _, parameter := range task.Offering.Parameters {
		raw, present := input[parameter.Name]
		if !present || len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var text string
		if json.Unmarshal(raw, &text) != nil {
			continue
		}
		switch {
		case parameter.Semantic == generation.SemanticNegativePrompt:
			if negative == "" {
				negative = text
			}
		case workPromptSlot(parameter):
			if strings.TrimSpace(text) == "" {
				continue
			}
			prompts = append(prompts, promptValue{name: parameter.Name, text: text})
		}
	}
	switch {
	case len(prompts) == 0:
		return "", negative
	case len(prompts) == 1 && prompts[0].name == "prompt":
		return prompts[0].text, negative
	}
	parts := make([]string, 0, len(prompts))
	for _, item := range prompts {
		parts = append(parts, item.name+": "+item.text)
	}
	return strings.Join(parts, "; "), negative
}

// workParameters projects every declared, non-secret parameter with its
// effective value and read-time source, and every reference parameter as a
// handle plus its declared purpose. Mask, source-URL, source-asset and hidden
// parameters never leave their raw values. Prompt slots are projected as
// prompts, not parameters.
func workParameters(
	task generation.Task,
	call *ToolCall,
	styleText string,
	stepHandles map[string]string,
	attachmentsByAsset map[string]Attachment,
	attachmentsByTask map[string]Attachment,
	states map[string]WorkParameterState,
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
		if workPromptSlot(parameter) {
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
			Source: workParameterSource(parameter, raw, arguments, styleText, states),
		})
	}
	return parameters, inputs
}

// workParameterSource reports where an effective value came from, in the
// declared order: a persisted user pin that still matches the frozen value,
// the Agent's call, the Offering default, the style lock text, otherwise
// unknown. It never guesses a person wrote a value.
func workParameterSource(
	parameter generation.Parameter,
	value json.RawMessage,
	arguments map[string]json.RawMessage,
	styleText string,
	states map[string]WorkParameterState,
) string {
	if state, exists := states[parameter.Name]; exists && state.Source == "user" &&
		len(state.Value) > 0 && workSameJSON(value, state.Value) {
		return "user"
	}
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
// steps they inspected. inspect_model contributes its pass, unknown and fail
// counts plus the failed checks' fix hints; inspect_image contributes
// pass/fail counts; render and compare contribute their failed views. A summary
// whose model token names no step handle, Task ID or attachment handle is not
// attached to an invented Task: it is recorded as a diagnosable omission.
func workSelfChecks(
	history []Message,
	assistantRole string,
	attachmentsByAsset map[string]Attachment,
	attachmentsByTask map[string]Attachment,
	stepHandles map[string]string,
) (map[string][]WorkSelfCheck, []WorkOmission) {
	byTask := map[string]map[string]WorkSelfCheck{}
	order := map[string][]string{}
	var omissions []WorkOmission
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
	resolve := func(tool, token string) string {
		token = strings.TrimSpace(token)
		if token == "" {
			// A summary that names no target cannot be attributed, but it also
			// makes no false claim about a step; only an unrecognised token is
			// a diagnostic.
			return ""
		}
		taskID, ok := workTaskOfToken(token, attachmentsByTask, stepHandles)
		if ok {
			return taskID
		}
		omissions = appendWorkOmissions(omissions, WorkOmission{
			Kind:    "self_check_unattributed",
			Subject: truncateRunes(tool+": "+token, 120),
			Detail:  "the token names no step handle, Task ID or attachment handle of this projection",
		})
		return ""
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
					switch item.Status {
					case "pass":
						check.Passed++
					case "fail":
						check.Failed++
						check.Leftovers = append(check.Leftovers, WorkLeftover{
							Check:   item.Check,
							Actual:  workAnyText(item.Actual),
							Budget:  workAnyText(item.Budget),
							FixHint: item.FixHint,
						})
					default:
						// unknown means the purpose did not ask for the
						// property or the file could not be judged; it is a
						// fact, not a pass.
						check.Unknown++
					}
				}
				taskID := summary.TaskID
				if taskID == "" {
					taskID = resolve("inspect_model", summary.Model)
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
					taskID = resolve("render_model_views", summary.Model)
				}
				update(taskID, WorkSelfCheck{
					Tool: "render_model_views", Passed: len(summary.Views),
					Failed: len(summary.FailedViews), FailedViews: summary.FailedViews,
				})
			case "compare":
				taskIDs := append([]string(nil), summary.TaskIDs...)
				if len(taskIDs) == 0 {
					for _, model := range summary.Models {
						if taskID := resolve("compare_models", model); taskID != "" {
							taskIDs = append(taskIDs, taskID)
						}
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
	return result, omissions
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
// handle, a Task ID or an attachment handle. Any other token (a display name,
// @selected, an old free-form value) resolves to nothing: it must not create a
// phantom step. The caller records the unresolved token as an omission.
func workTaskOfToken(token string, attachmentsByTask map[string]Attachment, stepHandles map[string]string) (string, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", false
	}
	if _, exists := stepHandles[token]; exists {
		return token, true
	}
	for taskID, handle := range stepHandles {
		if handle == token {
			return taskID, true
		}
	}
	if attachment, exists := attachmentsByTask[token]; exists && attachment.TaskID != "" {
		return attachment.TaskID, true
	}
	for _, attachment := range attachmentsByTask {
		if attachment.Handle == token && attachment.TaskID != "" {
			return attachment.TaskID, true
		}
	}
	return "", false
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

// workHistoryAttachments indexes the session's attachments by asset, by Task
// and by result Task. Only attachments the session already gave a handle to are
// indexed: an internal ID never becomes a handle here.
func workHistoryAttachments(history []Message) (map[string]Attachment, map[string]Attachment, map[string][]Attachment) {
	byAsset := map[string]Attachment{}
	byTask := map[string]Attachment{}
	results := map[string][]Attachment{}
	for index := range history {
		for _, attachment := range history[index].Attachments {
			if attachment.Handle == "" {
				continue
			}
			if attachment.AssetID != "" {
				byAsset[attachment.AssetID] = attachment
			}
			if attachment.TaskID != "" {
				byTask[attachment.TaskID] = attachment
				results[attachment.TaskID] = append(results[attachment.TaskID], attachment)
			}
		}
	}
	return byAsset, byTask, results
}

// workAdoptedTaskFromHistory resolves the picked attachment to its Task with
// the handles the history already carries.
func workAdoptedTaskFromHistory(pick Attachment, history []Message) string {
	byAsset, byTask, _ := workHistoryAttachments(history)
	return workAdoptedTask(pick, byAsset, byTask)
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

// appendWorkOmissions adds omissions without repeating the same fact. Two
// reads of the same unreadable store, or two self-checks naming the same
// unknown token, produce one diagnostic.
func appendWorkOmissions(list []WorkOmission, additions ...WorkOmission) []WorkOmission {
	for _, addition := range additions {
		duplicate := false
		for _, existing := range list {
			if existing.Kind == addition.Kind && existing.Subject == addition.Subject {
				duplicate = true
				break
			}
		}
		if !duplicate {
			list = append(list, addition)
		}
	}
	return list
}

// rejectedOrNil keeps an empty rejected list out of the JSON read model.
func rejectedOrNil(rejected []WorkDecision) []WorkDecision {
	if len(rejected) == 0 {
		return nil
	}
	return rejected
}

// omissionsOrNil keeps an empty omission list out of the JSON read model.
func omissionsOrNil(omissions []WorkOmission) []WorkOmission {
	if len(omissions) == 0 {
		return nil
	}
	return omissions
}

func workRejectedDecisions(decisions []WorkDecision) []WorkDecision {
	if len(decisions) == 0 {
		return nil
	}
	rejected := make([]WorkDecision, 0, len(decisions))
	for _, decision := range decisions {
		decision.State = strings.TrimSpace(decision.State)
		if decision.State != WorkDecisionRejected {
			continue
		}
		rejected = append(rejected, decision)
	}
	if len(rejected) == 0 {
		return nil
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
	used := 0
	for _, step := range ordered {
		if len(lines) >= policy.MaximumSteps {
			break
		}
		line := service.workStepLine(step, previews)
		if line == "" {
			continue
		}
		if policy.EntryMaximumCharacters > 0 && utf8.RuneCountInString(line) > policy.EntryMaximumCharacters {
			line = truncateRunes(line, policy.EntryMaximumCharacters)
		}
		if policy.SummaryMaximumCharacters > 0 && used+utf8.RuneCountInString(line)+1 > policy.SummaryMaximumCharacters {
			continue
		}
		lines = append(lines, line)
		used += utf8.RuneCountInString(line) + 1
	}
	if len(lines) == 0 {
		return ""
	}
	// Every session step not rendered in this block is counted, whether it fell
	// outside the newest page or past the summary's own bounds. The Schema note
	// tells the Agent the rest is still readable through get_production_step.
	truncated := max(0, ledger.TotalSteps-len(lines))
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
	omissions := ""
	if count := len(ledger.Omissions); count > 0 && strings.TrimSpace(policy.OmissionsNote) != "" {
		omissions = withoutMarkers(strings.ReplaceAll(policy.OmissionsNote, "{count}", strconv.Itoa(count)),
			policy.OpenMarker, policy.CloseMarker)
	}
	// One pass: substituted text is never scanned again, so a ledger line that
	// spells "{close}" stays literal. The projected text already has the frame
	// markers removed, so it cannot close the frame early either.
	return strings.NewReplacer(
		"{open}", policy.OpenMarker,
		"{steps}", stepsText,
		"{rejected}", rejected,
		"{omissions}", omissions,
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
		prompt = policy.PromptOmittedText
	}
	previewText := step.FinalPrompt
	if previewText == "" {
		// The reduced list projection keeps only parameters the Offering marks
		// semantic=prompt. A tool that carries one prompt per view has no such
		// parameter, so its recorded Agent arguments are the only preview the
		// summary can show; the full step still comes from get_production_step.
		previewText = step.RawPrompt
	}
	if previews && previewText != "" {
		preview := previewText
		if policy.PromptPreviewCharacters > 0 {
			preview = truncateRunes(preview, policy.PromptPreviewCharacters)
			if utf8.RuneCountInString(previewText) > policy.PromptPreviewCharacters {
				preview += "…"
			}
		}
		prompt = strings.ReplaceAll(policy.PromptLineTemplate, "{preview}", withoutMarkers(preview, policy.OpenMarker, policy.CloseMarker))
	}
	selfCheck := service.workSelfCheckLine(step.SelfChecks)
	decision := step.Decision
	if decision == "" {
		decision = policy.NoneText
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
		return policy.NoneText
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
			leftovers += strings.ReplaceAll(policy.FailedViewsTemplate, "{views}", strings.Join(check.FailedViews, "、"))
		}
		line := strings.NewReplacer(
			"{tool}", check.Tool,
			"{passed}", strconv.Itoa(check.Passed),
			"{unknown}", strconv.Itoa(check.Unknown),
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
func (orchestrator *Orchestrator) workAnchorsPrompt(ctx context.Context, ownerID, sessionID string, lock StyleLock) string {
	if orchestrator.service == nil || sessionID == "" {
		return ""
	}
	return orchestrator.service.workAnchorsSummary(ctx, ownerID, sessionID, lock)
}

// ReadProductionStep backs get_production_step: it resolves one step by handle
// or Task ID for the command's own owner and session and returns the complete
// record plus one-line summary. The lookup covers the complete session Task set,
// not only the newest summary page, so an old step is still readable after it
// fell out of the summary bound. It is read-only and never logs the record.
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
	owned, err := service.repository.OwnsSession(ctx, command.OwnerID, command.SessionID)
	if err != nil {
		return ToolResult{Error: service.workLedger.NotReadableTemplate, NoSubmission: true}, nil
	}
	if !owned {
		return ToolResult{Error: service.workLedger.NotReadableTemplate, NoSubmission: true}, nil
	}
	page, err := service.workLedgerTasks(ctx, command.OwnerID, command.SessionID, 0, service.workLedger.MaximumSteps)
	if err != nil {
		return ToolResult{Error: service.workLedger.NotReadableTemplate, NoSubmission: true}, nil
	}
	task, number, found, err := service.resolveProductionStep(ctx, command.OwnerID, command.SessionID, token, page)
	if err != nil {
		return ToolResult{Error: service.workLedger.NotReadableTemplate, NoSubmission: true}, nil
	}
	if !found {
		return ToolResult{
			Error:        strings.ReplaceAll(service.workLedger.UnknownStepTemplate, "{step}", truncateRunes(token, 200)),
			NoSubmission: true,
		}, nil
	}
	// The list projection carries only the semantic prompt; the full record
	// (every declared parameter, source and input reference) comes from the one
	// owner-scoped Task read. Session membership was established by the
	// session-scoped list above.
	if service.workLedgerDetail != nil {
		detailed, detailErr := service.workLedgerDetail(ctx, command.OwnerID, command.SessionID, string(task.ID))
		if detailErr != nil {
			return ToolResult{Error: service.workLedger.NotReadableTemplate, NoSubmission: true}, nil
		}
		task = detailed
	}
	facts, err := service.loadWorkLedgerFacts(ctx, command.OwnerID, command.SessionID)
	if err != nil {
		return ToolResult{Error: service.workLedger.NotReadableTemplate, NoSubmission: true}, nil
	}
	steps := []workNumberedStep{{task: task, step: number}}
	parameterStates, stateOmissions := service.workParameterStates(ctx, command.OwnerID, command.SessionID, steps)
	facts.omissions = appendWorkOmissions(facts.omissions, stateOmissions...)
	ledger := service.projectWorkLedger(steps, max(page.Total, len(steps)), facts, parameterStates)
	var foundStep *WorkStep
	for index := range ledger.Steps {
		if ledger.Steps[index].TaskID == string(task.ID) {
			foundStep = &ledger.Steps[index]
			break
		}
	}
	if foundStep == nil {
		return ToolResult{Error: service.workLedger.NotReadableTemplate, NoSubmission: true}, nil
	}
	content, err := json.Marshal(map[string]any{
		"ok":          true,
		"step":        foundStep,
		"total_steps": ledger.TotalSteps,
	})
	if err != nil {
		return ToolResult{Error: service.workLedger.NotReadableTemplate, NoSubmission: true}, nil
	}
	summary, _ := json.Marshal(map[string]any{
		"tool": "get_production_step", "step": foundStep.Step, "task_id": foundStep.TaskID,
		"state": foundStep.State, "decision": foundStep.Decision, "self_check": foundStep.SelfChecks,
	})
	return ToolResult{AgentContent: string(content), Summary: summary, NoSubmission: true}, nil
}

// resolveProductionStep resolves a step handle or a Task ID against the
// complete session Task set. The step handle is the exact @stepN spelling; any
// other token is looked up as a Task ID of this session only.
func (service *Service) resolveProductionStep(
	ctx context.Context,
	ownerID, sessionID, token string,
	page generation.TaskPage,
) (generation.Task, int, bool, error) {
	if number, ok := service.stepNumber(token); ok {
		return service.findSessionStep(ctx, ownerID, sessionID, number, page)
	}
	return service.findSessionTask(ctx, ownerID, sessionID, token, page)
}
