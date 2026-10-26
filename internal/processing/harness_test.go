package processing

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
)

type harnessPlanGateway struct {
	*gatewayFake
	submitted []generation.SubmitPreparedCommand
}

func (g *harnessPlanGateway) SubmitPrepared(ctx context.Context, cmd generation.SubmitPreparedCommand) (generation.Accepted, error) {
	g.submitted = append(g.submitted, cmd)
	return g.gatewayFake.SubmitPrepared(ctx, cmd)
}

func TestHarnessPlanBindsEveryStepGrantAndAdmission(t *testing.T) {
	ctx := context.Background()
	service, gateway, grants, legacy := policyPlanFixture(t, "remesh", "auto")
	delete(service.store.(*memoryStore).plans, legacy.ID)
	binding := harness.DefaultSelection("owner", "session").Binding
	command := CreateCommand{OwnerID: "owner", SessionID: "session", SourceTaskID: "source", SourceAssetID: "model", IdempotencyKey: "harness-plan-key-0001", Harness: binding, Steps: []StepRequest{{OfferingID: "step", ExpectedOfferingRevision: "v1", Input: json.RawMessage(`{}`)}}}
	plan, err := service.Create(ctx, command)
	if err != nil || plan.Harness != binding {
		t.Fatalf("plan binding %+v %v", plan, err)
	}
	changed := command
	changed.Harness.Revision = 1
	if _, err := service.Create(ctx, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("plan idempotency rebound %v", err)
	}
	spy := &harnessPlanGateway{gatewayFake: gateway}
	service.gateway = spy
	if err := service.advanceAll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(spy.submitted) != 1 || spy.submitted[0].Harness != binding {
		t.Fatalf("step admission binding %+v", spy.submitted)
	}
	for _, grant := range grants.grants {
		if grant.Harness != binding {
			t.Fatal("step grant lost binding")
		}
	}
	if !submitRejected(&generation.Error{Code: generation.ErrConflict, Cause: harness.ErrStale}) {
		t.Fatal("stale admission treated as unknown submission")
	}
}
