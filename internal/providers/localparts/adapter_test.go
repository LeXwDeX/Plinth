package localparts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modelinspect"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// memoryInputs serves the frozen source bytes, counting opens so a cached
// validation verdict is observable.
type memoryInputs struct {
	bodies map[generation.AssetID][]byte
	opens  map[generation.AssetID]int
}

func newMemoryInputs(bodies map[generation.AssetID][]byte) *memoryInputs {
	return &memoryInputs{bodies: bodies, opens: map[generation.AssetID]int{}}
}

func (*memoryInputs) Resolve(context.Context, generation.OwnerID, generation.Offering, json.RawMessage) (map[string][]generation.InputAsset, error) {
	return nil, errors.New("resolve is not used by the local parts adapter")
}

func (m *memoryInputs) Open(_ context.Context, asset generation.InputAsset) (io.ReadCloser, error) {
	m.opens[asset.ID]++
	body, found := m.bodies[asset.ID]
	if !found {
		return nil, errors.New("missing fixture source")
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

// memoryStore is the pinned AssetStore port: it records every write and keeps
// the bytes so a test can re-validate a claimed asset exactly as the kernel
// does.
type memoryStore struct {
	assets  map[generation.AssetID]generation.Asset
	bodies  map[generation.AssetID][]byte
	writes  []generation.AssetWrite
	deleted []generation.AssetID
}

func newMemoryStore() *memoryStore {
	return &memoryStore{assets: map[generation.AssetID]generation.Asset{}, bodies: map[generation.AssetID][]byte{}}
}

func (m *memoryStore) Put(_ context.Context, write generation.AssetWrite) (generation.Asset, error) {
	body, err := io.ReadAll(write.Body)
	if err != nil {
		return generation.Asset{}, err
	}
	if int64(len(body)) > write.MaxBytes {
		return generation.Asset{}, &generation.Error{Code: generation.ErrInvalidInput, Operation: "put"}
	}
	asset := generation.Asset{
		ID: write.AssetID, TaskID: write.TaskID, AttemptID: write.AttemptID,
		Role: write.Role, Variant: write.Variant, Required: write.Required, Ordinal: write.Ordinal,
		StorageKey: "cas/" + string(write.AssetID), MediaType: write.MediaType,
		Size: int64(len(body)), SHA256: digest(body),
	}
	m.writes = append(m.writes, write)
	m.assets[asset.ID] = asset
	m.bodies[asset.ID] = body
	return asset, nil
}

func (m *memoryStore) Open(_ context.Context, asset generation.Asset) (io.ReadCloser, error) {
	body, found := m.bodies[asset.ID]
	if !found {
		return nil, errors.New("missing stored fixture output")
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

func (m *memoryStore) Delete(_ context.Context, asset generation.Asset) error {
	m.deleted = append(m.deleted, asset.ID)
	delete(m.assets, asset.ID)
	delete(m.bodies, asset.ID)
	return nil
}

func loadTestOffering(t testing.TB, id generation.OfferingID) generation.Offering {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatalf("load default catalog: %v", err)
	}
	offering, found := product.Get(id)
	if !found {
		t.Fatalf("offering %q is missing from the default catalog", id)
	}
	return offering
}

func newTestAdapter(t testing.TB, inputs *memoryInputs, store *memoryStore) *Adapter {
	t.Helper()
	adapter, err := New(Config{Inputs: inputs, Assets: store})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}
	return adapter
}

const (
	testOwner   = generation.OwnerID("owner-a")
	testSession = "session-a"
)

// sourceAsset is one model file as the kernel freezes it into the input of a
// successful Task of this session.
func sourceAsset(t testing.TB, id, task string, raw []byte) generation.InputAsset {
	t.Helper()
	return generation.InputAsset{
		ID: generation.AssetID(id), OwnerID: testOwner, StorageKey: "cas/" + id,
		OriginalFilename: id + ".glb", MediaType: modelMIME,
		Size: int64(len(raw)), SHA256: digest(raw),
		Role: "model", Variant: "glb", TaskID: generation.TaskID(task), SessionID: testSession,
		Ordinal: 1,
	}
}

func splitInputJSON(t testing.TB, source, mode string, ratio float64, maxParts int) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"source_asset": source, "mode": mode, "min_part_ratio": ratio, "max_parts": maxParts,
	})
	if err != nil {
		t.Fatalf("encode split input: %v", err)
	}
	return raw
}

