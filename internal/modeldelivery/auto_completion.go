package modeldelivery

import (
	"context"
	"sort"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
)

// The platform keeps its own delivery promise (#457): when a generation
// succeeded but some of the seven formats are missing, the missing ones are
// filled without a person or an Agent asking. This file is that promise's whole
// behaviour: which Task kinds it applies to, in which order the routes run, what
// a Task may spend and how often the durable record is scanned all come from
// application.model_delivery.auto_completion; the generation kernel derives no
// completion Task of its own and the Offerings carry no product rule.
//
// The durable record (AutoCompletionRecord) is the only memory: it says which
// Task was looked at, what its automatic runs have cost and when to look again.
// Everything else is re-read from the task kernel and the plan store on every
// round, so a crash between two rounds cannot charge twice: a plan is found by
// its deterministic key, and a format a plan already converts is never started
// again.

// States of one Task's automatic completion record.
const (
	// AutoStatePending: the Task was accepted and will be looked at again.
	AutoStatePending = "pending"
	// AutoStateConverting: a plan of ours is running for it.
	AutoStateConverting = "converting"
	// AutoStateDone: nothing is missing, or the Task is no longer a candidate.
	AutoStateDone = "done"
	// AutoStateBlocked: no automatic route can fill the missing formats; a
	// person may still complete them by hand.
	AutoStateBlocked = "blocked"
)

// Reasons recorded on a record. They are internal: what a person reads is the
// format row's own reason (the coverage projection's), never this.
const (
	autoReasonTaskGone       = "task_gone"
	autoReasonNotCandidate   = "not_candidate"
	autoReasonNothingMissing = "nothing_missing"
	autoReasonNoRoute        = "no_route"
	autoReasonWaiting        = "waiting"
	autoReasonPaidSpent      = "paid_spent"
	autoReasonPriceUnbounded = "price_unbounded"
	autoReasonPlanStarted    = "plan_started"
	autoReasonDeadline       = "deadline"
)

// AutoCompletionCursor is the durable scan position: every Task whose row was
// updated after it has been looked at.
type AutoCompletionCursor struct {
	UpdatedAt time.Time
	TaskID    generation.TaskID
}

// AutoCompletionCandidate is one Task the scan found without a record yet.
type AutoCompletionCandidate struct {
	TaskID    generation.TaskID
	OwnerID   generation.OwnerID
	SessionID string
}

// AutoCompletionRecord is the durable per-Task record of automatic completion.
type AutoCompletionRecord struct {
	TaskID  generation.TaskID
	OwnerID generation.OwnerID
	// PolicyRevision is the Offering revision the running plan was planned
	// under. A Catalog publish that changes a route is not silently applied to
	// a run that is already going.
	PolicyRevision string
	State          string
	// Attempts counts the rounds that looked at this Task.
	Attempts int
	// PlanID is the last plan this Task's completion started.
	PlanID string
	// PaidCredits is what this Task's automatic runs have cost so far, and
	// PaidRuns how many of them were paid runs: one Task is paid for at most
	// once, and never beyond the Schema's ceiling.
	PaidCredits int
	PaidRuns    int
	Reason      string
	NextRunAt   time.Time
	UpdatedAt   time.Time
}

// AutoCompletionStore is the durable record and the scan position. It is the
// SQLite repository; nothing about its tables belongs here.
type AutoCompletionStore interface {
	// AutoCompletionWatermark returns the scan position. The second result is
	// false while no scan has ever run: the first one is then written as "now"
	// and no historical Task is ever looked at.
	AutoCompletionWatermark(context.Context) (AutoCompletionCursor, bool, error)
	SetAutoCompletionWatermark(context.Context, AutoCompletionCursor) error
	// AutoCompletionCandidates lists succeeded Tasks of the given task kinds
	// updated after the watermark that have no record yet, oldest first. It
	// advances the watermark past everything it listed, whether or not the
	// caller then manages to look at it. The kinds are the Schema's
	// auto_completion.source_capabilities, never a list of our own.
	AutoCompletionCandidates(context.Context, []generation.Capability, int) ([]AutoCompletionCandidate, error)
	AutoCompletionRecord(context.Context, generation.TaskID) (AutoCompletionRecord, bool, error)
	SaveAutoCompletionRecord(context.Context, AutoCompletionRecord) error
	// AutoCompletionDue returns the records whose NextRunAt has passed, oldest
	// first.
	AutoCompletionDue(context.Context, time.Time, int) ([]AutoCompletionRecord, error)
}

