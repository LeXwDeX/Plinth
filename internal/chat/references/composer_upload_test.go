package references_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"math/rand"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/visionimage"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/references"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// storedInputs keeps the bytes and media type of every input asset, like the
// input asset service, so a test can compare what each path hands on.
type storedInputs struct {
	assets  map[generation.AssetID]generation.InputAsset
	bodies  map[generation.AssetID][]byte
	uploads int
}

func (inputs *storedInputs) UploadAsset(
	_ context.Context,
	owner generation.OwnerID,
	upload generation.InputAssetUpload,
) (generation.InputAsset, error) {
	body, err := io.ReadAll(io.LimitReader(upload.Body, upload.MaxBytes+1))
	if err != nil || int64(len(body)) > upload.MaxBytes {
		return generation.InputAsset{}, errors.New("too large")
	}
	inputs.uploads++
	id := generation.AssetID("uploaded-" + string(rune('0'+inputs.uploads)))
	asset := generation.InputAsset{ID: id, OwnerID: owner, MediaType: upload.MediaType, Size: int64(len(body))}
	inputs.assets[id] = asset
	inputs.bodies[id] = body
	return asset, nil
}

func (inputs *storedInputs) OpenOwned(
	_ context.Context,
	owner generation.OwnerID,
	id generation.AssetID,
) (generation.InputAsset, io.ReadCloser, error) {
	asset, found := inputs.assets[id]
	if !found || asset.OwnerID != owner {
		return generation.InputAsset{}, nil, errors.New("not owned")
	}
	return asset, io.NopCloser(bytes.NewReader(inputs.bodies[id])), nil
}

// resolverLimits are the byte limits a test resolver runs with; the vision
// copy maker is nil unless a test gives one.
type resolverLimits struct {
	upload, vision int64
	tasks          *fakeTasks
	derivative     *visionimage.Deriver
}

func composerResolver(t *testing.T, inputs *storedInputs) *references.Resolver {
	return limitedResolver(t, inputs, resolverLimits{upload: 1024, vision: 1024})
}

func limitedResolver(t *testing.T, inputs *storedInputs, limits resolverLimits) *references.Resolver {
	if limits.tasks == nil {
		limits.tasks = &fakeTasks{}
	}
	t.Helper()
	resolver, err := references.New(references.Config{
		Inputs:                             inputs,
		Tasks:                              limits.tasks,
		Legacy:                             fakeLegacy{},
		InputAssetRoutes:                   []string{"/configured/assets/{assetID}"},
		TaskAssetRoute:                     "/configured/tasks/{taskID}/assets/{assetID}",
		GeneratedImageRoute:                "/configured/generated/{filename}",
		UploadMaxBytes:                     limits.upload,
		GeneratedReferenceMaxBytes:         max(testGeneratedReferenceMaxBytes, limits.upload),
		GeneratedReferenceTooLargeTemplate: "configured too large {size} over {limit}",
		DefaultMediaType:                   "application/octet-stream",
		DefaultAssetType:                   "configured-image",
		ReferenceFilename:                  "configured-reference",
		LegacyGeneratedImage:               catalog.LegacyGeneratedImageRuntimeSchema{DefaultMediaType: "image/png"},
		VisionInlineMaxBytes:               limits.vision,
		VisionDerivative:                   limits.derivative,
		VisionFallbackMediaType:            "image/png",
	})
	if err != nil {
		t.Fatal(err)
	}
	return resolver
}

