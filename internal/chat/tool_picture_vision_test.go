package chat

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func pngDataURI(t *testing.T, shade uint8) string {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, 32, 32))
	draw.Draw(picture, picture.Bounds(), &image.Uniform{
		C: color.RGBA{R: shade, G: 80, B: 160, A: 255},
	}, image.Point{}, draw.Src)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
}

func contactSheetOrchestrator(t *testing.T, schema catalog.ChatSchema, pictures []string) *Orchestrator {
	t.Helper()
	index := 0
	return &Orchestrator{
		schema: schema,
		model:  "test-model",
		visionInline: func(_ context.Context, _ generation.OwnerID, url string) (string, bool) {
			if url == "broken" {
				return "data:image/png;base64,AAAA", true
			}
			index++
			return pictures[(index-1)%len(pictures)], true
		},
	}
}

func imagePartsOf(message agents.Message, kind string) []agents.ContentPart {
	var parts []agents.ContentPart
	for _, part := range message.Parts {
		if part.Kind == kind {
			parts = append(parts, part)
		}
	}
	return parts
}

func replyText(message agents.Message) string {
	var builder strings.Builder
	builder.WriteString(message.Content)
	for _, part := range message.Parts {
		builder.WriteString(part.Text)
	}
	return builder.String()
}

// Six candidates over a role's two places become one labelled contact sheet:
// the Agent gets every handle in text and one picture to compare first.
func TestContactSheetReplacesOverflowCandidatesWithHandles(t *testing.T) {
	schema := visionTestSchema(t, []catalog.ChatVisionRoleSchema{
		visionRole("recent_result", []string{"generated"}, 2, 100, "high"),
		visionRole("history", []string{"history"}, 4, 10, "low"),
	}, 10, 10000)
	schema.Context.MaxResultPictures = 4
	png := pngDataURI(t, 30)
	orchestrator := contactSheetOrchestrator(t, schema, []string{png})
	budget := orchestrator.visionBudget(TurnOptions{})
	spend := visionSpend{}
	pictures := make([]ToolPicture, 6)
	for index := range pictures {
		pictures[index] = ToolPicture{
			Handle: "@img" + string(rune('1'+index)), URL: "/candidate/" + string(rune('a'+index)),
			Fresh: true, Purpose: ToolPictureResult,
		}
	}
	reply := agents.Message{Role: "tool", Content: "six candidates"}
	orchestrator.attachToolPictures(context.Background(), "owner", &reply, pictures, budget, &spend)

	images := imagePartsOf(reply, schema.Context.ImagePartKind)
	if len(images) != 1 || images[0].Detail != "high" || !strings.HasPrefix(images[0].URL, "data:image/jpeg;base64,") {
		t.Fatalf("contact sheet image parts = %+v", images)
	}
	text := replyText(reply)
	for index := 1; index <= 6; index++ {
		if !strings.Contains(text, "@img"+string(rune('0'+index))) {
			t.Fatalf("handle @img%d missing from the sheet text: %q", index, text)
		}
	}
	if !reply.FreshPictures || spend.Images != 1 || spend.Tokens != 500 {
		t.Fatalf("fresh=%v spend=%+v", reply.FreshPictures, spend)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(mustBase64(t, images[0].URL)))
	if err != nil || decoded.Bounds().Dx() <= 100 {
		t.Fatalf("sheet decodes to %v err=%v", decoded.Bounds(), err)
	}
}