// WithAutoCompletions gives the service its durable record. Without it the
// coverage, the completion action and the ZIP behave exactly as before and no
// automatic completion runs.
func (s *Service) WithAutoCompletions(store AutoCompletionStore) *Service {
	s.autoCompletions = store
	return s
}

// defaultAutoCompletionBatch bounds one scan's work per store read.
const defaultAutoCompletionBatch = 100

// ReconcileAutoCompletions advances the durable record by one scan: it looks
// again at the records whose round is due, then at the Tasks that succeeded
// since the last scan. It returns how many Tasks it acted on.
//
// It never starts a second paid run for a Task, never re-sends a run whose
// result is unknown, and never lets the platform's own plans queue up: at most
// auto_completion.max_inflight_plans of them run at once.
func (s *Service) ReconcileAutoCompletions(ctx context.Context, limit int) (int, error) {
	if s.autoCompletions == nil || !s.schema.AutoCompletion.Enabled() {
		return 0, nil
	}
	if limit <= 0 {
		limit = defaultAutoCompletionBatch
	}
	acted := 0
	due, err := s.autoCompletions.AutoCompletionDue(ctx, s.now(), limit)
	if err != nil {
		return 0, wrap("read due auto completions", err)
	}
	for _, record := range due {
		if ctx.Err() != nil {
			return acted, ctx.Err()
		}
		decided, err := s.reconcileAutoCompletion(ctx, record)
		if err != nil {
			return acted, err
		}
		if decided {
			acted++
		}
	}
	candidates, err := s.autoCompletions.AutoCompletionCandidates(
		ctx, s.schema.AutoCompletion.SourceCapabilities, limit-acted)
	if err != nil {
		return acted, wrap("read auto completion candidates", err)
	}
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return acted, ctx.Err()
		}
		record, exists, err := s.autoCompletions.AutoCompletionRecord(ctx, candidate.TaskID)
		if err != nil {
			return acted, wrap("read auto completion record", err)
		}
		if exists {
			continue
		}
		record = AutoCompletionRecord{
			TaskID: candidate.TaskID, OwnerID: candidate.OwnerID,
			State: AutoStatePending, NextRunAt: s.now(),
		}
		decided, err := s.reconcileAutoCompletion(ctx, record)
		if err != nil {
			return acted, err
		}
		if decided {
			acted++
		}
	}
	return acted, nil
}

