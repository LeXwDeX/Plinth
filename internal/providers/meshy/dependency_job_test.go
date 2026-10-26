package meshy_test

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
)

func TestDependencyJobUsesFrozenRefineCheckpoint(t *testing.T) {
	// A zero-value adapter has no HTTP client: resolution must remain pure.
	a := &meshy.Adapter{}
	for _, tc := range []struct{ name, options, opaque, want string }{
		{"single stage", `{}`, `broken`, "preview"},
		{"disabled", `{"refine_after_preview":false}`, ``, "preview"},
		{"historical boolean", `{"refine_after_preview":true}`, `{"refine_task_id":"refine"}`, "refine"},
		{"object", `{"refine_after_preview":{"set":{"mode":"refine"},"source_parameter":"preview_task_id"}}`, `{"refine_task_id":"refine"}`, "refine"},
		{"empty", `{"refine_after_preview":true}`, ``, ""},
		{"missing", `{"refine_after_preview":true}`, `{}`, ""},
		{"null", `{"refine_after_preview":true}`, `null`, ""},
		{"partial decode", `{"refine_after_preview":true}`, `{"refine_task_id":"refine","stage":3}`, ""},
		{"malformed", `{"refine_after_preview":true}`, `{"refine_task_id":"refine"`, ""},
		{"intent", `{"refine_after_preview":true}`, `{"stage":"text-to-3d.refine","refine_task_id":"refine"}`, ""},
		{"bad declaration", `{"refine_after_preview":{}}`, `{"refine_task_id":"refine"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := generation.DependencyJobRequest{Offering: generation.Offering{ProviderOptions: json.RawMessage(tc.options)},
				Job: generation.UpstreamJob{ProviderID: "meshy", RemoteID: "preview"}, Opaque: json.RawMessage(tc.opaque)}
			got, err := a.ResolveDependencyJob(r)
			if tc.want == "" {
				if err == nil {
					t.Fatal("invalid checkpoint accepted")
				}
			} else if err != nil || got.RemoteID != tc.want || got.ProviderID != "meshy" {
				t.Fatalf("job=%#v error=%v", got, err)
			}
			if r.Job.RemoteID != "preview" || string(r.Opaque) != tc.opaque {
				t.Fatal("source mutated")
			}
		})
	}
}
