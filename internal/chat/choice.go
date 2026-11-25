package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// An in-chat choice card (#321): a tool asks the person which of several
// options to use instead of choosing for them. The card is stored with the
// session, so it survives a refresh, another session or another device; an
// answered card shows its answer and cannot be answered again.

// ChoiceState is the lifecycle of a card: pending until answered, answered
// once (on the card, or by the Agent mapping a typed reply), or superseded
// when a newer card of the same session replaced it unanswered.
type ChoiceState string

const (
	ChoicePending    ChoiceState = "pending"
	ChoiceAnswered   ChoiceState = "answered"
	ChoiceSuperseded ChoiceState = "superseded"
)

// ChoiceAnswerSource says how a card was answered.
const (
	ChoiceAnsweredOnCard  = "card"
	ChoiceAnsweredByAgent = "agent"
)

var (
	// ErrChoiceNotFound covers a card of another owner or session too.
	ErrChoiceNotFound = errors.New("chat: choice not found")
	// ErrChoiceClosed refuses an answer to a card that is no longer pending.
	ErrChoiceClosed = errors.New("chat: choice is no longer open")
	// ErrChoiceRejected refuses an option the card does not offer.
	ErrChoiceRejected = errors.New("chat: choice option rejected")
	// ErrChoiceOpenInReply refuses a second card while a card this same Agent
	// reply opened is still pending (#615): one reply asks one card.
	ErrChoiceOpenInReply = errors.New("chat: this reply already opened a pending choice card")
)

// ChoiceOption is one option of a card: the exact Offering, the public names
// and Schema quote the card shows, and the arguments of the call that runs it.
// A generic card (#103) uses Label and Description instead of the Offering
// names and marks its recommended option. The Offering fields and Arguments
// stay for the #321 provider-choice cards.
type ChoiceOption struct {
	ID           string `json:"id"`
	ProviderName string `json:"provider_name,omitempty"`
	ModelName    string `json:"model_name,omitempty"`
	Label        string `json:"label,omitempty"`
	Description  string `json:"description,omitempty"`
	// ThumbnailURL shows a concept sketch of this option (#242). It is a
	// platform picture URL resolved from a session handle; empty when the
	// Agent gave none or the handle could not be resolved.
	ThumbnailURL     string              `json:"thumbnail_url,omitempty"`
	Recommended      bool                `json:"recommended,omitempty"`
	CreditsMetered   bool                `json:"credits_metered,omitempty"`
	EstimatedCredits int                 `json:"estimated_credits,omitempty"`
	NativeQuote      *schema.NativeQuote `json:"native_quote,omitempty"`
	Arguments        json.RawMessage     `json:"arguments"`
	// Action is what the page does when the person picks this option, before
	// it sends the answer (#374). Only a kind that declares actions carries
	// one; the server stores and shows it but never performs it.
	Action *ChoiceAction `json:"action,omitempty"`
	// Step is the one paid step the option runs (#549), on a kind that
	// declares steps: the tool and the capability. Nil is an option that runs
	// no paid step (an explanation, a hand-off). The quote fields above are
	// the server's estimate of this step.
	Step *ChoiceStep `json:"step,omitempty"`
}

// ChoiceStep is the one paid step a direction-card option names (#549).
type ChoiceStep struct {
	Tool       string `json:"tool"`
	Capability string `json:"capability,omitempty"`
}

// ChoiceScope is what the turn that answers a direction card on the card may
// submit (#549): only the chosen option's step, once. It is derived from the
// stored card and the turn's last user message on every round, so a restarted
// turn is scoped the same way. A nil scope is a turn nobody scoped (a typed
// instruction, a plan continuation, a card of another kind).
type ChoiceScope struct {
	ChoiceID    string
	OptionID    string
	OptionLabel string
	// Step is the chosen option's step; nil authorizes no paid step.
	Step *ChoiceStep
	// Refusal is the kind's Schema text for a step outside the scope ({tool},
	// {option}).
	Refusal string
}

// Permits reports whether the scope lets this tool run this capability. An
// empty capability matches on the tool alone (the orchestrator does not know
// the capability a call resolves to; the generation tools check it again).
func (scope *ChoiceScope) Permits(tool, capability string) bool {
	if scope == nil {
		return true
	}
	if scope.Step == nil || scope.Step.Tool != tool {
		return false
	}
	return capability == "" || scope.Step.Capability == "" || scope.Step.Capability == capability
}

