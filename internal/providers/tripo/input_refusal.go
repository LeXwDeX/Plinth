package tripo

import (
	"fmt"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// inputRefusal keeps the Provider diagnostic as SafeDetail/audit evidence and
// adds the Offering's Schema refusal for the Agent (#358).
func inputRefusal(o generation.Offering, reason schema.InputRefusalReason, values schema.InputRefusalValues, diagnostic string) error {
	return generation.ProviderInputRefusal(o, reason, values, generation.FailureInvalidInput, false, diagnostic)
}

// inputUnavailable is the same for a temporary server condition: the step may
// succeed later, so the kernel reports ErrUnavailable and the reason is still
// Schema copy.
func inputRefusalWithCause(o generation.Offering, reason schema.InputRefusalReason, values schema.InputRefusalValues, diagnostic string, cause error) error {
	return generation.ProviderInputRefusalFailure(
		&generation.Failure{Class: generation.FailureInvalidInput, SafeDetail: diagnostic},
		o, reason, values, diagnostic, cause)
}

func inputParametersRefusal(o generation.Offering, cause error) error {
	return inputRefusalWithCause(o, schema.InputRefusalParameters, schema.InputRefusalValues{},
		"input parameters invalid", cause)
}

func inputUnavailable(o generation.Offering, diagnostic string, cause error) error {
	return generation.ProviderInputRefusalFailure(
		&generation.Failure{Class: generation.FailureTransport, Retryable: true},
		o, schema.InputRefusalUnavailable, schema.InputRefusalValues{}, diagnostic, cause)
}

// inputParameterRefusal refuses one input parameter with its public Schema
// label. The parameter name and cause stay in the internal error chain; the
// Provider SafeDetail and the Agent-facing text never name them.
func inputParameterRefusal(o generation.Offering, p generation.Parameter, cause error) error {
	reason := schema.InputRefusalParameters
	values := schema.InputRefusalValues{}
	if strings.TrimSpace(p.Label) != "" {
		reason = schema.InputRefusalParameter
		values.Label = p.Label
	}
	internal := parameterDiagnostic(p.Name, cause)
	refusal := o.ExecutionMessages.InputRefusal(reason, values)
	failure := &generation.Failure{Class: generation.FailureInvalidInput, SafeDetail: "input parameter invalid"}
	if refusal == nil {
		return &generation.ProviderError{Failure: failure, Cause: internal}
	}
	refusal.Cause = internal
	return &generation.ProviderError{Failure: failure, Cause: refusal}
}

func parameterDiagnostic(name string, cause error) error {
	switch {
	case name != "" && cause != nil:
		return fmt.Errorf("parameter %q: %w", name, cause)
	case name != "":
		return fmt.Errorf("parameter %q", name)
	default:
		return cause
	}
}
