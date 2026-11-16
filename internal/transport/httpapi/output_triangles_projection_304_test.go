package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #304: the public Offering list carries an Offering's output_triangles as
// declared. A generation Offering (Rodin Gen-2.5) now declares it with a decimal
// factor for its Quad face; the processing Offering that declared it before
// (Meshy Remesh) is projected with the same whole numbers as ever.
func TestPublicOfferingsProjectTheTriangleFactorsAsDeclared(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	fetch := func(capability string) (body string, offerings []map[string]any) {
		t.Helper()
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/api/v1/offerings?capability="+capability, nil))
		if out.Code != http.StatusOK {
			t.Fatalf("%s: %d", capability, out.Code)
		}
		var decoded struct {
			Offerings []map[string]any `json:"offerings"`
		}
		if err := json.Unmarshal(out.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		return out.Body.String(), decoded.Offerings
	}
	estimate := func(offerings []map[string]any, id string) map[string]any {
		t.Helper()
		for _, offering := range offerings {
			if offering["id"] == id {
				declared, _ := offering["output_triangles"].(map[string]any)
				return declared
			}
		}
		t.Fatalf("the Catalog lists no %s", id)
		return nil
	}

	for _, capability := range []string{"text-to-3d", "image-to-3d"} {
		body, offerings := fetch(capability)
		id := "hyper3d/rodin-gen-2.5/" + capability
		declared := estimate(offerings, id)
		factors, _ := declared["triangles_per_face"].(map[string]any)
		if declared["count_parameter"] != "quality_override" || declared["topology_parameter"] != "mesh_mode" ||
			len(factors) != 2 || factors["Quad"] != 2.2 || factors["Raw"] != float64(1) {
			t.Fatalf("%s output_triangles = %v", id, declared)
		}
		if !strings.Contains(body, `"triangles_per_face":{"Quad":2.2,"Raw":1}`) {
			t.Fatalf("%s: the factors are not written as declared: %s", capability, body)
		}
		// Offerings that declare nothing about their triangles project nothing.
		if other := estimate(offerings, "meshy/meshy-7.1/"+capability); other != nil {
			t.Fatalf("Meshy 7.1 projects %v without declaring output_triangles", other)
		}
	}

	body, offerings := fetch("remesh")
	remesh := estimate(offerings, "meshy/processing/remesh")
	factors, _ := remesh["triangles_per_face"].(map[string]any)
	if remesh["count_parameter"] != "target_polycount" || factors["quad"] != float64(2) || factors["triangle"] != float64(1) ||
		!strings.Contains(body, `"triangles_per_face":{"quad":2,"triangle":1}`) {
		t.Fatalf("Meshy Remesh output_triangles = %v in %s", remesh, body)
	}
}
