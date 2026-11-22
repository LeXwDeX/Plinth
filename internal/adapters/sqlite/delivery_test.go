package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
)

// #461: the platform's own plan and the grant its step asks for must survive the
// store as the platform, not be downgraded to a browser's: origin delivery, a
// grant of source delivery decided by policy:delivery, and an audit that names
// model-delivery. A plan read back as a browser plan would let a person's
// approval mode decide the platform's step (a card, a step cap) and spend the
// session's plan on it.

func deliveryFixture(t *testing.T, source string) (*Repository, executiongrant.Grant) {
	t.Helper()
	repository, err := openTestRepository(filepath.Join(t.TempDir(), "delivery.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := repository.db.ExecContext(ctx, `INSERT INTO go_chat_sessions(id, owner_id, owner_name, title, created_at, updated_at)
 VALUES ('session','owner','owner','test',?,?)`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	identity := executiongrant.Identity{
		OwnerID: "owner", SessionID: "session", OfferingID: "provider/model/convert", OfferingRevision: "rev-1",
		PreparedInputID: "prepared-1", PreparedInputHash: "hash-1",
		IdempotencyKey: "key-1", OperationID: "op-1",
	}
	if err := repository.CreatePreparedInput(ctx, generation.PreparedInput{
		ID: generation.PreparedInputID(identity.PreparedInputID), OwnerID: generation.OwnerID(identity.OwnerID),
		SessionID: identity.SessionID, Capability: generation.Capability("format-conversion"),
		OfferingID: generation.OfferingID(identity.OfferingID), OfferingRevision: identity.OfferingRevision,
		NormalizedInput: []byte(`{}`), InputHash: identity.PreparedInputHash, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	grant, err := repository.PreparePending(ctx, identity, source, "")
	if err != nil {
		t.Fatal(err)
	}
	return repository, grant
}

func TestDeliveryGrantIsApprovedByThePlatformItself(t *testing.T) {
	repository, prepared := deliveryFixture(t, executiongrant.Delivery)
	ctx := context.Background()
	approved, err := repository.ApproveExecutionGrantByPolicy(ctx, prepared.OwnerID, prepared.RequestID, executiongrant.DeliveryActor)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != executiongrant.Approved || approved.Source != executiongrant.Delivery ||
		approved.DecidedBy != executiongrant.DeliveryActor || approved.BudgetEligible {
		t.Fatalf("delivery grant = %+v", approved)
	}
	var actor string
	if err := repository.db.QueryRowContext(ctx,
		`SELECT actor_id FROM execution_grant_audit WHERE request_id = ? AND event = 'approved'`,
		prepared.RequestID).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	if actor != executiongrant.DeliveryActorID {
		t.Fatalf("audit actor = %q, want %q", actor, executiongrant.DeliveryActorID)
	}
}

// The source list is fixed on both sides: a person's grant is never approved as
// the platform's, and the platform's never by a session's mode.
func TestDeliveryGrantIsNotApprovedAsABrowsers(t *testing.T) {
	ctx := context.Background()
	for _, testCase := range []struct {
		name   string
		source string
		actor  string
	}{
		{"a browser grant asked of the platform", executiongrant.HumanBrowser, executiongrant.DeliveryActor},
		{"the platform grant asked of a mode", executiongrant.Delivery, executiongrant.PolicyActorPrefix + "auto"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repository, prepared := deliveryFixture(t, testCase.source)
			if _, err := repository.ApproveExecutionGrantByPolicy(ctx, prepared.OwnerID, prepared.RequestID, testCase.actor); !errors.Is(err, executiongrant.ErrState) {
				t.Fatalf("err = %v, want ErrState", err)
			}
			stored, err := repository.GetExecutionGrant(ctx, prepared.OwnerID, prepared.RequestID)
			if err != nil || stored.Status != executiongrant.Pending || stored.DecidedBy != "" {
				t.Fatalf("grant changed: %+v err=%v", stored, err)
			}
		})
	}
}

func TestProcessingPlanKeepsThePlatformOrigin(t *testing.T) {
	repository, err := openTestRepository(filepath.Join(t.TempDir(), "plans.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := repository.db.ExecContext(ctx, `INSERT INTO go_chat_sessions(id, owner_id, owner_name, title, created_at, updated_at)
 VALUES ('session','owner','owner','test',?,?)`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name   string
		origin string
	}{
		{"a browser plan", processing.OriginBrowser},
		{"an agent plan", processing.OriginAgent},
		{"the platform's plan", processing.OriginDelivery},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			plan := processing.Plan{ID: "plan-" + testCase.name, OwnerID: "owner", SessionID: "session",
				SourceTaskID: "task", SourceAssetID: "asset", State: processing.Pending,
				RequestHash: "request", Origin: testCase.origin,
				Steps:   []processing.Step{{OfferingID: "convert", State: processing.Pending, Revision: "v1"}},
				Version: 1, CreatedAt: now, UpdatedAt: now}
			if err := repository.CreateProcessingPlan(ctx, plan); err != nil {
				t.Fatal(err)
			}
			stored, err := repository.GetProcessingPlan(ctx, "owner", plan.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Origin != testCase.origin {
				t.Fatalf("origin = %q, want %q", stored.Origin, testCase.origin)
			}
		})
	}
}