func mergeInputJSON(t testing.TB, sources []generation.InputAsset, name string) json.RawMessage {
	t.Helper()
	ids := make([]string, 0, len(sources))
	for _, source := range sources {
		ids = append(ids, string(source.ID))
	}
	input := map[string]any{"source_assets": ids}
	if name != "" {
		input["name"] = name
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("encode merge input: %v", err)
	}
	return raw
}

func splitValidation(offering generation.Offering, input json.RawMessage, sources ...generation.InputAsset) generation.InputValidationRequest {
	return generation.InputValidationRequest{
		SessionID: testSession, OwnerID: testOwner, Offering: offering,
		Input: input, InputAssets: map[string][]generation.InputAsset{"source_asset": sources},
	}
}

func mergeValidation(offering generation.Offering, input json.RawMessage, sources ...generation.InputAsset) generation.InputValidationRequest {
	return generation.InputValidationRequest{
		SessionID: testSession, OwnerID: testOwner, Offering: offering,
		Input: input, InputAssets: map[string][]generation.InputAsset{"source_assets": sources},
	}
}

func inspectFixture(t testing.TB, raw []byte) modelinspect.Report {
	t.Helper()
	model, err := modelinspect.Parse(bytes.NewReader(raw), int64(len(raw)), modelinspect.DefaultLimits())
	if err != nil {
		t.Fatalf("parse fixture model: %v", err)
	}
	return modelinspect.Inspect(model, modelinspect.InspectOptions{})
}

type worldBounds struct {
	min [3]float64
	max [3]float64
	set bool
}

func (b *worldBounds) add(report modelinspect.BBoxReport) {
	for axis := 0; axis < 3; axis++ {
		if !b.set {
			b.min[axis], b.max[axis] = report.Min[axis], report.Max[axis]
			continue
		}
		b.min[axis] = math.Min(b.min[axis], report.Min[axis])
		b.max[axis] = math.Max(b.max[axis], report.Max[axis])
	}
	b.set = true
}

func assertBounds(t testing.TB, got worldBounds, want modelinspect.BBoxReport) {
	t.Helper()
	if !got.set {
		t.Fatal("no output bounds were collected")
	}
	for axis := 0; axis < 3; axis++ {
		if math.Abs(got.min[axis]-want.Min[axis]) > 1e-9 || math.Abs(got.max[axis]-want.Max[axis]) > 1e-9 {
			t.Fatalf("union bounds min=%v max=%v, want min=%v max=%v", got.min, got.max, want.Min, want.Max)
		}
	}
}

func validateSplitOutputs(t testing.TB, store *memoryStore, claims []generation.ArtifactClaim, parts int) (worldBounds, int64) {
	t.Helper()
	if len(claims) != parts+1 {
		t.Fatalf("claims = %d, want %d model parts + 1 manifest", len(claims), parts)
	}
	var union worldBounds
	var triangles int64
	for index, claim := range claims[:parts] {
		if claim.Role != "model" || claim.StoredAsset == nil || claim.StoredAsset.Role != "model" {
			t.Fatalf("claim[%d] is not a model artifact: %+v", index, claim)
		}
		if claim.StoredAsset.Ordinal != index+1 {
			t.Fatalf("claim[%d] ordinal = %d, want %d", index, claim.StoredAsset.Ordinal, index+1)
		}
		body, found := store.bodies[claim.StoredAsset.ID]
		if !found {
			t.Fatalf("claim[%d] has no stored bytes", index)
		}
		if _, err := modelinput.ValidateGLBBytes(context.Background(), body, modelinput.GeneratedPolicy()); err != nil {
			t.Fatalf("output part %d is not a valid generated GLB: %v", index+1, err)
		}
		report := inspectFixture(t, body)
		union.add(report.BBox)
		triangles += report.Triangles
	}
	manifestClaim := claims[len(claims)-1]
	if manifestClaim.Role != manifestRole || manifestClaim.StoredAsset == nil ||
		manifestClaim.StoredAsset.MediaType != manifestMIME || manifestClaim.StoredAsset.Ordinal != 1 {
		t.Fatalf("manifest claim = %+v", manifestClaim)
	}
	return union, triangles
}