// reconcileAutoCompletion looks at one Task once and saves what it decided. The
// second result is false when the record was left for another round.
func (s *Service) reconcileAutoCompletion(ctx context.Context, record AutoCompletionRecord) (bool, error) {
	if record.TaskID == "" || record.OwnerID == "" {
		return true, nil
	}
	auto := s.schema.AutoCompletion
	task, err := s.gateway.GetScoped(ctx, generation.OwnTasks(record.OwnerID), record.TaskID)
	if err != nil {
		if generation.IsCode(err, generation.ErrNotFound) {
			// A deleted or tombstoned Task: nothing is left to deliver.
			return true, s.saveAutoCompletion(record, AutoStateDone, autoReasonTaskGone)
		}
		return false, wrap("read auto completion task", err)
	}
	if task.State != generation.TaskSucceeded || task.SessionID == "" ||
		!s.capabilityIn(task.Capability, auto.SourceCapabilities) {
		return true, s.saveAutoCompletion(record, AutoStateDone, autoReasonNotCandidate)
	}
	record.Attempts++
	state := s.analyze(ctx, task)
	if state.err != nil {
		// A read that failed may hide a running conversion: decide nothing and
		// look again rather than offer or start a second paid run.
		return false, s.scheduleAutoCompletion(record, AutoStatePending, autoReasonWaiting)
	}
	if !state.coverage.Applicable {
		return true, s.saveAutoCompletion(record, AutoStateDone, autoReasonNotCandidate)
	}
	missing := s.missingFormats(state.view)
	if len(missing) == 0 {
		return true, s.saveAutoCompletion(record, AutoStateDone, autoReasonNothingMissing)
	}
	if s.autoDeadlinePassed(record) {
		// The plan's own timeout decides what the formats show; we only stop
		// looking, so one stuck record cannot pin a slot in every scan.
		return true, s.saveAutoCompletion(record, AutoStateDone, autoReasonDeadline)
	}
	paidBlocked := ""
	if record.PaidRuns >= auto.MaxPaidRunsPerTask || record.PaidCredits >= auto.MaxCreditsPerTask {
		// The free routes may still run; the paid ones may not.
		paidBlocked = autoReasonPaidSpent
	}
	if paidBlocked == "" {
		inflight, err := s.autoInflightPlans(ctx)
		if err != nil {
			return false, err
		}
		if inflight >= auto.MaxInflightPlans {
			return false, s.scheduleAutoCompletion(record, AutoStatePending, autoReasonWaiting)
		}
	}
	return s.startAutoCompletion(ctx, record, task, state, missing, paidBlocked)
}

// startAutoCompletion creates the plans the routes may fill and records what
// happened. paidBlocked is the recorded reason the paid routes may not run.
func (s *Service) startAutoCompletion(
	ctx context.Context,
	record AutoCompletionRecord,
	task generation.Task,
	state analysis,
	missing []string,
	paidBlocked string,
) (bool, error) {
	plans, blocked := s.autoPlans(ctx, task, state, missing, paidBlocked)
	lastPlan := ""
	paidCredits, paidRuns := record.PaidCredits, record.PaidRuns
	for _, planned := range plans {
		plan, err := s.plans.Create(ctx, processing.CreateCommand{
			OwnerID: record.OwnerID, IdempotencyKey: planned.step.candidate.key,
			SessionID: task.SessionID, SourceTaskID: task.ID,
			SourceAssetID: planned.step.sourceAsset.ID,
			Steps: []processing.StepRequest{{
				OfferingID:               planned.route.offering.ID,
				ExpectedOfferingRevision: planned.route.offering.Revision,
				Input:                    planned.step.input,
			}},
			// The platform keeps its own promise: no card, no message, no
			// session budget and no 3D step, and the audit reads as the platform
			// rather than as a browser (#461).
			Origin: processing.OriginDelivery,
		})
		if err != nil {
			return false, wrap("create auto completion plan", err)
		}
		lastPlan = plan.ID
		if planned.route.paid {
			paidRuns++
			paidCredits += planned.step.credits
		}
	}
	if lastPlan != "" {
		record.PlanID = lastPlan
		record.PaidCredits, record.PaidRuns = paidCredits, paidRuns
		record.PolicyRevision = s.autoPolicyRevision()
		return true, s.scheduleAutoCompletion(record, AutoStateConverting, autoReasonPlanStarted)
	}
	// Nothing was started. Formats another run is already converting were taken
	// out of the round above, so what is left is what no automatic route may
	// fill: the record says so and stops starting anything. It stays due, so a
	// route that becomes usable later (a Catalog publish, a price change) is
	// picked up on the next scan.
	reason := autoReasonNoRoute
	for _, format := range missing {
		if code, ok := blocked[format]; ok && code != "" {
			reason = code
			break
		}
	}
	return true, s.scheduleAutoCompletion(record, AutoStateBlocked, reason)
}

// autoPlan is one automatic plan this round may create.
type autoPlan struct {
	route autoRoute
	step  plannedStep
}

// autoRoute is one declared route of the automatic completion, in the Schema's
// route order.
type autoRoute struct {
	conversion catalog.ModelConversionSchema
	offering   generation.Offering
	// paid is the route's own declaration: it may spend.
	paid bool
}

