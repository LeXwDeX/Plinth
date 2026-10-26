package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func approveOwnedTaskForCreate(t *testing.T, repository *Repository, task generation.Task) generation.Task {
	t.Helper()
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
	preparedID := generation.PreparedInputID("prepared-sqlite-test:" + string(task.ID))
	digest := sha256.Sum256([]byte(string(task.OwnerID) + "\x00" + task.SessionID + "\x00" + string(task.Offering.ID) + "\x00" + task.Offering.Revision + "\x00" + string(task.Input)))
	preparedHash := hex.EncodeToString(digest[:])
	prepared := generation.PreparedInput{ID: preparedID, OwnerID: task.OwnerID, SessionID: task.SessionID,
		Capability: task.Capability, OfferingID: task.Offering.ID, OfferingRevision: task.Offering.Revision,
		SchemaVersion: "sqlite-test-v1", NormalizedInput: append(json.RawMessage(nil), task.Input...),
		Assets: []generation.PreparedInputAsset{}, Dependencies: []generation.PreparedInputDependency{},
		InputHash: preparedHash, CreatedAt: time.Now().UTC()}
	ctx := context.Background()
	if err := repository.CreatePreparedInput(ctx, prepared); err != nil {
		t.Fatal(err)
	}
	principal := executiongrant.Principal{OwnerID: string(task.OwnerID), ActorID: "sqlite-test-human", Source: executiongrant.HumanBrowser}
	operationID := "sqlite-test-operation:" + string(task.ID)
	grants := executiongrant.Service{Grants: repository, Prepared: repository}
	grant, err := grants.Prepare(ctx, executiongrant.PrepareCommand{Principal: principal,
		SessionID: task.SessionID, OfferingID: string(task.Offering.ID), OfferingRevision: task.Offering.Revision,
		PreparedInputID: string(preparedID), PreparedInputHash: preparedHash,
		IdempotencyKey: task.IdempotencyKey, OperationID: operationID})
	if err != nil {
		t.Fatal(err)
	}
	if grant.Status != executiongrant.Approved {
		grant, err = grants.Decide(ctx, principal, grant.RequestID, executiongrant.Approve)
		if err != nil {
			t.Fatal(err)
		}
	}
	task.Admission = &generation.ExecutionAdmission{RequestID: grant.RequestID, PreparedInputID: preparedID,
		PreparedInputHash: preparedHash, OperationID: operationID}
	return task
}
