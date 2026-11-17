package imagereview

// Golden tests for the platform inspect_image result (#332 7b-1). They record
// what Inspect returns today, so extracting the check assembly into a package
// helper must not change the Agent content, the persisted summary or the
// attached pictures. Regenerate with UPDATE_GOLDEN=1 after an intended change;
// a data URI is recorded by its prefix only, never as image bytes.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type goldenPicture struct {
	Handle    string `json:"handle"`
	Purpose   string `json:"purpose"`
	Label     string `json:"label"`
	URLPrefix string `json:"url_prefix"`
}

type goldenRecord struct {
	AgentContent any             `json:"agent_content"`
	Summary      any             `json:"summary"`
	Error        string          `json:"error"`
	NoSubmission bool            `json:"no_submission"`
	Pictures     []goldenPicture `json:"pictures"`
}

func goldenRecordOf(t *testing.T, result chat.ToolResult) goldenRecord {
	t.Helper()
	record := goldenRecord{Error: result.Error, NoSubmission: result.NoSubmission}
	if result.AgentContent != "" {
		if err := json.Unmarshal([]byte(result.AgentContent), &record.AgentContent); err != nil {
			t.Fatalf("agent content: %v", err)
		}
	}
	if len(result.Summary) > 0 {
		if err := json.Unmarshal(result.Summary, &record.Summary); err != nil {
			t.Fatalf("summary: %v", err)
		}
	}
	for _, picture := range result.Pictures {
		url := picture.URL
		if prefix := strings.Index(url, ","); strings.HasPrefix(url, "data:") && prefix > 0 {
			url = url[:prefix+1]
		}
		record.Pictures = append(record.Pictures, goldenPicture{
			Handle: picture.Handle, Purpose: picture.Purpose, Label: picture.Label, URLPrefix: url,
		})
	}
	return record
}

// shippedTasks is shipped with the Task asset reader instead of the input
// asset reader, so the task-product delivery branch has the product Schema.
func shippedTasks(t *testing.T, data []byte) *Service {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := product.Application().Chat
	service, err := New(Config{
		Context: schema.Context, Review: schema.ImageReview, Tasks: fakeTasks{data: data},
		Usage: schema.Image2DUsage, Brief: schema.Model3DUsage.Brief,
		Offerings: func(id string) (generation.Offering, bool) { return product.Get(generation.OfferingID(id)) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func goldenInspect(t *testing.T, service *Service, cmd chat.ToolCommand) chat.ToolResult {
	t.Helper()
	result, err := service.Inspect(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestInspectGolden(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T) chat.ToolResult
	}{
		{
			// A whole delivery picture of an input asset, judged against the
			// brief's purpose, so the purpose note and the failed check's hint
			// are recorded.
			name: "input_asset_delivery",
			run: func(t *testing.T) chat.ToolResult {
				data := pngOf(t, 128, 128, subjectPicture(128, 128))
				service, _ := shipped(t, data)
				cmd := command(`{"image":"@img1"}`, nil)
				cmd.Options.ProductionBrief = briefOf(t, map[string]any{"image_use": "illustration"})
				return goldenInspect(t, service, cmd)
			},
		},
		{
			// The same check over a saved Task product.
			name: "task_asset_delivery",
			run: func(t *testing.T) chat.ToolResult {
				data := pngOf(t, 128, 128, subjectPicture(128, 128))
				service := shippedTasks(t, data)
				cmd := command(`{"image":"@img1"}`, nil)
				cmd.History[0].Attachments[0].TaskID = "task_1"
				cmd.Options.ProductionBrief = briefOf(t, map[string]any{"image_use": "illustration"})
				return goldenInspect(t, service, cmd)
			},
		},
		{
			// The 3D input checklist: its own gate, no 2D purpose checks.
			name: "three_d_input",
			run: func(t *testing.T) chat.ToolResult {
				data := pngOf(t, 128, 128, subjectPicture(128, 128))
				service, _ := shipped(t, data)
				return goldenInspect(t, service, command(`{"image":"@img1","checklist":"3d_input"}`, nil))
			},
		},
		{
			// A region crop judged on the cropped bytes, with both pictures
			// attached.
			name: "crop_show_picture",
			run: func(t *testing.T) chat.ToolResult {
				crop := catalog.ChatVisionCropSchema{
					Enabled: true, Detail: "high", TokenCost: 1105,
					RegionArgument: "region", ShowPictureArgument: "show_picture",
					MinimumSidePixels: 8, MaximumPixels: 1 << 20,
					TooLargeTemplate:             "too large {maximum}",
					PictureLabelTemplate:         "[crop {handle} {region} {width}x{height}]",
					OriginalPictureLabelTemplate: "[whole {handle} {width}x{height}]",
				}
				service := cropService(t, fakeInputs{data: picture(t)}, crop)
				return goldenInspect(t, service, cropCommand(
					`{"image":"@img1","region":{"x":0.3,"y":0.3,"width":0.4,"height":0.4},"show_picture":true}`))
			},
		},
		{
			// An unknown checklist is refused before any bytes are read.
			name: "unknown_checklist",
			run: func(t *testing.T) chat.ToolResult {
				service := service(t, fakeInputs{data: picture(t)}, nil)
				return goldenInspect(t, service, command(`{"image":"@img1","checklist":"retired"}`, nil))
			},
		},
		{
			// An input asset that cannot be read.
			name: "unreadable",
			run: func(t *testing.T) chat.ToolResult {
				service := service(t, failingInputs{}, nil)
				return goldenInspect(t, service, command(`{"image":"@img1"}`, nil))
			},
		},
		{
			// The byte cap refuses the picture before it is decoded.
			name: "too_large",
			run: func(t *testing.T) chat.ToolResult {
				service := service(t, fakeInputs{data: pngOf(t, 100, 100, subjectPicture(100, 100))}, nil)
				service.review.Limits.MaxBytes = 64
				return goldenInspect(t, service, command(`{"image":"@img1"}`, nil))
			},
		},
		{
			// A Catalog without image2d_usage keeps the historical delivery
			// checklist: no purpose block, no purpose checks.
			name: "no_usage",
			run: func(t *testing.T) chat.ToolResult {
				service := service(t, fakeInputs{data: picture(t)}, nil)
				cmd := command(`{"image":"@img1"}`, nil)
				cmd.Options.ProductionBrief = briefOf(t, map[string]any{"image_use": "illustration"})
				return goldenInspect(t, service, cmd)
			},
		},
	}

	update := os.Getenv("UPDATE_GOLDEN") == "1"
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			encoded, err := json.MarshalIndent(goldenRecordOf(t, testCase.run(t)), "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			encoded = append(encoded, '\n')
			path := filepath.Join("testdata", "inspect", testCase.name+".json")
			if update {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, encoded, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden: %v (generate with UPDATE_GOLDEN=1)", err)
			}
			if string(want) != string(encoded) {
				t.Fatalf("%s changed; regenerate with UPDATE_GOLDEN=1\ngot:\n%s", path, encoded)
			}
		})
	}
}
