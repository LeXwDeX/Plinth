package generation

import (
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #413: the running Catalog must expose the read-only view, or
// Module.OfferingsShared silently falls back to deep-copying the directory.
var _ sharedOfferingCatalog = (*catalog.Catalog)(nil)

func TestRunningCatalogExposesTheSharedOfferingView(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	shared := product.ListShared("")
	if len(shared) == 0 {
		t.Fatal("the default Catalog shares no enabled Offering")
	}
	if shared[0].ID == "" {
		t.Fatal("the shared Offering has no id")
	}
}

// TestOfferingsSharedGivesCallersTheirOwnSlice keeps the mutable part (the
// slice order) private to the caller while the Offering values stay shared:
// reordering what a caller received must not reorder the Catalog.
func TestOfferingsSharedGivesCallersTheirOwnSlice(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	module := &Module{catalog: product}
	want := append([]Offering(nil), product.ListShared("")...)
	if len(want) < 2 {
		t.Skip("the default Catalog shares fewer than two Offerings")
	}
	got := module.OfferingsShared("")
	got[0], got[1] = got[1], got[0]
	if !reflect.DeepEqual(product.ListShared(""), want) {
		t.Fatal("reordering OfferingsShared's result reordered the Catalog")
	}
	if again := module.OfferingsShared(""); !reflect.DeepEqual(again, want) {
		t.Fatal("OfferingsShared returned a stale or partial order")
	}
}
