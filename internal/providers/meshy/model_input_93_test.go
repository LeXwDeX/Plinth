package meshy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// widenedProfileRequest is a profiled request whose upload profile is smaller
// than the model, while the inline ceiling (the parameter's max_bytes) holds
// it: the shape of the shipped Offerings, where uploads stay within 10 MiB and
// a platform Task's GLB may be inlined up to 70 MiB (#93).
func widenedProfileRequest(t *testing.T, raw []byte, role string) generation.InputValidationRequest {
	t.Helper()
	r := modelProfileRequest(raw, "application/octet-stream")
	limits := *r.Offering.Parameters[0].UploadedModel.ProfileLimits
	limits.MaxBytes = int64(len(raw)) - 1
	limits.MaxJSONBytes = limits.MaxBytes
	inline := int64(len(raw)) + 1024
	r.Offering.Parameters[0].UploadedModel.ProfileLimits = &limits
	r.Offering.Parameters[0].MaxBytes = inline
	r.Offering.Parameters[0].Required = false
	r.Offering.Parameters = append(r.Offering.Parameters, schema.Parameter{Name: "source_task", Type: "task", Semantic: schema.SemanticSourceTask, WireName: "input_task_id", SameProvider: true})
	mutateProfileOptions(&r, func(o *wireOptions) {
		o.InlineModelInput.MaxBytes = inline
		o.ModelInputProfile.ProfileLimits = &limits
	})
	r.InputAssets["source_asset"][0].Role = role
	return r
}

func TestProfiledSourceSizeFollowsProvenance(t *testing.T) {
	raw := modelinputtest.Triangle(modelinputtest.Options{})
	for _, tc := range []struct {
		name, role string
		accepted   bool
	}{
		{"direct upload stays within the upload profile", "", false},
		{"platform task GLB uses the inline ceiling", "model", true},
		{"non-model task artifact is refused", "texture", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := widenedProfileRequest(t, raw, tc.role)
			store := &profiledInputs{bodies: [][]byte{raw}}
			err := (&Adapter{inputAssets: store}).ValidateInput(context.Background(), r)
			if tc.accepted != (err == nil) {
				t.Fatalf("accepted=%v err=%v", tc.accepted, err)
			}
			if tc.role == "" {
				var refusal *generation.Error
				if !errors.As(err, &refusal) || refusal.Code != generation.ErrInvalidInput {
					t.Fatalf("upload over the profile: %v", err)
				}
				if store.opens != 0 {
					t.Fatal("an oversize upload was read")
				}
			}
		})
	}
}

func TestProfiledTaskGLBKeepsTextureRequirements(t *testing.T) {
	raw := modelinputtest.Triangle(modelinputtest.Options{})
	r := widenedProfileRequest(t, raw, "model")
	r.Offering.Parameters[0].UploadedModel.RequireSingleBaseColorTexture = true
	if err := (&Adapter{inputAssets: &profiledInputs{bodies: [][]byte{raw}}}).ValidateInput(context.Background(), r); err == nil {
		t.Fatal("untextured Task GLB accepted for a single-base-color route")
	}
}

// The task route reads no profiled bytes; it checks source_model_limit on the
// GLB the source Task delivered and refuses before any network call.
func TestProfiledTaskRouteChecksTheSourceModelLimit(t *testing.T) {
	doc, bin := modelinputtest.Document(modelinputtest.Options{})
	doc["nodes"] = []any{map[string]any{"mesh": 0}, map[string]any{"mesh": 0}}
	doc["scenes"] = []any{map[string]any{"nodes": []any{0, 1}}}
	raw := modelinputtest.GLB(doc, bin)
	sum := sha256.Sum256(raw)
	for _, tc := range []struct {
		name  string
		limit int64
		ok    bool
	}{{"within", 2, true}, {"over", 1, false}} {
		t.Run(tc.name, func(t *testing.T) {
			r := widenedProfileRequest(t, raw, "model")
			r.Input = json.RawMessage(`{"source_task":"task-local"}`)
			r.InputAssets = nil
			r.Offering.SourceModelLimit = &schema.SourceModelLimit{MaxTriangles: tc.limit, CountBasis: "triangles"}
			r.TaskDependencies = map[string]generation.TaskDependency{"source_task": {
				TaskID: "task-local", ProviderID: "meshy", UpstreamJob: generation.UpstreamJob{ProviderID: "meshy", RemoteID: "remote"},
				Assets: []generation.Asset{{ID: "delivered", Role: "model", MediaType: "model/gltf-binary", StorageKey: "delivered", Size: int64(len(raw)), SHA256: hex.EncodeToString(sum[:])}},
			}}
			err := (&Adapter{inputAssets: &profiledInputs{bodies: [][]byte{raw}}}).ValidateInput(context.Background(), r)
			if tc.ok != (err == nil) {
				t.Fatalf("err=%v", err)
			}
			if !tc.ok {
				var refusal *generation.Error
				if !errors.As(err, &refusal) || !schema.IsCode(err, generation.ErrInvalidInput) {
					t.Fatalf("over the limit: %v", err)
				}
			}
		})
	}
}
