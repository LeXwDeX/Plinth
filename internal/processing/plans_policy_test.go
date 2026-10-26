package processing

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// memoryGrants is a minimal in-memory grant store for the plan policy tests.
type memoryGrants struct {
	grants   map[string]executiongrant.Grant
	gateway  *gatewayFake
	approved int
}

func (m *memoryGrants) PreparePending(_ context.Context, identity executiongrant.Identity, source, turn string) (executiongrant.Grant, error) {
	for _, grant := range m.grants {
		if grant.OwnerID == identity.OwnerID && grant.IdempotencyKey == identity.IdempotencyKey {
			return grant, nil
		}
	}
	grant := executiongrant.Grant{RequestID: fmt.Sprintf("grant-%d", len(m.grants)+1), Identity: identity,
		Status: executiongrant.Pending, Source: source, TriggerTurnID: turn, RequestVersion: 1}
	m.grants[grant.RequestID] = grant
	return grant, nil
}

func (m *memoryGrants) DecideExecutionGrant(context.Context, string, string, string, executiongrant.Decision) (executiongrant.Grant, error) {
	return executiongrant.Grant{}, executiongrant.ErrState
}

func (m *memoryGrants) GetExecutionGrant(_ context.Context, owner, id string) (executiongrant.Grant, error) {
	grant, ok := m.grants[id]
	if !ok || grant.OwnerID != owner {
		return executiongrant.Grant{}, executiongrant.ErrConflict
	}
	return grant, nil
}

func (m *memoryGrants) ApproveExecutionGrantByPolicy(_ context.Context, owner, id, decidedBy string) (executiongrant.Grant, error) {
	grant, err := m.GetExecutionGrant(context.Background(), owner, id)
	if err != nil {
		return grant, err
	}
	if grant.Status != executiongrant.Pending {
		return grant, executiongrant.ErrState
	}
	grant.Status, grant.DecidedBy = executiongrant.Approved, decidedBy
	m.grants[id] = grant
	m.approved++
	return grant, nil
}

func (m *memoryGrants) GetExecutionPreparedSnapshot(_ context.Context, owner, id string) (executiongrant.PreparedSnapshot, error) {
	prepared, ok := m.gateway.prepared[generation.PreparedInputID(id)]
	if !ok || string(prepared.OwnerID) != owner {
		return executiongrant.PreparedSnapshot{}, executiongrant.ErrConflict
	}
	return executiongrant.PreparedSnapshot{ID: id, OwnerID: owner, SessionID: prepared.SessionID,
		OfferingID: string(prepared.OfferingID), OfferingRevision: prepared.OfferingRevision,
		InputHash: prepared.InputHash, Capability: string(prepared.Capability)}, nil
}

type fixedMode string

func (m fixedMode) SessionApprovalMode(context.Context, string, string) (string, error) {
	return string(m), nil
}

func policyPlanFixture(t *testing.T, capability, mode string) (*Service, *gatewayFake, *memoryGrants, Plan) {
	t.Helper()
	catalog := memoryCatalog{"step": {ID: "step", Revision: "v1", ProviderID: "meshy", Capability: generation.Capability(capability),
		Domain: generation.DomainProcessing, Enabled: true, EstimatedCredits: 5,
		Parameters: []generation.Parameter{{Name: "source_task", Type: "task", TaskCapabilities: []generation.Capability{"text-to-3d"}}}}}
	source := generation.Task{ID: "source", OwnerID: "owner", SessionID: "session", Capability: "text-to-3d", State: generation.TaskSucceeded,
		Offering: generation.Offering{ProviderID: "meshy"}, Assets: []generation.Asset{{ID: "model", Role: "model", StorageKey: "ready"}}}
	gateway := &gatewayFake{tasks: map[generation.TaskID]generation.Task{"source": source}, byKey: map[string]generation.TaskID{}}
	service, err := New(&memoryStore{plans: map[string]Plan{}}, catalog, gateway, nil)
	if err != nil {
		t.Fatal(err)
	}
	grants := &memoryGrants{grants: map[string]executiongrant.Grant{}, gateway: gateway}
	policy := approvalpolicy.New("manual", []string{"auto", "manual"},
		[]string{"text-to-3d", "image-to-3d", "retexture"},
		[]string{"text-to-3d", "image-to-3d", "retexture", "remesh"})
	service.WithGrants(&executiongrant.Service{Grants: grants, Prepared: grants, Policy: policy, Modes: fixedMode(mode)})
	plan, err := service.Create(context.Background(), CreateCommand{OwnerID: "owner", SessionID: "session", SourceTaskID: "source",
		SourceAssetID: "model", IdempotencyKey: "policy-plan-key-0001",
		Steps: []StepRequest{{OfferingID: "step", ExpectedOfferingRevision: "v1", Input: json.RawMessage(`{}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	return service, gateway, grants, plan
}

func TestProcessingStepApprovedByPolicySubmitsWithoutPausing(t *testing.T) {
	for _, tc := range []struct{ name, capability, mode string }{
		{"manual remesh", "remesh", "manual"},
		{"auto retexture", "retexture", "auto"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			service, gateway, grants, plan := policyPlanFixture(t, tc.capability, tc.mode)
			if err := service.advanceAll(ctx); err != nil {
				t.Fatal(err)
			}
			if len(gateway.commands) != 1 || grants.approved != 1 {
				t.Fatalf("commands=%d approvals=%d", len(gateway.commands), grants.approved)
			}
			got, err := service.Get(ctx, "owner", plan.ID)
			if err != nil || got.State == Paused || got.Steps[0].AuthorizationRef == "" {
				t.Fatalf("plan = %+v err=%v", got, err)
			}
			grant, err := grants.GetExecutionGrant(ctx, "owner", got.Steps[0].AuthorizationRef)
			if err != nil || !grant.ApprovedByPolicy() || grant.PreparedInputID != string(got.Steps[0].PreparedInputID) ||
				grant.IdempotencyKey != got.Steps[0].IdempotencyKey {
				t.Fatalf("grant not bound to the frozen step: %+v %v", grant, err)
			}
			// A second pass must not submit or approve again.
			if err := service.advanceAll(ctx); err != nil || len(gateway.commands) != 1 || grants.approved != 1 {
				t.Fatalf("replay: commands=%d approvals=%d err=%v", len(gateway.commands), grants.approved, err)
			}
		})
	}
}

func TestProcessingStepNeedingHumanStillPausesInManualMode(t *testing.T) {
	ctx := context.Background()
	service, gateway, grants, plan := policyPlanFixture(t, "retexture", "manual")
	if err := service.advanceAll(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := service.Get(ctx, "owner", plan.ID)
	if err != nil || got.State != Paused || got.Failure != ReasonConsentRequired || len(gateway.commands) != 0 || grants.approved != 0 {
		t.Fatalf("retexture must wait for a person: plan=%+v commands=%d approvals=%d err=%v",
			got, len(gateway.commands), grants.approved, err)
	}
	if got.Steps[0].AuthorizationRef != "" {
		t.Fatalf("authorization invented: %+v", got.Steps[0])
	}
}

func TestProcessingStepWithoutGrantServiceStillPauses(t *testing.T) {
	ctx := context.Background()
	service, _, gateway, _, plan := singleRemeshFixture(t)
	if err := service.advanceAll(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := service.Get(ctx, "owner", plan.ID)
	if err != nil || got.State != Paused || len(gateway.commands) != 0 {
		t.Fatalf("plan = %+v err=%v", got, err)
	}
}
