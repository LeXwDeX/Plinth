package tripo_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
)

func TestFullFormatsCannotReachPaidBaseDispatchWithoutFrozenConsent(t *testing.T) {
	sent := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { sent++ }))
	defer upstream.Close()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := taskCatalog.Get("tripo/tripo-v3.1-full/text-to-3d")
	if !ok {
		t.Fatal("offering missing")
	}
	adapter, err := tripo.New(tripo.Config{BaseURL: upstream.URL, APIKey: "test", HTTPClient: upstream.Client()})
	if err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{"prompt":"ship","output_formats":["GLTF","USDZ","FBX","OBJ","STL","3MF"]}`)
	_, err = adapter.Start(context.Background(), generation.StartRequest{Offering: offering, Input: input})
	if err == nil || sent != 0 {
		t.Fatalf("error=%v, upstream calls=%d", err, sent)
	}
}

func TestFullFormatsRejectMalformedDirectInputBeforePaidBaseDispatch(t *testing.T) {
	sent := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { sent++ }))
	defer upstream.Close()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, _ := c.Get("tripo/tripo-v3.1-full/text-to-3d")
	adapter, err := tripo.New(tripo.Config{BaseURL: upstream.URL, APIKey: "test", HTTPClient: upstream.Client()})
	if err != nil {
		t.Fatal(err)
	}
	for _, formats := range []string{
		`["GLTF"]`,
		`["GLTF","GLTF","FBX","OBJ","STL","3MF"]`,
		`["GLTF","USDZ","FBX","OBJ","STL","UNKNOWN"]`,
	} {
		input := json.RawMessage(`{"prompt":"ship","output_formats":` + formats + `,"authorize_extra_credits":true}`)
		if _, err := adapter.Start(context.Background(), generation.StartRequest{Offering: offering, Input: input}); err == nil {
			t.Fatalf("accepted malformed formats %s", formats)
		}
	}
	if sent != 0 {
		t.Fatalf("upstream calls=%d", sent)
	}
}
