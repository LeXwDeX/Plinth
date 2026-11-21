package tripo

import (
	"context"
	"errors"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/upstreampool"
)

// TripoPoolName is the connection-declared pool shared by format-convert,
// texture and refine (the official default holds five jobs across the three).
const TripoPoolName = "model-processing"

// poolQueuePoll is the short fixed poll a queued round asks for. The upstream
// queue clock, not the job timeout, measures this wait (N1).
const poolQueuePoll = 2 * time.Second

// poolEnabled reports whether this Adapter queues through an upstream pool.
func (a *Adapter) poolEnabled() bool {
	return a.pool != nil && a.poolID != ""
}

func (a *Adapter) poolHolder(offering generation.Offering, attempt generation.AttemptID) upstreampool.Holder {
	return upstreampool.Holder{
		Connection: offering.AdapterKey,
		Pool:       a.poolID,
		Attempt:    string(attempt),
	}
}

func (a *Adapter) touchPool(offering generation.Offering, attempt generation.AttemptID) {
	if a.poolEnabled() {
		a.pool.Touch(a.poolHolder(offering, attempt))
	}
}

func (a *Adapter) acquirePool(
	ctx context.Context,
	offering generation.Offering,
	attempt generation.AttemptID,
) (upstreampool.Ticket, bool, time.Duration) {
	if !a.poolEnabled() {
		return upstreampool.Ticket{}, true, 0
	}
	return a.pool.Acquire(ctx, a.poolHolder(offering, attempt), 0)
}

func (a *Adapter) readoptPool(offering generation.Offering, attempt generation.AttemptID) {
	if a.poolEnabled() {
		a.pool.Readopt(a.poolHolder(offering, attempt))
	}
}

func (a *Adapter) handoffPool(offering generation.Offering, attempt generation.AttemptID) {
	if a.poolEnabled() {
		a.pool.Handoff(a.poolHolder(offering, attempt))
	}
}

func (a *Adapter) releasePool(offering generation.Offering, attempt generation.AttemptID) {
	if a.poolEnabled() {
		a.pool.ReleaseHolder(a.poolHolder(offering, attempt))
	}
}

// poolCountsPublicJob reports whether a public job's own upstream work belongs
// to the shared pool. Those jobs are counted only (C3): Start registers them,
// Observe refreshes them and a terminal observation releases them. They are
// never blocked, because the public state machine has no "not dispatched, try
// later" exit.
func poolCountsPublicJob(offering generation.Offering) bool {
	return offering.Capability == generation.FormatConversion ||
		offering.Capability == generation.Retexture
}

// poolQueueObservation marks a running observation as waiting for an upstream
// slot: it carries the upstream queue clock and a short poll, so the wait does
// not count toward the job timeout and reads as "queued", not as a failure.
func poolQueueObservation(observation generation.Observation, wait time.Duration) generation.Observation {
	if wait <= 0 || wait > poolQueuePoll {
		wait = poolQueuePoll
	}
	observation.PollAfter = wait
	observation.Queue = &generation.UpstreamQueue{}
	return observation
}

// poolFullRetryAfter reports an explicit code 2000 refusal (the account's
// concurrency pool is full) and its Retry-After. It is not a per-format rate
// limit: the caller releases its slot and queues again without spending the
// retry budget (C4).
func poolFullRetryAfter(err error) (time.Duration, bool) {
	if _, refused := explicitRateLimit(err); !refused {
		return 0, false
	}
	var provider *generation.ProviderError
	if !errors.As(err, &provider) || provider.Failure == nil {
		return 0, false
	}
	if provider.Failure.UpstreamCode != "2000" {
		return 0, false
	}
	return provider.Failure.RetryAfter, true
}

// terminalPhase reports whether an observation ends the attempt.
func terminalPhase(phase generation.UpstreamPhase) bool {
	switch phase {
	case generation.UpstreamCompleted, generation.UpstreamFailed, generation.UpstreamCanceled:
		return true
	default:
		return false
	}
}

// restoreConversionItem puts a format's entry back the way it was before a
// dispatch attempt that never reached the upstream (a full pool). An absent
// entry is removed again so the format still counts as unstarted.
func restoreConversionItem(
	conversions map[string]conversionItem,
	format string,
	previous conversionItem,
	existed bool,
) {
	if existed {
		conversions[format] = previous
		return
	}
	delete(conversions, format)
}