// When the pictures cannot be decoded into a sheet, the place count keeps
// evenly spaced candidates and the overflow note names the rest.
func TestContactSheetFallsBackToSamplingWhenCompositionFails(t *testing.T) {
	schema := visionTestSchema(t, []catalog.ChatVisionRoleSchema{
		visionRole("recent_result", []string{"generated"}, 2, 100, "high"),
	}, 10, 10000)
	schema.Context.MaxResultPictures = 4
	orchestrator := contactSheetOrchestrator(t, schema, []string{"data:image/png;base64,AAAA"})
	budget := orchestrator.visionBudget(TurnOptions{})
	spend := visionSpend{}
	var pictures []ToolPicture
	for _, suffix := range []string{"1", "2", "3", "4", "5", "6"} {
		pictures = append(pictures, ToolPicture{
			Handle: "@img" + suffix, URL: "broken", Fresh: true, Purpose: ToolPictureResult,
		})
	}
	reply := agents.Message{Role: "tool", Content: "six candidates"}
	orchestrator.attachToolPictures(context.Background(), "owner", &reply, pictures, budget, &spend)
	images := imagePartsOf(reply, schema.Context.ImagePartKind)
	if len(images) != 2 {
		t.Fatalf("fallback pictures = %+v; want two evenly spaced", images)
	}
	if !strings.Contains(reply.Content, "@img2") || !strings.Contains(reply.Content, "@img5") {
		t.Fatalf("the overflow note must name the dropped handles: %q", reply.Content)
	}
	if strings.Contains(reply.Content, "@img1") && strings.Contains(reply.Content, "@img6") {
		t.Fatalf("the kept ends must not be named as dropped: %q", reply.Content)
	}
}

// Every tool picture purpose uses its own declared detail and label.
func TestToolPicturesUseTheirPurposeDetailAndLabel(t *testing.T) {
	schema := visionTestSchema(t, []catalog.ChatVisionRoleSchema{
		visionRole("recent_result", []string{"generated"}, 4, 100, "high"),
		visionRole("self_check_render", []string{"render"}, 4, 100, "high"),
		visionRole("history", []string{"history"}, 4, 10, "low"),
	}, 10, 10000)
	orchestrator := contactSheetOrchestrator(t, schema, []string{pngDataURI(t, 40)})
	budget := orchestrator.visionBudget(TurnOptions{})
	spend := visionSpend{}
	reply := agents.Message{Role: "tool", Content: "pictures"}
	orchestrator.attachToolPictures(context.Background(), "owner", &reply, []ToolPicture{
		{Handle: "@img1", URL: "/result", Fresh: true, Purpose: ToolPictureResult},
		{Handle: "@img2", URL: "/render", Purpose: ToolPictureRender},
		{Handle: "@img3", URL: "/history", Purpose: ToolPictureHistory},
		{Handle: "@img4", URL: "/crop", Purpose: ToolPictureCrop, Detail: "crop-detail",
			Label: "[crop @img4 10,10 20×20]"},
	}, budget, &spend)
	images := imagePartsOf(reply, schema.Context.ImagePartKind)
	if len(images) != 4 {
		t.Fatalf("image parts = %+v", images)
	}
	wantDetails := []string{"high", "high", "low", "crop-detail"}
	for index, want := range wantDetails {
		if images[index].Detail != want {
			t.Fatalf("picture %d detail = %q want %q", index, images[index].Detail, want)
		}
	}
	text := replyText(reply)
	if !strings.Contains(text, "[crop @img4 10,10 20×20]") {
		t.Fatalf("a crop's own label must survive: %q", text)
	}
	if !strings.Contains(text, "历史图片 @img3") || !strings.Contains(text, "本回合生成结果 @img1") {
		t.Fatalf("result and history labels = %q", text)
	}
}

// The shared spend stops tool pictures once the request budget is used.
func TestToolPicturesRespectTheRequestBudget(t *testing.T) {
	schema := visionTestSchema(t, []catalog.ChatVisionRoleSchema{
		visionRole("history", []string{"history"}, 4, 10, "low"),
	}, 1, 10000)
	orchestrator := contactSheetOrchestrator(t, schema, []string{pngDataURI(t, 50)})
	budget := orchestrator.visionBudget(TurnOptions{})
	spend := visionSpend{Images: 1}
	reply := agents.Message{Role: "tool", Content: "lookup"}
	orchestrator.attachToolPictures(context.Background(), "owner", &reply, []ToolPicture{
		{Handle: "@img1", URL: "/history", Purpose: ToolPictureHistory},
	}, budget, &spend)
	if len(imagePartsOf(reply, schema.Context.ImagePartKind)) != 0 ||
		!strings.Contains(reply.Content, "@img1") || !strings.Contains(reply.Content, "[dropped @img1]") {
		t.Fatalf("reply = %+v", reply)
	}
}

