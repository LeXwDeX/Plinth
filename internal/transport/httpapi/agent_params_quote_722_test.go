package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

type agentParamsQuoteProjection struct {
	Params      map[string]json.RawMessage `json:"params"`
	NativeQuote *schema.NativeQuote        `json:"native_quote"`
}

func newAgentParamsQuoteServer(t *testing.T) (*Server, *catalog.Catalog) {
	t.Helper()
	repository, err := OpenMigratedTestRepository(filepath.Join(t.TempDir(), "quote.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	assets, err := local.New(filepath.Join(t.TempDir(), "assets"))
	if err != nil {
		t.Fatal(err)
	}
	module, err := generation.New(generation.Dependencies{Conversions: generation.NoConversions, Repository: repository, Catalog: taskCatalog, Assets: assets, Providers: providers.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{generation: module, agentParams: agentparams.NewPersistentParameterStore(repository, nil), ownsAgentSession: func(_ context.Context, owner, scope string) bool { return owner == "owner" && scope == "session" }}
	return server, taskCatalog
}

func agentParamsQuoteCall(t *testing.T, server *Server, method, offeringID, body string) agentParamsQuoteProjection {
	t.Helper()
	target := "/api/v1/agent/params?scope=session&offering_id=" + url.QueryEscape(offeringID)
	response := httptest.NewRecorder()
	if method == http.MethodPut {
		server.agentParamsPut(response, httptest.NewRequest(method, target, strings.NewReader(body)), "owner")
	} else {
		server.agentParamsGet(response, httptest.NewRequest(method, target, nil), "owner")
	}
	var result agentParamsQuoteProjection
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil {
		t.Fatalf("%s %s %s: %d %s", method, offeringID, body, response.Code, response.Body.String())
	}
	return result
}

func quoteComponentIDs(quote *schema.NativeQuote) []string {
	ids := []string{}
	for _, component := range quote.Components {
		ids = append(ids, component.ID)
	}
	return ids
}

// #722: the settings panel estimate follows the saved parameters. The params
// projection carries the quote of what the request would carry (declared
// defaults under the saved values, then forced_when), the same evaluation the
// harness status and the frozen input use, so turning texture off drops every
// texture component instead of repeating the Offering's default estimate.
func TestAgentParamsProjectionQuotesTheSavedTextureSwitch(t *testing.T) {
	t.Parallel()
	server, taskCatalog := newAgentParamsQuoteServer(t)
	for _, tc := range []struct {
		offering, capability string
		off                  string
		// total is the texture-off quote where a real run fixed it (#704 E4/E5: Tripo H3 v3.1 text 60).
		// tripo/tripo-v3.1/* stay disabled in the default Catalog and are not projected.
		total string
	}{
		{"tripo/tripo-v3.1-full/text-to-3d", "text-to-3d", `{"texture":false}`, "60"},
		{"tripo/tripo-v3.1-full/image-to-3d", "image-to-3d", `{"texture":false}`, ""},
		{"tripo/tripo-v3.1-multiview/image-to-3d", "image-to-3d", `{"texture":false}`, ""},
		{"tripo/tripo-p2/text-to-3d", "text-to-3d", `{"texture":false}`, ""},
		{"tripo/tripo-p2/image-to-3d", "image-to-3d", `{"texture":false}`, ""},
		{"tripo/tripo-p2-multiview/image-to-3d", "image-to-3d", `{"texture":false}`, ""},
		// Meshy multiview: forced_when turns the advised 8K texture_resolution into 2K without texture.
		{"meshy/meshy-7.1-multiview/image-to-3d", "image-to-3d", `{"should_texture":false}`, ""},
	} {
		t.Run(tc.offering, func(t *testing.T) {
			offering, ok := taskCatalog.Get(schema.OfferingID(tc.offering))
			if !ok {
				t.Fatalf("offering %s is not in the default catalog", tc.offering)
			}
			defaults, err := offering.DefaultNativeQuote(nil)
			if err != nil || defaults == nil {
				t.Fatalf("default quote = %+v, %v", defaults, err)
			}
			opened := agentParamsQuoteCall(t, server, http.MethodGet, tc.offering, "")
			if opened.NativeQuote == nil || opened.NativeQuote.TotalAmount != defaults.TotalAmount {
				t.Fatalf("nothing saved: native_quote = %+v, want the default %s", opened.NativeQuote, defaults.TotalAmount)
			}
			saved := agentParamsQuoteCall(t, server, http.MethodPut, tc.offering, `{"params":`+tc.off+`}`)
			reread := agentParamsQuoteCall(t, server, http.MethodGet, tc.offering, "")
			for name, got := range map[string]*schema.NativeQuote{"put": saved.NativeQuote, "get": reread.NativeQuote} {
				if got == nil {
					t.Fatalf("%s: no native_quote for the saved texture switch", name)
				}
				for _, id := range quoteComponentIDs(got) {
					if strings.Contains(id, "texture") {
						t.Fatalf("%s: texture off still quotes %s: %+v", name, id, got)
					}
				}
				low, _ := schema.ParseNativeAmount(got.TotalAmount)
				high, _ := schema.ParseNativeAmount(defaults.TotalAmount)
				if low >= high {
					t.Fatalf("%s: texture-off estimate %s is not below the default %s", name, got.TotalAmount, defaults.TotalAmount)
				}
				if tc.total != "" && got.TotalAmount != tc.total {
					t.Fatalf("%s: texture-off estimate %s, want %s", name, got.TotalAmount, tc.total)
				}
			}
			if tc.capability != "text-to-3d" {
				return
			}
			// The estimate equals what the submitted request is quoted: Normalize then EvaluateNativeQuote.
			var input map[string]any
			_ = json.Unmarshal([]byte(tc.off), &input)
			// The request also carries the consent to the declared format conversions.
			input["prompt"], input["authorize_extra_credits"] = "a compass", true
			encoded, _ := json.Marshal(input)
			normalizedOffering, normalized, err := taskCatalog.Normalize(schema.TextTo3D, offering.ID, encoded)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := normalizedOffering.EvaluateNativeQuote(normalized)
			if err != nil || actual == nil || actual.TotalAmount != saved.NativeQuote.TotalAmount ||
				strings.Join(quoteComponentIDs(actual), ",") != strings.Join(quoteComponentIDs(saved.NativeQuote), ",") {
				t.Fatalf("panel estimate %+v, submitted quote %+v (%v)", saved.NativeQuote, actual, err)
			}
		})
	}
}

// A residue the switch hides (texture_quality) or a value forced_when rewrites
// (generate_parts forces texture off) is priced as the request carries it, not
// as it was last saved.
func TestAgentParamsProjectionQuotesEffectiveValuesNotHiddenResidue(t *testing.T) {
	t.Parallel()
	server, _ := newAgentParamsQuoteServer(t)
	const id = "tripo/tripo-v3.1-full/text-to-3d"
	// texture_quality is advised: only the Agent sets it, with a reason; the person then turns texture off.
	if err := server.agentParams.SetAgentOffering(agentparams.ScopeKey("owner", "session"), id,
		map[string]json.RawMessage{"texture_quality": json.RawMessage(`"detailed"`)}, map[string]string{"texture_quality": "a lighter texture"}); err != nil {
		t.Fatal(err)
	}
	agent := agentParamsQuoteCall(t, server, http.MethodGet, id, "")
	if agent.NativeQuote == nil || !strings.Contains(strings.Join(quoteComponentIDs(agent.NativeQuote), ","), "detailed_texture") {
		t.Fatalf("the Agent's texture_quality is not priced: %+v", agent.NativeQuote)
	}
	residue := agentParamsQuoteCall(t, server, http.MethodPut, id, `{"params":{"texture":false}}`)
	if string(residue.Params["texture_quality"]) != `"detailed"` {
		t.Fatalf("the hidden residue is gone from the projection, the test no longer covers it: %s", residue.Params["texture_quality"])
	}
	if residue.NativeQuote == nil || residue.NativeQuote.TotalAmount != "60" {
		t.Fatalf("hidden texture_quality residue priced: %+v", residue.NativeQuote)
	}
	forced := agentParamsQuoteCall(t, server, http.MethodPut, id, `{"params":{"texture":null,"generate_parts":true}}`)
	if forced.NativeQuote == nil {
		t.Fatal("no native_quote with generate_parts")
	}
	for _, component := range quoteComponentIDs(forced.NativeQuote) {
		if strings.Contains(component, "texture") {
			t.Fatalf("generate_parts forces texture off, yet %s is quoted: %+v", component, forced.NativeQuote)
		}
	}
}

// Offerings priced in platform credits (Meshy single image) carry no native
// quote; the projection stays as before and does not fail.
func TestAgentParamsProjectionHasNoNativeQuoteWithoutNativePricing(t *testing.T) {
	t.Parallel()
	server, _ := newAgentParamsQuoteServer(t)
	for _, id := range []string{"meshy/meshy-7.1/image-to-3d", "openai/gpt-image-2/text-to-image"} {
		saved := agentParamsQuoteCall(t, server, http.MethodGet, id, "")
		if saved.NativeQuote != nil {
			t.Fatalf("%s: native_quote = %+v without native pricing", id, saved.NativeQuote)
		}
	}
	saved := agentParamsQuoteCall(t, server, http.MethodPut, "meshy/meshy-7.1/image-to-3d", `{"params":{"should_texture":false}}`)
	if saved.NativeQuote != nil || string(saved.Params["should_texture"]) != "false" {
		t.Fatalf("meshy single image: %+v", saved)
	}
}
