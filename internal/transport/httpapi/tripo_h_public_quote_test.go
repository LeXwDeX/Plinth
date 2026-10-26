package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestTripoHCurrentNativePriceIsPublicAcrossOfferingAndExtension(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	for _, tc := range []struct{ capability, total string }{{"text-to-3d", "90"}, {"image-to-3d", "100"}} {
		t.Run(tc.capability, func(t *testing.T) {
			var offerings struct {
				Offerings []map[string]any `json:"offerings"`
			}
			if err := json.Unmarshal(fetchSurface(t, handler, "/api/v1/offerings?capability="+tc.capability), &offerings); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, offering := range offerings.Offerings {
				if offering["id"] != "tripo/tripo-v3.1-full/"+tc.capability {
					continue
				}
				found = true
				quote, ok := offering["default_native_quote"].(map[string]any)
				if !ok || offering["price_mode"] != "native" || quote["total_amount"] != tc.total || quote["unit"] != "tripo.credit" {
					t.Fatalf("incorrect public H quote: %+v", offering)
				}
				if _, legacy := offering["estimated_credits"]; legacy {
					t.Fatal("native offering leaked legacy estimated credits")
				}
				if _, private := offering["native_pricing"]; private {
					t.Fatal("native price declaration leaked")
				}
			}
			if !found {
				t.Fatal("full H Offering missing")
			}
			response := serveLegacyRequest(handler, http.MethodGet, "/api/providers?capability="+tc.capability+"&projection=extended", nil)
			if response.Code != http.StatusOK {
				t.Fatal(response.Code)
			}
			var discovery struct {
				Providers []map[string]any `json:"providers"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &discovery); err != nil {
				t.Fatal(err)
			}
			found = false
			for _, provider := range discovery.Providers {
				if provider["id"] != "tripo-v3.1-full" {
					continue
				}
				found = true
				quotes, ok := provider["nativeQuotes"].(map[string]any)
				if !ok {
					t.Fatalf("extension native quotes missing: %+v", provider)
				}
				quote, ok := quotes[tc.capability].(map[string]any)
				if !ok || quote["total_amount"] != tc.total || quote["unit"] != "tripo.credit" {
					t.Fatalf("extension H quote=%+v", quote)
				}
				if _, legacy := provider["estimatedCredits"]; legacy {
					t.Fatal("extension native price advertised as legacy zero")
				}
			}
			if !found {
				t.Fatalf("extension H model missing: %+v", discovery.Providers)
			}
		})
	}
}

func TestTripoHNativeDiscoveryListsDoNotAdvertiseLegacyZero(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	for _, route := range []string{
		"/api/providers?projection=extended",
		"/api/providers?domain=model-3d&projection=extended",
	} {
		t.Run(route, func(t *testing.T) {
			response := serveLegacyRequest(handler, http.MethodGet, route, nil)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
			var providers []map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &providers); err != nil {
				t.Fatal(err)
			}
			for _, provider := range providers {
				if provider["id"] != "tripo-v3.1-full" {
					continue
				}
				quotes, ok := provider["nativeQuotes"].(map[string]any)
				if !ok || len(quotes) != 2 {
					t.Fatalf("missing text/image native quotes: %+v", provider)
				}
				if _, legacy := provider["estimatedCredits"]; legacy {
					t.Fatalf("aggregate native model advertises legacy estimate: %v", provider["estimatedCredits"])
				}
				return
			}
			t.Fatal("Tripo H model missing from discovery list")
		})
	}
}
