package tripo_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"testing"
)

type conversionAssetTransport func(*http.Request) (*http.Response, error)

func (f conversionAssetTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Load the production Offering, not a hand-written whitelist. The in-memory
// transport exercises real HTTPS/host/redirect guards without resolving a host.
func TestDefaultUploadedConversionAssetHostContract(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := product.Get("tripo/format-conversion/uploaded-glb-to-stl")
	if !ok {
		t.Fatal("missing uploaded conversion Offering")
	}
	if !reflect.DeepEqual(offering.AssetHosts, []string{"tripo3d.com", "tripo3d.ai"}) {
		t.Errorf("unexpected declared asset hosts: %v", offering.AssetHosts)
	}
	if len(offering.Outputs) != 1 || offering.Outputs[0].Role != "model" || offering.Outputs[0].Variant != "stl" || !reflect.DeepEqual(offering.Outputs[0].MediaTypes, []string{"model/stl"}) || offering.Outputs[0].Minimum == nil || *offering.Outputs[0].Minimum != 1 {
		t.Fatal("uploaded conversion must retain its single required STL slot")
	}
	body := make([]byte, 134)
	copy(body, "Synthetic binary STL for CDN contract")
	binary.LittleEndian.PutUint32(body[80:84], 1)
	for _, tc := range []struct {
		name, locator, redirect string
		wantRequests            []string
		allowed                 bool
	}{
		{"observed_cdn", "https://tripo-data.rg1.data.tripo3d.com/output.stl", "", []string{"tripo-data.rg1.data.tripo3d.com"}, true},
		{"declared_alternate_domain", "https://cdn.tripo3d.ai/output.stl", "", []string{"cdn.tripo3d.ai"}, true},
		{"foreign_host", "https://foreign.invalid/output.stl", "", nil, false},
		{"suffix_confusion", "https://tripo3d.com.foreign.invalid/output.stl", "", nil, false},
		{"prefix_confusion", "https://eviltripo3d.com/output.stl", "", nil, false},
		{"insecure_scheme", "http://tripo-data.rg1.data.tripo3d.com/output.stl", "", nil, false},
		{"redirect_foreign", "https://tripo-data.rg1.data.tripo3d.com/redirect", "https://foreign.invalid/output.stl", []string{"tripo-data.rg1.data.tripo3d.com"}, false},
		{"redirect_suffix_confusion", "https://tripo-data.rg1.data.tripo3d.com/redirect", "https://tripo3d.ai.foreign.invalid/output.stl", []string{"tripo-data.rg1.data.tripo3d.com"}, false},
		{"redirect_downgrade", "https://tripo-data.rg1.data.tripo3d.com/redirect", "http://cdn.tripo3d.ai/output.stl", []string{"tripo-data.rg1.data.tripo3d.com"}, false},
		{"redirect_declared_domain", "https://tripo-data.rg1.data.tripo3d.com/redirect", "https://cdn.tripo3d.ai/output.stl", []string{"tripo-data.rg1.data.tripo3d.com", "cdn.tripo3d.ai"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requested []string
			client := &http.Client{Transport: conversionAssetTransport(func(r *http.Request) (*http.Response, error) {
				requested = append(requested, r.URL.Hostname())
				if r.Method != http.MethodGet {
					t.Fatalf("asset access sent %s", r.Method)
				}
				header := make(http.Header)
				if r.URL.Path == "/redirect" {
					header.Set("Location", tc.redirect)
					return &http.Response{StatusCode: http.StatusFound, Header: header, Body: io.NopCloser(bytes.NewReader(nil)), Request: r}, nil
				}
				header.Set("Content-Type", "model/stl")
				header.Set("Content-Length", strconv.Itoa(len(body)))
				return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: r}, nil
			})}
			adapter, err := tripo.New(tripo.Config{BaseURL: "https://api.tripo3d.ai/v3", APIKey: "fixture-no-network-key", HTTPClient: client})
			if err != nil {
				t.Fatal(err)
			}
			reader, metadata, err := adapter.OpenAsset(context.Background(), generation.OpenAssetRequest{Offering: offering, Claim: generation.ArtifactClaim{ID: "model:stl", Role: "model", Variant: "stl", Locator: tc.locator, MediaType: "model/stl"}})
			if tc.allowed {
				if err != nil {
					t.Fatalf("declared HTTPS CDN rejected: %v", err)
				}
				defer reader.Close()
				actual, readErr := io.ReadAll(reader)
				if readErr != nil || !bytes.Equal(actual, body) || metadata.MediaType != "model/stl" || metadata.Size != int64(len(body)) {
					t.Fatalf("asset bytes/metadata changed: %#v, %v", metadata, readErr)
				}
			} else if err == nil {
				reader.Close()
				t.Fatal("undeclared or insecure CDN accepted")
			}
			if !reflect.DeepEqual(requested, tc.wantRequests) {
				t.Fatalf("transport hosts = %v, want %v (rejected targets must never reach transport)", requested, tc.wantRequests)
			}
		})
	}
}
