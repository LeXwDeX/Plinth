// Package budget declares the production plan and the credit budget a person
// approves once (#103, budget approval mode). The budget is kept per credit
// unit: each provider-native unit and the legacy bucket has its own limit,
// reservations and charges, and different units are never added (#120). It
// holds no storage and no approval logic: executiongrant owns reservations and
// decisions, and the callers only render or validate these values.
package budget

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// Budget states. pending waits for the person's decision; active is the one
// plan the session may spend under; exhausted means the credits actually
// charged reached the limit of every unit; closed means a newer plan replaced
// it; denied means the person refused it (or it expired undecided).
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
	// ReasonOverBudget: the step's estimate would exceed what the plan has
	// left in the step's unit, or the plan has no limit in that unit.
	ReasonOverBudget = "over_budget"
	// ReasonUnpriced: the step is metered but the server has no usable credit
	// quote for this exact input. A zero reservation would spend the budget
	// without covering the step, so the person decides it (#103 review B1).
	ReasonUnpriced = "unpriced"
)

// SystemActor marks a budget decision made by the server rather than a person.
const SystemActor = "system"

var (
	// ErrInactive means the plan cannot spend: it is not active (never
	// approved, denied, closed, or exhausted).
	ErrInactive = errors.New("budget: plan is not active")
	// ErrOverLimit means the step's estimate does not fit in what remains of
	// its unit, or the plan holds no limit in that unit.
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
	// Estimate is the server's quote for the step at plan time, in the step's
	// own unit; an Agent-supplied number is never trusted.
	Estimate Amount `json:"estimate"`
	// PurposeBudgetNote names the values the step would run with that are over
	// the session purpose's budget (#129), in the turn's language; empty when
	// they fit or the session has no purpose.
	PurposeBudgetNote string `json:"purpose_budget_note,omitempty"`
	// Check is an optional checkpoint declared after the step (#246). It is a
	// Schema-declared id; self_check means the result must be inspected and
	// its self-check conclusion recorded before the next step.
	Check string `json:"check,omitempty"`
	// CandidateGroup marks one candidate of a declared parallel group (#242):
	// every expanded step of the group shares the group's 1-based position in
	// the proposal, and CandidateIndex is its 1-based place inside the group.
	// Steps that declare no candidates carry zero for both.
	CandidateGroup int `json:"candidate_group,omitempty"`
	CandidateIndex int `json:"candidate_index,omitempty"`
}

