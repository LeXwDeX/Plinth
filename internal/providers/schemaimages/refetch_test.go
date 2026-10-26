package schemaimages_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/schemaimages"
)

// The synchronous image protocol hands its result over once, inline, and keeps
// no job (#295): there is nothing to read again, and the Schema declares no
// result_refetch for these connections. A read attempt contacts nobody.
func TestSyncImageResultsCannotBeFetchedAgain(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		http.Error(w, "unexpected", http.StatusTeapot)
	}))
	defer upstream.Close()
	adapter, err := schemaimages.New(schemaimages.Config{BaseURL: upstream.URL, APIKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []generation.OfferingID{"openai/gpt-image-2/text-to-image", "gemini/gemini-3-pro-image/text-to-image", "jimeng/seedream-4-5/text-to-image"} {
		offering, ok := c.Get(id)
		if !ok {
			t.Fatalf("offering %s missing", id)
		}
		if _, declared := c.ResultRefetchPolicy(offering.AdapterKey); declared {
			t.Fatalf("%s declares result_refetch for a one-shot protocol", id)
		}
		_, err := adapter.Observe(generation.WithDeniedDispatchCheckpointer(context.Background()), generation.ObserveRequest{Offering: offering})
		var provider *generation.ProviderError
		if !errors.As(err, &provider) {
			t.Fatalf("%s observe = %v", id, err)
		}
	}
	if calls != 0 {
		t.Fatalf("a refetch attempt reached the upstream %d times", calls)
	}
}
