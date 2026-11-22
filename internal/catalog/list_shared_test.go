package catalog

import (
	"encoding/json"
	"reflect"
	"testing"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// #413: ListShared is the read-only view List hands out copies of. It must
// return the same Offerings in the same order while borrowing the Catalog's
// values, so one Agent request does not deep-copy the Offering directory.
func TestListSharedBorrowsTheCatalogValues(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, capability := range sharedListCapabilities(product) {
		shared := product.ListShared(capability)
		if copied := product.List(capability); !reflect.DeepEqual(shared, copied) {
			t.Fatalf("ListShared(%q) differs from List(%q)", capability, capability)
		}
		if len(shared) == 0 {
			continue
		}
		again := product.ListShared(capability)
		if &shared[0] != &again[0] {
			t.Fatalf("ListShared(%q) copied its slice on the second call", capability)
		}
	}
}

// TestListSharedStaysReadOnly proves List copies before a caller may edit:
// editing a List result leaves the shared values alone.
func TestListSharedStaysReadOnly(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	want := product.List("")
	copied := product.List("")
	if len(copied) == 0 {
		t.Fatal("the default Catalog has no enabled Offering")
	}
	copied[0].ModelName = "edited"
	copied[0].ProviderOptions = json.RawMessage(`{"edited":true}`)
	copied[0].Parameters = append(copied[0].Parameters, schemacontract.Parameter{Name: "edited"})
	if got := product.ListShared(""); !reflect.DeepEqual(got, want) {
		t.Fatal("editing a List copy changed the shared Offering values")
	}
}

// TestListSharedSurvivesACallerThatAppends pins the half of the read-only
// contract a caller can break without editing an Offering (#424): appending to
// the borrowed slice must not reach the Catalog's own list, so two callers that
// append cannot see or race each other's values. The Catalog's list is
// length-bounded, so a write past its length stays invisible.
func TestListSharedSurvivesACallerThatAppends(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	shared := product.ListShared("")
	if len(shared) == 0 {
		t.Fatal("the default Catalog has no enabled Offering")
	}
	length := len(shared)
	if grown := append(shared, schemacontract.Offering{ID: "caller-added"}); len(grown) != length+1 {
		t.Fatalf("appending to the shared slice returned %d values, want %d", len(grown), length+1)
	}
	if got := product.ListShared(""); len(got) != length {
		t.Fatalf("a caller's append changed the Catalog's list to %d values, want %d", len(got), length)
	}
	for _, offering := range product.ListShared("") {
		if offering.ID == "caller-added" {
			t.Fatal("a caller's append reached the Catalog's list")
		}
	}
}

// sharedListCapabilities is every capability ListShared precomputes: the empty
// one (all enabled Offerings) plus each capability an enabled Offering serves.
func sharedListCapabilities(product *Catalog) []schemacontract.Capability {
	capabilities := []schemacontract.Capability{""}
	seen := map[schemacontract.Capability]bool{"": true}
	for _, offering := range product.List("") {
		if seen[offering.Capability] {
			continue
		}
		seen[offering.Capability] = true
		capabilities = append(capabilities, offering.Capability)
	}
	return capabilities
}
