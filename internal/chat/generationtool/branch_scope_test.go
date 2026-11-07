package generationtool_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func listedModelIDs(t *testing.T, raw json.RawMessage) map[string]bool {
	t.Helper()
	var listing struct {
		Models []struct {
			TaskID string `json:"task_id"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &listing); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, model := range listing.Models {
		ids[model.TaskID] = true
	}
	return ids
}

func TestListSessionModelsIncludesBranchAncestorModels(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	h.gateway.branchRefs = map[string]map[generation.TaskID]bool{
		"branch-a": {"model-1": true},
	}
	raw, err := h.executor.ListSessionModels(context.Background(), "owner-a", "branch-a")
	if err != nil {
		t.Fatal(err)
	}
	if ids := listedModelIDs(t, raw); !ids["model-1"] {
		t.Fatalf("branch listing lacks the ancestor model: %s", raw)
	}
	sibling, err := h.executor.ListSessionModels(context.Background(), "owner-a", "sibling-a")
	if err != nil {
		t.Fatal(err)
	}
	if ids := listedModelIDs(t, sibling); ids["model-1"] {
		t.Fatalf("sibling listing leaked the ancestor model: %s", sibling)
	}
}

func TestListSessionModelsIgnoresBranchRefsOfAnotherOwner(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	h.gateway.branchRefs = map[string]map[generation.TaskID]bool{
		"branch-a": {"model-1": true},
	}
	raw, err := h.executor.ListSessionModels(context.Background(), "owner-b", "branch-a")
	if err != nil {
		t.Fatal(err)
	}
	if ids := listedModelIDs(t, raw); len(ids) != 0 {
		t.Fatalf("another owner read branch refs: %s", raw)
	}
}
