package tripo

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #185 enabling check: the shipped default Catalog Offerings, with their
// Schema defaults, must produce exactly the request bodies of the official
// pages re-read on 2026-10-21 (Retopology, Mesh Segmentation, Mesh Part
// Completion and Model Format Conversion). The hand-built fixture tests above
// prove the protocol; this one pins the enabled declarations themselves.
func TestGeometryCatalogOfferingsSendOfficialWire(t *testing.T) {
	cases := []struct{ kind, endpoint, body string }{
		// Retopology v3.0: face_limit omitted for adaptive reduction; smart_uv only on v3.0.
		{"decimate-triangle", "mesh/decimate", `{"bake":true,"input":"task_native","model":"v3.0","quad":false,"smart_uv":true}`},
		{"decimate-quad", "mesh/decimate", `{"bake":true,"input":"task_native","model":"v3.0","quad":true,"smart_uv":true}`},
		// Segmentation v2 (semantic): no ref_image, so granularity and connectivity apply.
		{"segment", "mesh/segment", `{"input":"task_native","model":"v2.0-20260430","segmentation_granularity":"detailed","split_by_connectivity":true}`},
		// Part Completion: part_names omitted means every part is completed.
		{"complete", "mesh/complete", `{"completion_mode":"ai_completion","input":"task_segment","model":"v1.0-20250506"}`},
		{"quick-cap", "mesh/complete", `{"completion_mode":"quick_cap","input":"task_segment","model":"v1.0-20250506"}`},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			r := geometryCatalogFixture(t, tc.kind)
			r.Offering.AssetHosts = []string{"assets.example"}
			var base []string
			converts := map[string]string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				if q.Method == http.MethodPost {
					raw, _ := io.ReadAll(q.Body)
					if q.URL.Path == "/v3/models/convert" {
						format := ""
						for _, f := range []string{"FBX", "OBJ", "STL", "USDZ", "3MF"} {
							if strings.Contains(string(raw), `"format":"`+f+`"`) {
								format = f
							}
						}
						converts[format] = string(raw)
						fmt.Fprintf(w, `{"code":0,"data":{"task_id":"child_%s"}}`, format)
						return
					}
					if q.URL.Path != "/v3/"+tc.endpoint {
						t.Errorf("unexpected POST %s", q.URL.Path)
					}
					base = append(base, string(raw))
					io.WriteString(w, `{"code":0,"data":{"task_id":"task_processed"}}`)
					return
				}
				id := strings.TrimPrefix(q.URL.Path, "/v3/tasks/")
				locator := "https://assets.example/processed.glb"
				if id != "task_processed" {
					locator = "https://assets.example/" + id + ".bin"
				}
				fmt.Fprintf(w, `{"code":0,"data":{"task_id":%q,"status":"success","credits_consumed":10,"output":{"model_url":%q}}}`, id, locator)
			}))
			defer server.Close()
			downloads := 0
			factory := func() *Adapter {
				a := rigAdapter(t, server)
				a.nativeSourceClient = nativeDeliveryTestSourceClient(nativeTestGLB, &downloads)
				return a
			}
			ctx := generation.WithDispatchCheckpointer(context.Background(), &nativeDeliveryPermit{})
			accepted, err := factory().Start(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			q := rigObserve(r, accepted)
			var observed generation.Observation
			for i := 0; i < 16 && observed.Phase != generation.UpstreamCompleted; i++ {
				if observed, err = factory().Observe(ctx, q); err != nil {
					t.Fatal(err)
				}
				q.Opaque = observed.Opaque
			}
			if observed.Phase != generation.UpstreamCompleted || len(observed.Artifacts) != 6 {
				t.Fatalf("incomplete delivery: %+v", observed)
			}
			if len(base) != 1 || base[0] != tc.body {
				t.Fatalf("native body = %q, want exactly one %s", base, tc.body)
			}
			for _, format := range []string{"FBX", "OBJ", "STL", "USDZ", "3MF"} {
				want := `{"bake":true,"fbx_preset":"blender","format":"` + format + `","input":"https://assets.example/processed.glb","texture_format":"PNG","texture_size":8192,"with_animation":false}`
				if converts[format] != want {
					t.Fatalf("%s export body = %s, want %s", format, converts[format], want)
				}
			}
			if len(converts) != 5 {
				t.Fatalf("unexpected export set: %v", converts)
			}
		})
	}
}
