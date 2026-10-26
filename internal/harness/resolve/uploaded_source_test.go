package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/schema"
	"testing"
)

type uploadInspector struct {
	asset   generation.InputAsset
	profile modelinput.Profile
	err     error
	reads   int
}

func (u *uploadInspector) Inspect(_ context.Context, owner generation.OwnerID, id generation.AssetID) (generation.InputAsset, modelinput.Profile, error) {
	u.reads++
	if u.err != nil {
		return generation.InputAsset{}, modelinput.Profile{}, u.err
	}
	if owner != u.asset.OwnerID || id != u.asset.ID {
		return generation.InputAsset{}, modelinput.Profile{}, &generation.Error{Code: generation.ErrNotFound}
	}
	return u.asset, u.profile, nil
}
func (u *uploadInspector) InspectForParameter(ctx context.Context, owner generation.OwnerID, id generation.AssetID, _ generation.Parameter) (generation.InputAsset, modelinput.Profile, error) {
	return u.Inspect(ctx, owner, id)
}

type legacyUploadInspector struct{ source *uploadInspector }

func (u legacyUploadInspector) Inspect(ctx context.Context, owner generation.OwnerID, id generation.AssetID) (generation.InputAsset, modelinput.Profile, error) {
	return u.source.Inspect(ctx, owner, id)
}

func uploadedFixture(t *testing.T) (Service, *memoryStore, *readGateway, *uploadInspector, harness.Selection) {
	s, store, g := fixture(t)
	u := &uploadInspector{asset: generation.InputAsset{ID: "upload", OwnerID: "owner", OriginalFilename: "owned.glb", MediaType: "model/gltf-binary", Size: 512, SHA256: "original-bytes"}, profile: modelinput.Profile{ProfileID: modelinput.ProfileID, Revision: modelinput.ProfileRevision, Size: 512, SHA256: "original-bytes"}}
	s.UploadedModels = u
	selected := choose(t, s, "remesh")
	selected.Sources = []harness.Source{{Kind: harness.SourceInputAsset, Parameter: "source_asset", AssetID: "upload"}}
	return s, store, g, u, selected
}
func TestUploadedSourceUsesOwnedInputWithoutTask(t *testing.T) {
	s, store, g, _, selected := uploadedFixture(t)
	got, err := s.Update(context.Background(), "owner", "session", 0, selected)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.ResolveSelection(context.Background(), got)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != StatusReady || string(snapshot.Input["source_asset"]) != `"upload"` || len(snapshot.SourceFingerprints) != 1 {
		t.Fatalf("not ready: %+v", snapshot)
	}
	proof := snapshot.SourceFingerprints[0]
	if proof.Kind != harness.SourceInputAsset || proof.TaskID != "" || proof.AssetID != "upload" || proof.SHA256 != "original-bytes" || proof.ProfileID != modelinput.ProfileID || proof.ProfileRevision != modelinput.ProfileRevision {
		t.Fatalf("bad upload proof: %+v", proof)
	}
	if g.reads != 0 || g.paid != 0 || store.writes != 1 {
		t.Fatalf("unexpected Task access/write: %+v %+v", g, store)
	}
	encoded, _ := json.Marshal(snapshot)
	if len(encoded) == 0 {
		t.Fatal("empty checkpoint")
	}
}
func TestUploadedSourceRefusesForeignOrTaskOutputRelabel(t *testing.T) {
	s, store, g, u, selected := uploadedFixture(t)
	u.err = &generation.Error{Code: generation.ErrNotFound}
	_, err := s.Update(context.Background(), "owner", "session", 0, selected)
	var domain *generation.Error
	if !errors.As(err, &domain) || domain.Code != generation.ErrNotFound {
		t.Fatalf("want owned-upload not found: %v", err)
	}
	if store.writes != 0 || g.reads != 0 || g.paid != 0 {
		t.Fatal("fallback or mutation occurred")
	}
}

type parameterPolicyInspector struct {
	uploadInspector
	parameter                    generation.Parameter
	currentReads, parameterReads int
}

