package catalog_test

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #299: a GPU that runs out of memory is the matting service short of capacity,
// not a refusal of the request. The words that tell the two apart are the
// connection's, declared in the Schema, and they name a class that already
// exists: no new failure vocabulary. The class is not retried by itself, by the
// failure nor by the Offering: the same picture at the same resolution would run
// out again.
func TestMattingConnectionDeclaresTheOutOfMemoryMessageAsACapacityFailure(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var mapping *schema.ErrorMapping
	for _, connection := range loaded.Application().Runtime.ProviderConnections {
		if connection.ID == "matting" {
			mapping = connection.ErrorMapping
		}
	}
	if mapping == nil {
		t.Fatal("the matting connection declares no error_mapping")
	}
	if len(mapping.MessageMatchers) != 1 || len(mapping.HTTPStatusRanges) != 0 || len(mapping.UpstreamCodeMap) != 0 || len(mapping.UpstreamStatusMap) != 0 {
		t.Fatalf("the matting connection declares more than the one message matcher: %+v", mapping)
	}
	matcher := mapping.MessageMatchers[0]
	if matcher.Class != string(schema.FailureUpstreamUnavailable) || matcher.Retryable || matcher.HTTPStatus != nil ||
		len(matcher.ContainsAny) != 1 || matcher.ContainsAny[0] != "显存不足" || len(matcher.ContainsAll) != 0 || matcher.Equals != "" {
		t.Fatalf("matcher = %+v, want upstream_unavailable on 显存不足, not retried, for any HTTP status", matcher)
	}
	if !schema.FailureClass(matcher.Class).Valid() {
		t.Fatalf("%q is not a failure class", matcher.Class)
	}
	offering, ok := loaded.Get(mattingOfferingID)
	if !ok {
		t.Fatal("background-removal offering missing")
	}
	if offering.Retry.Allows(schema.FailureClass(matcher.Class), 2) || len(offering.Retry.Retryable) != 0 {
		t.Fatalf("the Offering retries a failed batch: %+v", offering.Retry)
	}
}
