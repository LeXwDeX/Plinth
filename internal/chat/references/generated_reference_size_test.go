package references_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"io"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assets/imagefit"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat/references"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #276: a result this platform generated is not a user upload. Using it as a
// reference copies it under the Schema's generated_reference_max_bytes, not
// upload_max_bytes; a data URI someone pasted is still an upload.
func TestGeneratedReferenceLargerThanUploadLimitIsCopied(t *testing.T) {
	large := strings.Repeat("g", 2000) // over the 1024 upload limit
	inputs := &fakeInputs{owned: map[generation.AssetID]generation.InputAsset{}}
	tasks := &fakeTasks{
		tasks: map[generation.TaskID]generation.Task{
			"task-a": {ID: "task-a", OwnerID: "owner-a", Assets: []generation.Asset{{
				ID: "result-a", Role: "configured-image-role", Ordinal: 1, MediaType: "image/png", Size: 2000,
			}}},
			"historical": {ID: "historical", OwnerID: "owner-a"},
		},
		contents: map[string]generation.AssetContent{},
	}
	open := func() {
		tasks.contents["task-a/result-a"] = generation.AssetContent{
			Body: io.NopCloser(strings.NewReader(large)), MediaType: "image/png", Size: 2000,
		}
	}
	legacy := fakeLegacy{files: map[string]string{"historical_0.png": large}}
	resolver := newResolver(t, inputs, tasks, legacy)
	for _, value := range []string{
		"/configured/tasks/task-a/assets/result-a",
		"/configured/generated/task-a_0.png",
		"/configured/generated/historical_0.png",
	} {
		open()
		ids, err := resolver.Resolve(t.Context(), "owner-a", []string{value})
		if err != nil || len(ids) != 1 {
			t.Fatalf("%s: ids=%v error=%v", value, ids, err)
		}
		upload := inputs.uploads[len(inputs.uploads)-1]
		if upload.body != large || upload.maxBytes != testGeneratedReferenceMaxBytes {
			t.Fatalf("%s: upload body=%d bytes max=%d", value, len(upload.body), upload.maxBytes)
		}
	}
	pasted := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte(large))
	if _, err := resolver.Resolve(t.Context(), "owner-a", []string{pasted}); !errors.Is(err, references.ErrUnsupportedReference) {
		t.Fatalf("pasted image over the upload limit: %v", err)
	}
}

// Over the generated-reference limit the copy is refused before any byte is
// read, with the Schema's text naming the size and the limit.
func TestGeneratedReferenceOverLimitIsRefusedWithSizeAndLimit(t *testing.T) {
	const size = testGeneratedReferenceMaxBytes + 1
	inputs := &fakeInputs{owned: map[generation.AssetID]generation.InputAsset{}}
	body := &countingReader{reader: strings.NewReader(strings.Repeat("g", size))}
	tasks := &fakeTasks{
		tasks: map[generation.TaskID]generation.Task{
			"task-a": {ID: "task-a", OwnerID: "owner-a", Assets: []generation.Asset{{
				ID: "result-a", Role: "configured-image-role", Ordinal: 1, MediaType: "image/png", Size: size,
			}}},
			"historical": {ID: "historical", OwnerID: "owner-a"},
		},
		contents: map[string]generation.AssetContent{
			"task-a/result-a": {Body: io.NopCloser(body), MediaType: "image/png", Size: size},
		},
	}
	legacy := fakeLegacy{files: map[string]string{"historical_0.png": strings.Repeat("g", size)}}
	resolver := newResolver(t, inputs, tasks, legacy)
	want := schema.FillSizeRefusal("configured too large {size} over {limit}", "", size, testGeneratedReferenceMaxBytes)
	for _, value := range []string{
		"/configured/tasks/task-a/assets/result-a",
		"/configured/generated/task-a_0.png",
		"/configured/generated/historical_0.png",
	} {
		_, err := resolver.Resolve(t.Context(), "owner-a", []string{value})
		var refusal *generation.Error
		if !errors.Is(err, references.ErrUnsupportedReference) || !errors.As(err, &refusal) ||
			refusal.Code != generation.ErrInvalidInput || refusal.Detail != want ||
			!refusal.UserFacing || refusal.DetailTemplate != "configured too large {size} over {limit}" ||
			len(refusal.DetailValues) != 4 {
			t.Fatalf("%s: error = %#v, want detail %q", value, err, want)
		}
	}
	if len(inputs.uploads) != 0 || body.read != 0 {
		t.Fatalf("uploads=%v bytes read=%d", inputs.uploads, body.read)
	}
}