func decodeManifest(t testing.TB, store *memoryStore, claim generation.ArtifactClaim) schema.ModelPartsManifest {
	t.Helper()
	body, found := store.bodies[claim.StoredAsset.ID]
	if !found {
		t.Fatal("manifest bytes are missing")
	}
	var manifest schema.ModelPartsManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return manifest
}

func TestAdapterSplitsEveryModeIntoOwnedValidParts(t *testing.T) {
	raw := twoCubesAndFragment(t)
	sourceReport := inspectFixture(t, raw)
	if sourceReport.Triangles != 25 {
		t.Fatalf("fixture triangles = %d, want 25", sourceReport.Triangles)
	}
	offering := loadTestOffering(t, "local/model-parts/split")
	cases := []struct {
		mode  string
		parts int
	}{
		{schema.ModelSplitModeNode, 1},
		{schema.ModelSplitModePrimitive, 3},
		{schema.ModelSplitModeMaterial, 2},
		{schema.ModelSplitModeConnectedComponent, 3},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			source := sourceAsset(t, "source-"+tc.mode, "source-task", raw)
			inputs := newMemoryInputs(map[generation.AssetID][]byte{source.ID: raw})
			store := newMemoryStore()
			adapter := newTestAdapter(t, inputs, store)
			input := splitInputJSON(t, string(source.ID), tc.mode, 0.002, 32)
			request := splitValidation(offering, input, source)
			if err := adapter.ValidateInput(context.Background(), request); err != nil {
				t.Fatalf("ValidateInput: %v (cause: %v)", err, errors.Unwrap(err))
			}
			observation, err := adapter.Start(context.Background(), generation.StartRequest{
				SessionID: testSession, OwnerID: testOwner, TaskID: "split-task", AttemptID: "attempt-1",
				Offering: offering, Input: input,
				InputAssets: map[string][]generation.InputAsset{"source_asset": {source}},
			})
			if err != nil {
				t.Fatalf("Start: %v (cause: %v)", err, errors.Unwrap(err))
			}
			if observation.Phase != generation.UpstreamCompleted || observation.Progress != 100 || observation.DedupeKey == "" {
				t.Fatalf("observation = %+v", observation)
			}
			union, triangles := validateSplitOutputs(t, store, observation.Artifacts, tc.parts)
			if triangles != sourceReport.Triangles {
				t.Fatalf("output triangles = %d, want %d", triangles, sourceReport.Triangles)
			}
			assertBounds(t, union, sourceReport.BBox)
			manifest := decodeManifest(t, store, observation.Artifacts[len(observation.Artifacts)-1])
			if manifest.Version != schema.ModelPartsManifestVersion || manifest.Mode != tc.mode || len(manifest.Parts) != tc.parts {
				t.Fatalf("manifest = %+v", manifest)
			}
			manifestTriangles := 0
			var manifestUnion worldBounds
			for index, part := range manifest.Parts {
				if part.Ordinal != index+1 || part.Name == "" || part.Triangles <= 0 {
					t.Fatalf("manifest part[%d] = %+v", index, part)
				}
				manifestTriangles += part.Triangles
				manifestUnion.add(modelinspect.BBoxReport{Min: part.Bounds.Min, Max: part.Bounds.Max})
			}
			if int64(manifestTriangles) != sourceReport.Triangles {
				t.Fatalf("manifest triangles = %d, want %d", manifestTriangles, sourceReport.Triangles)
			}
			assertBounds(t, manifestUnion, sourceReport.BBox)
			// Delivery isolation: every stored blob belongs to this attempt and
			// this owner only, and nothing was deleted on the success path.
			if len(store.writes) != tc.parts+1 || len(store.deleted) != 0 {
				t.Fatalf("writes = %d deleted = %d, want %d and 0", len(store.writes), len(store.deleted), tc.parts+1)
			}
			seen := map[generation.AssetID]bool{}
			for _, write := range store.writes {
				if write.OwnerID != testOwner || write.TaskID != "split-task" || write.AttemptID != "attempt-1" {
					t.Fatalf("write ownership = %+v", write)
				}
				if seen[write.AssetID] {
					t.Fatalf("duplicate asset ID %q", write.AssetID)
				}
				seen[write.AssetID] = true
			}
		})
	}
}

