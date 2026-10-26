package generation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Providers with source ordering metadata supply it verbatim. For protocols
// without a sequence/timestamp, phase and progress form a conservative partial
// order; stale observations must not overwrite opaque continuation state.
func staleObservation(task Task, attempt *Attempt, obs Observation) bool {
	if attempt == nil || task.State.Terminal() || task.State == TaskMaterializing {
		return true
	}
	// 已失败的 Attempt 是历史事实：它的迟到回执（含同 remote ID 的
	// Webhook）不得把失败 Attempt/任务改回进行中（复活路径）。
	if attempt.State.Terminal() {
		return true
	}
	if obs.Job != nil && attempt.UpstreamJob != nil && *obs.Job != *attempt.UpstreamJob {
		return true
	}
	if obs.Sequence > 0 && attempt.LastObservationSequence > 0 && obs.Sequence <= attempt.LastObservationSequence {
		return true
	}
	if !obs.ObservedAt.IsZero() && !attempt.LastObservationAt.IsZero() {
		if obs.ObservedAt.Before(attempt.LastObservationAt) {
			return true
		}
		if obs.Sequence == 0 && obs.ObservedAt.Equal(attempt.LastObservationAt) && obs.Phase == attempt.LastObservationPhase && obs.Progress <= task.Progress {
			return true
		}
	}
	if obs.Sequence == 0 && obs.ObservedAt.IsZero() && obs.Phase == attempt.LastObservationPhase && (obs.Phase == UpstreamAccepted || obs.Phase == UpstreamRunning) && obs.Progress <= task.Progress {
		return true
	}
	if attempt.LastObservationPhase == UpstreamRunning && obs.Phase == UpstreamAccepted {
		return true
	}
	if (obs.Phase == UpstreamAccepted || obs.Phase == UpstreamRunning) && obs.Progress < task.Progress {
		return true
	}
	return false
}

// A polling response may append private evidence at identical public progress.
// This deliberately does not weaken staleObservation or supply source ordering.
// The repository's version/token/expiry predicate remains the final lease fence.
func validPrivateCheckpointUpdate(lease Lease, attempt *Attempt, obs Observation, eventType string) bool {
	if eventType != "provider.observe" || lease.Token == "" || lease.Task.State != TaskRunning ||
		attempt == nil || lease.CurrentAttempt == nil || attempt.State != AttemptRunning ||
		lease.CurrentAttempt.ID != attempt.ID || attempt.ID == "" || attempt.TaskID != lease.Task.ID ||
		lease.Task.CurrentAttemptID != attempt.ID || !bytes.Equal(attempt.Opaque, lease.CurrentAttempt.Opaque) {
		return false
	}
	if (obs.Phase != UpstreamAccepted && obs.Phase != UpstreamRunning) ||
		obs.Phase != attempt.LastObservationPhase || obs.Progress != lease.Task.Progress ||
		obs.Job == nil || attempt.UpstreamJob == nil || *obs.Job != *attempt.UpstreamJob ||
		obs.Sequence != 0 || attempt.LastObservationSequence != 0 ||
		!obs.ObservedAt.IsZero() || !attempt.LastObservationAt.IsZero() ||
		obs.Dispatch != nil || obs.Failure != nil || len(obs.Artifacts) != 0 || len(obs.ResourceUpdates) != 0 ||
		obs.DedupeKey == "" || obs.DedupeKey == attempt.LastObservationKey ||
		len(obs.Opaque) == 0 || !json.Valid(obs.Opaque) || bytes.Equal(obs.Opaque, attempt.Opaque) {
		return false
	}
	digest := sha256.Sum256(attempt.Opaque)
	return obs.CheckpointBaseSHA256 == hex.EncodeToString(digest[:])
}
