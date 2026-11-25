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
	// LegacyAgentTaskIDKey is the task id the retired PUT /api/v1/agent/bind
	// stored per session (#511). Nothing writes or reads it any more; it stays a
	// private key so rows an older server stored never reach a parameter
	// projection or a generation request.
	LegacyAgentTaskIDKey = "task_id"
	// LegacyImageCountKey is the session-wide 2D image count the retired
	// panel control stored (#486, #564). The count is the Agent's call now,
	// made from what the person says (#586), and nothing writes or reads the
	// key. It stays a private key so rows an older server stored never reach a
	// parameter projection or a generation request; they are left in place.
	LegacyImageCountKey = "image_count"
)

func isPrivateAgentStateKey(key string) bool {
	switch key {
	case ConsentPendingKey, ConsentDecisionKey, ConsentApprovedKey, ConsentRequestIDKey,
		ConsentSnapshotKey, ConsentClaimKey, LegacyAgentTaskIDKey, LegacyImageCountKey:
		return true
	default:
		return false
	}
}