func (u *parameterPolicyInspector) Inspect(context.Context, generation.OwnerID, generation.AssetID) (generation.InputAsset, modelinput.Profile, error) {
	u.currentReads++
	return generation.InputAsset{}, modelinput.Profile{}, &generation.Error{Code: generation.ErrModelInputLimit}
}
func (u *parameterPolicyInspector) InspectForParameter(ctx context.Context, owner generation.OwnerID, id generation.AssetID, p generation.Parameter) (generation.InputAsset, modelinput.Profile, error) {
	u.parameterReads++
	u.parameter = p
	return u.uploadInspector.Inspect(ctx, owner, id)
}
func TestUploadedSourceRestoresFrozenParameterPolicyWithoutCurrentFallback(t *testing.T) {
	s, _, _, base, selected := uploadedFixture(t)
	inspector := &parameterPolicyInspector{uploadInspector: *base}
	s.UploadedModels = inspector
	limits := &schema.ModelInputLimits{MaxBytes: 1024, MaxJSONBytes: 1024, MaxDepth: 64, MaxElements: 1000, MaxVertices: 100, MaxTriangles: 100, MaxNodes: 100, MaxWork: 1 << 20, MaxImagePixels: 100, MaxTotalImagePixels: 100}
	parameter := generation.Parameter{Name: "source_asset", Type: "asset", Semantic: schema.SemanticSourceAsset, UploadedModel: &schema.UploadedModel{ProfileID: modelinput.ProfileID, ProfileRevision: modelinput.ProfileRevision, ProfileLimits: limits}}
	offering := generation.Offering{Parameters: []generation.Parameter{parameter}}
	frozen := Snapshot{Selection: selected, Input: map[string]json.RawMessage{"source_asset": json.RawMessage(`"upload"`)}, SourceFingerprints: []SourceFingerprint{{Kind: harness.SourceInputAsset, Parameter: "source_asset", AssetID: "upload", SHA256: base.asset.SHA256, Size: base.asset.Size, ProfileID: base.profile.ProfileID, ProfileRevision: base.profile.Revision}}}
	raw, err := json.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	var restored Snapshot
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyUploadedSource(context.Background(), "owner", restored, offering, restored.Input); err != nil {
		t.Fatalf("frozen restore used current policy: %v", err)
	}
	if inspector.currentReads != 0 || inspector.parameterReads != 1 || inspector.parameter.UploadedModel.ProfileLimits.MaxVertices != 100 {
		t.Fatalf("did not inspect exact frozen policy: %+v", inspector)
	}
}
func TestUploadedSourceFrozenPolicyRequiresParameterizedInspector(t *testing.T) {
	s, _, _, inspector, selected := uploadedFixture(t)
	s.UploadedModels = legacyUploadInspector{source: inspector}
	parameter := generation.Parameter{Name: "source_asset", Type: "asset", Semantic: schema.SemanticSourceAsset, UploadedModel: &schema.UploadedModel{ProfileID: modelinput.ProfileID, ProfileRevision: modelinput.ProfileRevision, ProfileLimits: &schema.ModelInputLimits{MaxBytes: 1024, MaxJSONBytes: 1024, MaxDepth: 64, MaxElements: 1000, MaxVertices: 100, MaxTriangles: 100, MaxNodes: 100, MaxWork: 1 << 20, MaxImagePixels: 100, MaxTotalImagePixels: 100}}}
	if _, _, err := s.uploadedSource(context.Background(), selected, generation.Offering{Parameters: []generation.Parameter{parameter}}); !errors.Is(err, ErrUnavailable) || inspector.reads != 0 {
		t.Fatalf("legacy inspector used for frozen policy: %v reads=%d", err, inspector.reads)
	}
	parameter.UploadedModel.ProfileRevision = ""
	parameter.UploadedModel.ProfileLimits = nil
	if _, _, err := s.uploadedSource(context.Background(), selected, generation.Offering{Parameters: []generation.Parameter{parameter}}); err != nil || inspector.reads != 1 {
		t.Fatalf("legacy declaration lost compatibility: %v reads=%d", err, inspector.reads)
	}
}

func TestUploadedSourcePartialPolicyIsInvalidWithoutAnyRead(t *testing.T) {
	for _, onlyRevision := range []bool{false, true} {
		s, _, _, inspector, selected := uploadedFixture(t)
		declaration := &schema.UploadedModel{ProfileID: modelinput.ProfileID}
		if onlyRevision {
			declaration.ProfileRevision = modelinput.ProfileRevision
		} else {
			declaration.ProfileLimits = &schema.ModelInputLimits{MaxBytes: 1024}
		}
		parameter := generation.Parameter{Name: "source_asset", Type: "asset", Semantic: schema.SemanticSourceAsset, UploadedModel: declaration}
		if _, _, err := s.uploadedSource(context.Background(), selected, generation.Offering{Parameters: []generation.Parameter{parameter}}); !errors.Is(err, harness.ErrInvalid) || inspector.reads != 0 {
			t.Fatalf("partial policy read/fallback: %v reads=%d", err, inspector.reads)
		}
	}
}

func TestUploadedSourceInvalidFrozenPolicyIsSnapshotRejection(t *testing.T) {
	s, _, _, inspector, selected := uploadedFixture(t)
	declaration := &schema.UploadedModel{ProfileID: modelinput.ProfileID, ProfileRevision: modelinput.ProfileRevision, ProfileLimits: &schema.ModelInputLimits{MaxBytes: 1024}}
	parameter := generation.Parameter{Name: "source_asset", Type: "asset", Semantic: schema.SemanticSourceAsset, UploadedModel: declaration}
	if _, _, err := s.uploadedSource(context.Background(), selected, generation.Offering{Parameters: []generation.Parameter{parameter}}); !errors.Is(err, harness.ErrInvalid) {
		t.Fatalf("malformed snapshot misclassified as infrastructure: %v", err)
	}
	if inspector.reads != 1 {
		t.Fatalf("fixture did not reach route verification: %d", inspector.reads)
	}
}
