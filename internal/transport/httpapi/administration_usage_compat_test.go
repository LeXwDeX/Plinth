package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestAdministrationUsageRouteQueryWireAndTextUseProductSchema(
	t *testing.T,
) {
	t.Parallel()
	_, handler, _, _ := newHTTPHarnessWithServices(
		t,
		[]string{"admin"},
		func(schema *catalog.ApplicationSchema) {
			schema.Runtime.HTTP.LegacyRoutes.AdminUsage =
				"/custom/admin/usage"
			schema.Runtime.HTTP.LegacyRoutes.AdminBalanceSummary =
				"/custom/admin/balance"
			usage := &schema.Administration.Usage
			usage.API.Query.Days = "window"
			usage.API.Query.StartDate = "begin"
			usage.API.Query.EndDate = "finish"
			usage.API.Fields.Data = "records"
			usage.API.Fields.Error = "failure"
			usage.API.Fields.UserID = "account_id"
			usage.API.Fields.UserName = "display_name"
			usage.API.Fields.UserEmail = "mail"
			usage.API.Fields.TotalTasks = "task_count"
			usage.API.Fields.SucceededTasks = "done_count"
			usage.API.Fields.FailedTasks = "broken_count"
			usage.API.Fields.EstimatedCredits = "credits"
			usage.API.Fields.LastActiveAt = "last_seen"
			usage.API.Fields.ByType = "breakdown"
			usage.API.Fields.Total = "overall"
			usage.API.BalanceFields.CurrentBalance = "available"
			usage.API.BalanceFields.History = "snapshots"
			usage.API.BalanceFields.Balance = "remaining"
			usage.API.BalanceFields.CapturedAt = "observed"
			usage.Messages.InvalidRange = "schema invalid range"
		},
		nil,
		nil,
		nil,
		seedLegacyAdministrationGeneration,
	)

	response := administrationRequest(
		handler,
		http.MethodGet,
		"/custom/admin/usage?window=30",
		"",
		nil,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	records, ok := body["records"].([]any)
	if !ok || len(records) != 1 {
		t.Fatalf("usage body = %#v", body)
	}
	record, ok := records[0].(map[string]any)
	if !ok {
		t.Fatalf("usage record = %#v", records[0])
	}
	if record["account_id"] != "legacy-user" ||
		record["display_name"] != "Legacy User" ||
		record["mail"] != "legacy@example.test" ||
		record["task_count"] != float64(1) ||
		record["done_count"] != float64(1) ||
		record["broken_count"] != float64(0) ||
		record["credits"] != float64(1) {
		t.Fatalf("usage record = %#v", record)
	}
	breakdown, ok := record["breakdown"].(map[string]any)
	if !ok || breakdown["text-to-image"] != float64(1) {
		t.Fatalf("usage breakdown = %#v", record["breakdown"])
	}
	// #327: the total of all listed users, for the home card.
	overall, ok := body["overall"].(map[string]any)
	if !ok || overall["credits"] != float64(1) || overall["price_totals"] != nil {
		t.Fatalf("usage total = %#v", body["overall"])
	}
	if _, exists := record["userId"]; exists {
		t.Fatalf("hard-coded legacy field leaked: %#v", record)
	}

	balanceResponse := administrationRequest(
		handler,
		http.MethodGet,
		"/custom/admin/balance",
		"",
		nil,
	)
	if balanceResponse.Code != http.StatusOK {
		t.Fatalf(
			"balance status=%d body=%s",
			balanceResponse.Code,
			balanceResponse.Body.String(),
		)
	}
	var balanceBody map[string]any
	if err := json.Unmarshal(
		balanceResponse.Body.Bytes(),
		&balanceBody,
	); err != nil {
		t.Fatal(err)
	}
	snapshots, ok := balanceBody["snapshots"].([]any)
	if balanceBody["available"] != float64(1000) ||
		!ok ||
		len(snapshots) != 1 {
		t.Fatalf("balance body = %#v", balanceBody)
	}
	snapshot, ok := snapshots[0].(map[string]any)
	if !ok ||
		snapshot["remaining"] != float64(1000) ||
		snapshot["observed"] == "" {
		t.Fatalf("balance snapshot = %#v", snapshots[0])
	}

	invalid := administrationRequest(
		handler,
		http.MethodGet,
		"/custom/admin/usage?begin=2026-07-25&finish=2026-07-24",
		"",
		nil,
	)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf(
			"invalid status=%d body=%s",
			invalid.Code,
			invalid.Body.String(),
		)
	}
	var invalidBody map[string]any
	if err := json.Unmarshal(invalid.Body.Bytes(), &invalidBody); err != nil {
		t.Fatal(err)
	}
	if invalidBody["failure"] != "schema invalid range" {
		t.Fatalf("invalid body = %#v", invalidBody)
	}
}
