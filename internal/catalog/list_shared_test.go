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
