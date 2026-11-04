// Package budget declares the production plan and the total credit budget a
// person approves once (#103, budget approval mode). It holds no storage and
// no approval logic: executiongrant owns reservations and decisions, and the
// callers only render or validate these values.
package budget

import (
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// Budget states. pending waits for the person's decision; active is the one
// plan the session may spend under; exhausted means the credits actually
// charged reached the limit; closed means a newer plan replaced it; denied
// means the person refused it (or it expired undecided).
const (
	StatePending   = "pending"
	StateActive    = "active"
	StateExhausted = "exhausted"
	StateClosed    = "closed"
	StateDenied    = "denied"
)

// Reservation states of one approved step. reserved holds budget until the
// Task ends; charged and released are final.
const (
	ReservationReserved = "reserved"
	ReservationCharged  = "charged"
	ReservationReleased = "released"
)

// Reasons a budget-mode step leaves the policy for a human decision.
const (
	// ReasonNoPlan: the session has no active production plan.
	ReasonNoPlan = "no_plan"
	// ReasonOffPlan: the step's capability (or pinned Offering) is outside the
	// approved plan and its correction capabilities.
	ReasonOffPlan = "off_plan"
	// ReasonOverBudget: the step's estimate would exceed the plan's limit.
	ReasonOverBudget = "over_budget"
)

// SystemActor marks a budget decision made by the server rather than a person.
const SystemActor = "system"

var (
	// ErrInactive means the plan cannot spend: it is not active (never
	// approved, denied, closed, or exhausted).
	ErrInactive = errors.New("budget: plan is not active")
	// ErrOverLimit means the step's estimate does not fit in what remains.
	ErrOverLimit = errors.New("budget: estimate exceeds the remaining budget")
	// ErrState means the plan is not in a state the write allows.
	ErrState = errors.New("budget: invalid state")
	// ErrNotFound means the plan does not belong to the owner.
	ErrNotFound = errors.New("budget: plan not found")
)

// PlanStep is one step of the approved plan, frozen when the person decides.
type PlanStep struct {
	Capability string `json:"capability"`
	// OfferingID is empty when the plan does not pin a model; a pinned
	// Offering of the capability restricts steps of that capability to it.
	OfferingID string `json:"offering_id,omitempty"`
	Purpose    string `json:"purpose,omitempty"`
	// EstimatedCredits is the server's quote for the step at plan time; an
	// Agent-supplied number is never trusted.
	EstimatedCredits int `json:"estimated_credits"`
}

// Plan is the frozen summary of what the person approved.
type Plan struct {
	Title                  string     `json:"title"`
	Steps                  []PlanStep `json:"steps"`
	CorrectionCapabilities []string   `json:"correction_capabilities,omitempty"`
}

// MaxTitleCharacters bounds a plan title; it is a display string, not free
// text the Agent may grow without limit.
const MaxTitleCharacters = 120

// EstimatedTotal is the sum of the plan's step quotes.
func (p Plan) EstimatedTotal() int {
	total := 0
	for _, step := range p.Steps {
		total += step.EstimatedCredits
	}
	return total
}

// Allows reports whether a step of capability may run under this plan. The
// capability must appear in the steps or in the correction capabilities. A
// step whose capability is pinned to one or more Offerings must use one of
// them; a capability with no pinned Offering accepts any Offering.
func (p Plan) Allows(capability, offeringID string) bool {
	if capability == "" {
		return false
	}
	if !p.capabilityPlanned(capability) && !containsString(p.CorrectionCapabilities, capability) {
		return false
	}
	var pinned []string
	for _, step := range p.Steps {
		if step.Capability == capability && step.OfferingID != "" {
			pinned = append(pinned, step.OfferingID)
		}
	}
	return len(pinned) == 0 || containsString(pinned, offeringID)
}

func (p Plan) capabilityPlanned(capability string) bool {
	for _, step := range p.Steps {
		if step.Capability == capability {
			return true
		}
	}
	return false
}

// Summary is what the Agent, the session projection and the tool results read:
// the frozen limit next to what is spent, reserved and still available.
type Summary struct {
	ID               string     `json:"id"`
	State            string     `json:"state"`
	LimitCredits     int        `json:"limit_credits"`
	SpentCredits     int        `json:"spent_credits"`
	ReservedCredits  int        `json:"reserved_credits"`
	RemainingCredits int        `json:"remaining_credits"`
	Plan             Plan       `json:"plan"`
	CreatedAt        time.Time  `json:"created_at"`
	DecidedAt        *time.Time `json:"decided_at,omitempty"`
}

// SummaryOf builds the summary of a stored budget from its settled totals.
func SummaryOf(production ProductionBudget, spent, reserved int) Summary {
	remaining := production.LimitCredits - spent - reserved
	if remaining < 0 {
		remaining = 0
	}
	return Summary{
		ID: production.ID, State: production.State, LimitCredits: production.LimitCredits,
		SpentCredits: spent, ReservedCredits: reserved, RemainingCredits: remaining,
		Plan: production.Plan, CreatedAt: production.CreatedAt, DecidedAt: production.DecidedAt,
	}
}

// ProductionBudget is one stored plan and its decision state.
type ProductionBudget struct {
	ID           string
	OwnerID      string
	SessionID    string
	Plan         Plan
	LimitCredits int
	State        string
	CreatedAt    time.Time
	DecidedAt    *time.Time
	DecidedBy    string
	// MessageID and CallID identify the assistant message and tool call that
	// proposed the plan; the pending card is anchored there.
	MessageID string
	CallID    string
}

// PlanPromptLine renders the plan for the Agent's approval-mode context:
// title and steps in order, with pinned models and quotes.
func (p Plan) PromptLine() string {
	parts := make([]string, 0, len(p.Steps))
	for _, step := range p.Steps {
		part := step.Capability
		if step.OfferingID != "" {
			part += "（" + step.OfferingID + "）"
		}
		if step.Purpose != "" {
			part += "：" + step.Purpose
		}
		part += "，约 " + strconv.Itoa(step.EstimatedCredits) + " 积分"
		parts = append(parts, part)
	}
	return strings.TrimSpace(p.Title) + "；" + strings.Join(parts, "；")
}

// SortedCapabilities returns the plan's capabilities without duplicates, in
// plan order, for stable display and tests.
func (p Plan) SortedCapabilities() []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(p.Steps))
	for _, step := range p.Steps {
		if !seen[step.Capability] {
			seen[step.Capability] = true
			result = append(result, step.Capability)
		}
	}
	sort.Strings(result)
	return result
}

// EstimateOffering is the server's quote for one planned step before any
// optional billed choice: the legacy base price plus fixed extras, or the
// native base amount rounded up to whole provider credits. Native units in
// this Catalog are provider credits, so one native unit is one credit for
// budgeting purposes; the actual native total is still what the person sees.
func EstimateOffering(offering schema.Offering) int {
	if offering.NativePricing != nil {
		micros, err := schema.ParseNativeAmount(offering.NativePricing.BaseAmount)
		if err != nil {
			return 0
		}
		if micros <= 0 {
			return 0
		}
		return int((micros + 999999) / 1000000)
	}
	return offering.QuotedCredits()
}

// EstimateNativeTotal is the quote of a frozen native amount rounded up to
// whole credits. It is used when a prepared input carries a native quote.
func EstimateNativeTotal(quote *schema.NativeQuote) int {
	if quote == nil {
		return 0
	}
	micros, err := schema.ParseNativeAmount(quote.TotalAmount)
	if err != nil || micros <= 0 {
		return 0
	}
	return int((micros + 999999) / 1000000)
}

func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}
