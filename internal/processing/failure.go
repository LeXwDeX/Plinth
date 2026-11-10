package processing

import "github.com/LeXwDeX/Plinth/internal/generation"

// ClassifyStepFailure maps a plan step's stored reason (and, when readable,
// the failed Task's own failure) to the Agent-actionable vocabulary (#251).
// Plan reasons come first because they describe the plan's own rules; an
// ordinary upstream failure falls back to the Task's class. Unknown internal
// details never leave this function: the report holds only category, action
// and the public retry flags.
func ClassifyStepFailure(reason string, task *generation.Task) generation.FailureReport {
	switch reason {
	case ReasonSourceIncompatible, ReasonSourceExpired,
		ReasonRetrySourceIncompatible, ReasonRetrySourceExpired, ReasonResumeSourceIncompatible,
		ReasonResumeSourceExpired, ReasonResumeFrozenExpired:
		return generation.FailureReport{Category: generation.CategoryInputInvalid, Action: generation.ActionFixInput}
	case ReasonSubmitRejected:
		return generation.FailureReport{Category: generation.CategoryParameterInvalid, Action: generation.ActionFixParameters}
	case ReasonTaskCanceled:
		return generation.FailureReport{Category: generation.CategoryUpstreamRefused, Action: generation.ActionAskUser}
	case ReasonOfferingChanged, ReasonOfferingUnavailable, ReasonPrepareUnavailable:
		return generation.FailureReport{Category: generation.CategoryUpstreamCapacity, Action: generation.ActionRetryLater}
	case ReasonSubmitUnknown, ReasonSubmitUnreconciled, ReasonReconciledAbsent, ReasonReconciledMismatch:
		return generation.FailureReport{Category: generation.CategoryInternal, Action: generation.ActionContactAdmin,
			UserDecisionRequired: true}
	case ReasonCheckpointFailed:
		return generation.FailureReport{Category: generation.CategoryParameterInvalid, Action: generation.ActionFixParameters}
	case ReasonCheckpointUnavailable:
		return generation.FailureReport{Category: generation.CategoryInternal, Action: generation.ActionAskUser}
	case ReasonTaskFailed, "":
		if task != nil && task.Failure != nil {
			return generation.ClassifyFailure(task.Failure.Class, task.Failure.Retryable, task.Failure.Ambiguous)
		}
		return generation.ClassifyFailure(generation.FailureInternal, false, false)
	}
	return generation.ClassifyFailure(generation.FailureInternal, false, false)
}
