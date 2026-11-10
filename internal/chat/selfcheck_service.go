package chat

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// ErrSelfCheckUnavailable refuses the self-check store before assembly
// installed one.
var ErrSelfCheckUnavailable = errors.New("chat: self-check record unavailable")

// errSelfCheckNoCheck makes AttachCheckHandles a no-op when the check it
// names is not in the record; it never reaches the caller.
var errSelfCheckNoCheck = errors.New("chat: no matching self-check evidence")

// SelfCheckStore is the narrow persistence port of the self-check records.
// SaveSelfCheck writes record only when the stored revision still equals
// expectedRevision (zero for a new record) and reports conflict when another
// writer changed it in between. Every method is scoped to the owner and the
// session; a foreign session is indistinguishable from a missing one.
type SelfCheckStore interface {
	ListSelfChecks(ctx context.Context, owner, session string) ([]SelfCheckRecord, error)
	SelfCheck(ctx context.Context, owner, session, taskID string) (SelfCheckRecord, bool, error)
	SaveSelfCheck(ctx context.Context, owner, session string, record SelfCheckRecord,
		expectedRevision int64) (SelfCheckRecord, bool, error)
}

// SelfCheckService owns the read-modify-write of the records. It uses the
// Schema's states and limits only; it never creates a Task, spends credits or
// changes the session.
type SelfCheckService struct {
	store  SelfCheckStore
	schema catalog.ChatSelfCheckSchema
	now    func() time.Time
}

// NewSelfCheckService builds the record service. A nil store keeps it
// unavailable (historical Catalogs and tests).
func NewSelfCheckService(store SelfCheckStore, schema catalog.ChatSelfCheckSchema) *SelfCheckService {
	return &SelfCheckService{store: store, schema: schema, now: time.Now}
}

// Schema returns the declared self-check block.
func (service *SelfCheckService) Schema() catalog.ChatSelfCheckSchema {
	if service == nil {
		return catalog.ChatSelfCheckSchema{}
	}
	return service.schema
}

func (service *SelfCheckService) available() bool {
	return service != nil && service.store != nil
}

// List returns the owner's records of one session.
func (service *SelfCheckService) List(ctx context.Context, owner, session string) ([]SelfCheckRecord, error) {
	if !service.available() {
		return nil, ErrSelfCheckUnavailable
	}
	return service.store.ListSelfChecks(ctx, owner, session)
}

// Get returns one record of the owner's session.
func (service *SelfCheckService) Get(ctx context.Context, owner, session, taskID string) (SelfCheckRecord, bool, error) {
	if !service.available() {
		return SelfCheckRecord{}, false, ErrSelfCheckUnavailable
	}
	return service.store.SelfCheck(ctx, owner, session, taskID)
}

// Counts derives the persisted counters of a record.
func (service *SelfCheckService) Counts(record SelfCheckRecord) SelfCheckCounters {
	return SelfCheckCounts(service.schema, record)
}

// WritableStatuses lists the status ids a conclusion may name (every declared
// role except unchecked).
func (service *SelfCheckService) WritableStatuses() []string {
	var statuses []string
	for _, status := range service.schema.Statuses {
		if status.Role != SelfCheckRoleUnchecked {
			statuses = append(statuses, status.ID)
		}
	}
	return statuses
}

// StatusIDs lists every declared status id, for an unknown-status refusal.
func (service *SelfCheckService) StatusIDs() []string {
	statuses := make([]string, 0, len(service.schema.Statuses))
	for _, status := range service.schema.Statuses {
		statuses = append(statuses, status.ID)
	}
	return statuses
}

// CheckpointIDs lists the plan check ids the Schema declares.
func (service *SelfCheckService) CheckpointIDs() []string {
	ids := make([]string, 0, len(service.schema.Checkpoints))
	for _, checkpoint := range service.schema.Checkpoints {
		ids = append(ids, checkpoint.ID)
	}
	return ids
}

// AppendCheck records one real tool call. A successful inspect opens a new
// round (its number is the count of inspect checks); render, compare and
// reference evidence belongs to the current round. New evidence invalidates a
// written conclusion, so the delivery state returns to unchecked.
func (service *SelfCheckService) AppendCheck(ctx context.Context, owner, session, taskID, modelAssetID string,
	check SelfCheckCheck) (SelfCheckRecord, error) {
	if !service.available() {
		return SelfCheckRecord{}, ErrSelfCheckUnavailable
	}
	if strings.TrimSpace(taskID) == "" {
		return SelfCheckRecord{}, errors.New("chat: self-check needs a task")
	}
	record, err := service.mutate(ctx, owner, session, taskID, modelAssetID, func(record *SelfCheckRecord) error {
		counter := SelfCheckCounts(service.schema, *record)
		if check.Kind == SelfCheckKindInspect {
			check.Round = counter.RoundsUsed + 1
		} else {
			check.Round = counter.RoundsUsed
			if check.Round < 1 {
				check.Round = 1
			}
		}
		if check.At.IsZero() {
			check.At = service.now().UTC()
		}
		record.Checks = append(record.Checks, check)
		// New evidence makes an older conclusion stale: the state goes back to
		// unchecked rather than claiming a result the evidence no longer
		// supports.
		record.Conclusion = nil
		return nil
	})
	return record, err
}