// RefusalFor words the refusal of a step outside the scope. localize
// translates the Schema template into the turn's locale first; nil keeps the
// default-locale copy.
func (scope *ChoiceScope) RefusalFor(tool string, localize func(string) string) string {
	template := scope.Refusal
	if localize != nil {
		template = localize(template)
	}
	return strings.NewReplacer("{tool}", tool, "{option}", scope.OptionLabel).Replace(template)
}

// ChoiceAction is an option's action (#374): Type is one of the catalog's
// ChoiceAction constants. switch_operation names the function operation to fix
// in OperationID; clear_operation carries none. The page performs it through
// the session's function selection, which only the person changes.
type ChoiceAction struct {
	Type        string `json:"type"`
	OperationID string `json:"operation_id,omitempty"`
}

// Title is how the Agent and the card name the option. A generic option's
// Schema label wins over the Offering names.
func (option ChoiceOption) Title() string {
	if label := strings.TrimSpace(option.Label); label != "" {
		return label
	}
	return strings.TrimSpace(option.ProviderName + " · " + option.ModelName)
}

// Choice is one card. Options are listed in Schema order; DefaultOptionID is
// the first of them, which runs with DefaultArguments when the person accepts
// the default.
type Choice struct {
	ID                 string
	OwnerID            string
	SessionID          string
	AssistantMessageID string
	CallID             string
	ToolName           string
	// Kind is the generic choice kind (#103), e.g. usage_profile. Empty is a
	// #321 provider-choice card.
	Kind string
	// Capability is the step being chosen for; StepIndex its position in a
	// plan (-1 for a single-step tool). A generic card stores its kind here
	// too, for the continuation, and -1 as its index.
	Capability       string
	StepIndex        int
	Options          []ChoiceOption
	DefaultOptionID  string
	AllowDefault     bool
	DefaultArguments json.RawMessage

	State           ChoiceState
	AnswerOptionID  string
	AnswerDefault   bool
	AnswerSource    string
	AnswerMessageID string
	CreatedAt       time.Time
	AnsweredAt      time.Time
}

// Option returns the card's option with that ID.
func (choice Choice) Option(id string) (ChoiceOption, bool) {
	for _, option := range choice.Options {
		if option.ID == id {
			return option, true
		}
	}
	return ChoiceOption{}, false
}

// ChoiceAnswer is the person's answer that travels with a turn.
type ChoiceAnswer struct {
	ChoiceID   string
	OptionID   string
	UseDefault bool
}

// ChoiceStore keeps the cards of a session. Every read and write is owner and
// session scoped; a card of anyone else is ErrChoiceNotFound.
type ChoiceStore interface {
	// CreateChoice stores a pending card and supersedes the other pending
	// cards of the session. A second create for the same tool call returns
	// the card already stored.
	CreateChoice(ctx context.Context, choice Choice) (Choice, error)
	ListChoices(ctx context.Context, ownerID, sessionID string, limit int) ([]Choice, error)
	GetChoice(ctx context.Context, ownerID, sessionID, choiceID string) (Choice, error)
	// AnswerChoice answers a pending card once; a card that is no longer
	// pending is ErrChoiceClosed.
	AnswerChoice(ctx context.Context, ownerID, sessionID, choiceID string, answer ChoiceRecord) (Choice, error)
	// ReopenChoice undoes an answer recorded for a turn that then failed to
	// start: only the answer bound to that message is reverted.
	ReopenChoice(ctx context.Context, ownerID, sessionID, choiceID, messageID string) error
}

// ChoiceRecord is what AnswerChoice stores.
type ChoiceRecord struct {
	OptionID   string
	UseDefault bool
	Source     string
	MessageID  string
	AnsweredAt time.Time
}