func TestAdapterKeepsTexturesAndAncestorTransforms(t *testing.T) {
	raw := texturedHierarchy(t)
	sourceReport := inspectFixture(t, raw)
	if sourceReport.Triangles != 4 || sourceReport.TextureCount != 2 || sourceReport.MaterialCount != 2 {
		t.Fatalf("fixture report = %+v", sourceReport)
	}
	offering := loadTestOffering(t, "local/model-parts/split")
	source := sourceAsset(t, "textured-source", "source-task", raw)
	inputs := newMemoryInputs(map[generation.AssetID][]byte{source.ID: raw})
	store := newMemoryStore()
	adapter := newTestAdapter(t, inputs, store)
	input := splitInputJSON(t, string(source.ID), schema.ModelSplitModePrimitive, 0.002, 32)
	observation, err := adapter.Start(context.Background(), generation.StartRequest{
		SessionID: testSession, OwnerID: testOwner, TaskID: "split-task", AttemptID: "attempt-1",
		Offering: offering, Input: input,
		InputAssets: map[string][]generation.InputAsset{"source_asset": {source}},
	})
	if err != nil {
		t.Fatalf("Start: %v (cause: %v)", err, errors.Unwrap(err))
	}
	union, triangles := validateSplitOutputs(t, store, observation.Artifacts, 2)
	if triangles != sourceReport.Triangles {
		t.Fatalf("output triangles = %d, want %d", triangles, sourceReport.Triangles)
	}
	assertBounds(t, union, sourceReport.BBox)
	for index := 0; index < 2; index++ {
		body := store.bodies[observation.Artifacts[index].StoredAsset.ID]
		document, _, err := parseGLB(body, modelinput.GeneratedPolicy().Limits)
		if err != nil {
			t.Fatalf("parse output %d: %v", index, err)
		}
		if len(document.Images) != 1 || document.Images[0].MimeType != "image/png" ||
			len(document.Textures) != 1 || len(document.Materials) != 1 {
			t.Fatalf("output %d textures = images:%d textures:%d materials:%d", index,
				len(document.Images), len(document.Textures), len(document.Materials))
		}
		if document.Materials[0].PBR == nil || document.Materials[0].PBR.BaseColorTexture == nil ||
			document.Materials[0].PBR.BaseColorTexture.Index != 0 {
			t.Fatalf("output %d lost its base-color texture: %+v", index, document.Materials[0])
		}
		if len(document.Nodes) != 4 {
			// One part root plus the source chain stage -> turntable -> model.
			t.Fatalf("output %d nodes = %d, want 4", index, len(document.Nodes))
		}
		// buildGroup writes the leaf first, then its ancestors, and buildPart
		// appends the part root last: model, turntable, stage, root.
		names := []string{document.Nodes[0].Name, document.Nodes[1].Name, document.Nodes[2].Name}
		if strings.Join(names, "/") != "model/turntable/stage" {
			t.Fatalf("output %d ancestor chain = %v", index, names)
		}
		if len(document.Nodes[2].Translation) != 3 || document.Nodes[2].Translation[0] != 10 ||
			len(document.Nodes[1].Rotation) != 4 || len(document.Nodes[1].Scale) != 3 ||
			document.Nodes[1].Scale[0] != 2 || len(document.Nodes[0].Translation) != 3 ||
			document.Nodes[0].Translation[0] != 1 {
			t.Fatalf("output %d lost a source transform: %+v", index, document.Nodes)
		}
	}
}

