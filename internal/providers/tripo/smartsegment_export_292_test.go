package tripo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	tripov3 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/tripo/v3"
)

// #292: the private exports of a Smart Segmentation take their texture size from
// the frozen input like every other export, so a purpose that bounds it (a game,
// 2K) reaches each of the five files of the segmented model, and the shipped
// default of 8K is the request every export has always been. The shipped Offerings
// of both kinds run through the real adapter (the private upload, the creation of
// the segmentation task, its observation and the exports) against the Tripo v3
// fake, and exportRun.collect holds what the adapter sent against what the fake
// ran.

// smartSegmentOwnedSource is the owned file a Smart Segmentation of the kind
// starts from: a picture for "image", a GLB for "model". It returns the name of
// the Offering's source parameter, the asset's id and media type and its bytes.
func smartSegmentOwnedSource(t *testing.T, kind string) (parameter, id, mediaType string, body []byte) {
	t.Helper()
	if kind == "image" {
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
			t.Fatal(err)
		}
		return "image", "owned-image", "image/png", encoded.Bytes()
	}
	return "source_asset", "owned-glb", "model/gltf-binary", modelinputtest.Triangle(modelinputtest.Options{Indexed: true, Interleaved: true, Textured: true})
}

// driveSmartSegment runs the shipped Smart Segmentation Offering of the kind,
// frozen through the Catalog with the given export texture size, to completion
// against the Tripo v3 fake. Each step uses a new adapter, as a restart does.
func driveSmartSegment(t *testing.T, kind, size string) exportRun {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	declared, ok := product.Get(generation.OfferingID("tripo/geometry/smartsegment-" + kind))
	if !ok {
		t.Fatalf("missing the %s Smart Segmentation Offering", kind)
	}
	name, id, mediaType, body := smartSegmentOwnedSource(t, kind)
	input, _ := json.Marshal(map[string]any{
		name: id, "seg_type": kind, "granularity": "fine", "output_formats": []string{"FBX", "OBJ", "STL", "USDZ", "3MF"},
		"export_texture_size": size, "authorize_extra_credits": true,
	})
	offering, frozen, err := product.Normalize(declared.Capability, declared.ID, input)
	if err != nil {
		t.Fatalf("%s %s: %v", kind, size, err)
	}
	offering.AssetHosts = []string{exportAssetHost}
	sum := sha256.Sum256(body)
	request := generation.StartRequest{
		TaskID: "public-smartseg", AttemptID: "attempt-smartseg", OwnerID: "owner", SubmissionKey: "smartseg-submit",
		Offering: offering, Input: frozen,
		InputAssets: map[string][]generation.InputAsset{name: {{
			ID: generation.AssetID(id), OwnerID: "owner", StorageKey: "owned", Size: int64(len(body)),
			SHA256: hex.EncodeToString(sum[:]), MediaType: mediaType,
		}}},
	}
	stack := newExportStack(t, &smartSegmentDeliveryInputs{body: body})
	ctx := generation.WithDispatchCheckpointer(context.Background(), &nativeDeliveryPermit{})

	prepared, err := stack.adapter().Start(ctx, request)
	if err != nil {
		t.Fatalf("the private upload: %v", err)
	}
	accepted, err := stack.adapter().ResumePreparation(ctx, generation.PreparationRequest{
		TaskID: request.TaskID, AttemptID: request.AttemptID, OwnerID: request.OwnerID, SubmissionKey: request.SubmissionKey,
		Offering: request.Offering, Input: request.Input, Opaque: prepared.Opaque,
	})
	if err != nil || accepted.Job == nil {
		t.Fatalf("the creation of the segmentation: %+v %v", accepted, err)
	}
	run := exportRun{fake: stack.fixture, baseID: accepted.Job.RemoteID}
	q := generation.ObserveRequest{
		TaskID: request.TaskID, AttemptID: request.AttemptID, Offering: request.Offering, Input: request.Input,
		Opaque: accepted.Opaque, Job: *accepted.Job,
	}
	for i := 0; i < 60 && run.observed.Phase != generation.UpstreamCompleted; i++ {
		if run.observed, err = stack.adapter().Observe(ctx, q); err != nil {
			t.Fatal(err)
		}
		if run.observed.Phase == generation.UpstreamFailed {
			t.Fatalf("the segmentation failed: %+v", run.observed.Failure)
		}
		q.Opaque = run.observed.Opaque
	}
	if run.observed.Phase != generation.UpstreamCompleted {
		t.Fatalf("the segmentation did not complete: %+v", run.observed)
	}

	// Nothing but the upload, the creation, the five exports and the polls of the
	// tasks was asked of the upstream.
	uploads := 0
	for _, wire := range stack.recorder.requests {
		switch {
		case wire.path == "POST "+tripov3.Prefix+"/files":
			uploads++
		case wire.path == "POST "+tripov3.Prefix+"/mesh/smartsegment", wire.path == "POST "+tripov3.Prefix+"/models/convert",
			strings.HasPrefix(wire.path, "GET "+tripov3.Prefix+"/tasks/"):
		default:
			t.Errorf("unexpected request %s", wire.path)
		}
	}
	if uploads != 1 {
		t.Errorf("the source was uploaded %d times", uploads)
	}
	var state smartSegmentState
	if err := json.Unmarshal(run.observed.Opaque, &state); err != nil {
		t.Fatal(err)
	}
	run.collect(t, stack, "mesh/smartsegment", state.Conversions)
	return run
}

func TestSmartSegmentExportsFollowTheBoundTextureSize(t *testing.T) {
	formats := []string{"FBX", "OBJ", "STL", "USDZ", "3MF"}
	for _, kind := range []string{"image", "model"} {
		for _, test := range []struct {
			size string
			want float64
		}{{"2k", 2048}, {"4k", 4096}, {"8k", 8192}} {
			t.Run(kind+"/"+test.size, func(t *testing.T) {
				run := driveSmartSegment(t, kind, test.size)
				if !slices.Equal(run.order, formats) {
					t.Fatalf("exports = %v, want %v", run.order, formats)
				}
				for _, format := range formats {
					want := map[string]any{
						"input": run.model, "format": format, "texture_size": test.want, "texture_format": "PNG",
						"with_animation": false, "bake": true, "fbx_preset": "blender",
					}
					if !reflect.DeepEqual(run.converts[format], want) {
						t.Fatalf("export %s wire %v, want %v", format, run.converts[format], want)
					}
					// The fake ran the export under the same size and format.
					if echoed := run.echoes[format]; echoed["texture_size"] != test.want || echoed["texture_format"] != "PNG" {
						t.Fatalf("the fake ran the %s export under %v", format, echoed)
					}
				}
				// The bound size is an export option, never a segmentation field.
				if len(run.base) != 1 {
					t.Fatalf("segmentation requests = %v", run.base)
				}
				var created map[string]any
				if err := json.Unmarshal([]byte(run.base[0]), &created); err != nil {
					t.Fatal(err)
				}
				for name := range created {
					if strings.Contains(name, "texture") {
						t.Fatalf("the segmentation request carries the export option %s: %v", name, created)
					}
				}
			})
		}
	}
}