// Resolve checks an answer against the card and returns the option it picks
// (the default option for UseDefault).
func (choice Choice) Resolve(answer ChoiceAnswer) (ChoiceOption, error) {
	if choice.State != ChoicePending {
		return ChoiceOption{}, ErrChoiceClosed
	}
	if answer.UseDefault {
		if !choice.AllowDefault || answer.OptionID != "" {
			return ChoiceOption{}, ErrChoiceRejected
		}
		option, ok := choice.Option(choice.DefaultOptionID)
		if !ok {
			return ChoiceOption{}, ErrChoiceRejected
		}
		return option, nil
	}
	option, ok := choice.Option(answer.OptionID)
	if !ok {
		return ChoiceOption{}, ErrChoiceRejected
	}
	return option, nil
}

// choiceContinuation is the JSON the Agent reads about a card: the tool to
// call and, per option and for the default, the exact arguments.
type choiceContinuation struct {
	ChoiceID string                   `json:"choice_id"`
	Tool     string                   `json:"tool"`
	Step     string                   `json:"step,omitempty"`
	Chosen   *choiceContinuationItem  `json:"chosen,omitempty"`
	Options  []choiceContinuationItem `json:"options,omitempty"`
	Default  *choiceContinuationItem  `json:"default,omitempty"`
}

type choiceContinuationItem struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Arguments json.RawMessage `json:"arguments"`
}

// AnsweredContinuation is the continuation of an answered card: only the
// chosen entry, so the Agent repeats exactly that call.
func (choice Choice) AnsweredContinuation() (string, bool) {
	if choice.State != ChoiceAnswered {
		return "", false
	}
	option, ok := choice.Option(choice.AnswerOptionID)
	if !ok {
		return "", false
	}
	arguments := option.Arguments
	if choice.AnswerDefault && len(choice.DefaultArguments) > 0 {
		arguments = choice.DefaultArguments
	}
	encoded, err := json.Marshal(choiceContinuation{ChoiceID: choice.ID, Tool: choice.ToolName, Step: choice.Capability,
		Chosen: &choiceContinuationItem{ID: option.ID, Title: option.Title(), Arguments: arguments}})
	return string(encoded), err == nil
}

// PendingContinuation lists every option and the default of an open card, so
// a typed reply maps to one exact call.
func (choice Choice) PendingContinuation() (string, bool) {
	if choice.State != ChoicePending {
		return "", false
	}
	value := choiceContinuation{ChoiceID: choice.ID, Tool: choice.ToolName, Step: choice.Capability}
	for _, option := range choice.Options {
		value.Options = append(value.Options, choiceContinuationItem{ID: option.ID, Title: option.Title(), Arguments: option.Arguments})
	}
	if choice.AllowDefault {
		if option, ok := choice.Option(choice.DefaultOptionID); ok {
			value.Default = &choiceContinuationItem{ID: option.ID, Title: option.Title(), Arguments: choice.DefaultArguments}
		}
	}
	encoded, err := json.Marshal(value)
	return string(encoded), err == nil
}

// checkChoiceActionWiring refuses a configuration in which a card kind may
// switch functions but the service knows none (#374): every switch would be
// refused and the card would quietly lose its point, so assembly fails fast.
func checkChoiceActionWiring(kinds []catalog.ChatChoiceKindSchema, operations []catalog.HarnessOperationSchema) error {
	if len(operations) > 0 {
		return nil
	}
	for _, kind := range kinds {
		if slices.Contains(kind.Actions, catalog.ChoiceActionSwitchOperation) {
			return fmt.Errorf("chat: choice kind %q may switch functions but no harness operations are configured", kind.ID)
		}
	}
	return nil
}

// directionContinuation is the JSON the Agent reads about a card whose kind
// names no target tool (#374): the chosen option, or every option while the
// card is open. The answer continues the conversation; there is no call to
// repeat.
type directionContinuation struct {
	ChoiceID string                      `json:"choice_id"`
	Kind     string                      `json:"kind"`
	Chosen   *directionContinuationItem  `json:"chosen,omitempty"`
	Options  []directionContinuationItem `json:"options,omitempty"`
}

type directionContinuationItem struct {
	ID          string        `json:"id"`
	Title       string        `json:"title"`
	Description string        `json:"description,omitempty"`
	Action      *ChoiceAction `json:"action,omitempty"`
	Step        *ChoiceStep   `json:"step,omitempty"`
}

func directionItem(option ChoiceOption) directionContinuationItem {
	return directionContinuationItem{ID: option.ID, Title: option.Title(), Description: option.Description,
		Action: option.Action, Step: option.Step}
}

