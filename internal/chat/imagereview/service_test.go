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
		Limits:     catalog.ImageInspectLimitsSchema{MaxBytes: 1 << 20, MaxPixels: 1 << 20},
		Delivery:   catalog.ChatImageChecklistSchema{MinSubjectRatio: 0.01, MaxSubjectRatio: 0.99},
		Input3D:    catalog.ChatImageChecklistSchema{RequirePlainBackground: true, MaxBackgroundSpread: 20, MinSubjectRatio: 0.05, RequireEdgeClear: true, MaxComponents: 1},
		MaxRounds:  3,
		OverrideNote: "override {reason}",
		UnreadableTemplate: "unreadable {handle}: {reason}",
		RoundsExhaustedTemplate: "rounds exhausted {maximum}",
		DeliveryChecklist: "delivery checklist",
		Input3DChecklist: "3d input checklist",
	}
	got, err := New(Config{
		Context: catalog.ChatContextSchema{
			HandlePrefixes: map[string]string{"image": "@img", "model": "@model"},
			SelectionAlias: "@selected",
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