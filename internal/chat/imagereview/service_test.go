package imagereview

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/visionview"
)

type fakeInputs struct {
	data []byte
}

func (f fakeInputs) OpenOwned(context.Context, generation.OwnerID, generation.AssetID) (generation.InputAsset, io.ReadCloser, error) {
	return generation.InputAsset{ID: "a1"}, io.NopCloser(bytes.NewReader(f.data)), nil
}

type fakeTasks struct {
	data []byte
}

func (f fakeTasks) OpenAsset(context.Context, generation.OwnerID, generation.TaskID, generation.AssetID) (generation.AssetContent, error) {
	return generation.AssetContent{Body: io.NopCloser(bytes.NewReader(f.data)), MediaType: "image/png"}, nil
}

func picture(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 100, 100))
	for y := 30; y < 70; y++ {
		for x := 30; x < 70; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func service(t *testing.T, inputs InputAssetReader, tasks TaskAssetReader) *Service {
	t.Helper()
	review := catalog.ChatImageReviewSchema{
		Limits:                  catalog.ImageInspectLimitsSchema{MaxBytes: 1 << 20, MaxPixels: 1 << 20},
		Delivery:                catalog.ChatImageChecklistSchema{MinSubjectRatio: 0.01, MaxSubjectRatio: 0.99},
		Input3D:                 catalog.ChatImageChecklistSchema{RequirePlainBackground: true, MaxBackgroundSpread: 20, MinSubjectRatio: 0.05, RequireEdgeClear: true, MaxComponents: 1},
		MaxRounds:               3,
		OverrideNote:            "override {reason}",
		UnreadableTemplate:      "unreadable {handle}: {reason}",
		RoundsExhaustedTemplate: "rounds exhausted {maximum}",
		DeliveryChecklist:       "delivery checklist",
		Input3DChecklist:        "3d input checklist",
	}
	got, err := New(Config{
		Context: catalog.ChatContextSchema{
			HandlePrefixes:        map[string]string{"image": "@img", "model": "@model"},
			SelectionAlias:        "@selected",
			UnknownHandleTemplate: "unknown {handle}", WrongHandleKindTemplate: "kind {handle}",
		},
		Review: review, Inputs: inputs, Tasks: tasks,
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func command(arguments string, earlier []chat.ToolCall) chat.ToolCommand {
	return chat.ToolCommand{
		OwnerID: "owner",
		Call:    agentsCall(arguments),
		History: []chat.Message{{Role: "assistant", Attachments: []chat.Attachment{{
			Handle: "@img1", Kind: chat.AttachmentKindImage, AssetID: "a1",
		}}}},
		EarlierCalls: earlier,
	}
}

func agentsCall(arguments string) agents.ToolCall {
	return agents.ToolCall{ID: "call_1", Name: "inspect_image", Arguments: []byte(arguments)}
}

func TestInspectAnInputAsset(t *testing.T) {
	svc := service(t, fakeInputs{data: picture(t)}, nil)
	result, err := svc.Inspect(context.Background(), command(`{"image":"@img1","checklist":"3d_input"}`, nil))
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != "" || result.NoSubmission != true {
		t.Fatalf("result = %+v", result)
	}
	var payload struct {
		OK     bool `json:"ok"`
		Report struct {
			Width    int  `json:"width"`
			HasAlpha bool `json:"has_alpha"`
		} `json:"report"`
		UsageCheck struct {
			Checklist string `json:"checklist"`
			Checks    []struct {
				Status string `json:"status"`
			} `json:"checks"`
		} `json:"usage_check"`
		RoundsRemaining int `json:"rounds_remaining"`
	}
	if err := json.Unmarshal([]byte(result.AgentContent), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.OK || payload.Report.Width != 100 || !payload.Report.HasAlpha {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.UsageCheck.Checklist != "3d_input" || len(payload.UsageCheck.Checks) == 0 {
		t.Fatalf("usage = %+v", payload.UsageCheck)
	}
	if payload.RoundsRemaining != 2 {
		t.Fatalf("rounds remaining = %d", payload.RoundsRemaining)
	}
	if len(result.Summary) == 0 || !strings.Contains(string(result.Summary), `"tool":"inspect_image"`) {
		t.Fatalf("summary = %s", result.Summary)
	}
}

func TestInspectATaskAsset(t *testing.T) {
	svc := service(t, nil, fakeTasks{data: picture(t)})
	cmd := command(`{"image":"@img1"}`, nil)
	cmd.History[0].Attachments[0].TaskID = "task_1"
	result, err := svc.Inspect(context.Background(), cmd)
	if err != nil || result.Error != "" {
		t.Fatalf("result = %+v err=%v", result, err)
	}
	if !strings.Contains(result.AgentContent, `"checklist":"delivery"`) {
		t.Fatalf("content = %s", result.AgentContent)
	}
}

func TestUnknownHandleAndRounds(t *testing.T) {
	svc := service(t, fakeInputs{data: picture(t)}, nil)
	if result, _ := svc.Inspect(context.Background(), command(`{"image":"@img9"}`, nil)); !strings.Contains(result.Error, "unknown @img9") {
		t.Fatalf("unknown handle result = %+v", result)
	}
	earlier := []chat.ToolCall{{Name: "inspect_image"}, {Name: "inspect_image"}, {Name: "inspect_image"}}
	if result, _ := svc.Inspect(context.Background(), command(`{"image":"@img1"}`, earlier)); !strings.Contains(result.Error, "rounds exhausted 3") {
		t.Fatalf("rounds result = %+v", result)
	}
}

func TestUnreadableAssetUsesTheSchemaTemplate(t *testing.T) {
	svc := service(t, failingInputs{}, nil)
	result, _ := svc.Inspect(context.Background(), command(`{"image":"@img1"}`, nil))
	if !strings.Contains(result.Error, "unreadable @img1") {
		t.Fatalf("result = %+v", result)
	}
}

type failingInputs struct{}

func (failingInputs) OpenOwned(context.Context, generation.OwnerID, generation.AssetID) (generation.InputAsset, io.ReadCloser, error) {
	return generation.InputAsset{}, nil, io.ErrUnexpectedEOF
}

// cropService is the same review service with the #247 crop policy declared.
func cropService(t *testing.T, inputs InputAssetReader, crop catalog.ChatVisionCropSchema) *Service {
	t.Helper()
	context := catalog.ChatContextSchema{
		HandlePrefixes:        map[string]string{"image": "@img", "model": "@model"},
		SelectionAlias:        "@selected",
		UnknownHandleTemplate: "unknown {handle}", WrongHandleKindTemplate: "kind {handle}",
		VisionInlineMaxBytes: 1 << 20,
		Vision:               &catalog.ChatVisionSchema{Crop: crop},
	}
	review := catalog.ChatImageReviewSchema{
		Limits:                  catalog.ImageInspectLimitsSchema{MaxBytes: 1 << 20, MaxPixels: 1 << 20},
		Delivery:                catalog.ChatImageChecklistSchema{MinSubjectRatio: 0.01, MaxSubjectRatio: 0.99},
		Input3D:                 catalog.ChatImageChecklistSchema{RequirePlainBackground: true, MaxBackgroundSpread: 20, MinSubjectRatio: 0.05, RequireEdgeClear: true, MaxComponents: 1},
		MaxRounds:               3,
		OverrideNote:            "override {reason}",
		UnreadableTemplate:      "unreadable {handle}: {reason}",
		RoundsExhaustedTemplate: "rounds exhausted {maximum}",
		DeliveryChecklist:       "delivery checklist",
		Input3DChecklist:        "3d input checklist",
	}
	got, err := New(Config{Context: context, Review: review, Inputs: inputs})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func cropCommand(arguments string) chat.ToolCommand {
	command := command(arguments, nil)
	command.History[0].Attachments[0].URL = "/api/v1/assets/a1"
	return command
}

// #247: a region crop keeps the original pixels (no scaling) and reports the
// source size and the pixel rectangle beside the crop's own facts.
func TestInspectImageCropKeepsOriginalPixelsAndOwnership(t *testing.T) {
	crop := catalog.ChatVisionCropSchema{
		Enabled: true, Detail: "high", TokenCost: 1105,
		RegionArgument: "region", ShowPictureArgument: "show_picture",
		MinimumSidePixels: 8, MaximumPixels: 1 << 20,
		TooLargeTemplate:     "too large {maximum}",
		PictureLabelTemplate: "[crop {handle} {region} {width}x{height}]",
	}
	svc := cropService(t, fakeInputs{data: picture(t)}, crop)
	result, err := svc.Inspect(context.Background(), cropCommand(
		`{"image":"@img1","region":{"x":0.3,"y":0.3,"width":0.4,"height":0.4},"show_picture":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != "" {
		t.Fatalf("result = %+v", result)
	}
	var payload struct {
		Image struct {
			Width        int
			Height       int
			SourceWidth  int `json:"source_width"`
			SourceHeight int `json:"source_height"`
			Region       struct{ X, Y, Width, Height int }
			CropWidth    int `json:"crop_width"`
		} `json:"image"`
	}
	if err := json.Unmarshal([]byte(result.AgentContent), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Image.Region.X != 30 || payload.Image.Region.Y != 30 ||
		payload.Image.Region.Width != 40 || payload.Image.Region.Height != 40 ||
		payload.Image.SourceWidth != 100 || payload.Image.SourceHeight != 100 ||
		payload.Image.Width != 40 || payload.Image.Height != 40 {
		t.Fatalf("crop facts = %+v", payload.Image)
	}
	if len(result.Pictures) != 2 {
		t.Fatalf("show_picture pictures = %+v", result.Pictures)
	}
	original, cropped := result.Pictures[0], result.Pictures[1]
	if original.URL != "/api/v1/assets/a1" || original.Purpose != chat.ToolPictureCrop ||
		original.Detail != "high" {
		t.Fatalf("original picture = %+v", original)
	}
	if cropped.Handle != "@img1" || cropped.Detail != "high" ||
		!strings.Contains(cropped.Label, "@img1") || !strings.Contains(cropped.Label, "30,30 40×40") ||
		!strings.HasPrefix(cropped.URL, "data:image/jpeg;base64,") {
		t.Fatalf("crop picture = %+v", cropped)
	}
	decoded, err := visionview.DecodeDataURI(cropped.URL)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() != 40 || decoded.Bounds().Dy() != 40 {
		t.Fatalf("crop pixels = %v", decoded.Bounds())
	}
}

// A crop over the declared pixel bound is refused with the Schema text and no
// picture.
func TestCropOverLimitsIsRefused(t *testing.T) {
	crop := catalog.ChatVisionCropSchema{
		Enabled: true, Detail: "high", TokenCost: 1105,
		RegionArgument: "region", ShowPictureArgument: "show_picture",
		MinimumSidePixels: 8, MaximumPixels: 100,
		TooLargeTemplate:     "too large {width}x{height} maximum {maximum}",
		PictureLabelTemplate: "[crop {handle}]",
	}
	svc := cropService(t, fakeInputs{data: picture(t)}, crop)
	result, err := svc.Inspect(context.Background(), cropCommand(
		`{"image":"@img1","region":{"x":0,"y":0,"width":1,"height":1},"show_picture":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Error, "100x100 maximum 100") || len(result.Pictures) != 0 {
		t.Fatalf("result = %+v", result)
	}
}

// show_picture without a region attaches the named original.
func TestInspectImageShowsTheNamedOriginal(t *testing.T) {
	crop := catalog.ChatVisionCropSchema{
		Enabled: true, Detail: "high", TokenCost: 1105,
		RegionArgument: "region", ShowPictureArgument: "show_picture",
		MinimumSidePixels: 8, MaximumPixels: 1 << 20,
		TooLargeTemplate:     "too large {maximum}",
		PictureLabelTemplate: "[crop {handle} {region}]",
	}
	svc := cropService(t, fakeInputs{data: picture(t)}, crop)
	result, err := svc.Inspect(context.Background(), cropCommand(`{"image":"@img1","show_picture":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Pictures) != 1 || result.Pictures[0].URL != "/api/v1/assets/a1" ||
		!strings.Contains(result.Pictures[0].Label, "@img1") {
		t.Fatalf("pictures = %+v", result.Pictures)
	}
}
