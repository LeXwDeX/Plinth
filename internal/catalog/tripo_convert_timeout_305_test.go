package catalog

import (
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #305: the rigging and decimation steps export their GLB through a private
// models/convert that is given the upstream's own GLB as a URL. The paid run that
// backs the issue saw the upstream take 47 to 48 seconds to answer that creation
// request (a conversion from an uploaded file token answers at once), so the
// declared timeouts must leave room for it: the request timeout of the Tripo
// connection bounds each HTTP request, and the timeout of the Offering bounds the
// provider call that dispatches the conversion. Both are declared in the Schema
// (the kernel hard-codes no run parameter), and neither needed a change: this pins
// that a future tightening would not cut a slow creation off and fail the step.
func TestTripoConversionFromAURLHasRoomForASlowCreation(t *testing.T) {
	const slowestObserved = 48 * time.Second
	const margin = 60 * time.Second

	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, connection := range product.Application().Runtime.ProviderConnections {
		if connection.ID != "tripo" {
			continue
		}
		found = true
		if got := time.Duration(connection.RequestTimeoutSeconds) * time.Second; got < margin {
			t.Fatalf("the Tripo connection times a request out after %s, a conversion creation takes up to %s", got, slowestObserved)
		}
	}
	if !found {
		t.Fatal("no Tripo provider connection is declared")
	}
	for _, id := range []string{"tripo/processing/rigging", "tripo/processing/rigging-v3", "tripo/geometry/decimate-triangle"} {
		offering, ok := product.Get(generation.OfferingID(id))
		if !ok {
			t.Fatalf("missing %s", id)
		}
		if offering.Timeout < margin {
			t.Fatalf("%s cuts a provider call off after %s, a conversion creation takes up to %s", id, offering.Timeout, slowestObserved)
		}
	}
}