// AnsweredDirection is the continuation of an answered card that names no
// target tool: only the chosen option, with its title, one-line description and
// action.
func (choice Choice) AnsweredDirection() (string, bool) {
	if choice.State != ChoiceAnswered {
		return "", false
	}
	option, ok := choice.Option(choice.AnswerOptionID)
	if !ok {
		return "", false
	}
	item := directionItem(option)
	encoded, err := json.Marshal(directionContinuation{ChoiceID: choice.ID, Kind: choice.Kind, Chosen: &item})
	return string(encoded), err == nil
}

// PendingDirection lists every option of an open card that names no target
// tool, so a typed reply maps to one option.
func (choice Choice) PendingDirection() (string, bool) {
	if choice.State != ChoicePending {
		return "", false
	}
	value := directionContinuation{ChoiceID: choice.ID, Kind: choice.Kind}
	for _, option := range choice.Options {
		value.Options = append(value.Options, directionItem(option))
	}
	encoded, err := json.Marshal(value)
	return string(encoded), err == nil
}

// namesNoTargetTool reports whether a card is a generic card whose kind names
// no target tool (#374): its answer is text, not a call.
func (choice Choice) namesNoTargetTool() bool {
	return choice.Kind != "" && choice.ToolName == ""
}

// directionKind finds the declared kind of a card that names no target tool.
// A card whose kind the Schema no longer declares has no wording to show.
func (orchestrator *Orchestrator) directionKind(choice Choice) (catalog.ChatChoiceKindSchema, bool) {
	if !choice.namesNoTargetTool() {
		return catalog.ChatChoiceKindSchema{}, false
	}
	for _, kind := range orchestrator.schema.Choice.Kinds {
		if kind.ID == choice.Kind && kind.ToolName == "" {
			return kind, true
		}
	}
	return catalog.ChatChoiceKindSchema{}, false
}

// turnChoiceScope derives the scope of a turn that answers a direction card on
// the card (#549) from stored data only: the last user message of the turn's
// history and the card answered with that message. A turn whose last user
// message answered no card, a card answered by the Agent mapping a typed reply
// and a card of a kind that declares no step are not scoped. When the cards
// cannot be read the turn is scoped with no step at all: an unreadable answer
// must never widen what the turn may charge.
func (orchestrator *Orchestrator) turnChoiceScope(ctx context.Context, ownerID, sessionID string, history []Message) *ChoiceScope {
	if orchestrator.service == nil || sessionID == "" {
		return nil
	}
	steps := map[string]catalog.ChatChoiceStepSchema{}
	failClosed := ""
	for _, kind := range orchestrator.schema.Choice.Kinds {
		if kind.Step != nil && kind.ToolName == "" {
			steps[kind.ID] = *kind.Step
			if failClosed == "" {
				failClosed = kind.Step.UnchosenRefusal
			}
		}
	}
	if len(steps) == 0 {
		return nil
	}
	messageID := ""
	for index := len(history) - 1; index >= 0; index-- {
		if history[index].Role == orchestrator.schema.Roles.User {
			messageID = history[index].ID
			break
		}
	}
	if messageID == "" {
		return nil
	}
	choices, err := orchestrator.service.ListChoices(ctx, ownerID, sessionID)
	if err != nil {
		if orchestrator.logger != nil {
			orchestrator.logger.Warn("读取方向卡失败，本回合不放行任何付费步骤", "session_id", sessionID, "error", err)
		}
		return &ChoiceScope{Refusal: failClosed}
	}
	for _, choice := range choices {
		if choice.State != ChoiceAnswered || choice.AnswerSource != ChoiceAnsweredOnCard ||
			choice.AnswerMessageID != messageID || !choice.namesNoTargetTool() {
			continue
		}
		kind, declared := steps[choice.Kind]
		if !declared {
			continue
		}
		scope := &ChoiceScope{ChoiceID: choice.ID, OptionID: choice.AnswerOptionID, Refusal: kind.UnchosenRefusal}
		if option, found := choice.Option(choice.AnswerOptionID); found {
			scope.OptionLabel = option.Title()
			if option.Step != nil {
				step := *option.Step
				scope.Step = &step
			}
		}
		return scope
	}
	return nil
}

