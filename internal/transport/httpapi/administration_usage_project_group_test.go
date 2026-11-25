package httpapi_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #599: the administration usage response splits spending by the project group
// each user is in now: a by_group row per group with task counts and, per unit,
// the amount and its share in basis points; the ungrouped row has a null key.
// User rows carry their group. The account endpoint reads the same usage
// service and never shows any group data.
func TestAdministrationUsageSplitsSpendingByProjectGroup599(t *testing.T) {
	t.Parallel()
	var databasePath string
	_, handler, _, _ := newHTTPHarnessWithServices(
		t, []string{"admin"}, nil, nil, nil, nil,
		seedLegacyAdministrationGeneration,
		func(_ *testing.T, path string) { databasePath = path },
	)

	created := administrationRequest(handler, http.MethodPost, "/api/admin/users",
		`{"username":"grouped","password":"secret12","email":"grouped@example.test","projectGroupKey":"studio-a"}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body)
	}
	userID := decodeAdministrationBody(t, created)["data"].(map[string]any)["id"].(string)
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC().UnixMilli()
	if _, err := database.Exec(`INSERT INTO usage_ledger (
			id, userId, userName, userEmail, taskType, taskId,
			providerId, estimatedCredits, taskStatus, params,
			resultUrls, createdAt, finishedAt
		) VALUES ('grouped-ledger', ?, 'grouped', 'grouped@example.test', 'text-to-3d', 'grouped-task',
			'meshy', 3, 'SUCCEEDED', '{}', '[]', ?, ?)`, userID, now, now); err != nil {
		t.Fatal(err)
	}

	response := administrationRequest(handler, http.MethodGet, "/api/admin/usage?days=7", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("usage status=%d body=%s", response.Code, response.Body)
	}
	body := decodeAdministrationBody(t, response)
	wantGroups := []any{
		map[string]any{
			"projectGroupKey": "studio-a", "totalTasks": float64(1),
			"succeededTasks": float64(1), "failedTasks": float64(0),
			"amounts": []any{map[string]any{
				"unit": "legacy.credits", "unit_label": "legacy.credits",
				"amount": "3", "share_basis_points": float64(7500),
			}},
		},
		map[string]any{
			"projectGroupKey": nil, "totalTasks": float64(1),
			"succeededTasks": float64(1), "failedTasks": float64(0),
			"amounts": []any{map[string]any{
				"unit": "legacy.credits", "unit_label": "legacy.credits",
				"amount": "1", "share_basis_points": float64(2500),
			}},
		},
	}
	if !reflect.DeepEqual(body["byGroup"], wantGroups) {
		t.Fatalf("byGroup = %#v", body["byGroup"])
	}
	userGroups := map[string]any{}
	for _, item := range body["data"].([]any) {
		row := item.(map[string]any)
		group, present := row["projectGroupKey"]
		if !present {
			t.Fatalf("user row must carry the group field, null when ungrouped: %#v", row)
		}
		userGroups[row["userId"].(string)] = group
	}
	if !reflect.DeepEqual(userGroups, map[string]any{userID: "studio-a", "legacy-user": nil}) {
		t.Fatalf("user groups = %#v", userGroups)
	}

	login := administrationRequest(handler, http.MethodPost, "/api/auth/login",
		`{"username":"grouped","password":"secret12"}`, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body)
	}
	account := administrationRequest(handler, http.MethodGet, "/api/v1/account/usage?days=7", "", login.Result().Cookies()[0])
	if account.Code != http.StatusOK {
		t.Fatalf("account usage status=%d body=%s", account.Code, account.Body)
	}
	text := account.Body.String()
	for _, leaked := range []string{"projectGroupKey", "project_group", "byGroup", "by_group", "studio-a", "share_basis_points"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("account usage exposes project group data %q: %s", leaked, text)
		}
	}
	var accountBody map[string]any
	if err := json.Unmarshal([]byte(text), &accountBody); err != nil {
		t.Fatal(err)
	}
	if accountBody["user_id"] != userID || accountBody["total_tasks"] != float64(1) {
		t.Fatalf("account usage = %#v", accountBody)
	}
}

func TestAdministrationUsageProjectGroupWireNamesFollowProductSchema599(t *testing.T) {
	t.Parallel()
	_, handler, _, _ := newHTTPHarnessWithServices(
		t, []string{"admin"},
		func(schema *catalog.ApplicationSchema) {
			fields := &schema.Administration.Usage.API.Fields
			fields.ByGroup = "teams"
			fields.ProjectGroupKey = "team"
			fields.Amounts = "spend"
			fields.Unit = "currency"
			fields.UnitLabel = "currency_name"
			fields.Amount = "value"
			fields.ShareBasisPoints = "bp"
		},
		nil, nil, nil,
		seedLegacyAdministrationGeneration,
	)
	response := administrationRequest(handler, http.MethodGet, "/api/admin/usage?days=7", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("usage status=%d body=%s", response.Code, response.Body)
	}
	body := decodeAdministrationBody(t, response)
	want := []any{map[string]any{
		"team": nil, "totalTasks": float64(1), "succeededTasks": float64(1), "failedTasks": float64(0),
		"spend": []any{map[string]any{
			"currency": "legacy.credits", "currency_name": "legacy.credits", "value": "1", "bp": float64(10000),
		}},
	}}
	if !reflect.DeepEqual(body["teams"], want) {
		t.Fatalf("teams = %#v", body["teams"])
	}
	if _, exists := body["byGroup"]; exists {
		t.Fatalf("hard-coded group field leaked: %#v", body)
	}
	if row := body["data"].([]any)[0].(map[string]any); row["team"] != nil {
		t.Fatalf("user row = %#v", row)
	} else if _, present := row["team"]; !present {
		t.Fatalf("user row must carry the Schema-named group field: %#v", row)
	}
}

func TestAdministrationUsageProjectGroupsRequireAdministrator599(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithRoles(t, []string{"user"})
	response := administrationRequest(handler, http.MethodGet, "/api/admin/usage?days=7", "", nil)
	if response.Code != http.StatusForbidden {
		t.Fatalf("non-administrator usage status=%d body=%s", response.Code, response.Body)
	}
	if strings.Contains(response.Body.String(), "byGroup") {
		t.Fatalf("forbidden response carries group data: %s", response.Body)
	}
}

func TestAdministrationUsageProjectGroupsEmptyWithoutTasks599(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithRoles(t, []string{"admin"})
	response := administrationRequest(handler, http.MethodGet, "/api/admin/usage?days=7", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("usage status=%d body=%s", response.Code, response.Body)
	}
	groups, ok := decodeAdministrationBody(t, response)["byGroup"].([]any)
	if !ok || len(groups) != 0 {
		t.Fatalf("byGroup must be an empty list without tasks: %s", response.Body)
	}
}
