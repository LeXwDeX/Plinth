package generation_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"math"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/catalog/catalogtest"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

type splatInlineAdapter struct {
	generation.ProviderPort
	raw     []byte
	starts  int
	missing bool
}

func (a *splatInlineAdapter) Start(_ context.Context, _ generation.StartRequest) (generation.Observation, error) {
	a.starts++
	claims := []generation.ArtifactClaim{{ID: "splat-output", Role: schema.GaussianSplatRole, Variant: schema.GaussianSplatVariant, MediaType: schema.GaussianSplatMediaType, Inline: a.raw}}
	if a.missing {
		claims = nil
	}
	return generation.Observation{Phase: generation.UpstreamCompleted, Artifacts: claims}, nil
}
func splatIntegrationBytes() []byte {
	b := make([]byte, 32)
	for i := 0; i < 3; i++ {
		binary.LittleEndian.PutUint32(b[12+i*4:], math.Float32bits(.01))
	}
	b[27] = 255
	b[28] = 255
	b[29] = 128
	b[30] = 128
	b[31] = 128
	return b
}
func enabledSplatCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	var d map[string]any
	if err := json.Unmarshal(catalogtest.DefaultCatalogBytes(t), &d); err != nil {
		t.Fatal(err)
	}
	for _, entry := range d["offerings"].([]any) {
		o := entry.(map[string]any)
		if o["id"] == "tripo/image-to-splat" {
			o["materialize_attempts"] = 1
		}
	}
	raw, _ := json.Marshal(d)
	c, err := catalog.Load(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestSplatRequiredDeliveryRestartOwnershipAndMeshRejection(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "invalid-bytes", "store-omits-profile", "store-changes-role"} {
		t.Run(mode, func(t *testing.T) {
			c := enabledSplatCatalog(t)
			a := &splatInlineAdapter{ProviderPort: mock.New(), raw: splatIntegrationBytes(), missing: mode == "missing"}
			if mode == "invalid-bytes" {
				a.raw[27] = 0
			}
			cas := filepath.Join(t.TempDir(), "cas")
			blobs, err := local.New(cas)
			if err != nil {
				t.Fatal(err)
			}
			store := &splatReturnStore{Store: blobs, mode: mode}
			var inputs *inputassets.Service
			h := newConfiguredHarness(t, a, store, func(d *generation.Dependencies) {
				d.Catalog = c
				inputs, err = inputassets.New(d.Repository.(*sqlite.Repository), blobs)
				if err != nil {
					t.Fatal(err)
				}
				d.Inputs = inputs
			})
			ctx := t.Context()
			var imageBytes bytes.Buffer
			if err = png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
				t.Fatal(err)
			}
			input, err := inputs.UploadAsset(ctx, "owner", generation.InputAssetUpload{Body: bytes.NewReader(imageBytes.Bytes()), OriginalFilename: "input.png", AssetType: "image", MediaType: "image/png", MaxBytes: 20000000})
			if err != nil {
				t.Fatal(err)
			}
			accepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{OwnerID: "owner", Capability: schema.ImageToSplat, OfferingID: "tripo/image-to-splat", Input: json.RawMessage(`{"image":"` + string(input.ID) + `"}`)})
			if err != nil {
				t.Fatal(err)
			}
			task := h.runUntilTerminal(t, "owner", accepted.TaskID)
			if mode != "valid" {
				if task.State != generation.TaskFailed || len(task.Assets) != 0 {
					t.Fatalf("bad delivery succeeded: %s %+v", task.State, task.Assets)
				}
				return
			}
			if task.State != generation.TaskSucceeded || len(task.Assets) != 1 {
				t.Fatalf("state=%s failure=%+v assets=%+v", task.State, task.Failure, task.Assets)
			}
			asset := task.Assets[0]
			if asset.DeliveryProfile != schema.DeliveryProfileGaussianSplat32V1 || asset.Role != schema.GaussianSplatRole || asset.Variant != schema.GaussianSplatVariant || asset.MediaType != schema.GaussianSplatMediaType {
				t.Fatalf("identity lost %+v", asset)
			}
			for i := 0; i < 2; i++ {
				content, e := h.module.OpenAsset(ctx, "owner", task.ID, asset.ID)
				if e != nil {
					t.Fatal(e)
				}
				body, e := io.ReadAll(content.Body)
				_ = content.Body.Close()
				if e != nil || !bytes.Equal(body, a.raw) {
					t.Fatal("owner bytes changed")
				}
			}
			for _, owner := range []generation.OwnerID{"other", ""} {
				if content, e := h.module.OpenAsset(ctx, owner, task.ID, asset.ID); e == nil {
					content.Body.Close()
					t.Fatal("unowned read accepted")
				}
			}
			for _, id := range []generation.OfferingID{"meshy/processing/remesh", "tripo/processing/rig-check", "tripo/processing/rigging"} {
				o, ok := c.Get(id)
				if !ok || !o.Enabled {
					t.Fatalf("missing source exclusion fixture %s", id)
				}
				_, e := h.module.PrepareInput(ctx, generation.PrepareInputCommand{OwnerID: "owner", Capability: o.Capability, OfferingID: id, Input: json.RawMessage(`{"source_task":"` + string(task.ID) + `"}`)})
				if e == nil {
					t.Fatalf("Splat accepted by mesh processing %s", id)
				}
			}
			if a.starts != 1 {
				t.Fatal("mesh rejection dispatched paid work")
			}
			if err = h.module.Close(); err != nil {
				t.Fatal(err)
			}
			repository, err := sqlite.Open(h.dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer repository.Close()
			reopened, err := local.New(cas)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			disabled, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			restoredModule, err := generation.New(generation.Dependencies{Repository: repository, Catalog: disabled, Providers: providers.NewRegistry(), Assets: reopened})
			if err != nil {
				t.Fatal(err)
			}
			defer restoredModule.Close()
			restored, err := restoredModule.Get(ctx, "owner", task.ID)
			if err != nil || len(restored.Assets) != 1 || restored.Assets[0].DeliveryProfile != asset.DeliveryProfile || restored.Offering.Outputs[0].DeliveryProfile != asset.DeliveryProfile {
				t.Fatalf("restart profile drift: %+v %v", restored.Assets, err)
			}
			page, err := restoredModule.List(ctx, generation.OwnTasks("owner"), generation.ListQuery{Limit: 10})
			if err != nil || len(page.Tasks) != 1 || page.Tasks[0].Assets[0].DeliveryProfile != asset.DeliveryProfile {
				t.Fatalf("list profile lost: %+v %v", page, err)
			}
			content, err := restoredModule.OpenAsset(ctx, "owner", task.ID, asset.ID)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(content.Body)
			content.Body.Close()
			if err != nil || !bytes.Equal(body, a.raw) {
				t.Fatal("restart changed bytes")
			}
		})
	}
}

type splatReturnStore struct {
	*local.Store
	mode string
}

func (s *splatReturnStore) Put(ctx context.Context, w generation.AssetWrite) (generation.Asset, error) {
	a, e := s.Store.Put(ctx, w)
	if s.mode == "store-omits-profile" {
		a.DeliveryProfile = ""
	}
	if s.mode == "store-changes-role" {
		a.Role = "model"
	}
	return a, e
}