// choiceStepRefusal is the orchestrator's half of the answer-turn scope
// (#549): before a paid tool runs in a scoped turn, it refuses a tool the
// chosen option does not name and a second submission of the one it names.
// The capability a call resolves to is checked again by the generation tools,
// which know it. A tool whose capability the session's approved budget plan
// lists is left to them and to the plan: the person approved those steps as
// a whole. It returns "" when the call may run.
func (orchestrator *Orchestrator) choiceStepRefusal(ctx context.Context, command ProcessTurn, scope *ChoiceScope,
	call agents.ToolCall, tool catalog.ChatToolSchema, earlier []ToolCall, submitted map[string]string) string {
	if scope == nil || !paidTool(tool.Execution) {
		return ""
	}
	if scope.Permits(call.Name, "") {
		again := false
		for _, previous := range earlier {
			if previous.Name == call.Name && (previous.TaskID != "" || submitted[previous.ID] != "") {
				again = true
			}
		}
		if !again {
			return ""
		}
	}
	if orchestrator.choicePlanCovers(ctx, command.OwnerID, command.SessionID, tool) {
		return ""
	}
	return scope.RefusalFor(call.Name, func(text string) string { return orchestrator.text(ctx, text) })
}

// choicePlanCovers reports whether the session is in budget mode with an
// active plan that lists one of the tool's capabilities (#549).
func (orchestrator *Orchestrator) choicePlanCovers(ctx context.Context, ownerID, sessionID string, tool catalog.ChatToolSchema) bool {
	mode, err := orchestrator.service.SessionApprovalMode(ctx, ownerID, sessionID)
	if err != nil || mode != approvalpolicy.ModeBudget {
		return false
	}
	summary, found, err := orchestrator.service.ProductionBudgetSummary(ctx, ownerID, sessionID)
	if err != nil || !found || summary.State != budget.StateActive {
		return false
	}
	for _, capability := range tool.Execution.Capabilities {
		if summary.Plan.ListsCapability(string(capability)) {
			return true
		}
	}
	return false
}

// pendingChoicePrompt tells the Agent about the session's open card, so a
// typed reply maps to one exact option (#321). The concept quantity card
// (#360) words its own prompt: its answer is a count, not a tool call. A card
// that names no target tool (#374) words its prompt in its kind: its answer is
// the chosen direction as text.
func (orchestrator *Orchestrator) pendingChoicePrompt(choices []Choice) string {
	if prompt, ok := orchestrator.conceptQuantityPendingPrompt(choices); ok {
		return prompt
	}
	for _, choice := range choices {
		if choice.namesNoTargetTool() {
			if kind, ok := orchestrator.directionKind(choice); ok {
				if continuation, ok := choice.PendingDirection(); ok {
					return strings.ReplaceAll(kind.PendingTemplate, "{continuation}", continuation)
				}
			}
			continue
		}
		if continuation, ok := choice.PendingContinuation(); ok {
			return strings.ReplaceAll(orchestrator.schema.Choice.PendingTemplate, "{continuation}", continuation)
		}
	}
	return ""
}

// choiceAnswerText follows the user message that answered a card on the card.
// A concept quantity answer carries the chosen count instead (#360), and the
// answer of a card that names no target tool the chosen direction (#374).
func (orchestrator *Orchestrator) choiceAnswerText(choices []Choice, messageID string) string {
	for _, choice := range choices {
		if text, ok := orchestrator.conceptQuantityAnswerText(choice, messageID); ok {
			return text
		}
	}
	for _, choice := range choices {
		if choice.AnswerSource != ChoiceAnsweredOnCard || choice.AnswerMessageID != messageID {
			continue
		}
		if choice.namesNoTargetTool() {
			if kind, ok := orchestrator.directionKind(choice); ok {
				if continuation, ok := choice.AnsweredDirection(); ok {
					return strings.ReplaceAll(kind.AnswerTemplate, "{continuation}", continuation)
				}
			}
			continue
		}
		if continuation, ok := choice.AnsweredContinuation(); ok {
			return strings.ReplaceAll(orchestrator.schema.Choice.AnswerTemplate, "{continuation}", continuation)
		}
	}
	return ""
}