func TestAdapterMergeWrapsEverySourceAsItsOwnNode(t *testing.T) {
	first := twoCubesAndFragment(t)
	second := texturedHierarchy(t)
	firstReport := inspectFixture(t, first)
	secondReport := inspectFixture(t, second)
	offering := loadTestOffering(t, "local/model-parts/merge")
	one := sourceAsset(t, "first-model", "task-a", first)
	one.OriginalFilename = "toy.glb"
	two := sourceAsset(t, "second-model", "task-b", second)
	two.OriginalFilename = "model.glb"
	inputs := newMemoryInputs(map[generation.AssetID][]byte{one.ID: first, two.ID: second})
	store := newMemoryStore()
	adapter := newTestAdapter(t, inputs, store)
	input := mergeInputJSON(t, []generation.InputAsset{one, two}, "collection")
	request := mergeValidation(offering, input, one, two)
	if err := adapter.ValidateInput(context.Background(), request); err != nil {
		t.Fatalf("ValidateInput: %v (cause: %v)", err, errors.Unwrap(err))
	}
	observation, err := adapter.Start(context.Background(), generation.StartRequest{
		SessionID: testSession, OwnerID: testOwner, TaskID: "merge-task", AttemptID: "attempt-1",
		Offering: offering, Input: input,
		InputAssets: map[string][]generation.InputAsset{"source_assets": {one, two}},
	})
	if err != nil {
		t.Fatalf("Start: %v (cause: %v)", err, errors.Unwrap(err))
	}
	if len(observation.Artifacts) != 1 || observation.Artifacts[0].Role != "model" || observation.Artifacts[0].StoredAsset == nil {
		t.Fatalf("merge artifacts = %+v", observation.Artifacts)
	}
	body := store.bodies[observation.Artifacts[0].StoredAsset.ID]
	if _, err := modelinput.ValidateGLBBytes(context.Background(), body, modelinput.GeneratedPolicy()); err != nil {
		t.Fatalf("merged output is not a valid generated GLB: %v", err)
	}
	report := inspectFixture(t, body)
	if report.Triangles != firstReport.Triangles+secondReport.Triangles {
		t.Fatalf("merged triangles = %d, want %d", report.Triangles, firstReport.Triangles+secondReport.Triangles)
	}
	var want worldBounds
	want.add(firstReport.BBox)
	want.add(secondReport.BBox)
	assertBounds(t, want, report.BBox)
	document, _, err := parseGLB(body, modelinput.GeneratedPolicy().Limits)
	if err != nil {
		t.Fatalf("parse merged output: %v", err)
	}
	if len(document.Scenes) != 1 || len(document.Scenes[0].Nodes) != 2 {
		t.Fatalf("merged scene roots = %+v", document.Scenes)
	}
	wrappers := []nodeDef{document.Nodes[document.Scenes[0].Nodes[0]], document.Nodes[document.Scenes[0].Nodes[1]]}
	if wrappers[0].Name != "toy" || wrappers[1].Name != "model" {
		t.Fatalf("merge wrapper names = %q, %q", wrappers[0].Name, wrappers[1].Name)
	}
	if len(wrappers[0].Children) != 1 || len(wrappers[1].Children) != 1 {
		t.Fatalf("merge wrappers = %+v", wrappers)
	}
	if len(document.Materials) != 4 || len(document.Images) != 2 {
		// Two untextured cube materials plus two textured quad materials.
		t.Fatalf("merged materials = %d images = %d, want 4 and 2", len(document.Materials), len(document.Images))
	}
	if len(store.writes) != 1 || len(store.deleted) != 0 {
		t.Fatalf("merge writes = %d deleted = %d, want 1 and 0", len(store.writes), len(store.deleted))
	}
	if write := store.writes[0]; write.OwnerID != testOwner || write.TaskID != "merge-task" || write.AttemptID != "attempt-1" {
		t.Fatalf("merge write ownership = %+v", write)
	}
}

