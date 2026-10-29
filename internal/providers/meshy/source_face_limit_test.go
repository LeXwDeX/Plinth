package meshy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #103: Meshy Rigging accepts at most 320,000 faces; the Schema counts
// triangles. These tests drive the real default-catalog Rigging Offering.
func riggingFaceRequest(t *testing.T, raw []byte, source string) (generation.InputValidationRequest, *catalog.Catalog) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := product.Get("meshy/processing/rigging")
	if !ok {
		t.Fatal("rigging offering missing")
	}
	offering.ExecutionMessages = product.Application().Runtime.GenerationMessages
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	request := generation.InputValidationRequest{OwnerID: "owner", Offering: offering}
	switch source {
	case "task":
		request.Input = json.RawMessage(`{"source_task":"src-task"}`)
		request.TaskDependencies = map[string]generation.TaskDependency{"source_task": {
			TaskID: "src-task", ProviderID: "meshy", OfferingID: "meshy/processing/remesh", Capability: generation.Remesh,
			Assets: []generation.Asset{{ID: "glb", TaskID: "src-task", Role: "model", Variant: "glb", StorageKey: "k", MediaType: "model/gltf-binary", Size: int64(len(raw)), SHA256: digest}},
		}}
	case "asset":
		request.Input = json.RawMessage(`{"source_asset":"owned-model"}`)
		request.InputAssets = map[string][]generation.InputAsset{"source_asset": {{ID: "owned-model", OwnerID: "owner", StorageKey: "k", MediaType: "model/gltf-binary", Size: int64(len(raw)), SHA256: digest}}}
	}
	return request, product
}

func TestRiggingSourceFaceLimit(t *testing.T) {
	for _, source := range []string{"task", "asset"} {
		for _, test := range []struct {
			name  string
			grid  int // Large(grid) has 2*(grid-1)^2 triangles
			faces int64
			allow bool
		}{
			{"below limit", 100, 19602, true},
			{"exactly at limit", 401, 320000, true},
			{"one step over limit", 402, 321602, false},
			{"remesh quad default (about 600k)", 548, 598418, false},
		} {
			t.Run(source+"/"+test.name, func(t *testing.T) {
				raw := modelinputtest.Large(modelinputtest.LargeOptions{Grid: test.grid})
				request, _ := riggingFaceRequest(t, raw, source)
				err := (&Adapter{inputAssets: &profiledInputs{bodies: [][]byte{raw}}}).ValidateInput(context.Background(), request)
				if test.allow {
					if err != nil {
						t.Fatalf("within limit refused: %v", err)
					}
					return
				}
				refusal := generation.UserFacingError(err)
				if refusal == nil || !refusal.UserFacing || refusal.Code != generation.ErrInvalidInput {
					t.Fatalf("over limit error = %v", err)
				}
				for _, want := range []string{formatInt(test.faces), "320000", "没有提交"} {
					if !strings.Contains(refusal.Detail, want) {
						t.Fatalf("detail %q lacks %q", refusal.Detail, want)
					}
				}
			})
		}
	}
}

func TestRiggingSourceFaceLimitEnglishAndSchemaCopy(t *testing.T) {
	raw := modelinputtest.Large(modelinputtest.LargeOptions{Grid: 402})
	request, product := riggingFaceRequest(t, raw, "task")
	texts, err := catalog.NewTextLocalizer(product.Application())
	if err != nil {
		t.Fatal(err)
	}
	refusal := generation.UserFacingError((&Adapter{inputAssets: &profiledInputs{bodies: [][]byte{raw}}}).ValidateInput(context.Background(), request))
	if refusal == nil {
		t.Fatal("expected refusal")
	}
	english := refusal.LocalizedDetail(func(text string) string { return texts.Text("en", text) })
	if han.MatchString(english) || !strings.Contains(english, "321602") || !strings.Contains(english, "320000") {
		t.Fatalf("english refusal = %q", english)
	}
	if refusal.Cause == nil {
		t.Fatal("original diagnostic lost")
	}
}

// Sources the platform cannot measure are not blocked: the upstream decides.
func TestRiggingSourceFaceLimitUnknownSourcesPass(t *testing.T) {
	big := modelinputtest.Large(modelinputtest.LargeOptions{Grid: 402})
	request, _ := riggingFaceRequest(t, big, "none")
	request.Input = json.RawMessage(`{"source_url":"https://assets.meshy.ai/x.glb"}`)
	if err := (&Adapter{inputAssets: &profiledInputs{bodies: [][]byte{big}}}).ValidateInput(context.Background(), request); err != nil {
		t.Fatalf("url source blocked: %v", err)
	}
	garbage := []byte("not a glb at all, but long enough to be stored data")
	request, _ = riggingFaceRequest(t, garbage, "task")
	if err := (&Adapter{inputAssets: &profiledInputs{bodies: [][]byte{garbage}}}).ValidateInput(context.Background(), request); err != nil {
		t.Fatalf("unparseable source blocked: %v", err)
	}
	request, _ = riggingFaceRequest(t, big, "task")
	dependency := request.TaskDependencies["source_task"]
	dependency.Assets[0].MediaType = "application/octet-stream"
	request.TaskDependencies["source_task"] = dependency
	if err := (&Adapter{inputAssets: &profiledInputs{bodies: [][]byte{big}}}).ValidateInput(context.Background(), request); err != nil {
		t.Fatalf("non-GLB source blocked: %v", err)
	}
}

func TestSourceTriangleCountOnMinimalGLB(t *testing.T) {
	policy := modelinput.Policy{ProfileID: modelinput.GeneratedProfileID, Revision: modelinput.GeneratedProfileRevision, Limits: sourceFaceReadLimits}
	for name, test := range map[string]struct {
		raw  []byte
		want int64
	}{
		"one triangle": {modelinputtest.Triangle(modelinputtest.Options{}), 1},
		"3x3 grid":     {modelinputtest.Large(modelinputtest.LargeOptions{Grid: 3}), 8},
	} {
		t.Run(name, func(t *testing.T) {
			request, _ := riggingFaceRequest(t, test.raw, "asset")
			asset := request.InputAssets["source_asset"][0]
			got, known, err := (&Adapter{inputAssets: &profiledInputs{bodies: [][]byte{test.raw}}}).countSourceTriangles(context.Background(), request.Offering, asset, policy)
			if err != nil || !known || got != test.want {
				t.Fatalf("triangles=%d known=%v err=%v want %d", got, known, err, test.want)
			}
		})
	}
}

func TestSourceModelLimitDeclarationIsValidated(t *testing.T) {
	request, _ := riggingFaceRequest(t, nil, "none")
	if err := ValidateOffering(request.Offering); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*wireOptions){
		"zero limit": func(o *wireOptions) { o.SourceModelLimit.MaxTriangles = 0 },
		"bad basis":  func(o *wireOptions) { o.SourceModelLimit.CountBasis = "quads" },
		"huge limit": func(o *wireOptions) { o.SourceModelLimit.MaxTriangles = 1 << 40 },
	} {
		t.Run(name, func(t *testing.T) {
			bad := request
			mutateProfileOptions(&bad, mutate)
			if ValidateOffering(bad.Offering) == nil {
				t.Fatal("invalid source_model_limit accepted")
			}
		})
	}
}

func formatInt(n int64) string { return strconv.FormatInt(n, 10) }