// End to end with the Schema's limits and the real input asset service: a
// 2048² PNG over the 10 MB upload limit (#276) is copied as a reference, then
// every Offering that takes reference images either accepts it as is or gets
// a re-encoded copy within its max_bytes of the same width and height. The
// copy records its source; the original is unchanged.
func TestGeneratedReferenceUsesOfferingLimitNotUploadLimit(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	runtime := application.Runtime.InputAssets
	// #274 raised the Schema's upload limit to 50 MiB, beyond what a 2048²
	// PNG can reach, so the resolver here keeps the 10 MiB upload limit the
	// #276 picture (10,516,505 bytes) failed against.
	const uploadLimit int64 = 10 << 20
	picture := largePNG(t, uploadLimit)
	size := int64(len(picture))
	if size <= uploadLimit || size > runtime.GeneratedReferenceMaxBytes {
		t.Fatalf("fixture %d must exceed the upload limit %d and fit generated_reference_max_bytes %d",
			size, uploadLimit, runtime.GeneratedReferenceMaxBytes)
	}
	root := t.TempDir()
	repository, err := sqlite.Open(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	store, err := local.New(filepath.Join(root, "storage"))
	if err != nil {
		t.Fatal(err)
	}
	fitting := application.Runtime.InputAssets.ImageFitting
	inputs, err := inputassets.NewWithConfig(repository, store, inputassets.Config{
		Messages: inputassets.Messages{
			ParameterTooLarge:       runtime.Messages.ParameterTooLargeTemplate,
			CompressionInsufficient: runtime.Messages.CompressionInsufficientTemplate,
			TransparencyKept:        runtime.Messages.TransparencyKeptTemplate,
		},
		Fitting: inputassets.Fitting{
			ParameterSemantics: fitting.ParameterSemantics,
			Policy: imagefit.Policy{Steps: fitting.Steps, JPEGQualities: fitting.JPEGQualities,
				MaxDecodePixels: fitting.MaxDecodePixels},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	tasks := &fakeTasks{
		tasks: map[generation.TaskID]generation.Task{
			"task-a": {ID: "task-a", OwnerID: "owner-a", Assets: []generation.Asset{{
				ID: "result-a", Role: "image", Ordinal: 1, MediaType: "image/png", Size: size,
			}}},
		},
		contents: map[string]generation.AssetContent{
			"task-a/result-a": {Body: io.NopCloser(bytes.NewReader(picture)), MediaType: "image/png", Size: size},
		},
	}
	resolver, err := references.New(references.Config{
		Inputs: inputs, Tasks: tasks, Legacy: fakeLegacy{},
		InputAssetRoutes:                   []string{application.Runtime.HTTP.Routes.AssetItem},
		TaskAssetRoute:                     application.Runtime.HTTP.Routes.TaskAsset,
		GeneratedImageRoute:                application.Runtime.HTTP.LegacyRoutes.GeneratedImage,
		UploadMaxBytes:                     uploadLimit,
		GeneratedReferenceMaxBytes:         runtime.GeneratedReferenceMaxBytes,
		GeneratedReferenceTooLargeTemplate: runtime.Messages.GeneratedReferenceTooLargeTemplate,
		DefaultMediaType:                   runtime.DefaultMediaType,
		DefaultAssetType:                   runtime.DefaultAssetType,
		ReferenceFilename:                  application.Chat.Context.ReferenceFilename,
		LegacyGeneratedImage:               application.Runtime.AssetDelivery.LegacyGeneratedImage,
	})
	if err != nil {
		t.Fatal(err)
	}
	reference := strings.NewReplacer("{taskID}", "task-a", "{assetID}", "result-a").
		Replace(application.Runtime.HTTP.Routes.TaskAsset)
	ids, err := resolver.Resolve(t.Context(), "owner-a", []string{reference})
	if err != nil || len(ids) != 1 {
		t.Fatalf("generated reference over the upload limit: ids=%v error=%v", ids, err)
	}
	accepted, compressed := 0, 0
	for _, offering := range product.List("") {
		for _, parameter := range offering.Parameters {
			if parameter.Semantic != generation.SemanticReferenceImage || parameter.MaxBytes <= 0 ||
				!imagefit.Accepts(parameter.MediaTypes, "image/png") {
				continue
			}
			value := `"` + string(ids[0]) + `"`
			if parameter.Type == "asset-array" {
				value = "[" + value + "]"
			}
			only := offering
			only.Parameters = []generation.Parameter{parameter}
			fitted, err := inputs.Fit(t.Context(), "owner-a", only, []byte(`{"`+parameter.Name+`":`+value+`}`))
			if err != nil {
				t.Fatalf("%s: fit error = %v", offering.ID, err)
			}
			resolved, err := inputs.Resolve(t.Context(), "owner-a", only, fitted)
			if err != nil || len(resolved[parameter.Name]) != 1 {
				t.Fatalf("%s accepts %d bytes but refused: %v", offering.ID, parameter.MaxBytes, err)
			}
			sent := resolved[parameter.Name][0]
			if parameter.MaxBytes >= size {
				if sent.ID != ids[0] {
					t.Fatalf("%s: an image within its limit was re-encoded", offering.ID)
				}
				accepted++
				continue
			}
			if sent.ID == ids[0] || sent.Size > parameter.MaxBytes {
				t.Fatalf("%s: sent %s of %d bytes over %d", offering.ID, sent.ID, sent.Size, parameter.MaxBytes)
			}
			body, err := inputs.Open(t.Context(), sent)
			if err != nil {
				t.Fatal(err)
			}
			config, _, err := image.DecodeConfig(body)
			_ = body.Close()
			if err != nil || config.Width != 2048 || config.Height != 2048 {
				t.Fatalf("%s: compressed image is %dx%d (%v), want 2048x2048", offering.ID, config.Width, config.Height, err)
			}
			derivation, found, err := repository.InputAssetDerivation(t.Context(), "owner-a", sent.ID)
			if err != nil || !found || derivation.SourceAssetID != ids[0] || derivation.SourceSize != size || derivation.Method == "" {
				t.Fatalf("%s: derivation = %+v found=%v error=%v", offering.ID, derivation, found, err)
			}
			compressed++
		}
	}
	if accepted == 0 || compressed == 0 {
		t.Fatalf("expected both outcomes from the default catalog: accepted=%d compressed=%d", accepted, compressed)
	}
	if original, err := repository.GetInputAsset(t.Context(), ids[0]); err != nil || original.Size != size {
		t.Fatalf("original changed: %+v %v", original, err)
	}
}

// largePNG is an opaque 2048² PNG just over the upload limit, like the
// highest-quality results #276 reports: noise rows (incompressible, about
// 3 bytes per pixel) above flat rows.
func largePNG(t *testing.T, over int64) []byte {
	t.Helper()
	random := rand.New(rand.NewSource(276))
	noisyRows := int(over*102/100/(2048*3)) + 1
	for attempt := 0; attempt < 8; attempt++ {
		picture := image.NewNRGBA(image.Rect(0, 0, 2048, 2048))
		for index := range picture.Pix {
			switch {
			case index%4 == 3:
				picture.Pix[index] = 255
			case index/(2048*4) < noisyRows:
				picture.Pix[index] = byte(random.Intn(256))
			default:
				picture.Pix[index] = 128
			}
		}
		var buffer bytes.Buffer
		if err := png.Encode(&buffer, picture); err != nil {
			t.Fatal(err)
		}
		switch size := int64(buffer.Len()); {
		case size <= over:
			noisyRows += 16
		case size > over*11/10:
			noisyRows -= 16
		default:
			return buffer.Bytes()
		}
	}
	t.Fatal("could not build a PNG just over the requested size")
	return nil
}

type countingReader struct {
	reader io.Reader
	read   int
}

func (reader *countingReader) Read(buffer []byte) (int, error) {
	count, err := reader.reader.Read(buffer)
	reader.read += count
	return count, err
}