// A legacy Catalog without context.vision keeps context.max_result_pictures
// as the hard cap of a result's candidates and keeps #245's selection: six
// candidates become four evenly spaced pictures, not all six (#247 review).
func TestLegacyResultPicturesKeepMaxResultPictures(t *testing.T) {
	for name, maximum := range map[string]int{"declared": 4, "undeclared default": 0} {
		t.Run(name, func(t *testing.T) {
			schema := visionTestSchema(t, nil, 10, 10000)
			schema.Context.Vision = nil
			schema.Context.ImageDetail = "low"
			schema.Context.MaxResultPictures = maximum
			orchestrator := visionTestOrchestrator(schema, nil)
			budget := orchestrator.visionBudget(TurnOptions{})
			spend := visionSpend{}
			var pictures []ToolPicture
			for _, suffix := range []string{"a", "b", "c", "d", "e", "f"} {
				pictures = append(pictures, ToolPicture{
					Handle: "@img" + suffix, URL: "/candidate/" + suffix,
					Fresh: true, Purpose: ToolPictureResult,
				})
			}
			reply := agents.Message{Role: "tool", Content: "six candidates"}
			orchestrator.attachToolPictures(context.Background(), "owner", &reply, pictures, budget, &spend)
			images := imagePartsOf(reply, schema.Context.ImagePartKind)
			if len(images) != 4 {
				t.Fatalf("image parts = %d; want the four places max_result_pictures allows", len(images))
			}
			want := []string{
				"data:image/png;base64,_candidate_a", "data:image/png;base64,_candidate_b",
				"data:image/png;base64,_candidate_d", "data:image/png;base64,_candidate_f",
			}
			for index, url := range want {
				if images[index].URL != url || images[index].Detail != "low" {
					t.Fatalf("picture %d = %+v; want %q at image_detail", index, images[index], url)
				}
			}
			if strings.Contains(reply.Content, "data:image/jpeg") {
				t.Fatalf("a legacy Catalog must not compose a contact sheet: %q", reply.Content)
			}
		})
	}
}

// A picture without a handle is named by the Schema's unnamed_picture_label,
// never by a hardcoded word (#247 review).
func TestUnnamedToolPictureUsesTheSchemaLabel(t *testing.T) {
	schema := visionTestSchema(t, []catalog.ChatVisionRoleSchema{
		visionRole("history", []string{"history"}, 1, 10, "low"),
	}, 10, 10000)
	orchestrator := contactSheetOrchestrator(t, schema, []string{pngDataURI(t, 60)})
	budget := orchestrator.visionBudget(TurnOptions{})
	spend := visionSpend{}
	reply := agents.Message{Role: "tool", Content: "lookup"}
	orchestrator.attachToolPictures(context.Background(), "owner", &reply, []ToolPicture{
		{URL: "broken", Purpose: ToolPictureHistory},
		{URL: "broken", Purpose: ToolPictureHistory},
	}, budget, &spend)
	text := replyText(reply)
	if !strings.Contains(text, "picture") {
		t.Fatalf("an unnamed picture must use unnamed_picture_label: %q", text)
	}
	if !strings.Contains(reply.Content, "[dropped picture]") {
		t.Fatalf("the refused unnamed picture must be named by the Schema text: %q", reply.Content)
	}
}

func mustBase64(t *testing.T, dataURI string) []byte {
	t.Helper()
	comma := strings.Index(dataURI, ",")
	if comma < 0 {
		t.Fatalf("not a data URI: %q", dataURI)
	}
	decoded, err := base64.StdEncoding.DecodeString(dataURI[comma+1:])
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}
