package localmodel_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/assets/modelconvert/modelconverttest"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/assets/modelsource"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modeldelivery"
	"github.com/LeXwDeX/Plinth/internal/processing"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/localmodel"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

type countingOpens struct {
	generation.InputAssetResolver
	opens atomic.Int64
}

func (c *countingOpens) Open(ctx context.Context, asset generation.InputAsset) (io.ReadCloser, error) {
	c.opens.Add(1)
	return c.InputAssetResolver.Open(ctx, asset)
}

func uploadPolicyFrom(t *testing.T, product *catalog.Catalog) modelinput.Policy {
	t.Helper()
	profile := product.Application().Runtime.InputAssets.ModelProfile
	l := profile.Limits
	return modelinput.Policy{ProfileID: profile.ID, Revision: profile.Revision, Limits: modelinput.Limits{MaxBytes: l.MaxBytes, MaxJSONBytes: l.MaxJSONBytes, MaxDepth: l.MaxDepth, MaxElements: l.MaxElements, MaxVertices: l.MaxVertices, MaxTriangles: l.MaxTriangles, MaxNodes: l.MaxNodes, MaxWork: l.MaxWork, MaxImagePixels: l.MaxImagePixels, MaxTotalImagePixels: l.MaxTotalImagePixels}}
}

// #264 end to end on the default Product Schema: a generation-sized GLB that a
// successful Task stored is offered as a free glTF, converts through the real
// kernel, CAS inspection and SQLite lifecycle into a package ZIP, while the
// same Offering refuses a direct upload and the upload profile still refuses
// the large model.
func TestGeneratedGLBBecomesAGLTFPackageThroughTheKernelAndStore(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo, err := sqlite.Open(filepath.Join(root, "generation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	blobs, err := local.New(filepath.Join(root, "cas"))
	if err != nil {
		t.Fatal(err)
	}
	defer blobs.Close()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	upload := uploadPolicyFrom(t, product)
	profile := product.Application().Runtime.InputAssets.ModelProfile
	native, err := inputassets.NewWithConfig(repo, blobs, inputassets.Config{UploadValidator: modelsource.UploadValidator(upload, profile.AssetType, profile.Extensions, profile.MediaTypes)})
	if err != nil {
		t.Fatal(err)
	}
	models, err := modelsource.New(native, upload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateSession(ctx, chat.Session{ID: "local-session", OwnerID: "owner", Title: "package fixture", CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	raw := modelinputtest.Large(modelinputtest.LargeOptions{Grid: 722, Images: []modelinputtest.LargeImage{{Width: 8192, Height: 8192, RGBA: true}, {Width: 512, Height: 512, Noise: true}}})
	generated, _ := product.Get(generatedOfferingID)
	static, _ := product.Get("local/static-glb/to-gltf")
	one := 1
	sourceOffering := generation.Offering{ID: "legacy/generated-model", Revision: "legacy-v1", Enabled: true, ProviderID: "legacy", AdapterKey: "legacy", Capability: generation.FormatConversion, Domain: generation.DomainProcessing, Outputs: []generation.AssetRequirement{{Role: "model", Variant: "glb", MediaTypes: []string{"model/gltf-binary"}, MaxBytes: 128 << 20, Minimum: &one}}}
	fixture := fixtureCatalog{offerings: map[generation.OfferingID]generation.Offering{generated.ID: generated, static.ID: static, sourceOffering.ID: sourceOffering}}
	opens := &countingOpens{InputAssetResolver: models}
	port, err := localmodel.New(localmodel.Config{Inputs: opens, Assets: blobs})
	if err != nil {
		t.Fatal(err)
	}
	registry := providers.NewRegistry()
	if err := registry.Register(generated.AdapterKey, port); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("legacy", historicalModelPort{body: raw}); err != nil {
		t.Fatal(err)
	}
	module, err := generation.New(generation.Dependencies{Repository: repo, Catalog: fixture, Providers: registry, Assets: blobs, Inputs: models})
	if err != nil {
		t.Fatal(err)
	}
	source := runLocalTask(t, module, submitApprovedLocal(t, module, repo, sourceOffering, json.RawMessage(`{}`), "generated-source").TaskID)
	if len(source.Assets) != 1 || source.Assets[0].Size != int64(len(raw)) || source.Assets[0].Size <= profile.Limits.MaxBytes {
		t.Fatalf("source task did not store the generation-sized model: %#v", source.Assets)
	}
	modelAsset := source.Assets[0]
	sourceInput, _ := json.Marshal(map[string]string{"source_asset": string(modelAsset.ID)})

	// The upload-profile route still refuses the stored model by size.
	if _, err := module.PrepareInput(ctx, generation.PrepareInputCommand{OwnerID: "owner", SessionID: "local-session", Capability: generation.FormatConversion, OfferingID: static.ID, Input: sourceInput}); err == nil {
		t.Fatal("upload-profile route accepted a generation-sized model")
	}
	// The coverage projection offers the free package route, checking real bytes once.
	plans, err := processing.New(repo, deliveryPlanCatalog{fixtureCatalog: fixture, module: module}, module, nil)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := modeldelivery.New(product.Application().ModelDelivery, module, plans, nil)
	if err != nil {
		t.Fatal(err)
	}
	for repeat := 0; repeat < 3; repeat++ {
		coverage, err := delivery.Coverage(ctx, generation.OwnTasks("owner"), source.ID)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range coverage.Formats {
			if row.ID != "gltf" {
				continue
			}
			found = true
			if row.Status != modeldelivery.StatusObtainable || row.Conversion == nil || row.Conversion.OfferingID != generatedOfferingID || row.Conversion.EstimatedCredits != 0 {
				t.Fatalf("gltf row = %#v", row)
			}
		}
		if !coverage.Applicable || !found {
			t.Fatalf("coverage = %#v", coverage)
		}
	}
	if opens.opens.Load() != 1 {
		t.Fatalf("polled coverage read the large source %d times", opens.opens.Load())
	}

	converted := runLocalTask(t, module, submitApprovedLocal(t, module, repo, generated, sourceInput, "generated-package").TaskID)
	if len(converted.Assets) != 1 {
		t.Fatalf("package task assets = %#v", converted.Assets)
	}
	asset := converted.Assets[0]
	if asset.Role != "model" || asset.Variant != "gltf" || asset.MediaType != "application/zip" || asset.Size > generated.Outputs[0].MaxBytes {
		t.Fatalf("package asset = %#v", asset)
	}
	reader, err := blobs.Open(ctx, asset)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || int64(len(archive)) != asset.Size {
		t.Fatalf("stored package unreadable: %v", err)
	}
	if err := modelconverttest.VerifyPackage(archive, raw); err != nil {
		t.Fatal(err)
	}

	// A direct upload never reaches the package route, even a valid small one.
	small := modelinputtest.Triangle(modelinputtest.Options{Indexed: true, Textured: true})
	uploaded, err := native.UploadAsset(ctx, "owner", generation.InputAssetUpload{Body: bytes.NewReader(small), OriginalFilename: "mine.glb", AssetType: "model", MediaType: "model/gltf-binary", MaxBytes: profile.Limits.MaxBytes})
	if err != nil {
		t.Fatal(err)
	}
	uploadInput, _ := json.Marshal(map[string]string{"source_asset": string(uploaded.ID)})
	if _, err := module.PrepareInput(ctx, generation.PrepareInputCommand{OwnerID: "owner", SessionID: "local-session", Capability: generation.FormatConversion, OfferingID: generated.ID, Input: uploadInput}); !schema.IsCode(err, generation.ErrUnsupportedModelInput) {
		t.Fatalf("package route accepted a direct upload: %v", err)
	}
	// And the upload profile itself still refuses the generation-sized model.
	if _, err := native.UploadAsset(ctx, "owner", generation.InputAssetUpload{Body: bytes.NewReader(raw), OriginalFilename: "large.glb", AssetType: "model", MediaType: "model/gltf-binary", MaxBytes: 128 << 20}); err == nil {
		t.Fatal("upload profile accepted a generation-sized model")
	}
}

// measureHeap reports the peak live heap above the pre-call baseline (sampled
// every millisecond, so it includes garbage the GC has not reclaimed yet) and
// the bytes allocated by run.
func measureHeap(run func()) (int64, uint64) {
	runtime.GC()
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(sample)
	baseline := sample[0].Value.Uint64()
	var peak atomic.Uint64
	stop := make(chan struct{})
	var sampler sync.WaitGroup
	sampler.Add(1)
	go func() {
		defer sampler.Done()
		current := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
		for {
			metrics.Read(current)
			if value := current[0].Value.Uint64(); value > peak.Load() {
				peak.Store(value)
			}
			select {
			case <-stop:
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	run()
	close(stop)
	sampler.Wait()
	runtime.ReadMemStats(&after)
	return int64(peak.Load()) - int64(baseline), after.TotalAlloc - before.TotalAlloc
}

// TestGeneratedPackageMemoryBudget measures one conversion of a 63 MB model
// (about 1.04M vertices, 2.08M triangles, an 8192-square RGBA texture) through
// the real CAS store, including its pre-commit inspection, next to the cost of
// storing that GLB itself (what the source Task's materialization already
// paid). It allocates about 2 GiB in total, so it runs only on request:
//
//	ART_AI_GLB_MEMORY=1 go test ./internal/providers/localmodel -run TestGeneratedPackageMemoryBudget -v
func TestGeneratedPackageMemoryBudget(t *testing.T) {
	if os.Getenv("ART_AI_GLB_MEMORY") != "1" {
		t.Skip("set ART_AI_GLB_MEMORY=1 to measure the 63 MB conversion")
	}
	raw := modelinputtest.Large(modelinputtest.LargeOptions{Grid: 1020, Images: []modelinputtest.LargeImage{{Width: 8192, Height: 8192, RGBA: true}, {Width: 2200, Height: 2200, Noise: true}}})
	blobs, err := local.New(filepath.Join(t.TempDir(), "cas"))
	if err != nil {
		t.Fatal(err)
	}
	defer blobs.Close()
	port, err := localmodel.New(localmodel.Config{Inputs: &inputMemory{body: raw}, Assets: blobs})
	if err != nil {
		t.Fatal(err)
	}
	var sourceErr error
	sourcePeak, sourceAllocated := measureHeap(func() {
		_, sourceErr = blobs.Put(context.Background(), generation.AssetWrite{AssetID: "source", OwnerID: "owner", TaskID: "source-task", AttemptID: "source-attempt", Role: "model", Variant: "glb", Ordinal: 1, MediaType: "model/gltf-binary", MaxBytes: 128 << 20, Body: bytes.NewReader(raw)})
	})
	if sourceErr != nil {
		t.Fatal(sourceErr)
	}
	request := packageRequest(generatedOffering(t), raw, "model")
	var observation generation.Observation
	started := time.Now()
	packagePeak, packageAllocated := measureHeap(func() {
		observation, err = port.Start(context.Background(), request)
	})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	stored := observation.Artifacts[0].StoredAsset
	mib := func(value int64) float64 { return float64(value) / (1 << 20) }
	t.Logf("source %.1f MiB -> package %.1f MiB in %s", mib(int64(len(raw))), mib(stored.Size), elapsed.Round(time.Millisecond))
	t.Logf("package conversion: peak live heap +%.1f MiB, allocated %.1f MiB", mib(packagePeak), mib(int64(packageAllocated)))
	t.Logf("storing the source GLB (reference): peak live heap +%.1f MiB, allocated %.1f MiB", mib(sourcePeak), mib(int64(sourceAllocated)))
	// Budget: the one exact-size source copy plus the store's existing
	// pre-commit inspection, with slack for garbage the GC has not reclaimed.
	if budget := 2*int64(len(raw)) + 128<<20; packagePeak > budget {
		t.Fatalf("peak %.1f MiB above baseline exceeds the %.1f MiB budget", mib(packagePeak), mib(budget))
	}
}
