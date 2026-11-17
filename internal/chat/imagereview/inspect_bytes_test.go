package imagereview

// Parity proves the extracted inspectBytes assembly matches what the platform
// inspect_image tool returns for the same bytes (#332 7b-1).

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

type parityPayload struct {
	Report     json.RawMessage `json:"report"`
	UsageCheck struct {
		Checklist     string          `json:"checklist"`
		ChecklistText string          `json:"checklist_text"`
		Purpose       string          `json:"purpose"`
		PurposeLabel  string          `json:"purpose_label"`
		PurposeSource string          `json:"purpose_source"`
		Note          string          `json:"note"`
		Checks        json.RawMessage `json:"checks"`
	} `json:"usage_check"`
}

func checkParity(t *testing.T, result chat.ToolResult, checked Inspection, size int64) {
	t.Helper()
	if result.Error != "" {
		t.Fatalf("platform result = %+v", result)
	}
	var payload parityPayload
	if err := json.Unmarshal([]byte(result.AgentContent), &payload); err != nil {
		t.Fatal(err)
	}
	report, err := json.Marshal(checked.Report)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload.Report) != string(report) {
		t.Fatalf("report drifted:\nplatform %s\nextracted %s", payload.Report, report)
	}
	checks, err := json.Marshal(checked.Checks)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload.UsageCheck.Checks) != string(checks) {
		t.Fatalf("checks drifted:\nplatform %s\nextracted %s", payload.UsageCheck.Checks, checks)
	}
	if payload.UsageCheck.Checklist != "delivery" || checked.ChecklistText != payload.UsageCheck.ChecklistText {
		t.Fatalf("checklist text drifted: platform %q, extracted %q", payload.UsageCheck.ChecklistText, checked.ChecklistText)
	}
	if checked.Purpose == nil {
		t.Fatal("the extracted inspection has no purpose, the platform result judged one")
	}
	if checked.Purpose.ID != payload.UsageCheck.Purpose ||
		checked.Purpose.Label != payload.UsageCheck.PurposeLabel ||
		checked.Purpose.Source != payload.UsageCheck.PurposeSource ||
		checked.Purpose.Note != payload.UsageCheck.Note {
		t.Fatalf("purpose drifted: platform %+v, extracted %+v", payload.UsageCheck, *checked.Purpose)
	}
	if checked.MediaType != "image/png" || checked.Size != size {
		t.Fatalf("byte facts = %q, %d; want image/png, %d", checked.MediaType, checked.Size, size)
	}
}

func TestInspectBytesMatchesThePlatformTool(t *testing.T) {
	data := pngOf(t, 128, 128, subjectPicture(128, 128))
	brief := briefOf(t, map[string]any{"image_use": "illustration"})

	t.Run("input asset", func(t *testing.T) {
		service, _ := shipped(t, data)
		cmd := command(`{"image":"@img1"}`, nil)
		cmd.Options.ProductionBrief = brief
		result, err := service.Inspect(context.Background(), cmd)
		if err != nil {
			t.Fatal(err)
		}
		checked, err := service.inspectBytes(data, "delivery", brief)
		if err != nil {
			t.Fatal(err)
		}
		checkParity(t, result, checked, int64(len(data)))
	})

	t.Run("task asset", func(t *testing.T) {
		service := shippedTasks(t, data)
		cmd := command(`{"image":"@img1"}`, nil)
		cmd.History[0].Attachments[0].TaskID = "task_1"
		cmd.Options.ProductionBrief = brief
		result, err := service.Inspect(context.Background(), cmd)
		if err != nil {
			t.Fatal(err)
		}
		checked, err := service.inspectBytes(data, "delivery", brief)
		if err != nil {
			t.Fatal(err)
		}
		checkParity(t, result, checked, int64(len(data)))
	})
}

// The inspected bytes' actual container becomes the media type; an unknown
// format is never guessed.
func TestMediaTypeOfTheInspectedBytes(t *testing.T) {
	for format, want := range map[string]string{
		"png": "image/png", "jpeg": "image/jpeg", "webp": "image/webp", "gif": "",
	} {
		if got := mediaTypeOf(format); got != want {
			t.Fatalf("mediaTypeOf(%q) = %q, want %q", format, got, want)
		}
	}
}
