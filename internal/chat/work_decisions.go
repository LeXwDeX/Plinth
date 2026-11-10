package chat

import (
	"context"
	"sort"
)

// Explicit rejection writes and their reasons belong to #241. Until that
// writer lands, the ledger still shows the directions the existing records can
// prove were dropped instead of an empty or invented list.
//
// SessionDecisionReader projects those facts from the persisted turn options:
// `TurnOptions.Selection` is the set of objects the person selected for that
// turn, and `SelectionPicked` marks a selection that is only the session pick
// being carried into the turn, not a new choice. The newest turn with an
// explicit selection is the latest chosen set; an object chosen by an earlier
// turn and absent from that set is superseded. The session pick supersedes that
// set only when a turn newer than it carried the pick, proving the pick was set
// afterwards. A superseded object is reported as a rejected direction with the
// Schema's superseded_reason.
//
// A session whose existing records prove nothing returns no decisions, so the
// summary omits the block instead of rendering an empty "do not do again" list.
// The reader never writes, adds no migration and never widens owner or session
// scope.
type SessionDecisionReader struct {
	repository Repository
	reason     string
	// maximumTurns bounds the persisted turn options read. The rejected block
	// itself is bounded by the Schema; this reader keeps the read bounded too.
	maximumTurns int
}

// workDecisionTurnLimit is how many newest turns the projection reads.
const workDecisionTurnLimit = 100

// NewSessionDecisionReader builds the production decision reader from the
// existing session records. reason is the Schema's superseded_reason text.
func NewSessionDecisionReader(repository Repository, reason string) SessionDecisionReader {
	return SessionDecisionReader{repository: repository, reason: reason, maximumTurns: workDecisionTurnLimit}
}

// WorkDecisions reports the chosen generated results a later selection or pick
// superseded, newest first. It reads only the session's persisted turn options
// and pick.
func (reader SessionDecisionReader) WorkDecisions(ctx context.Context, ownerID, sessionID string) ([]WorkDecision, error) {
	if reader.repository == nil {
		return nil, nil
	}
	limit := reader.maximumTurns
	if limit <= 0 {
		limit = workDecisionTurnLimit
	}
	options, err := reader.repository.RecentSessionTurnOptions(ctx, ownerID, sessionID, limit)
	if err != nil {
		return nil, err
	}
	// explicit holds the chosen Task sets of turns where the person selected
	// something themselves, newest first. A turn that only carried the session
	// pick (SelectionPicked) is not a new choice.
	var explicit []map[string]bool
	latestExplicit := -1
	pick, found, err := reader.repository.SessionPick(ctx, ownerID, sessionID)
	if err != nil {
		return nil, err
	}
	current := ""
	if found {
		current = pick.TaskID
	}
	pickEvidence := -1
	for index, option := range options {
		if option.SelectionPicked {
			if current != "" && pickEvidence < 0 && selectionContains(option.Selection, current) {
				// A later turn carried the current pick, so the pick existed
				// after that turn; this is the evidence that it is newer than
				// an explicit selection further down the list.
				pickEvidence = index
			}
			continue
		}
		set := map[string]bool{}
		for _, attachment := range option.Selection {
			if attachment.TaskID != "" {
				set[attachment.TaskID] = true
			}
		}
		if len(set) == 0 {
			continue
		}
		if latestExplicit < 0 {
			latestExplicit = index
		}
		explicit = append(explicit, set)
	}
	if len(explicit) == 0 {
		return nil, nil
	}
	final := explicit[0]
	if current != "" && !final[current] && pickEvidence >= 0 && pickEvidence < latestExplicit {
		// A newer turn carried the pick, proving it was set after the last
		// explicit selection; the selected object is superseded.
		final = map[string]bool{current: true}
	}
	rejected := make([]WorkDecision, 0, len(explicit))
	seen := map[string]bool{}
	for _, set := range explicit {
		taskIDs := make([]string, 0, len(set))
		for taskID := range set {
			if final[taskID] || seen[taskID] {
				continue
			}
			seen[taskID] = true
			taskIDs = append(taskIDs, taskID)
		}
		sort.Strings(taskIDs)
		for _, taskID := range taskIDs {
			rejected = append(rejected, WorkDecision{
				TaskID: taskID,
				State:  WorkDecisionRejected,
				Reason: reader.reason,
			})
		}
	}
	return rejectedOrNil(rejected), nil
}

// selectionContains reports whether a turn's selection names the Task.
func selectionContains(selection []Attachment, taskID string) bool {
	for _, attachment := range selection {
		if attachment.TaskID == taskID {
			return true
		}
	}
	return false
}
