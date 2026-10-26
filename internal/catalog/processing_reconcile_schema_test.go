package catalog

import (
	"encoding/json"
	"testing"
)

func TestProcessingReconcileRouteAndTextsInPublicSchema(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	if application.Runtime.HTTP.Routes.ProcessingPlanReconcile != "/api/v1/processing-plans/{planID}/reconcile" {
		t.Fatal("missing reconcile route")
	}
	for _, key := range []string{"plan_reconcile_action", "plan_reconcile_linked", "plan_reconcile_undetermined", "plan_reconcile_frozen_changed", "plan_reconciled_absent", "plan_reconciled_mismatch"} {
		if application.Content.Messages["ui.workspace.processing."+key] == "" {
			t.Fatalf("missing text %s", key)
		}
	}
	data, err := json.Marshal(application.Runtime.HTTP.Routes)
	if err != nil {
		t.Fatal(err)
	}
	var routes map[string]string
	if err := json.Unmarshal(data, &routes); err != nil {
		t.Fatal(err)
	}
	if routes["processing_plan_reconcile"] != application.Runtime.HTTP.Routes.ProcessingPlanReconcile {
		t.Fatal("route omitted from projection")
	}
	for _, invalid := range []string{"not-a-path", "/api/v1/processing-plans/reconcile", "/api/v1/processing-plans/{otherID}/reconcile", application.Runtime.HTTP.Routes.ProcessingPlanResume} {
		changed := application
		changed.Runtime.HTTP.Routes.ProcessingPlanReconcile = invalid
		if err := ValidateApplicationSchema(changed); err == nil {
			t.Fatalf("accepted invalid route %q", invalid)
		}
	}
	// Older snapshots are readable and disable the new endpoint.
	application.Runtime.HTTP.Routes.ProcessingPlanReconcile = ""
	if err := ValidateApplicationSchema(application); err != nil {
		t.Fatalf("historical schema rejected: %v", err)
	}
}