// UnmarshalJSON also reads a step stored before #120, whose integer
// estimated_credits had no unit: it is a historical estimate and lands in the
// legacy bucket unchanged.
func (s *PlanStep) UnmarshalJSON(data []byte) error {
	var wire struct {
		Capability        string          `json:"capability"`
		OfferingID        string          `json:"offering_id"`
		Purpose           string          `json:"purpose"`
		Estimate          json.RawMessage `json:"estimate"`
		EstimatedCredits  int             `json:"estimated_credits"`
		PurposeBudgetNote string          `json:"purpose_budget_note"`
		Check             string          `json:"check"`
		CandidateGroup    int             `json:"candidate_group"`
		CandidateIndex    int             `json:"candidate_index"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	step := PlanStep{Capability: wire.Capability, OfferingID: wire.OfferingID, Purpose: wire.Purpose,
		Estimate: Legacy(wire.EstimatedCredits), PurposeBudgetNote: wire.PurposeBudgetNote, Check: wire.Check,
		CandidateGroup: wire.CandidateGroup, CandidateIndex: wire.CandidateIndex}
	if len(wire.Estimate) > 0 && string(wire.Estimate) != "null" {
		if err := json.Unmarshal(wire.Estimate, &step.Estimate); err != nil {
			return err
		}
	}
	*s = step
	return nil
}

// PlanCheck is one acceptance threshold of a planned checkpoint (#251). The
// server derives the thresholds from the purpose budget and the frozen brief;
// the Agent only names the check. A check without a bound is judged unknown.
type PlanCheck struct {
	Check string `json:"check"`
	// Minimum and Maximum bound a measured value in the check's own unit.
	Minimum *float64 `json:"minimum,omitempty"`
	Maximum *float64 `json:"maximum,omitempty"`
	// MaximumRatio bounds a ratio of the measured value to a total (a
	// distance over the bbox diagonal, a fraction of the triangles).
	MaximumRatio *float64 `json:"maximum_ratio,omitempty"`
	// Required makes a boolean property mandatory (UV, watertight, skin).
	// For y_up it marks an upright subject, so a lying model fails (#280).
	Required bool `json:"required,omitempty"`
}

// PlanCheckpoint is the declared check after one planned capability (#251).
type PlanCheckpoint struct {
	AfterCapability string      `json:"after_capability"`
	Checks          []PlanCheck `json:"checks"`
}

// PlanContingency is a declared repair step (#251): it may run only when one
// of its triggers failed at a checkpoint, and its estimate counts toward the
// plan's approved limit even though it usually does not run.
type PlanContingency struct {
	ID      string   `json:"id"`
	Trigger []string `json:"trigger"`
	Step    PlanStep `json:"step"`
}

// AcceptanceCriterion is one final-delivery condition the person approves on
// the plan card (#251). Statement is frozen in the proposal turn's language.
type AcceptanceCriterion struct {
	ID        string `json:"id"`
	Statement string `json:"statement"`
	Check     string `json:"check"`
}

// Plan is the frozen summary of what the person approved.
type Plan struct {
	Title                  string                `json:"title"`
	Steps                  []PlanStep            `json:"steps"`
	CorrectionCapabilities []string              `json:"correction_capabilities,omitempty"`
	Acceptance             []AcceptanceCriterion `json:"acceptance,omitempty"`
	Checkpoints            []PlanCheckpoint      `json:"checkpoints,omitempty"`
	Contingencies          []PlanContingency     `json:"contingencies,omitempty"`
}

// MaxTitleCharacters bounds a plan title; it is a display string, not free
// text the Agent may grow without limit.
const MaxTitleCharacters = 120

// StepTotals are the main steps' quotes summed per unit, ordered by unit.
func (p Plan) StepTotals() []Amount {
	amounts := make([]Amount, 0, len(p.Steps))
	for _, step := range p.Steps {
		amounts = append(amounts, step.Estimate)
	}
	return SumByUnit(amounts)
}

// ContingencyTotals are the declared contingency steps' quotes summed per
// unit. They are part of the approved limit because a contingency that fires
// must still fit (#251).
func (p Plan) ContingencyTotals() []Amount {
	amounts := make([]Amount, 0, len(p.Contingencies))
	for _, contingency := range p.Contingencies {
		amounts = append(amounts, contingency.Step.Estimate)
	}
	return SumByUnit(amounts)
}

// EstimatedTotals are the plan's worst-case quotes: the main steps plus every
// declared contingency, summed per unit and ordered by unit. Different units
// are never added.
func (p Plan) EstimatedTotals() []Amount {
	amounts := make([]Amount, 0, len(p.Steps)+len(p.Contingencies))
	for _, step := range p.Steps {
		amounts = append(amounts, step.Estimate)
	}
	for _, contingency := range p.Contingencies {
		amounts = append(amounts, contingency.Step.Estimate)
	}
	return SumByUnit(amounts)
}

// Uncovered lists the plan's per-unit estimates that the limits do not cover:
// a unit without a limit, or with a limit below the worst-case quote (steps
// plus contingencies) in it. Units whose steps cost nothing need no limit.
func (p Plan) Uncovered(limits []Amount) []Amount {
	var missing []Amount
	for _, total := range p.EstimatedTotals() {
		if total.Micros <= 0 {
			continue
		}
		if limit, found := limitOf(limits, total.Unit); !found || limit.Micros < total.Micros {
			missing = append(missing, total)
		}
	}
	return missing
}

// Allows reports whether a step of capability may run under this plan. The
// capability must appear in the steps, the declared contingencies or the
// correction capabilities. A step whose capability is pinned to one or more
// Offerings must use one of them; a capability with no pinned Offering accepts
// any Offering.
func (p Plan) Allows(capability, offeringID string) bool {
	if capability == "" {
		return false
	}
	if !p.capabilityPlanned(capability) && !p.capabilityContingent(capability) &&
		!containsString(p.CorrectionCapabilities, capability) {
		return false
	}
	var pinned []string
	for _, step := range p.Steps {
		if step.Capability == capability && step.OfferingID != "" {
			pinned = append(pinned, step.OfferingID)
		}
	}
	for _, contingency := range p.Contingencies {
		if contingency.Step.Capability == capability && contingency.Step.OfferingID != "" {
			pinned = append(pinned, contingency.Step.OfferingID)
		}
	}
	return len(pinned) == 0 || containsString(pinned, offeringID)
}

// Covers reports whether the plan explicitly reserves at least count steps of
// the capability and, when a step pins an Offering, runs that same Offering
// (#243). Correction capabilities do not promise a series and are not counted.
func (p Plan) Covers(capability, offeringID string, count int) bool {
	if capability == "" || count < 1 {
		return false
	}
	matched := 0
	for _, step := range p.Steps {
		if step.Capability != capability {
			continue
		}
		if step.OfferingID != "" && step.OfferingID != offeringID {
			continue
		}
		matched++
	}
	return matched >= count
}

func (p Plan) capabilityPlanned(capability string) bool {
	for _, step := range p.Steps {
		if step.Capability == capability {
			return true
		}
	}
	return false
}

func (p Plan) capabilityContingent(capability string) bool {
	for _, contingency := range p.Contingencies {
		if contingency.Step.Capability == capability {
			return true
		}
	}
	return false
}

// Contingency finds a declared contingency by id.
func (p Plan) Contingency(id string) (PlanContingency, bool) {
	for _, contingency := range p.Contingencies {
		if contingency.ID == id {
			return contingency, true
		}
	}
	return PlanContingency{}, false
}

// CheckpointAfter finds the declared checkpoint after one capability.
func (p Plan) CheckpointAfter(capability string) (PlanCheckpoint, bool) {
	for _, checkpoint := range p.Checkpoints {
		if checkpoint.AfterCapability == capability {
			return checkpoint, true
		}
	}
	return PlanCheckpoint{}, false
}

// Summary is what the Agent, the session projection and the tool results read:
// for every unit of the plan, the frozen limit next to what is spent, reserved
// and still available.
type Summary struct {
	ID        string       `json:"id"`
	State     string       `json:"state"`
	Units     []UnitBudget `json:"units"`
	Plan      Plan         `json:"plan"`
	CreatedAt time.Time    `json:"created_at"`
	DecidedAt *time.Time   `json:"decided_at,omitempty"`
}

// Unit is the summary's budget in one unit.
func (s Summary) Unit(unit string) (UnitBudget, bool) {
	for _, item := range s.Units {
		if item.Unit == unit {
			return item, true
		}
	}
	return UnitBudget{}, false
}

// SummaryOf builds the summary of a stored budget from its settled totals,
// one entry per limited unit in unit order.
func SummaryOf(production ProductionBudget, totals Totals) Summary {
	units := make([]UnitBudget, 0, len(production.Limits))
	for _, limit := range production.Limits {
		settled := totals[limit.Unit]
		units = append(units, NewUnitBudget(limit, settled.Spent, settled.Reserved))
	}
	return Summary{
		ID: production.ID, State: production.State, Units: units,
		Plan: production.Plan, CreatedAt: production.CreatedAt, DecidedAt: production.DecidedAt,
	}
}

// ProductionBudget is one stored plan and its decision state.
type ProductionBudget struct {
	ID        string
	OwnerID   string
	SessionID string
	Plan      Plan
	// Limits are the approved budget, one positive amount per unit, ordered
	// by unit (NormalizeLimits).
	Limits    []Amount
	State     string
	CreatedAt time.Time
	DecidedAt *time.Time
	DecidedBy string
	// MessageID and CallID identify the assistant message and tool call that
	// proposed the plan; the pending card is anchored there.
	MessageID string
	CallID    string
}

// Limit is the plan's limit in one unit.
func (p ProductionBudget) Limit(unit string) (Amount, bool) {
	return limitOf(p.Limits, unit)
}

// ExhaustedBy reports whether the charged totals reached the limit of every
// unit. A unit with budget left keeps the plan active: its steps may still
// run, while a step of a used-up unit finds no room and waits for a person.
func (p ProductionBudget) ExhaustedBy(totals Totals) bool {
	if len(p.Limits) == 0 {
		return false
	}
	for _, limit := range p.Limits {
		if totals[limit.Unit].Spent < limit.Micros {
			return false
		}
	}
	return true
}

func limitOf(limits []Amount, unit string) (Amount, bool) {
	for _, limit := range limits {
		if limit.Unit == unit {
			return limit, true
		}
	}
	return Amount{}, false
}

// PromptLine renders the plan for the Agent's approval-mode context: title
// and steps in order, with pinned models and each step's quote in its unit.
func (p Plan) PromptLine(format AmountFormat) string {
	parts := make([]string, 0, len(p.Steps))
	for _, step := range p.Steps {
		part := step.Capability
		if step.OfferingID != "" {
			part += "（" + step.OfferingID + "）"
		}
		if step.Purpose != "" {
			part += "：" + step.Purpose
		}
		part += "，约 " + format.One(step.Estimate)
		if step.Check != "" {
			part += "，检查点 " + step.Check
		}
		parts = append(parts, part)
	}
	return strings.TrimSpace(p.Title) + "；" + strings.Join(parts, "；")
}

// AcceptanceLine renders the final-delivery criteria for the Agent's context
// and the plan card. Empty when the plan declares none (a historical plan).
func (p Plan) AcceptanceLine() string {
	parts := make([]string, 0, len(p.Acceptance))
	for _, criterion := range p.Acceptance {
		parts = append(parts, criterion.Statement)
	}
	return strings.Join(parts, "；")
}

// CheckpointLine renders the declared checkpoints with their checks.
func (p Plan) CheckpointLine() string {
	parts := make([]string, 0, len(p.Checkpoints))
	for _, checkpoint := range p.Checkpoints {
		checks := make([]string, 0, len(checkpoint.Checks))
		for _, check := range checkpoint.Checks {
			checks = append(checks, check.Check)
		}
		parts = append(parts, checkpoint.AfterCapability+": "+strings.Join(checks, ", "))
	}
	return strings.Join(parts, "；")
}

// ContingencyLine renders the declared contingency steps and their estimates,
// separately from the main steps so the card never looks more expensive than
// the usual route.
func (p Plan) ContingencyLine(format AmountFormat) string {
	parts := make([]string, 0, len(p.Contingencies))
	for _, contingency := range p.Contingencies {
		part := contingency.ID + "（触发 " + strings.Join(contingency.Trigger, "/") + "）：" +
			contingency.Step.Capability + "，约 " + format.One(contingency.Step.Estimate)
		parts = append(parts, part)
	}
	return strings.Join(parts, "；")
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
// optional billed choice, in the Offering's own unit: the provider-native base
// amount exactly, or the legacy base price plus fixed extras in the legacy
// bucket. Native amounts are never rounded or converted (#120).
func EstimateOffering(offering schema.Offering) Amount {
	if pricing := offering.NativePricing; pricing != nil {
		micros, err := schema.ParseNativeAmount(pricing.BaseAmount)
		if err != nil || micros < 0 {
			micros = 0
		}
		return Amount{Unit: pricing.Unit, UnitLabel: pricing.UnitLabel, Micros: micros}
	}
	return Legacy(offering.QuotedCredits())
}

// EstimateOfferingCredits is the Offering's base quote as whole credits: the
// legacy integer estimate, or the native base amount rounded up. It only feeds
// the prepared input's frozen legacy-compatible EstimatedCredits (part of the
// input hash); budgets read EstimateOffering in the Offering's own unit.
func EstimateOfferingCredits(offering schema.Offering) int {
	if pricing := offering.NativePricing; pricing != nil {
		micros, err := schema.ParseNativeAmount(pricing.BaseAmount)
		if err != nil || micros <= 0 {
			return 0
		}
		return int((micros + microsPerCredit - 1) / microsPerCredit)
	}
	return offering.QuotedCredits()
}

// EstimateNativeTotal is the quote of a frozen native amount rounded up to
// whole credits. It is the prepared input's frozen legacy-compatible estimate
// (part of its input hash); budgets read the native quote itself.
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
