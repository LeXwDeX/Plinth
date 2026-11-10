package pendingapproval_test

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/pendingapproval"
)

// correctionStore is the in-memory self-check record store of this file. The
// approval service's callback counts against it exactly like the production
// one does against SQLite.
type correctionStore struct {
	mu      sync.Mutex
	records map[string]chat.SelfCheckRecord
}

func newCorrectionStore() *correctionStore {
	return &correctionStore{records: map[string]chat.SelfCheckRecord{}}
}

func correctionKey(owner, session, task string) string {
	return owner + "\x00" + session + "\x00" + task
}

func (store *correctionStore) ListSelfChecks(_ context.Context, owner, session string) ([]chat.SelfCheckRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var records []chat.SelfCheckRecord
	for key, record := range store.records {
		if strings.HasPrefix(key, owner+"\x00"+session+"\x00") {
			records = append(records, record)
		}
	}
	return records, nil
}

func (store *correctionStore) SelfCheck(_ context.Context, owner, session, task string) (chat.SelfCheckRecord, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	record, found := store.records[correctionKey(owner, session, task)]
	return record, found, nil
}

func (store *correctionStore) SaveSelfCheck(_ context.Context, owner, session string,
	record chat.SelfCheckRecord, expected int64) (chat.SelfCheckRecord, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := correctionKey(owner, session, record.TaskID)
	stored, found := store.records[key]
	if expected <= 0 {
		if found {
			return chat.SelfCheckRecord{}, true, nil
		}
		record.Revision = 1
		store.records[key] = record
		return record, false, nil
	}
	if !found || stored.Revision != expected {
		return chat.SelfCheckRecord{}, true, nil
	}
	record.Revision = expected + 1
	store.records[key] = record
	return record, false, nil
}

// TestApprovedProcessingStepCountsOneCorrection is the #246 regression: a
// detached retexture/process/convert step submits only after the person
// approved it, and that submission is still a paid correction of the result it
// changes. The same operation settling again must not count it twice.
func TestApprovedProcessingStepCountsOneCorrection(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		tool       string
		capability generation.Capability
	}{
		{"retexture", "retexture_model", generation.Retexture},
		{"process", "process_model", generation.Remesh},
		{"convert", "convert_model_format", generation.FormatConversion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newCorrectionStore()
			selfChecks := chat.NewSelfCheckService(store, product.Application().Chat.SelfCheck)
			ctx := context.Background()
			if _, err := selfChecks.AppendCheck(ctx, "owner-a", "session-a", "task-model", "", chat.SelfCheckCheck{
				Kind: chat.SelfCheckKindInspect, Tool: "inspect_model", OK: true,
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := selfChecks.AppendCheck(ctx, "owner-a", "session-a", "task-model", "", chat.SelfCheckCheck{
				Kind: chat.SelfCheckKindRender, Tool: "render_model_views", OK: true,
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := selfChecks.RecordConclusion(ctx, "owner-a", "session-a", "task-model", "",
				chat.SelfCheckConclusion{Status: "passed", Summary: "looks right"}); err != nil {
				t.Fatal(err)
			}

			var counted []generationtool.Operation
			w := openWorldWithConfig(t, filepath.Join(t.TempDir(), "approval.db"), time.Hour, nil,
				func(config *pendingapproval.Config) {
					config.ProcessingCorrection = func(ctx context.Context, op generationtool.Operation) error {
						counted = append(counted, op)
						_, _, err := selfChecks.IncrementCorrectionsOnce(ctx, op.OwnerID, op.SessionID,
							op.CorrectionTaskID, generationtool.CorrectionKey(op))
						return err
					}
				})
			w.tool = tc.tool
			w.capability = tc.capability
			w.correctionTaskID = "task-model"
			operation, grant := w.pending("call")

			outcome, handled, err := w.service.Decide(ctx, w.operator, grant.RequestID, executiongrant.Approve)
			if err != nil || !handled || outcome.TaskID == "" {
				t.Fatalf("decide = %+v handled=%v err=%v", outcome, handled, err)
			}
			record, found, err := store.SelfCheck(ctx, "owner-a", "session-a", "task-model")
			if err != nil || !found {
				t.Fatalf("record = %+v err=%v", record, err)
			}
			if record.CorrectionsUsed != 1 || record.Conclusion != nil {
				t.Fatalf("approved correction was not counted once: %+v", record)
			}
			if len(counted) != 1 || counted[0].OwnerID != "owner-a" || counted[0].SessionID != "session-a" ||
				counted[0].CorrectionTaskID != "task-model" || counted[0].OperationID != operation.OperationID {
				t.Fatalf("correction callback = %+v", counted)
			}

			// The sweep converges an already settled operation without counting
			// it a second time; the callback is keyed by the operation for the
			// retry case.
			if err := w.service.Continue(ctx, operation); err != nil {
				t.Fatal(err)
			}
			record, _, err = store.SelfCheck(ctx, "owner-a", "session-a", "task-model")
			if err != nil {
				t.Fatal(err)
			}
			if record.CorrectionsUsed != 1 {
				t.Fatalf("settled approval counted again: %+v", record)
			}
		})
	}
}

// TestApprovedNonProcessingStepLeavesSelfChecksAlone guards the other side:
// a generation approval has no corrected result and must not touch any record.
func TestApprovedNonProcessingStepLeavesSelfChecksAlone(t *testing.T) {
	store := newCorrectionStore()
	called := 0
	w := openWorldWithConfig(t, filepath.Join(t.TempDir(), "approval.db"), time.Hour, nil,
		func(config *pendingapproval.Config) {
			config.ProcessingCorrection = func(context.Context, generationtool.Operation) error {
				called++
				return nil
			}
		})
	_, grant := w.pending("call")
	if _, handled, err := w.service.Decide(context.Background(), w.operator, grant.RequestID,
		executiongrant.Approve); err != nil || !handled {
		t.Fatalf("decide handled=%v err=%v", handled, err)
	}
	if called != 0 || len(store.records) != 0 {
		t.Fatalf("a generation approval counted a correction: called=%d", called)
	}
}