// AttachCheckHandles names the pictures a recorded check produced once the
// reply's attachments have assigned them. It finds the newest check of the
// same tool without handles and leaves the conclusion untouched: naming a
// picture is not new evidence.
func (service *SelfCheckService) AttachCheckHandles(ctx context.Context, owner, session, taskID, tool string,
	handles []string) error {
	if !service.available() || strings.TrimSpace(taskID) == "" || strings.TrimSpace(tool) == "" || len(handles) == 0 {
		return nil
	}
	_, err := service.mutate(ctx, owner, session, taskID, "", func(record *SelfCheckRecord) error {
		for index := len(record.Checks) - 1; index >= 0; index-- {
			if record.Checks[index].Tool == tool && len(record.Checks[index].Handles) == 0 {
				record.Checks[index].Handles = append([]string(nil), handles...)
				return nil
			}
		}
		return errSelfCheckNoCheck
	})
	if errors.Is(err, errSelfCheckNoCheck) {
		return nil
	}
	return err
}

// RecordConclusion writes the Agent's conclusion after applying the Schema's
// rules.
func (service *SelfCheckService) RecordConclusion(ctx context.Context, owner, session, taskID,
	modelAssetID string, conclusion SelfCheckConclusion) (SelfCheckRecord, error) {
	if !service.available() {
		return SelfCheckRecord{}, ErrSelfCheckUnavailable
	}
	if strings.TrimSpace(taskID) == "" {
		return SelfCheckRecord{}, errors.New("chat: self-check needs a task")
	}
	conclusion.Status = strings.TrimSpace(conclusion.Status)
	conclusion.Summary = strings.TrimSpace(conclusion.Summary)
	conclusion.Findings = selfCheckTrimmed(conclusion.Findings)
	conclusion.Fixes = selfCheckTrimmed(conclusion.Fixes)
	conclusion.Remaining = selfCheckTrimmed(conclusion.Remaining)
	var validation *SelfCheckConclusionError
	record, err := service.mutate(ctx, owner, session, taskID, modelAssetID, func(record *SelfCheckRecord) error {
		validation = ValidateSelfCheckConclusion(service.schema, *record, conclusion)
		if validation != nil {
			return validation
		}
		if conclusion.At.IsZero() {
			conclusion.At = service.now().UTC()
		}
		next := conclusion
		record.Conclusion = &next
		return nil
	})
	if validation != nil {
		return SelfCheckRecord{}, validation
	}
	return record, err
}

// IncrementCorrectionsOnce counts one paid correction step against the result
// it changes. key identifies the paid step (an operation, a tool call); a key
// that is already on the record makes this a no-op, so a restarted turn and a
// retried detached approval cannot count the same submission twice. A
// correction is new evidence too, so a written conclusion is invalidated and
// the state returns to unchecked. A result without a record stays without one:
// a correction of an unchecked result is already visible as unchecked, and
// recording a paid step as self-check evidence would be false.
func (service *SelfCheckService) IncrementCorrectionsOnce(ctx context.Context, owner, session, taskID,
	key string) (SelfCheckRecord, bool, error) {
	if !service.available() {
		return SelfCheckRecord{}, false, ErrSelfCheckUnavailable
	}
	record, found, err := service.Get(ctx, owner, session, taskID)
	if err != nil || !found {
		return SelfCheckRecord{}, found, err
	}
	key = strings.TrimSpace(key)
	if key != "" && slices.Contains(record.CorrectionKeys, key) {
		return record, true, nil
	}
	next, err := service.mutate(ctx, owner, session, taskID, record.ModelAssetID, func(record *SelfCheckRecord) error {
		if key != "" {
			if slices.Contains(record.CorrectionKeys, key) {
				return nil
			}
			record.CorrectionKeys = append(record.CorrectionKeys, key)
		}
		record.CorrectionsUsed++
		record.Conclusion = nil
		return nil
	})
	return next, true, err
}

// mutate is the revision compare-and-swap of both writers. It reads the record,
// lets apply change it and retries once when another writer won the race.
func (service *SelfCheckService) mutate(ctx context.Context, owner, session, taskID, modelAssetID string,
	apply func(*SelfCheckRecord) error) (SelfCheckRecord, error) {
	for attempt := 0; attempt < 2; attempt++ {
		record, found, err := service.Get(ctx, owner, session, taskID)
		if err != nil {
			return SelfCheckRecord{}, err
		}
		if !found {
			record = SelfCheckRecord{TaskID: taskID, CreatedAt: service.now().UTC()}
			record.Checks = []SelfCheckCheck{}
		}
		if strings.TrimSpace(modelAssetID) != "" {
			record.ModelAssetID = modelAssetID
		}
		if err := apply(&record); err != nil {
			return SelfCheckRecord{}, err
		}
		record.TaskID = taskID
		record.UpdatedAt = service.now().UTC()
		saved, conflict, err := service.store.SaveSelfCheck(ctx, owner, session, record, record.Revision)
		if err != nil {
			return SelfCheckRecord{}, err
		}
		if !conflict {
			return saved, nil
		}
	}
	return SelfCheckRecord{}, fmt.Errorf("chat: self-check record changed concurrently")
}

func selfCheckTrimmed(values []string) []string {
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			trimmed = append(trimmed, value)
		}
	}
	return trimmed
}