// autoRoutes resolves auto_completion.route_order against the loaded Offerings,
// in that order and without repeating one.
func (s *Service) autoRoutes() []autoRoute {
	routes := make([]autoRoute, 0, len(s.schema.AutoCompletion.RouteOrder))
	byID := map[string]catalog.ModelConversionSchema{}
	for _, conversion := range s.schema.Conversions {
		byID[conversion.OfferingID] = conversion
	}
	for _, id := range s.schema.AutoCompletion.RouteOrder {
		conversion, declared := byID[id]
		if !declared || conversion.Auto == "" || conversion.Retired {
			continue
		}
		offering, exists := s.gateway.Offering(generation.OfferingID(conversion.OfferingID))
		if !exists {
			continue
		}
		routes = append(routes, autoRoute{
			conversion: conversion, offering: offering,
			paid: conversion.Auto == catalog.ConversionAutoPaid,
		})
	}
	return routes
}

// autoPlans decides which plans this round may create, in route order, and
// names the formats no automatic route may fill.
func (s *Service) autoPlans(
	ctx context.Context,
	task generation.Task,
	state analysis,
	missing []string,
	paidBlocked string,
) ([]autoPlan, map[string]string) {
	remaining := map[string]bool{}
	for _, format := range missing {
		remaining[format] = true
	}
	blocked := map[string]string{}
	var plans []autoPlan
	for _, route := range s.autoRoutes() {
		if !route.offering.Enabled || !s.gateway.OfferingAvailable(route.offering.ID) {
			continue
		}
		for _, candidate := range s.routeCandidates(task, route, remaining) {
			key := state.view.key(candidate.key)
			// A run that already exists owns these formats, whatever revision
			// it was made under: the record must not pay for them a second
			// time. A run that finished without delivering them is not
			// resubmitted either; the format's own reason says what happened.
			if _, exists := state.view.plans[key]; exists {
				s.dropCovered(remaining, blocked, candidate)
				continue
			}
			if _, locked := lockingPlan(state.view, plannedStep{
				candidate: candidate, formats: candidate.providedFormats(),
			}); locked {
				s.dropCovered(remaining, blocked, candidate)
				continue
			}
			if _, renewed := state.view.previous[candidate.key]; renewed {
				// A run that failed for a reason no retry can fix, or one whose
				// result is unknown: never re-sent automatically.
				s.dropCovered(remaining, blocked, candidate)
				continue
			}
			// The run also produces a format the Task still owes as a
			// post-success resource (#459): the kernel is converting it already.
			if candidate.overlapsPending(state.view.pending) {
				s.noteBlocked(blocked, remaining, candidate, reasonConverting)
				continue
			}
			step, code := s.buildStep(ctx, task, state.view, candidate)
			if code != "" {
				s.noteBlocked(blocked, remaining, candidate, code)
				continue
			}
			if route.paid {
				if paidBlocked != "" {
					s.noteBlocked(blocked, remaining, candidate, paidBlocked)
					continue
				}
				// A price this route cannot bound in credits (a native quote in
				// another unit) and a price that no longer fits the declared
				// bound are both the person's to accept, never ours to spend.
				if step.credits <= 0 || step.credits > route.conversion.MaxCredits {
					s.noteBlocked(blocked, remaining, candidate, autoReasonPriceUnbounded)
					continue
				}
			}
			plans = append(plans, autoPlan{route: route, step: step})
			for _, format := range candidate.providedFormats() {
				delete(remaining, format)
				delete(blocked, format)
			}
		}
	}
	return plans, blocked
}

// dropCovered takes the formats a run already delivers out of the round.
func (s *Service) dropCovered(remaining map[string]bool, blocked map[string]string, candidate candidate) {
	for _, format := range candidate.providedFormats() {
		delete(remaining, format)
		delete(blocked, format)
	}
}