// #292: the composer now uploads an attached picture as an input asset at
// send time and the message stores the asset URL instead of the data URI. The
// Agent must get the same picture either way: the same label, the same vision
// data URI and the same reference bytes.
func TestComposerPictureAsInputAssetReachesTheAgentAsTheDataURIDid(t *testing.T) {
	ctx := context.Background()
	picture := []byte("\x89PNG\r\n\x1a\nthe picture the person attached")
	dataURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(picture)
	inputs := &storedInputs{assets: map[generation.AssetID]generation.InputAsset{}, bodies: map[generation.AssetID][]byte{}}
	resolver := composerResolver(t, inputs)

	// What the composer does now: one upload of the picture's bytes.
	uploaded, err := inputs.UploadAsset(ctx, "owner-a", generation.InputAssetUpload{
		Body: bytes.NewReader(picture), MediaType: "image/png", MaxBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	assetURL := "/configured/assets/" + string(uploaded.ID)

	// Vision: the asset is inlined back to exactly the data URI the message used to hold.
	inlined, ok := resolver.OpenVisionImage(ctx, "owner-a", assetURL)
	if !ok || inlined != dataURI {
		t.Fatalf("vision got %q (ok=%v), want the original data URI", inlined, ok)
	}

	// Attachment: both are an owned upload, so the Agent sees the same label; the asset also has its ID.
	fromData, err := resolver.VerifyAttachment(ctx, "owner-a", "session-a", chat.AttachmentRef{Kind: chat.AttachmentKindImage, URL: dataURI, ImageSlot: 1})
	if err != nil {
		t.Fatal(err)
	}
	fromAsset, err := resolver.VerifyAttachment(ctx, "owner-a", "session-a", chat.AttachmentRef{
		Kind: chat.AttachmentKindImage, URL: assetURL, AssetID: string(uploaded.ID), ImageSlot: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if fromData.Kind != fromAsset.Kind || fromData.Role != fromAsset.Role || fromAsset.Role != chat.AttachmentRoleUpload ||
		fromAsset.AssetID != string(uploaded.ID) || fromData.AssetID != "" {
		t.Fatalf("data URI attachment %#v, asset attachment %#v", fromData, fromAsset)
	}

	// Reference: the data URI was uploaded again at every use; the asset URL is the asset itself.
	before := inputs.uploads
	ids, err := resolver.Resolve(ctx, "owner-a", []string{dataURI, assetURL})
	if err != nil {
		t.Fatal(err)
	}
	if inputs.uploads != before+1 || ids[1] != uploaded.ID {
		t.Fatalf("ids %#v, uploads %d → %d", ids, before, inputs.uploads)
	}
	if !bytes.Equal(inputs.bodies[ids[0]], picture) || !bytes.Equal(inputs.bodies[ids[1]], picture) {
		t.Fatal("both references must carry the attached picture's bytes")
	}

	// The asset stays the owner's: another owner reaches it by no path.
	if _, ok := resolver.OpenVisionImage(ctx, "owner-b", assetURL); ok {
		t.Fatal("another owner must not read the picture")
	}
	if _, err := resolver.Resolve(ctx, "owner-b", []string{assetURL}); err == nil {
		t.Fatal("another owner must not reference the picture")
	}
	if _, err := resolver.VerifyAttachment(ctx, "owner-b", "session-b", chat.AttachmentRef{Kind: chat.AttachmentKindImage, URL: assetURL}); err == nil {
		t.Fatal("another owner must not claim the picture")
	}
}

// busyPhoto draws a picture with gradient and grain, so its PNG is several MB.
func busyPhoto(t *testing.T, width, height int) []byte {
	t.Helper()
	random := rand.New(rand.NewSource(int64(width + height)))
	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			grain := func(base int) uint8 { return uint8(min(255, max(0, base+random.Intn(33)-16))) }
			picture.SetNRGBA(x, y, color.NRGBA{R: grain(x * 255 / width), G: grain(y * 255 / height), B: grain(96), A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buffer, picture); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func decodedDataURI(t *testing.T, value string) ([]byte, string) {
	t.Helper()
	head, payload, found := strings.Cut(value, ";base64,")
	if !found || !strings.HasPrefix(head, "data:") {
		t.Fatalf("not a base64 data URI: %.40q", value)
	}
	body, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	return body, strings.TrimPrefix(head, "data:")
}

// #292: a composer picture over vision_inline_max_bytes (4 MiB) used to reach
// the model whole as a data URI. As an input asset it now reaches vision as a
// scaled copy within the limit, while a generation reference still resolves
// to the untouched original asset (#276 fitting happens at the Offering, never
// by resizing here).
func TestLargeComposerPictureReachesVisionAsAScaledCopyAndReferencesStayOriginal(t *testing.T) {
	ctx := context.Background()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := c.Application()
	deriver, err := visionimage.New(schema.Chat.Context.VisionDerivative)
	if err != nil {
		t.Fatal(err)
	}
	visionLimit := schema.Chat.Context.VisionInlineMaxBytes
	picture := busyPhoto(t, 3000, 2000)
	if int64(len(picture)) <= visionLimit {
		t.Fatalf("the picture must be over the vision limit: %d bytes", len(picture))
	}
	inputs := &storedInputs{assets: map[generation.AssetID]generation.InputAsset{}, bodies: map[generation.AssetID][]byte{}}
	resolver := limitedResolver(t, inputs, resolverLimits{
		upload: schema.Runtime.InputAssets.UploadMaxBytes, vision: visionLimit, derivative: deriver,
	})
	uploaded, err := inputs.UploadAsset(ctx, "owner-a", generation.InputAssetUpload{
		Body: bytes.NewReader(picture), MediaType: "image/png", MaxBytes: schema.Runtime.InputAssets.UploadMaxBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	assetURL := "/configured/assets/" + string(uploaded.ID)

	inlined, ok := resolver.OpenVisionImage(ctx, "owner-a", assetURL)
	if !ok {
		t.Fatal("a large picture must still reach vision")
	}
	body, mediaType := decodedDataURI(t, inlined)
	config, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if mediaType != "image/jpeg" || format != "jpeg" || int64(len(body)) > visionLimit ||
		config.Width != schema.Chat.Context.VisionDerivative.LongSideSteps[0] || config.Height != 1365 {
		t.Fatalf("vision copy %s %dx%d, %d bytes", mediaType, config.Width, config.Height, len(body))
	}

	// The generation reference is the asset itself: same ID, original bytes, nothing re-encoded or uploaded.
	before := inputs.uploads
	ids, err := resolver.Resolve(ctx, "owner-a", []string{assetURL})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != uploaded.ID || inputs.uploads != before || !bytes.Equal(inputs.bodies[ids[0]], picture) {
		t.Fatalf("reference %#v, uploads %d → %d", ids, before, inputs.uploads)
	}

	// A task output (a generated picture, the same vision path) gets its copy too.
	tasks := &fakeTasks{contents: map[string]generation.AssetContent{
		"task-a/asset-a": {Body: io.NopCloser(bytes.NewReader(picture)), MediaType: "image/png", Size: int64(len(picture))},
	}}
	taskResolver := limitedResolver(t, inputs, resolverLimits{
		upload: schema.Runtime.InputAssets.UploadMaxBytes, vision: visionLimit, derivative: deriver, tasks: tasks,
	})
	fromTask, ok := taskResolver.OpenVisionImage(ctx, "owner-a", "/configured/tasks/task-a/assets/asset-a")
	if !ok {
		t.Fatal("a large task picture must still reach vision")
	}
	if taskBody, _ := decodedDataURI(t, fromTask); int64(len(taskBody)) > visionLimit {
		t.Fatalf("task copy is %d bytes", len(taskBody))
	}

	// Without a copy maker the picture is left to its label, as before.
	if _, ok := limitedResolver(t, inputs, resolverLimits{upload: schema.Runtime.InputAssets.UploadMaxBytes, vision: visionLimit}).
		OpenVisionImage(ctx, "owner-a", assetURL); ok {
		t.Fatal("without a vision copy maker a large picture is not inlined")
	}
}
