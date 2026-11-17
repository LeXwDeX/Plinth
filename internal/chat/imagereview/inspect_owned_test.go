package imagereview

// Tests for the read-only entry the external MCP inspector calls (#332 7b-2):
// the same check assembly as the platform tool, but with the picture named by
// an input asset or a Task asset and every refusal classified for a transport.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// ownedInputAsset serves one input asset, enforcing the owner and ID the
// request names and recording whether the body was read.
type ownedInputAsset struct {
	data    []byte
	size    int64 // 0 means len(data)
	owner   generation.OwnerID
	id      generation.AssetID
	read    bool
	nilBody bool
}

func (fake *ownedInputAsset) OpenOwned(_ context.Context, owner generation.OwnerID,
	id generation.AssetID,
) (generation.InputAsset, io.ReadCloser, error) {
	if (fake.owner != "" && owner != fake.owner) || (fake.id != "" && id != fake.id) {
		return generation.InputAsset{}, nil, &generation.Error{
			Code: generation.ErrNotFound, Operation: "open_input_asset", Detail: "输入资产不存在",
		}
	}
	size := fake.size
	if size == 0 {
		size = int64(len(fake.data))
	}
	asset := generation.InputAsset{ID: id, OwnerID: owner, StorageKey: "key", Size: size}
	if fake.nilBody {
		return asset, nil, nil
	}
	return asset, &trackedBody{Reader: bytes.NewReader(fake.data), read: &fake.read}, nil
}

// ownedTaskAsset serves one saved Task asset the same way.
type ownedTaskAsset struct {
	data    []byte
	size    int64
	owner   generation.OwnerID
	task    generation.TaskID
	id      generation.AssetID
	read    bool
	nilBody bool
}

func (fake *ownedTaskAsset) OpenAsset(_ context.Context, owner generation.OwnerID,
	task generation.TaskID, id generation.AssetID,
) (generation.AssetContent, error) {
	if (fake.owner != "" && owner != fake.owner) || (fake.task != "" && task != fake.task) ||
		(fake.id != "" && id != fake.id) {
		return generation.AssetContent{}, &generation.Error{
			Code: generation.ErrNotFound, Operation: "open_asset", Detail: "资产不存在",
		}
	}
	size := fake.size
	if size == 0 {
		size = int64(len(fake.data))
	}
	content := generation.AssetContent{MediaType: "image/png", Size: size}
	if !fake.nilBody {
		content.Body = &trackedBody{Reader: bytes.NewReader(fake.data), read: &fake.read}
	}
	return content, nil
}

// trackedBody records whether any byte was actually read.
type trackedBody struct {
	io.Reader
	read *bool
}

func (body *trackedBody) Read(p []byte) (int, error) {
	*body.read = true
	return body.Reader.Read(p)
}

func (body *trackedBody) Close() error { return nil }

