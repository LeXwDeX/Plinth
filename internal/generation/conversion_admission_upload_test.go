package generation_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modeldelivery"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/localmodel"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

// #330 final-review P1: production injects the Schema conversion rule and the
// local-model adapter. These tests exercise the same wiring for the two
// uploaded/saved-file routes the projection can offer: a direct local GLB
// upload and the Meshy saved-file fallback of a generated model.

func newProductionConversionHarness(t *testing.T) *harness {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return newConfiguredHarness(t, mock.New(), nil, func(deps *generation.Dependencies) {
		adapter, err := localmodel.New(localmodel.Config{Inputs: deps.Inputs, Assets: deps.Assets})
		if err != nil {
			t.Fatal(err)
		}
		registry, ok := deps.Providers.(*providers.Registry)
		if !ok {
			t.Fatalf("provider registry = %T", deps.Providers)
		}
		if err := registry.Register("local.model", adapter); err != nil {
			t.Fatal(err)
		}
		deps.Conversions = modeldelivery.NewSchemaConversionRule(product.Application().ModelDelivery)
	})
}

func uploadTriangleGLB(t *testing.T, h *harness, owner generation.OwnerID) generation.AssetID {
	t.Helper()
	raw := modelinputtest.Triangle(modelinputtest.Options{Indexed: true, Interleaved: true, Textured: true})
	uploaded, err := h.inputs.UploadAsset(context.Background(), owner, generation.InputAssetUpload{
		Body: bytes.NewReader(raw), OriginalFilename: "uploaded-triangle.glb",
		AssetType: "model", MediaType: "model/gltf-binary", MaxBytes: 2 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	return uploaded.ID
}

func prepareSourceAsset(t *testing.T, h *harness, owner generation.OwnerID, session string, offering generation.OfferingID, asset generation.AssetID) generation.PreparedInput {
	t.Helper()
	prepared, err := h.module.PrepareInput(context.Background(), generation.PrepareInputCommand{
		OwnerID: owner, SessionID: session, Capability: generation.FormatConversion,
		OfferingID: offering, Input: []byte(`{"source_asset":"` + string(asset) + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func TestUploadedLocalGLBConversionIsAdmittedAndDuplicatesRefused330(t *testing.T) {
	h := newProductionConversionHarness(t)
	ctx := context.Background()
	session := "generation-test-session:owner-a"
	h.submitFixtureState.ensureSession(t, ctx, h.repository, h.clock, "owner-a", session)
	first := uploadTriangleGLB(t, h, "owner-a")
	second := uploadTriangleGLB(t, h, "owner-a")

	one := prepareSourceAsset(t, h, "owner-a", session, "local/static-glb/to-gltf", first)
	accepted, err := h.module.SubmitPrepared(ctx, authorizePrepared(t, ctx, h.repository, one, "uploaded-local-one"))
	if err != nil {
		t.Fatalf("first uploaded local conversion: %v", err)
	}
	if accepted.TaskID == "" {
		t.Fatal("no Task for the uploaded local conversion")
	}
	duplicate := prepareSourceAsset(t, h, "owner-a", session, "local/static-glb/to-gltf", first)
	if _, err := h.module.SubmitPrepared(ctx, authorizePrepared(t, ctx, h.repository, duplicate, "uploaded-local-two")); !errors.Is(err, generation.ErrConversionInProgress) {
		t.Fatalf("duplicate uploaded local conversion = %v", err)
	}
	other := prepareSourceAsset(t, h, "owner-a", session, "local/static-glb/to-gltf", second)
	if _, err := h.module.SubmitPrepared(ctx, authorizePrepared(t, ctx, h.repository, other, "uploaded-local-other")); err != nil {
		t.Fatalf("another uploaded input was refused: %v", err)
	}
}

func TestUploadedLocalGLBConversionRejectsAnotherOwner330(t *testing.T) {
	h := newProductionConversionHarness(t)
	ctx := context.Background()
	ownerSession := "generation-test-session:owner-a"
	foreignSession := "generation-test-session:owner-b"
	h.submitFixtureState.ensureSession(t, ctx, h.repository, h.clock, "owner-a", ownerSession)
	h.submitFixtureState.ensureSession(t, ctx, h.repository, h.clock, "owner-b", foreignSession)
	uploaded := uploadTriangleGLB(t, h, "owner-a")

	_, err := h.module.PrepareInput(ctx, generation.PrepareInputCommand{
		OwnerID: "owner-b", SessionID: foreignSession, Capability: generation.FormatConversion,
		OfferingID: "local/static-glb/to-gltf", Input: []byte(`{"source_asset":"` + string(uploaded) + `"}`),
	})
	if err == nil {
		t.Fatal("another owner used an uploaded source")
	}
	page, err := h.module.List(ctx, generation.OwnTasks("owner-b"), generation.ListQuery{SessionID: foreignSession, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 {
		t.Fatalf("cross-owner refusal created tasks: %#v", page.Tasks)
	}
}

func TestMeshySavedFileConversionIsAdmittedAndDuplicatesRefused330(t *testing.T) {
	h := newProductionConversionHarness(t)
	ctx := context.Background()
	firstSource := runToTerminal(t, h, "owner-a", submitConversionSource(t, h, "saved-file one"), 30)
	secondSource := runToTerminal(t, h, "owner-a", submitConversionSource(t, h, "saved-file two"), 30)
	firstGLB := modelAssetOf(t, firstSource, "glb")
	secondGLB := modelAssetOf(t, secondSource, "glb")
	// The fixture's source Tasks are older than Meshy's declared retention, so
	// Meshy Convert binds the saved model file rather than the original job.
	one := prepareSourceAsset(t, h, "owner-a", firstSource.SessionID, "meshy/processing/convert", firstGLB.ID)
	if _, err := h.module.SubmitPrepared(ctx, authorizePrepared(t, ctx, h.repository, one, "saved-file-one")); err != nil {
		t.Fatalf("first saved-file conversion: %v", err)
	}
	duplicate := prepareSourceAsset(t, h, "owner-a", firstSource.SessionID, "meshy/processing/convert", firstGLB.ID)
	if _, err := h.module.SubmitPrepared(ctx, authorizePrepared(t, ctx, h.repository, duplicate, "saved-file-two")); !errors.Is(err, generation.ErrConversionInProgress) {
		t.Fatalf("duplicate saved-file conversion = %v", err)
	}
	other := prepareSourceAsset(t, h, "owner-a", secondSource.SessionID, "meshy/processing/convert", secondGLB.ID)
	if _, err := h.module.SubmitPrepared(ctx, authorizePrepared(t, ctx, h.repository, other, "saved-file-other")); err != nil {
		t.Fatalf("another saved-file source was refused: %v", err)
	}
}

func modelAssetOf(t *testing.T, task generation.Task, variant string) generation.Asset {
	t.Helper()
	for _, asset := range task.Assets {
		if asset.Role == "model" && asset.Variant == variant && asset.StorageKey != "" {
			return asset
		}
	}
	t.Fatalf("task %s has no READY %s model: %#v", task.ID, variant, task.Assets)
	return generation.Asset{}
}

// PrepareInput itself has no session requirement. The public paid path still
// needs an ExecutionGrant, whose identity requires a session, so a sessionless
// conversion cannot be submitted today; this test records the contract without
// adding a guard-only restriction.
func TestSessionlessLocalConversionPreparationRemainsAllowed330(t *testing.T) {
	h := newProductionConversionHarness(t)
	uploaded := uploadTriangleGLB(t, h, "owner-a")
	prepared, err := h.module.PrepareInput(context.Background(), generation.PrepareInputCommand{
		OwnerID: "owner-a", Capability: generation.FormatConversion,
		OfferingID: "local/static-glb/to-gltf", Input: []byte(`{"source_asset":"` + string(uploaded) + `"}`),
	})
	if err != nil {
		t.Fatalf("sessionless preparation was refused: %v", err)
	}
	if prepared.SessionID != "" {
		t.Fatalf("sessionless preparation gained a session: %q", prepared.SessionID)
	}
}
