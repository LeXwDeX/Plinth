package sqlite_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// approveCreateFixture persists a prepared snapshot, records an explicit human
// decision, then returns the Task carrying the admission reference consumed by
// Repository.Create in the same transaction as Task/event insertion.
func approveCreateFixture(t *testing.T, repository *sqlite.Repository, task generation.Task) generation.Task {
	t.Helper()
	ctx := context.Background()
	if task.ID == "" || task.OwnerID == "" || task.Offering.ID == "" {
		t.Fatal("approved Create fixture requires task, owner, and offering identities")
	}
	if task.SessionID == "" {
		task.SessionID = "sqlite-test-session:" + string(task.OwnerID)
	}
	owned, err := repository.OwnsSession(ctx, string(task.OwnerID), task.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !owned {
		now := time.Now().UTC()
		if _, err := repository.CreateSession(ctx, chat.Session{
			ID: task.SessionID, OwnerID: string(task.OwnerID), OwnerName: "SQLite test human",
			Title: "SQLite admission fixture", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if task.IdempotencyKey == "" {
		task.IdempotencyKey = "sqlite-test:" + string(task.ID)
	}
	if task.Offering.Revision == "" {
		task.Offering.Revision = "sqlite-test-revision"
	}
	if len(task.Input) == 0 {
		task.Input = json.RawMessage(`{}`)
	}
	task.RequestHash = generation.ExpectedRequestHash(task.Offering.ID, task.Input)
	preparedID := "prepared-sqlite-test:" + string(task.ID)
	preparedHashBytes := sha256.Sum256(append(append([]byte(string(task.OwnerID)+"\x00"+task.SessionID+"\x00"+string(task.Offering.ID)+"\x00"+task.Offering.Revision+"\x00"), task.Input...), []byte("\x00"+task.IdempotencyKey)...))
	preparedHash := hex.EncodeToString(preparedHashBytes[:])
	prepared := generation.PreparedInput{
		ID: generation.PreparedInputID(preparedID), OwnerID: task.OwnerID, SessionID: task.SessionID,
		Capability: task.Capability, OfferingID: task.Offering.ID, OfferingRevision: task.Offering.Revision,
		SchemaVersion: "sqlite-test-v1", NormalizedInput: append(json.RawMessage(nil), task.Input...),
		Assets: []generation.PreparedInputAsset{}, Dependencies: []generation.PreparedInputDependency{},
		InputHash: preparedHash, CreatedAt: time.Now().UTC(),
	}
	if err := repository.CreatePreparedInput(ctx, prepared); err != nil {
		t.Fatal(err)
	}
	principal := executiongrant.Principal{OwnerID: string(task.OwnerID), ActorID: "sqlite-test-human", Source: executiongrant.HumanBrowser}
	operationID := "sqlite-test-operation:" + string(task.ID)
	grants := executiongrant.Service{Grants: repository, Prepared: repository}
	grant, err := grants.Prepare(ctx, executiongrant.PrepareCommand{
		Principal: principal, SessionID: task.SessionID, OfferingID: string(task.Offering.ID),
		OfferingRevision: task.Offering.Revision, PreparedInputID: preparedID, PreparedInputHash: preparedHash,
		IdempotencyKey: task.IdempotencyKey, OperationID: operationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if grant.Status != executiongrant.Approved {
		grant, err = grants.Decide(ctx, principal, grant.RequestID, executiongrant.Approve)
		if err != nil {
			t.Fatal(err)
		}
	}
	task.Admission = &generation.ExecutionAdmission{
		RequestID: grant.RequestID, PreparedInputID: prepared.ID, PreparedInputHash: preparedHash,
		OperationID: operationID,
	}
	return task
}

func approvedCreateFixture(t *testing.T, repository *sqlite.Repository, task generation.Task, event generation.Event, maxInflight int) (generation.Task, bool, error) {
	t.Helper()
	task = approveCreateFixture(t, repository, task)
	event.TaskID = task.ID
	if event.CreatedAt.IsZero() {
		event.CreatedAt = task.CreatedAt
	}
	if event.Type == "" {
		event.Type = "task.submitted"
	}
	if len(event.Data) == 0 {
		event.Data = json.RawMessage(`{}`)
	}
	return repository.Create(context.Background(), task, event, maxInflight)
}

func mustApprovedCreate(t *testing.T, repository *sqlite.Repository, task generation.Task, event generation.Event, maxInflight int) generation.Task {
	t.Helper()
	created, replayed, err := approvedCreateFixture(t, repository, task, event, maxInflight)
	if err != nil || replayed {
		t.Fatalf("approved Create: replayed=%v err=%v", replayed, err)
	}
	return created
}

func fixtureSessionID(owner generation.OwnerID, suffix string) string {
	return fmt.Sprintf("sqlite-test-session:%s:%s", owner, suffix)
}