func TestAdapterSplitHonoursRatioAndPartCap(t *testing.T) {
	raw := twoCubesAndFragment(t)
	sourceReport := inspectFixture(t, raw)
	offering := loadTestOffering(t, "local/model-parts/split")
	cases := []struct {
		name     string
		mode     string
		ratio    float64
		maxParts int
		want     int
		wantRest bool
	}{
		// The one-triangle fragment is 4% of the model; a 10% floor folds it
		// into the nearest surviving part without changing the union bounds.
		{name: "small parts are absorbed", mode: schema.ModelSplitModeConnectedComponent, ratio: 0.1, maxParts: 32, want: 2},
		// Three primitives with a cap of two keep the largest and collect the
		// rest, including the fragment, under the name "rest".
		{name: "parts above the cap become rest", mode: schema.ModelSplitModePrimitive, ratio: 0, maxParts: 2, want: 2, wantRest: true},
		{name: "a cap of one folds everything", mode: schema.ModelSplitModePrimitive, ratio: 0, maxParts: 1, want: 1, wantRest: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := sourceAsset(t, "source-"+tc.mode, "source-task", raw)
			inputs := newMemoryInputs(map[generation.AssetID][]byte{source.ID: raw})
			store := newMemoryStore()
			adapter := newTestAdapter(t, inputs, store)
			input := splitInputJSON(t, string(source.ID), tc.mode, tc.ratio, tc.maxParts)
			observation, err := adapter.Start(context.Background(), generation.StartRequest{
				SessionID: testSession, OwnerID: testOwner, TaskID: "split-task", AttemptID: "attempt-1",
				Offering: offering, Input: input,
				InputAssets: map[string][]generation.InputAsset{"source_asset": {source}},
			})
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			union, triangles := validateSplitOutputs(t, store, observation.Artifacts, tc.want)
			if triangles != sourceReport.Triangles {
				t.Fatalf("output triangles = %d, want %d", triangles, sourceReport.Triangles)
			}
			assertBounds(t, union, sourceReport.BBox)
			manifest := decodeManifest(t, store, observation.Artifacts[len(observation.Artifacts)-1])
			rest := false
			for _, part := range manifest.Parts {
				rest = rest || part.Name == "rest"
			}
			if rest != tc.wantRest {
				t.Fatalf("manifest parts = %+v, want a rest part: %v", manifest.Parts, tc.wantRest)
			}
		})
	}
}

func TestAdapterRejectsInvalidSourcesAndBounds(t *testing.T) {
	valid := twoCubesAndFragment(t)
	offering := loadTestOffering(t, "local/model-parts/split")
	mergeOffering := loadTestOffering(t, "local/model-parts/merge")
	cases := []struct {
		name     string
		offering generation.Offering
		input    json.RawMessage
		sources  []generation.InputAsset
		change   func(*generation.InputAsset)
		body     []byte
		code     generation.ErrorCode
	}{
		{
			name: "skinned source", offering: offering, body: valid,
			input:   splitInputJSON(t, "source-id", schema.ModelSplitModeNode, 0.002, 32),
			sources: []generation.InputAsset{sourceAsset(t, "source-id", "task", valid)},
			code:    generation.ErrUnsupportedModelInput,
		},
		{
			name: "cross-user source", offering: offering, body: valid,
			input:  splitInputJSON(t, "source-id", schema.ModelSplitModeNode, 0.002, 32),
			change: func(asset *generation.InputAsset) { asset.OwnerID = "owner-b" },
			code:   generation.ErrInvalidInput,
		},
		{
			name: "unfinished task source", offering: offering, body: valid,
			input:  splitInputJSON(t, "source-id", schema.ModelSplitModeNode, 0.002, 32),
			change: func(asset *generation.InputAsset) { asset.TaskID = "" },
			code:   generation.ErrUnsupportedModelInput,
		},
		{
			name: "non-model source", offering: offering, body: valid,
			input:  splitInputJSON(t, "source-id", schema.ModelSplitModeNode, 0.002, 32),
			change: func(asset *generation.InputAsset) { asset.Role = "thumbnail" },
			code:   generation.ErrUnsupportedModelInput,
		},
		{
			name: "changed bytes", offering: offering, body: valid,
			input:  splitInputJSON(t, "source-id", schema.ModelSplitModeNode, 0.002, 32),
			change: func(asset *generation.InputAsset) { asset.SHA256 = strings.Repeat("0", 64) },
			code:   generation.ErrInvalidInput,
		},
		{
			name: "too many parts", offering: offering, body: valid,
			input: splitInputJSON(t, "source-id", schema.ModelSplitModeNode, 0.002, 65),
			code:  generation.ErrInvalidInput,
		},
		{
			name: "too few merge sources", offering: mergeOffering, body: valid,
			input: mergeInputJSON(t, []generation.InputAsset{sourceAsset(t, "source-id", "task", valid)}, ""),
			code:  generation.ErrInvalidInput,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sources := tc.sources
			if len(sources) == 0 {
				sources = []generation.InputAsset{sourceAsset(t, "source-id", "task", valid)}
			}
			for index := range sources {
				if tc.change != nil {
					tc.change(&sources[index])
				}
			}
			body := tc.body
			if tc.name == "skinned source" {
				body = skinnedModel(t)
				sources[0] = sourceAsset(t, "source-id", "task", body)
			}
			inputs := newMemoryInputs(map[generation.AssetID][]byte{sources[0].ID: body})
			store := newMemoryStore()
			adapter := newTestAdapter(t, inputs, store)
			var request generation.InputValidationRequest
			if tc.offering.Capability == generation.ModelMerge {
				request = mergeValidation(tc.offering, tc.input, sources...)
			} else {
				request = splitValidation(tc.offering, tc.input, sources[0])
			}
			err := adapter.ValidateInput(context.Background(), request)
			if !generation.IsCode(err, tc.code) {
				t.Fatalf("ValidateInput error = %v, want code %q", err, tc.code)
			}
			if len(store.writes) != 0 {
				t.Fatalf("a rejected validation wrote %d assets", len(store.writes))
			}
		})
	}
}