// noteBlocked names, for every format the candidate would have filled, why no
// automatic plan was started for it.
func (s *Service) noteBlocked(blocked map[string]string, remaining map[string]bool, candidate candidate, code string) {
	for _, format := range candidate.providedFormats() {
		if !remaining[format] {
			continue
		}
		if _, noted := blocked[format]; !noted {
			blocked[format] = code
		}
	}
}

// routeCandidates is one run of the route: every format of a per-format route
// that is still missing, or the single run that delivers everything the route
// provides.
func (s *Service) routeCandidates(task generation.Task, route autoRoute, remaining map[string]bool) []candidate {
	provided := route.providedFormats()
	if route.conversion.FormatParameter == "" {
		wanted := false
		for _, format := range provided {
			if remaining[format] {
				wanted = true
				break
			}
		}
		if !wanted {
			return nil
		}
		return []candidate{{
			conversion: route.conversion, offering: route.offering,
			key: planKey(task, route.offering, ""),
		}}
	}
	var result []candidate
	for _, format := range provided {
		if !remaining[format] {
			continue
		}
		result = append(result, candidate{
			conversion: route.conversion, offering: route.offering, format: format,
			key: planKey(task, route.offering, format),
		})
	}
	return result
}

// providedFormats is everything one run of the route can deliver.
func (r autoRoute) providedFormats() []string {
	if r.conversion.FormatParameter == "" {
		return r.conversion.Provides
	}
	formats := make([]string, 0, len(r.conversion.FormatValues))
	for format := range r.conversion.FormatValues {
		formats = append(formats, format)
	}
	sort.Strings(formats)
	return formats
}

// missingFormats names the formats the Task still has to deliver: no saved
// file, nothing a plan obtained, and nothing the kernel is converting.
func (s *Service) missingFormats(v view) []string {
	var missing []string
	for _, format := range s.schema.Formats {
		if _, own := v.own[format.ID]; own {
			continue
		}
		if _, got := v.obtained[format.ID]; got {
			continue
		}
		if _, pending := v.pending[format.ID]; pending {
			continue
		}
		missing = append(missing, format.ID)
	}
	return missing
}

// autoPolicyRevision is the revision of the route that would run first, so a
// record says what it planned under.
func (s *Service) autoPolicyRevision() string {
	for _, route := range s.autoRoutes() {
		if !route.paid {
			return route.offering.Revision
		}
	}
	return ""
}

// autoInflightPlans counts the platform's own plans that are still running.
func (s *Service) autoInflightPlans(ctx context.Context) (int, error) {
	counter, ok := s.plans.(interface {
		CountActiveByOrigin(context.Context, string) (int, error)
	})
	if !ok {
		return 0, nil
	}
	count, err := counter.CountActiveByOrigin(ctx, processing.OriginDelivery)
	if err != nil {
		return 0, wrap("count auto completion plans", err)
	}
	return count, nil
}

// autoDeadlinePassed reports whether the Schema's deadline for this record's
// first round has passed.
func (s *Service) autoDeadlinePassed(record AutoCompletionRecord) bool {
	hours := s.schema.AutoCompletion.DeadlineHours
	if hours <= 0 || record.NextRunAt.IsZero() {
		return false
	}
	return s.now().After(record.NextRunAt.Add(time.Duration(hours) * time.Hour))
}

// scheduleAutoCompletion records a round that decides nothing yet.
func (s *Service) scheduleAutoCompletion(record AutoCompletionRecord, state, reason string) error {
	interval := time.Duration(s.schema.AutoCompletion.ScanIntervalMilliseconds) * time.Millisecond
	if interval <= 0 {
		interval = time.Minute
	}
	record.NextRunAt = s.now().Add(interval)
	return s.saveAutoCompletion(record, state, reason)
}

func (s *Service) saveAutoCompletion(record AutoCompletionRecord, state, reason string) error {
	record.State = state
	record.Reason = reason
	record.UpdatedAt = s.now()
	if s.autoCompletions == nil {
		return nil
	}
	if err := s.autoCompletions.SaveAutoCompletionRecord(context.Background(), record); err != nil {
		return wrap("save auto completion record", err)
	}
	return nil
}