// shippedOwned wires the service from the shipped Catalog with the test's own
// readers, so purposes, thresholds and notes are the product's.
func shippedOwned(t *testing.T, inputs InputAssetReader, tasks TaskAssetReader) *Service {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := product.Application().Chat
	service, err := New(Config{
		Context: schema.Context, Review: schema.ImageReview, Inputs: inputs, Tasks: tasks,
		Usage: schema.Image2DUsage, Brief: schema.Model3DUsage.Brief,
		Offerings: func(id string) (generation.Offering, bool) { return product.Get(generation.OfferingID(id)) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func refusalOf(t *testing.T, err error) *InspectRefusal {
	t.Helper()
	var refusal *InspectRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("error = %v, want an InspectRefusal", err)
	}
	return refusal
}

func TestInspectOwnedReadsAnInputAsset(t *testing.T) {
	data := pngOf(t, 128, 128, subjectPicture(128, 128))
	inputs := &ownedInputAsset{data: data, owner: "owner", id: "a1"}
	service := shippedOwned(t, inputs, nil)
	checked, err := service.InspectOwned(context.Background(), InspectRequest{
		Owner: "owner", Source: PictureSource{InputAsset: "a1"}, Checklist: "delivery",
		Brief: briefOf(t, map[string]any{"image_use": "illustration"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked.Report.Width != 128 || checked.Report.Height != 128 {
		t.Fatalf("report = %+v", checked.Report)
	}
	if checked.MediaType != "image/png" || checked.Size != int64(len(data)) {
		t.Fatalf("byte facts = %q, %d", checked.MediaType, checked.Size)
	}
	if checked.ChecklistText != service.review.DeliveryChecklist || len(checked.Checks) == 0 {
		t.Fatalf("checklist = %q, checks = %+v", checked.ChecklistText, checked.Checks)
	}
	if checked.Purpose == nil || checked.Purpose.ID != "illustration" ||
		checked.Purpose.Source != purposeSourceBrief {
		t.Fatalf("purpose = %+v", checked.Purpose)
	}
	if !inputs.read {
		t.Fatal("the input asset body was never read")
	}
}

func TestInspectOwnedReadsATaskAsset(t *testing.T) {
	data := pngOf(t, 128, 128, subjectPicture(128, 128))
	tasks := &ownedTaskAsset{data: data, owner: "owner", task: "task_1", id: "asset_1"}
	service := shippedOwned(t, nil, tasks)
	checked, err := service.InspectOwned(context.Background(), InspectRequest{
		Owner: "owner", Source: PictureSource{TaskID: "task_1", TaskAsset: "asset_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked.Report.Width != 128 || checked.MediaType != "image/png" || !tasks.read {
		t.Fatalf("inspection = %+v read=%v", checked, tasks.read)
	}
}

func TestInspectOwnedRefusesAnotherOwnersPicture(t *testing.T) {
	inputs := &ownedInputAsset{data: pngOf(t, 32, 32, subjectPicture(32, 32)),
		owner: "someone_else", id: "a1"}
	service := shippedOwned(t, inputs, nil)
	_, err := service.InspectOwned(context.Background(), InspectRequest{
		Owner: "owner", Source: PictureSource{InputAsset: "a1"},
	})
	if refusal := refusalOf(t, err); refusal.Kind != InspectNotFound {
		t.Fatalf("refusal = %+v", refusal)
	}
	if inputs.read {
		t.Fatal("a foreign picture's bytes were read")
	}
}

func TestInspectOwnedRefusesAnUnknownChecklist(t *testing.T) {
	inputs := &ownedInputAsset{data: picture(t)}
	service := shippedOwned(t, inputs, nil)
	_, err := service.InspectOwned(context.Background(), InspectRequest{
		Owner: "owner", Source: PictureSource{InputAsset: "a1"}, Checklist: "retired",
	})
	if refusal := refusalOf(t, err); refusal.Kind != InspectUnknownChecklist {
		t.Fatalf("refusal = %+v", refusal)
	}
	if inputs.read {
		t.Fatal("an unknown checklist read bytes")
	}
}

func TestInspectOwnedRefusesAnUnknownPurpose(t *testing.T) {
	inputs := &ownedInputAsset{data: picture(t)}
	service := shippedOwned(t, inputs, nil)
	_, err := service.InspectOwned(context.Background(), InspectRequest{
		Owner: "owner", Source: PictureSource{InputAsset: "a1"}, Purpose: "retired",
	})
	if refusal := refusalOf(t, err); refusal.Kind != InspectUnknownPurpose {
		t.Fatalf("refusal = %+v", refusal)
	}
	if inputs.read {
		t.Fatal("an unknown purpose read bytes")
	}
}

func TestInspectOwnedRefusesAPurposeWithThe3DChecklist(t *testing.T) {
	service := shippedOwned(t, &ownedInputAsset{data: picture(t)}, nil)
	_, err := service.InspectOwned(context.Background(), InspectRequest{
		Owner: "owner", Source: PictureSource{InputAsset: "a1"},
		Checklist: "3d_input", Purpose: "illustration",
	})
	if refusal := refusalOf(t, err); refusal.Kind != InspectUnknownPurpose {
		t.Fatalf("refusal = %+v", refusal)
	}
}

// The purpose argument judges exactly what the platform judged from a brief
// naming the same purpose; only its source is the argument and it has no note.
func TestInspectOwnedJudgesAnExplicitPurposeLikeThePlatform(t *testing.T) {
	data := pngOf(t, 128, 128, subjectPicture(128, 128))
	service := shippedOwned(t, &ownedInputAsset{data: data, owner: "owner", id: "a1"}, nil)
	cmd := command(`{"image":"@img1"}`, nil)
	cmd.Options.ProductionBrief = briefOf(t, map[string]any{"image_use": "illustration"})
	result, err := service.Inspect(context.Background(), cmd)
	if err != nil || result.Error != "" {
		t.Fatalf("platform result = %+v err=%v", result, err)
	}
	var platform parityPayload
	if err := json.Unmarshal([]byte(result.AgentContent), &platform); err != nil {
		t.Fatal(err)
	}
	checked, err := service.InspectOwned(context.Background(), InspectRequest{
		Owner: "owner", Source: PictureSource{InputAsset: "a1"}, Purpose: "illustration",
	})
	if err != nil {
		t.Fatal(err)
	}
	report, _ := json.Marshal(checked.Report)
	if string(platform.Report) != string(report) {
		t.Fatalf("report drifted:\nplatform %s\nargument %s", platform.Report, report)
	}
	checks, _ := json.Marshal(checked.Checks)
	if string(platform.UsageCheck.Checks) != string(checks) {
		t.Fatalf("checks drifted:\nplatform %s\nargument %s", platform.UsageCheck.Checks, checks)
	}
	if checked.Purpose == nil || checked.Purpose.ID != platform.UsageCheck.Purpose ||
		checked.Purpose.Label != platform.UsageCheck.PurposeLabel {
		t.Fatalf("purpose = %+v, platform = %+v", checked.Purpose, platform.UsageCheck)
	}
	if checked.Purpose.Source != purposeSourceArgument || checked.Purpose.Note != "" {
		t.Fatalf("argument purpose = %+v; want source argument and no note", *checked.Purpose)
	}
}

// Without a purpose argument or a brief purpose, the Schema's default judges
// the picture, with the platform's default note.
func TestInspectOwnedWithoutABriefUsesTheDefaultPurpose(t *testing.T) {
	service := shippedOwned(t, &ownedInputAsset{data: picture(t)}, nil)
	checked, err := service.InspectOwned(context.Background(), InspectRequest{
		Owner: "owner", Source: PictureSource{InputAsset: "a1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked.Purpose == nil || checked.Purpose.Source != purposeSourceDefault ||
		checked.Purpose.ID != service.usage.DefaultProfile || checked.Purpose.Note == "" {
		t.Fatalf("default purpose = %+v", checked.Purpose)
	}
}

// The 3D input checklist has its own gate: no purpose checks and no purpose.
func TestInspectOwnedRunsThe3DInputChecklist(t *testing.T) {
	service := shippedOwned(t, &ownedInputAsset{data: picture(t)}, nil)
	checked, err := service.InspectOwned(context.Background(), InspectRequest{
		Owner: "owner", Source: PictureSource{InputAsset: "a1"}, Checklist: "3d_input",
		Brief: briefOf(t, map[string]any{"image_use": "illustration"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked.Purpose != nil || checked.ChecklistText != service.review.Input3DChecklist {
		t.Fatalf("inspection = %+v", checked)
	}
	for _, check := range checked.Checks {
		if check.Name == "short_edge" {
			t.Fatal("the 3D input checklist judged a 2D purpose")
		}
	}
}

// The recorded size refuses the picture before any byte is read.
func TestInspectOwnedRefusesAPictureOverTheByteLimit(t *testing.T) {
	data := pngOf(t, 100, 100, subjectPicture(100, 100))
	inputs := &ownedInputAsset{data: data, owner: "owner", id: "a1"}
	service := shippedOwned(t, inputs, nil)
	_, err := service.InspectOwned(context.Background(), InspectRequest{
		Owner: "owner", Source: PictureSource{InputAsset: "a1"}, MaxBytes: int64(len(data)) - 1,
	})
	refusal := refusalOf(t, err)
	if refusal.Kind != InspectTooLarge || !strings.Contains(refusal.Message, "byte limit") {
		t.Fatalf("refusal = %+v", refusal)
	}
	if inputs.read {
		t.Fatal("the body was read although the recorded size was over the cap")
	}
}

// A caller's cap never loosens the Schema's.
func TestInspectOwnedNeverLoosensTheSchemaByteLimit(t *testing.T) {
	data := pngOf(t, 100, 100, subjectPicture(100, 100))
	service := shippedOwned(t, &ownedInputAsset{data: data, owner: "owner", id: "a1"}, nil)
	service.review.Limits.MaxBytes = 64
	_, err := service.InspectOwned(context.Background(), InspectRequest{
		Owner: "owner", Source: PictureSource{InputAsset: "a1"}, MaxBytes: 1 << 30,
	})
	if refusal := refusalOf(t, err); refusal.Kind != InspectTooLarge {
		t.Fatalf("refusal = %+v", refusal)
	}
}

// The pixel cap is classified from the decode sentinel, not from the words.
func TestInspectOwnedRefusesAPictureOverThePixelLimit(t *testing.T) {
	service := shippedOwned(t, &ownedInputAsset{data: picture(t)}, nil)
	_, err := service.InspectOwned(context.Background(), InspectRequest{
		Owner: "owner", Source: PictureSource{InputAsset: "a1"}, MaxPixels: 50,
	})
	if refusal := refusalOf(t, err); refusal.Kind != InspectTooLarge {
		t.Fatalf("refusal = %+v", refusal)
	}
}

func TestInspectOwnedNeverLoosensTheSchemaPixelLimit(t *testing.T) {
	service := shippedOwned(t, &ownedInputAsset{data: picture(t)}, nil)
	service.review.Limits.MaxPixels = 50
	_, err := service.InspectOwned(context.Background(), InspectRequest{
		Owner: "owner", Source: PictureSource{InputAsset: "a1"}, MaxPixels: 1 << 30,
	})
	if refusal := refusalOf(t, err); refusal.Kind != InspectTooLarge {
		t.Fatalf("refusal = %+v", refusal)
	}
}

func TestInspectOwnedRefusesUnreadableBytes(t *testing.T) {
	service := shippedOwned(t, &ownedInputAsset{data: []byte("not a picture"), owner: "owner", id: "a1"}, nil)
	_, err := service.InspectOwned(context.Background(), InspectRequest{
		Owner: "owner", Source: PictureSource{InputAsset: "a1"},
	})
	if refusal := refusalOf(t, err); refusal.Kind != InspectUnreadable {
		t.Fatalf("refusal = %+v", refusal)
	}
}

func TestInspectOwnedRefusesAnUnwiredReader(t *testing.T) {
	service := shippedOwned(t, &ownedInputAsset{data: picture(t)}, nil)
	_, err := service.InspectOwned(context.Background(), InspectRequest{
		Owner: "owner", Source: PictureSource{TaskID: "task_1", TaskAsset: "a1"},
	})
	if refusal := refusalOf(t, err); refusal.Kind != InspectUnavailable {
		t.Fatalf("refusal = %+v", refusal)
	}
}

func TestInspectOwnedRefusesAPictureWithoutStoredBytes(t *testing.T) {
	service := shippedOwned(t, &ownedInputAsset{owner: "owner", id: "a1", nilBody: true}, nil)
	_, err := service.InspectOwned(context.Background(), InspectRequest{
		Owner: "owner", Source: PictureSource{InputAsset: "a1"},
	})
	if refusal := refusalOf(t, err); refusal.Kind != InspectNotReady {
		t.Fatalf("refusal = %+v", refusal)
	}
}

func TestInspectOwnedRefusesAMixedPictureSource(t *testing.T) {
	service := shippedOwned(t, &ownedInputAsset{data: picture(t)}, nil)
	for name, source := range map[string]PictureSource{
		"both":        {InputAsset: "a1", TaskID: "task_1", TaskAsset: "a1"},
		"neither":     {},
		"half a task": {TaskID: "task_1"},
	} {
		_, err := service.InspectOwned(context.Background(), InspectRequest{Owner: "owner", Source: source})
		if err == nil {
			t.Fatalf("%s: no error", name)
		}
		var refusal *InspectRefusal
		if errors.As(err, &refusal) {
			t.Fatalf("%s: a caller bug read as a refusal: %+v", name, refusal)
		}
	}
}

// The platform tool and the read-only entry return the same inspection for the
// same picture and brief.
func TestInspectOwnedMatchesThePlatformTool(t *testing.T) {
	data := pngOf(t, 128, 128, subjectPicture(128, 128))
	service := shippedOwned(t, &ownedInputAsset{data: data, owner: "owner", id: "a1"}, nil)
	brief := briefOf(t, map[string]any{"image_use": "illustration"})
	cmd := command(`{"image":"@img1"}`, nil)
	cmd.Options.ProductionBrief = brief
	result, err := service.Inspect(context.Background(), cmd)
	if err != nil || result.Error != "" {
		t.Fatalf("platform result = %+v err=%v", result, err)
	}
	checked, err := service.InspectOwned(context.Background(), InspectRequest{
		Owner: "owner", Source: PictureSource{InputAsset: "a1"}, Brief: brief,
	})
	if err != nil {
		t.Fatal(err)
	}
	checkParity(t, result, checked, int64(len(data)))
}
