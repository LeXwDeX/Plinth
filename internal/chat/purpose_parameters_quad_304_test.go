package chat_test

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #304: the direct task API fills a face count the way the Agent's tools do,
// so a Quad request of the shipped Rodin Gen-2.5 gets a count whose triangles
// stay inside the budget of the session's purpose and platform, whichever
// purpose it is. The Offering declares what a Quad face comes to
// (output_triangles): the game purposes' explicit counts are held to it at load
// time, and a purpose that names no count gets the most its budget holds.

func rodinDirectRequest(offering schema.Offering, mode string) string {
	request := `{"prompt":"a wooden crate","authorize_extra_credits":true,"mesh_mode":"` + mode + `"`
	if offering.Capability == schema.ImageTo3D {
		request += `,"reference_assets":["reference"]`
	}
	return request + `}`
}

// applyShippedPurpose resolves a direct request against the purposes of the
// shipped Catalog (shippedPurposeResolver).
func applyShippedPurpose(t *testing.T, resolver chat.PurposeParameters, fields map[string]any, offering schema.Offering,
	request string) chat.DirectPurposeInput {
	t.Helper()
	applied, err := resolver.ApplyToDirectInput(purposeParamsBrief(t, fields), offering, json.RawMessage(request), nil)
	if err != nil {
		t.Fatal(err)
	}
	return applied
}

// Every shipped purpose and every platform it declares, for both Rodin
// Offerings: the Quad count that the request freezes (after the kernel's own
// normalization) is at most what the budget holds. A purpose with a count of its
// own (the game purposes) keeps it; one without gets no smaller a count than the
// budget holds (one more face would break it, unless the Offering's own Quad
// ceiling is what stops it).
func TestDirectQuadRequestsFitTheBudgetOfEveryPurposeAndPlatform(t *testing.T) {
	resolver, product := shippedPurposeResolver(t)
	usage := resolver.Model3D
	checked, explicitCounts := 0, 0
	for _, id := range []schema.OfferingID{"hyper3d/rodin-gen-2.5/text-to-3d", "hyper3d/rodin-gen-2.5/image-to-3d"} {
		offering, found := product.Get(id)
		if !found || offering.OutputTriangles == nil {
			t.Fatalf("%s declares no output_triangles", id)
		}
		factor, declared := offering.OutputTriangles.Factor("Quad")
		if !declared {
			t.Fatalf("%s declares no Quad factor", id)
		}
		for _, profile := range usage.Profiles {
			platforms := []string{""}
			for _, platform := range profile.Platforms {
				platforms = append(platforms, platform.IDs...)
			}
			for _, platform := range platforms {
				resolved, _ := usage.ForPlatform(profile, platform)
				fields := map[string]any{"purpose": profile.ID}
				if platform != "" {
					fields["target_platform"] = platform
				}
				applied := applyShippedPurpose(t, resolver, fields, offering, rodinDirectRequest(offering, "Quad"))
				_, normalized, err := product.Normalize(offering.Capability, id, applied.Input)
				if err != nil {
					t.Fatalf("%s on %s/%q does not freeze: %v (%s)", id, profile.ID, platform, err, applied.Input)
				}
				var frozen struct {
					Mode  string  `json:"mesh_mode"`
					Count float64 `json:"quality_override"`
				}
				if err := json.Unmarshal(normalized, &frozen); err != nil || frozen.Mode != "Quad" {
					t.Fatalf("%s on %s/%q froze %s (%v)", id, profile.ID, platform, normalized, err)
				}
				maximum := float64(resolved.Budget.MaxTriangles)
				if triangles := schema.TrianglesForFaces(frozen.Count, factor); triangles > maximum {
					t.Fatalf("%s on %s/%q froze %v Quad faces, %v triangles over the %v budget", id, profile.ID, platform, frozen.Count, triangles, maximum)
				}
				if explicit, given := resolved.ParameterOverrides[string(id)]["quality_override"]; given {
					// A purpose with a count of its own keeps it (a tenth of the budget stays free).
					var want float64
					if err := json.Unmarshal(explicit, &want); err != nil || frozen.Count != want {
						t.Fatalf("%s on %s/%q froze %v Quad faces, want the purpose's %s", id, profile.ID, platform, frozen.Count, explicit)
					}
					explicitCounts++
				} else if next := schema.TrianglesForFaces(frozen.Count+1, factor); next <= maximum && frozen.Count < 200000 {
					// One that names none gets the largest count its budget holds.
					t.Fatalf("%s on %s/%q froze %v Quad faces although %v still fit the %v budget", id, profile.ID, platform, frozen.Count, frozen.Count+1, maximum)
				}
				checked++
			}
		}
	}
	// The loop is not vacuous: every purpose is resolved at least once for each
	// Offering, and the purposes with platform tiers once more per platform.
	if minimum := 2 * len(usage.Profiles); checked <= minimum || explicitCounts == 0 {
		t.Fatalf("checked %d purpose and platform pairs (%d with the purpose's own count), want more than %d and some own counts",
			checked, explicitCounts, minimum)
	}
}

// A triangle request needs no conversion, and what the request chose stays its
// own: a Quad count over the budget is not rewritten (the Agent's tools report
// it instead, as for any chosen value).
func TestDirectRodinRequestKeepsItsOwnCountAndCountsTrianglesOneToOne(t *testing.T) {
	resolver, product := shippedPurposeResolver(t)
	offering, _ := product.Get("hyper3d/rodin-gen-2.5/text-to-3d")
	brief := map[string]any{"purpose": "web_ar", "triangle_budget": 30000}
	for name, test := range map[string]struct {
		mode  string
		extra string
		want  float64
	}{
		"quad faces the budget holds":     {"Quad", "", 13636},
		"triangles one to one":            {"Raw", "", 30000},
		"the request's own count (quad)":  {"Quad", `,"quality_override":20000`, 20000},
		"the request's own count (Raw)":   {"Raw", `,"quality_override":20000`, 20000},
		"a count that already fits quads": {"Quad", `,"quality_override":13000`, 13000},
	} {
		request := rodinDirectRequest(offering, test.mode)
		request = request[:len(request)-1] + test.extra + "}"
		applied := applyShippedPurpose(t, resolver, brief, offering, request)
		if got := purposeParamsDecode(t, applied.Input)["quality_override"]; got != test.want {
			t.Fatalf("%s: quality_override = %v, want %v (%s)", name, got, test.want, applied.Input)
		}
	}
}
