package processing

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #461: a delivery plan is the platform keeping its own promise. It is never
// downgraded to a browser plan, its steps are asked for as the platform rather
// than as a person, and the budget only counts an Agent plan.

func deliveryFixture(t *testing.T) (*Service, *gatewayFake, *budgetMemoryGrants, Plan) {
	t.Helper()
	return deliveryFixtureAs(t, OriginDelivery, "delivery-processing-plan-0001")
}

func deliveryFixtureAs(t *testing.T, origin, key string) (*Service, *gatewayFake, *budgetMemoryGrants, Plan) {
	t.Helper()
	product := memoryCatalog{"step": {ID: "step", Revision: "v1", ProviderID: "meshy",
		Capability: "format-conversion", Domain: generation.DomainProcessing, Enabled: true, EstimatedCredits: 1,
		Parameters: []generation.Parameter{{Name: "source_task", Type: "task", TaskCapabilities: []generation.Capability{"text-to-3d"}}}}}
	source := generation.Task{ID: "source", OwnerID: "owner", SessionID: "session", Capability: "text-to-3d",
		State: generation.TaskSucceeded, Offering: generation.Offering{ProviderID: "meshy"},
		Assets: []generation.Asset{{ID: "model", Role: "model", StorageKey: "ready"}}}
	gateway := &gatewayFake{tasks: map[generation.TaskID]generation.Task{"source": source}, byKey: map[string]generation.TaskID{}}
	service, err := New(&memoryStore{plans: map[string]Plan{}}, product, gateway, nil)
	if err != nil {
		t.Fatal(err)
	}
	grants := &budgetMemoryGrants{
		memoryGrants: &memoryGrants{grants: map[string]executiongrant.Grant{}, gateway: gateway},
		summary: budget.Summary{ID: "pb-processing", State: budget.StateActive,
			Units: legacyBudget(20, 0, 0)},
	}
	// The strictest of a person's modes: a delivery step must run without a
	// card under it too.
	policy := approvalpolicy.New("manual", []string{"auto", "manual", "budget"},
		[]string{"retexture", "format-conversion"}, []string{"format-conversion"})
	service.WithGrants(&executiongrant.Service{Grants: grants, Prepared: grants, Budgets: grants,
		Policy: policy, Modes: fixedMode("manual")})
	plan, err := service.Create(context.Background(), CreateCommand{OwnerID: "owner", SessionID: "session",
		SourceTaskID: "source", SourceAssetID: "model", IdempotencyKey: key,
		Origin: origin, Steps: []StepRequest{{OfferingID: "step", ExpectedOfferingRevision: "v1",
			Input: json.RawMessage(`{}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	return service, gateway, grants, plan
}

func TestDeliveryPlanKeepsItsOriginAndAsksAsThePlatform(t *testing.T) {
	ctx := context.Background()
	service, gateway, grants, plan := deliveryFixture(t)
	if plan.Origin != OriginDelivery {
		t.Fatalf("origin = %q, want %q", plan.Origin, OriginDelivery)
	}
	if err := service.advanceAll(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := service.Get(ctx, "owner", plan.ID)
	if err != nil || got.State == Paused || len(gateway.commands) != 1 {
		t.Fatalf("delivery plan = %+v commands=%d err=%v", got, len(gateway.commands), err)
	}
	if got.Origin != OriginDelivery {
		t.Fatalf("origin = %q, want it kept after the round", got.Origin)
	}
	// The step's grant is the platform's own: a delivery source and no budget
	// eligibility, so the audit never reads as a browser action and the session
	// plan is untouched. The actor that may ask for it is pinned by
	// executiongrant's own test (only model-delivery may).
	grant, err := grants.GetExecutionGrant(ctx, "owner", got.Steps[0].AuthorizationRef)
	if err != nil || grant.Source != executiongrant.Delivery {
		t.Fatalf("step grant = %+v err=%v", grant, err)
	}
	if grant.BudgetEligible {
		t.Fatalf("step grant is budget eligible: %+v", grant)
	}
	// The session's plan is untouched: the automatic completion's own ceiling
	// is what bounds this run (#457 D7).
	if grants.reserved != 0 || grants.summary.Units[0].Remaining != credits(20) {
		t.Fatalf("delivery plan used the session budget: reserved=%d summary=%+v", grants.reserved, grants.summary)
	}
}

// The platform's own plans are counted apart from a person's and an Agent's,
// so the reconciler can hold them to auto_completion.max_inflight_plans.
func TestCountActiveByOriginCountsOnlyThatOrigin(t *testing.T) {
	ctx := context.Background()
	service, _, _, _ := deliveryFixture(t)
	// A plan a person started, on the same service so both share one store.
	if _, err := service.Create(ctx, CreateCommand{OwnerID: "owner", SessionID: "session",
		SourceTaskID: "source", SourceAssetID: "model", IdempotencyKey: "browser-processing-plan-0001",
		Origin: OriginBrowser,
		Steps:  []StepRequest{{OfferingID: "step", ExpectedOfferingRevision: "v1", Input: json.RawMessage(`{}`)}}}); err != nil {
		t.Fatal(err)
	}
	delivery, err := service.CountActiveByOrigin(ctx, OriginDelivery)
	if err != nil || delivery != 1 {
		t.Fatalf("delivery plans = %d err=%v, want the one delivery plan", delivery, err)
	}
	browserCount, err := service.CountActiveByOrigin(ctx, OriginBrowser)
	if err != nil || browserCount != 1 {
		t.Fatalf("browser plans = %d err=%v, want the one browser plan", browserCount, err)
	}
}