func TestAdapterRejectsWorkAboveTheFrozenBudget(t *testing.T) {
	raw := twoCubesAndFragment(t)
	offering := loadTestOffering(t, "local/model-parts/split")
	var options map[string]any
	if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
		t.Fatal(err)
	}
	options["max_work"] = 1
	offering.ProviderOptions, _ = json.Marshal(options)
	source := sourceAsset(t, "source-id", "task", raw)
	inputs := newMemoryInputs(map[generation.AssetID][]byte{source.ID: raw})
	adapter := newTestAdapter(t, inputs, newMemoryStore())
	err := adapter.ValidateInput(context.Background(),
		splitValidation(offering, splitInputJSON(t, string(source.ID), schema.ModelSplitModeNode, 0.002, 32), source))
	if !generation.IsCode(err, generation.ErrModelInputLimit) {
		t.Fatalf("ValidateInput error = %v, want %q", err, generation.ErrModelInputLimit)
	}
}

func TestAdapterCachesDeterministicValidationVerdicts(t *testing.T) {
	raw := twoCubesAndFragment(t)
	offering := loadTestOffering(t, "local/model-parts/split")
	source := sourceAsset(t, "source-id", "task", raw)
	inputs := newMemoryInputs(map[generation.AssetID][]byte{source.ID: raw})
	adapter := newTestAdapter(t, inputs, newMemoryStore())
	request := splitValidation(offering, splitInputJSON(t, string(source.ID), schema.ModelSplitModeNode, 0.002, 32), source)
	if err := adapter.ValidateInput(context.Background(), request); err != nil {
		t.Fatalf("first validation: %v (cause: %v)", err, errors.Unwrap(err))
	}
	opens := inputs.opens[source.ID]
	if opens == 0 {
		t.Fatal("validation did not read the source")
	}
	if err := adapter.ValidateInput(context.Background(), request); err != nil {
		t.Fatalf("second validation: %v", err)
	}
	if inputs.opens[source.ID] != opens {
		t.Fatalf("cached validation re-read the source: opens %d -> %d", opens, inputs.opens[source.ID])
	}
	// A different source digest is a different verdict; the cache must not
	// answer for it.
	changed := sourceAsset(t, "other-id", "task", raw)
	inputs.bodies[changed.ID] = raw
	if err := adapter.ValidateInput(context.Background(),
		splitValidation(offering, splitInputJSON(t, string(changed.ID), schema.ModelSplitModeNode, 0.002, 32), changed)); err != nil {
		t.Fatalf("changed source validation: %v", err)
	}
	if inputs.opens[changed.ID] == 0 {
		t.Fatal("a different source digest reused another verdict")
	}
}
