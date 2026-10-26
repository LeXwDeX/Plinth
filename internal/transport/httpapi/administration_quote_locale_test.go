package httpapi_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestAdministrationHistoricalQuoteLocale(t *testing.T) {
	t.Parallel()

	var databasePath string
	module, handler, _, _ := newHTTPHarnessWithServices(
		t,
		[]string{"admin"},
		nil,
		nil,
		nil,
		nil,
		func(t *testing.T, path string) {
			databasePath = path
			database, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			_, err = database.Exec(`INSERT INTO authentication_users (
				id, username, nickname, name, email, roles_json,
				is_admin, is_active, created_at, updated_at
			) VALUES ('local-user', 'local-user', 'Local User', 'Local User',
				'local@example.test', '["user"]', 0, 1,
				'2026-10-21T00:00:00Z', '2026-10-21T00:00:00Z')`)
			if err != nil {
				t.Fatal(err)
			}
		},
	)
	accepted, err := submitHarnessTask(t, module, context.Background(), generation.SubmitCommand{
		OwnerID:    "local-user",
		Capability: generation.TextTo3D,
		OfferingID: "tripo/tripo-v3.1-full/text-to-3d",
		Input:      json.RawMessage(`{"prompt":"a small red fox","authorize_extra_credits":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Model a historical snapshot whose Chinese label predates locale-aware reads.
	// The expected values below are deliberately fixed and independent of localization code.
	const quoteJSON = `{"version":1,"unit":"tripo.credit","unit_label":"Tripo credits","base_amount":"10","extra_amount":"20","total_amount":"30","components":[{"id":"extreme_texture","label":"8K 极致纹理","amount":"20"}]}`
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`DROP TRIGGER generation_task_native_quote_immutable`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE generation_tasks SET native_quote_json = ? WHERE id = ?`, quoteJSON, accepted.TaskID); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	type readCase struct {
		name  string
		path  string
		owner string
	}
	reads := []readCase{
		{name: "admin list", path: "/api/admin/generations?userId=local-user"},
		{name: "admin detail", path: "/api/admin/generations/" + string(accepted.TaskID)},
		{name: "admin user assets", path: "/api/admin/user-assets/local-user"},
		{name: "task detail", path: "/api/v1/tasks/" + string(accepted.TaskID), owner: "local-user"},
	}
	for _, locale := range []struct {
		name, accept, expected string
	}{
		{name: "en", accept: "en", expected: "Extreme texture"},
		{name: "zh-CN", accept: "zh-CN", expected: "8K 极致纹理"},
	} {
		for _, read := range reads {
			t.Run(locale.name+"/"+read.name, func(t *testing.T) {
				request := httptest.NewRequest(http.MethodGet, read.path, nil)
				request.Header.Set("Accept-Language", locale.accept)
				if read.owner != "" {
					request.Header.Set("X-User-ID", read.owner)
				}
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusOK {
					t.Fatalf("GET %s status=%d body=%s", read.path, response.Code, response.Body.String())
				}
				body := response.Body.String()
				for _, privateField := range []string{
					"offering_ref_json", "api_key", "apiKey", "credentials", "secret",
				} {
					if strings.Contains(body, privateField) {
						t.Fatalf("private offering data leaked: %s", body)
					}
				}
				var value any
				if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
					t.Fatal(err)
				}
				component := findHistoricalQuoteComponent(value)
				if component == nil {
					t.Fatalf("extreme_texture quote component missing: %s", body)
				}
				if component["label"] != locale.expected ||
					component["id"] != "extreme_texture" ||
					component["amount"] != "20" {
					t.Fatalf("quote component = %#v, want label=%q id=extreme_texture amount=20", component, locale.expected)
				}
				checkDatabase, err := sql.Open("sqlite", databasePath)
				if err != nil {
					t.Fatal(err)
				}
				defer checkDatabase.Close()
				var persisted string
				if err := checkDatabase.QueryRow(`SELECT native_quote_json FROM generation_tasks WHERE id = ?`, accepted.TaskID).Scan(&persisted); err != nil {
					t.Fatal(err)
				}
				if persisted != quoteJSON {
					t.Fatalf("GET %s changed persisted native_quote_json: got %s", read.path, persisted)
				}
			})
		}
	}
}

func findHistoricalQuoteComponent(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		if id, ok := typed["id"].(string); ok && id == "extreme_texture" {
			return typed
		}
		for _, child := range typed {
			if found := findHistoricalQuoteComponent(child); found != nil {
				return found
			}
		}
	case []any:
		for _, child := range typed {
			if found := findHistoricalQuoteComponent(child); found != nil {
				return found
			}
		}
	}
	return nil
}
