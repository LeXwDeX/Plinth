package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

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
)

// ChoiceOption is one option of a card: the exact Offering, the public names
// and Schema quote the card shows, and the arguments of the call that runs it.
type ChoiceOption struct {
	ID               string              `json:"id"`
	ProviderName     string              `json:"provider_name"`
	ModelName        string              `json:"model_name"`
	CreditsMetered   bool                `json:"credits_metered,omitempty"`
	EstimatedCredits int                 `json:"estimated_credits,omitempty"`
	NativeQuote      *schema.NativeQuote `json:"native_quote,omitempty"`
	Arguments        json.RawMessage     `json:"arguments"`
}

// Title is how the Agent and the card name the option.
func (option ChoiceOption) Title() string {
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
	// Capability is the step being chosen for; StepIndex its position in a
	// plan (-1 for a single-step tool).
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

// pendingChoicePrompt tells the Agent about the session's open card, so a
// typed reply maps to one exact option (#321).
func (orchestrator *Orchestrator) pendingChoicePrompt(choices []Choice) string {
	for _, choice := range choices {
		if continuation, ok := choice.PendingContinuation(); ok {
			return strings.ReplaceAll(orchestrator.schema.Choice.PendingTemplate, "{continuation}", continuation)
		}
	}
	return ""
}

// choiceAnswerText follows the user message that answered a card on the card.
func (orchestrator *Orchestrator) choiceAnswerText(choices []Choice, messageID string) string {
	for _, choice := range choices {
		if choice.AnswerSource != ChoiceAnsweredOnCard || choice.AnswerMessageID != messageID {
			continue
		}
		if continuation, ok := choice.AnsweredContinuation(); ok {
			return strings.ReplaceAll(orchestrator.schema.Choice.AnswerTemplate, "{continuation}", continuation)
		}
	}
	return ""
}
