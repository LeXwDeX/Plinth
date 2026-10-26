package generation

import "github.com/LeXwDeX/Plinth/internal/schema"

// ProviderInputRefusal wraps the Offering's Schema text for a synchronous
// input check as the ProviderError class the kernel already maps. The Cause
// is the UserFacing Schema refusal, so the Agent can write the reason in the
// turn's language; SafeDetail keeps the Provider's original diagnostic for
// logs and the audit trail. Without Schema text the diagnostic is returned
// unchanged, never replaced by a generic sentence.
func ProviderInputRefusal(
	offering Offering,
	reason schema.InputRefusalReason,
	values schema.InputRefusalValues,
	class FailureClass,
	retryable bool,
	diagnostic string,
) error {
	return ProviderInputRefusalFailure(&Failure{Class: class, Retryable: retryable}, offering, reason, values, diagnostic, nil)
}

// ProviderInputRefusalFailure keeps the original Provider failure metadata
// (upstream status/code/excerpt, retry timing and ambiguity) while adding the
// Offering's UserFacing refusal as Cause. The diagnostic replaces only
// SafeDetail when one is given.
func ProviderInputRefusalFailure(
	base *Failure,
	offering Offering,
	reason schema.InputRefusalReason,
	values schema.InputRefusalValues,
	diagnostic string,
	cause error,
) error {
	if base == nil {
		base = &Failure{Class: FailureInvalidInput}
	}
	failure := *base
	if diagnostic != "" {
		failure.SafeDetail = diagnostic
	}
	refusal := offering.ExecutionMessages.InputRefusal(reason, values)
	if refusal == nil {
		return &ProviderError{Failure: &failure, Cause: cause}
	}
	refusal.Cause = cause
	return &ProviderError{Failure: &failure, Cause: refusal}
}
