package generation

// The Agent-actionable failure vocabulary (#251). Every failed Task or plan
// step is classified into one of five categories and one suggested action, so
// the Agent can decide whether to change the input, change parameters, wait,
// retry, ask the person or stop. The internal failure class and its SafeDetail
// stay server-side; only the category and action reach the Agent.
type FailureCategory string

const (
	// CategoryInputInvalid: the source or input does not satisfy the service.
	CategoryInputInvalid FailureCategory = "input_invalid"
	// CategoryParameterInvalid: the parameters are outside what the service
	// accepts, or the produced result missed a declared checkpoint.
	CategoryParameterInvalid FailureCategory = "parameter_invalid"
	// CategoryUpstreamCapacity: the provider is busy, throttled, timed out or
	// temporarily unavailable; waiting or retrying later is reasonable.
	CategoryUpstreamCapacity FailureCategory = "upstream_capacity"
	// CategoryUpstreamRefused: the provider refused the job, the account is
	// out of balance/quota or authentication failed; a retry alone will not help.
	CategoryUpstreamRefused FailureCategory = "upstream_refused"
	// CategoryInternal: the platform could not complete the job or cannot prove
	// whether it was charged; support or the person must decide.
	CategoryInternal FailureCategory = "internal"
)

type FailureAction string

const (
	ActionRetry         FailureAction = "retry"
	ActionRetryLater    FailureAction = "retry_later"
	ActionFixInput      FailureAction = "fix_input"
	ActionFixParameters FailureAction = "fix_parameters"
	ActionAskUser       FailureAction = "ask_user"
	ActionContactAdmin  FailureAction = "contact_admin"
)

// FailureCategories and FailureActions list the declared vocabulary in a
// stable order; the Product Schema validation and tests align with it.
func FailureCategories() []FailureCategory {
	return []FailureCategory{
		CategoryInputInvalid, CategoryParameterInvalid, CategoryUpstreamCapacity,
		CategoryUpstreamRefused, CategoryInternal,
	}
}

func FailureActions() []FailureAction {
	return []FailureAction{
		ActionRetry, ActionRetryLater, ActionFixInput, ActionFixParameters,
		ActionAskUser, ActionContactAdmin,
	}
}

// FailureReport is the Agent-facing classification of one failure. It never
// carries SafeDetail or an upstream excerpt.
type FailureReport struct {
	Category FailureCategory `json:"category"`
	Action   FailureAction   `json:"next_action"`
	// Retryable is the kernel's own judgement; UserDecisionRequired marks a
	// retry that may already have been charged and must be the person's call.
	Retryable            bool `json:"retryable"`
	UserDecisionRequired bool `json:"user_decision_required,omitempty"`
}

// ClassifyFailure maps a Task failure class to the actionable category and
// action. ambiguous means the upstream may have run and charged: the report
// then requires the person's decision and never offers an automatic retry.
func ClassifyFailure(class FailureClass, retryable, ambiguous bool) FailureReport {
	report := FailureReport{Retryable: retryable, UserDecisionRequired: ambiguous}
	switch class {
	case FailureInvalidInput, FailureContentRejected, FailureSourceExpired:
		report.Category, report.Action = CategoryInputInvalid, ActionFixInput
	case FailureRateLimited, FailureUpstreamUnavailable, FailureUpstreamQueueTimeout,
		FailureTimedOut, FailureTransport:
		report.Category, report.Action = CategoryUpstreamCapacity, ActionRetryLater
	case FailureAuthentication, FailureCredentialQuota, FailureInsufficientBalance,
		FailureUpstreamDenied, FailureRejected:
		report.Category, report.Action = CategoryUpstreamRefused, ActionAskUser
	case FailureSubmissionUnknown:
		report.Category, report.Action = CategoryUpstreamCapacity, ActionAskUser
		report.UserDecisionRequired = true
	case FailureIntegrity, FailureProtocol, FailureInternal:
		report.Category, report.Action = CategoryInternal, ActionContactAdmin
	default:
		report.Category, report.Action = CategoryInternal, ActionContactAdmin
	}
	if report.UserDecisionRequired {
		report.Retryable = false
		report.Action = ActionAskUser
	}
	return report
}
