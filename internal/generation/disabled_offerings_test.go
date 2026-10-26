package generation_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

type rawCatalogOffering struct {
	ID         string `json:"id"`
	Capability string `json:"capability"`
	Enabled    bool   `json:"enabled"`
}

// disabledCatalogOfferings reads the shipped Product Schema directly so the
// test covers every offering the document declares disabled, including ones
// added after this test was written.
func disabledCatalogOfferings(t *testing.T) ([]rawCatalogOffering, int) {
	t.Helper()
	raw, err := os.ReadFile("../catalog/default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Offerings []rawCatalogOffering `json:"offerings"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	disabled := make([]rawCatalogOffering, 0)
	for _, offering := range document.Offerings {
		if !offering.Enabled {
			disabled = append(disabled, offering)
		}
	}
	return disabled, len(document.Offerings)
}

func assertDisabledOfferingRefusal(t *testing.T, operation string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s accepted a disabled offering", operation)
	}
	var schemaErr *schemacontract.Error
	if !errors.As(err, &schemaErr) || schemaErr.Operation != "select_offering" {
		t.Fatalf("%s error = %v, want an offering-selection refusal", operation, err)
	}
	if schemaErr.Code != schemacontract.ErrUnknownOffering && schemaErr.Code != schemacontract.ErrInvalidInput {
		t.Fatalf("%s error code = %s, want unknown_offering or invalid_input", operation, schemaErr.Code)
	}
}

func assertEmptyTable(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("%s rows = %d, want 0", table, count)
	}
}

func TestDisabledCatalogOfferingsRejectPrepareAndSubmitWithoutProviderCalls(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	disabled, rawCount := disabledCatalogOfferings(t)
	if len(disabled) == 0 {
		t.Fatal("default Catalog declares no disabled offerings")
	}
	if enabled := len(taskCatalog.List("")); enabled+len(disabled) != rawCount {
		t.Fatalf("enabled=%d disabled=%d raw=%d, disabled scan is incomplete", enabled, len(disabled), rawCount)
	}

	port := &countingPort{ProviderPort: mock.New()}
	h := newHarness(t, port, nil)
	ctx := context.Background()
	const owner = generation.OwnerID("disabled-offerings-owner")

	startsBefore, observesBefore := port.counts()
	for _, candidate := range disabled {
		t.Run(candidate.ID, func(t *testing.T) {
			offering, exists := taskCatalog.Get(generation.OfferingID(candidate.ID))
			if !exists || offering.Enabled {
				t.Fatalf("raw disabled offering %q is missing or enabled in the loaded Catalog", candidate.ID)
			}
			capability := generation.Capability(candidate.Capability)

			_, err := h.module.PrepareInput(ctx, generation.PrepareInputCommand{
				OwnerID: owner, Capability: capability,
				OfferingID: generation.OfferingID(candidate.ID), Input: json.RawMessage(`{}`),
			})
			assertDisabledOfferingRefusal(t, "prepare", err)

			_, err = h.module.Submit(ctx, generation.SubmitCommand{
				OwnerID: owner, Capability: capability,
				OfferingID: generation.OfferingID(candidate.ID), Input: json.RawMessage(`{}`),
			})
			assertDisabledOfferingRefusal(t, "submit", err)
		})
	}
	if starts, observes := port.counts(); starts != startsBefore || observes != observesBefore {
		t.Fatalf("disabled offerings reached the provider: starts %d->%d observes %d->%d",
			startsBefore, starts, observesBefore, observes)
	}

	page, err := h.repository.List(ctx, generation.OwnTasks(owner), generation.ListQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 || len(page.Tasks) != 0 {
		t.Fatalf("disabled offerings created public Tasks: %+v", page)
	}
	db, err := sql.Open("sqlite", h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"generation_tasks", "generation_attempts", "generation_prepared_inputs", "execution_grants"} {
		assertEmptyTable(t, db, table)
	}

	// The enabled control proves the counted provider seam is wired: a
	// disabled Offering creates no work, while an enabled one reaches Start.
	enabled, exists := taskCatalog.Get("gemini/gemini-3-pro-image/text-to-image")
	if !exists || !enabled.Enabled {
		t.Fatal("enabled control offering is unavailable")
	}
	accepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{
		OwnerID: owner, Capability: generation.TextToImage,
		OfferingID: enabled.ID, Input: json.RawMessage(`{"prompt":"counted provider seam"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	h.reconcile(t)
	if starts, _ := port.counts(); starts <= startsBefore {
		t.Fatalf("enabled control did not reach the provider: starts %d->%d", startsBefore, starts)
	}
	if _, err := h.module.Get(ctx, owner, accepted.TaskID); err != nil {
		t.Fatalf("enabled control did not create a public Task: %v", err)
	}
}
