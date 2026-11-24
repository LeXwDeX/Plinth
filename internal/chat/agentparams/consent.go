package agentparams

// The keys below belonged to the retired conversational consent state machine
// (#491): an Agent-side "approved" flag that the generation tool used to claim.
// Paid steps are authorized by exact execution grants now and nothing writes
// these keys any more. They stay declared only so a row an earlier version
// persisted is still treated as private state: it never shows up in a
// parameter projection and never reaches a generation request. The rows are
// left in place; there is no destructive migration.
const (
	ConsentPendingKey   = "consent_pending"
	ConsentDecisionKey  = "consent_decision"
	ConsentApprovedKey  = "consent_approved"
	ConsentSnapshotKey  = "consent_snapshot"
	ConsentClaimKey     = "consent_claim"
	ConsentRequestIDKey = "consent_request_id"
	AgentTaskIDKey      = "task_id"
	// ImageCountKey holds the 2D image count the person set for this session
	// (#486). It is one value per session, not one per Offering: the count is
	// the person's own expectation, and Offerings name the parameter they price
	// by differently (n, max_images), so no parameter name can carry it. It is
	// private state like the retired consent keys: it never appears in a
	// parameter projection and never reaches a generation request under its own
	// name.
	ImageCountKey = "image_count"
)

func isPrivateAgentStateKey(key string) bool {
	switch key {
	case ConsentPendingKey, ConsentDecisionKey, ConsentApprovedKey, ConsentRequestIDKey,
		ConsentSnapshotKey, ConsentClaimKey, AgentTaskIDKey, ImageCountKey:
		return true
	default:
		return false
	}
}
