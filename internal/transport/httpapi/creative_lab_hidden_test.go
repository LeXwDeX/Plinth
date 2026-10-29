package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #100: Meshy Creative Lab Offerings stay defined but are not offered, so no public listing names one.
func TestCreativeLabOfferingsAreAbsentFromPublicListings(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	for _, path := range []string{
		"/api/v1/offerings",
		"/api/v1/offerings?capability=creative-lab-prototype",
		"/api/v1/offerings?capability=creative-lab-build",
		"/api/providers",
		"/api/providers?projection=extended",
		"/api/providers?domain=model-3d",
		"/api/providers?domain=image",
		"/api/v1/schema",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("X-User-ID", "owner-a")
		response := serveWithLocale(handler, request, "")
		body := strings.ToLower(response.Body.String())
		if response.Code != http.StatusOK && response.Code != http.StatusBadRequest {
			t.Fatalf("GET %s = %d", path, response.Code)
		}
		if path == "/api/v1/offerings" && !strings.Contains(body, "meshy/meshy-7.1/text-to-3d") {
			t.Fatal("the listing lost its other Meshy Offerings")
		}
		for _, hidden := range []string{"meshy/creative-lab/", "creative-lab-keychain", "creative-lab-figure", "meshy-creative-lab"} {
			if strings.Contains(body, hidden) {
				t.Fatalf("GET %s exposes %q", path, hidden)
			}
		}
	}
}

// Tasks made while Creative Lab was offered keep their frozen Offering and stay readable.
func TestHistoricalCreativeLabTaskStaysReadable(t *testing.T) {
	t.Parallel()
	seed := func(t *testing.T, databasePath string) {
		t.Helper()
		taskCatalog, err := catalog.LoadDefault()
		if err != nil {
			t.Fatal(err)
		}
		offering, ok := taskCatalog.Get("meshy/creative-lab/keycap/build")
		if !ok || offering.Enabled {
			t.Fatal("keycap build must stay defined and hidden")
		}
		repository, err := sqlite.Open(databasePath)
		if err != nil {
			t.Fatal(err)
		}
		defer repository.Close()
		now := time.Now().UTC().Add(-time.Hour)
		_, err = repository.ImportGenerationTasksAndAssets(context.Background(), []sqlite.GenerationTaskImport{{
			Task: generation.Task{
				ID: "creative-lab-history", OwnerID: "owner-a", Capability: offering.Capability, Offering: offering,
				Input: json.RawMessage(`{"prompt":"a keycap"}`), State: generation.TaskFailed, Version: 1,
				CreatedAt: now, UpdatedAt: now, FinishedAt: &now,
				Failure: &generation.Failure{Class: generation.FailureRejected, SafeDetail: "x"},
			},
			Attempts: []sqlite.GenerationAttemptImport{{
				ID: "creative-lab-history-attempt-1", Ordinal: 1, Offering: offering, State: generation.AttemptFailed,
				SubmissionKey: "history:creative-lab", StartedAt: now, UpdatedAt: now, FinishedAt: &now,
			}},
		}}, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, handler, _, _ := newHTTPHarnessWithServices(t, nil, nil, nil, nil, nil, seed)
	for _, path := range []string{"/api/v1/tasks/creative-lab-history", "/api/v1/tasks"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("X-User-ID", "owner-a")
		response := serveWithLocale(handler, request, "")
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "creative-lab-history") {
			t.Fatalf("GET %s = %d %s", path, response.Code, response.Body.String())
		}
	}
}
