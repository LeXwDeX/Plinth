package imagereview

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// pngOf encodes a picture painted pixel by pixel.
func pngOf(t *testing.T, width, height int, paint func(x, y int) color.NRGBA) []byte {
	t.Helper()
	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			picture.SetNRGBA(x, y, paint(x, y))
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, picture); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// shipped builds the review service from the shipped Catalog, the way the
// application wires it, so the thresholds, hints and model names under test are
// the product's own.
func shipped(t *testing.T, data []byte) (*Service, catalog.ChatSchema) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := product.Application().Chat
	service, err := New(Config{
		Context: schema.Context, Review: schema.ImageReview, Inputs: fakeInputs{data: data},
		Usage: schema.Image2DUsage, Brief: schema.Model3DUsage.Brief,
		Offerings: func(id string) (generation.Offering, bool) { return product.Get(generation.OfferingID(id)) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, schema
}

func briefOf(t *testing.T, fields map[string]any) chat.ProductionBrief {
	t.Helper()
	brief := chat.ProductionBrief{}
	for name, value := range fields {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		brief[name] = raw
	}
	return brief
}

type usagePayload struct {
	UsageCheck struct {
		Checklist     string `json:"checklist"`
		Purpose       string `json:"purpose"`
		PurposeLabel  string `json:"purpose_label"`
		PurposeSource string `json:"purpose_source"`
		Note          string `json:"note"`
		Checks        []struct {
			Check    string `json:"check"`
			Status   string `json:"status"`
			Actual   string `json:"actual"`
			Expected string `json:"expected"`
			Fix      string `json:"fix"`
			Hint     string `json:"hint"`
		} `json:"checks"`
	} `json:"usage_check"`
	Report struct {
		ShortEdge int `json:"short_edge"`
		Tiling    *struct {
			LeftRight []json.RawMessage `json:"left_right"`
		} `json:"tiling"`
		EdgeFringe *struct {
			Measured bool `json:"measured"`
		} `json:"edge_fringe"`
	} `json:"report"`
}

// inspectWith runs one inspect_image call with a production brief and returns
// its payload and persisted summary.
func inspectWith(t *testing.T, service *Service, arguments string, brief chat.ProductionBrief) (usagePayload, string, chat.ToolResult) {
	t.Helper()
	cmd := command(arguments, nil)
	cmd.Options.ProductionBrief = brief
	result, err := service.Inspect(context.Background(), cmd)
	if err != nil || result.Error != "" {
		t.Fatalf("result = %+v err=%v", result, err)
	}
	var payload usagePayload
	if err := json.Unmarshal([]byte(result.AgentContent), &payload); err != nil {
		t.Fatalf("content = %s: %v", result.AgentContent, err)
	}
	return payload, string(result.Summary), result
}

func (payload usagePayload) check(t *testing.T, name string) (status, actual, hint, fix string) {
	t.Helper()
	for _, check := range payload.UsageCheck.Checks {
		if check.Check == name {
			return check.Status, check.Actual, check.Hint, check.Fix
		}
	}
	t.Fatalf("no %q check in %+v", name, payload.UsageCheck.Checks)
	return
}

func (payload usagePayload) has(name string) bool {
	for _, check := range payload.UsageCheck.Checks {
		if check.Check == name {
			return true
		}
	}
	return false
}

// subjectPicture is a plain light picture with one dark rectangle in the middle:
// it passes the subject checks of the delivery checklist at any size, so what a
// test sees fail is the purpose check under test.
func subjectPicture(width, height int) func(x, y int) color.NRGBA {
	return func(x, y int) color.NRGBA {
		if x > width/4 && x < width*3/4 && y > height/4 && y < height*3/4 {
			return color.NRGBA{R: 52, G: 58, B: 70, A: 255}
		}
		return color.NRGBA{R: 250, G: 250, B: 250, A: 255}
	}
}

// #282: a stand-up illustration needs a 1536-pixel short edge. A picture below
// it fails with the Schema's next step, which names the models that can reach
// it and does not switch the model; a picture at it passes.
func TestDeliveryChecklistFailsAPictureBelowThePurposeMinimum(t *testing.T) {
	service, _ := shipped(t, pngOf(t, 128, 128, subjectPicture(128, 128)))
	brief := briefOf(t, map[string]any{"image_use": "illustration"})
	payload, summary, result := inspectWith(t, service, `{"image":"@img1"}`, brief)

	if payload.UsageCheck.Purpose != "illustration" || payload.UsageCheck.PurposeSource != "production_brief" ||
		payload.UsageCheck.PurposeLabel != "角色立绘 / 宣传插画" ||
		!strings.Contains(payload.UsageCheck.Note, "角色立绘 / 宣传插画") {
		t.Fatalf("purpose = %+v", payload.UsageCheck)
	}
	status, actual, hint, fix := payload.check(t, "short_edge")
	if status != "fail" || fix != "raise_resolution" || !strings.Contains(actual, "128 px") {
		t.Fatalf("short_edge = %s %q fix=%s", status, actual, fix)
	}
	// The next step names a model that can reach 1536 and warns against a silent
	// switch; Nano Banana has no resolution declaration and is not named.
	for _, want := range []string{"1536", "Gemini 3 Pro", "不要静默换模型", "角色立绘 / 宣传插画"} {
		if !strings.Contains(hint, want) {
			t.Fatalf("hint omits %q: %s", want, hint)
		}
	}
	if strings.Contains(hint, "Nano Banana") || strings.Contains(hint, "{") {
		t.Fatalf("hint names an unproven model or leaks a placeholder: %s", hint)
	}
	if payload.Report.ShortEdge != 128 {
		t.Fatalf("report short edge = %d", payload.Report.ShortEdge)
	}
	// The persisted summary keeps what failed, for the next turn's ledger.
	for _, want := range []string{`"failed":1`, `"check":"short_edge"`, `"fix":"raise_resolution"`} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary omits %s: %s", want, summary)
		}
	}
	if result.NoSubmission != true {
		t.Fatalf("an inspection submitted something: %+v", result)
	}

	big, _ := shipped(t, pngOf(t, 1536, 2048, subjectPicture(1536, 2048)))
	payload, summary, _ = inspectWith(t, big, `{"image":"@img1"}`, brief)
	if status, _, hint, _ := payload.check(t, "short_edge"); status != "pass" || hint != "" {
		t.Fatalf("a picture at the minimum failed: %s %q", status, hint)
	}
	if strings.Contains(summary, "short_edge") {
		t.Fatalf("a passed check was kept as a leftover: %s", summary)
	}
}

// With no purpose in the brief the Schema's default purpose judges the picture,
// and the result says so; a stale purpose id counts as no purpose.
func TestDeliveryChecklistFallsBackToTheDefaultPurposeAndSaysSo(t *testing.T) {
	service, schema := shipped(t, pngOf(t, 1100, 1100, subjectPicture(1100, 1100)))
	for name, brief := range map[string]chat.ProductionBrief{
		"empty brief":     nil,
		"stale purpose":   briefOf(t, map[string]any{"image_use": "retired_purpose"}),
		"only a 3D brief": briefOf(t, map[string]any{"purpose": "web_ar"}),
	} {
		payload, _, _ := inspectWith(t, service, `{"image":"@img1"}`, brief)
		if payload.UsageCheck.Purpose != schema.Image2DUsage.DefaultProfile ||
			payload.UsageCheck.PurposeSource != "default_profile" ||
			!strings.Contains(payload.UsageCheck.Note, "没有写 2D 用途") {
			t.Fatalf("%s: purpose = %+v", name, payload.UsageCheck)
		}
		// 1100 reaches the default purpose's 1024.
		if status, _, _, _ := payload.check(t, "short_edge"); status != "pass" {
			t.Fatalf("%s: short_edge = %s", name, status)
		}
	}
}

// The 3D input checklist keeps its own gate, and a region crop is a part of the
// picture: neither is judged against the purpose.
func TestPurposeChecksApplyOnlyToTheWholeDeliveryPicture(t *testing.T) {
	service, _ := shipped(t, pngOf(t, 128, 128, subjectPicture(128, 128)))
	brief := briefOf(t, map[string]any{"image_use": "illustration"})

	payload, _, _ := inspectWith(t, service, `{"image":"@img1","checklist":"3d_input"}`, brief)
	if payload.UsageCheck.Purpose != "" || payload.has("short_edge") {
		t.Fatalf("the 3D input checklist was judged against a 2D purpose: %+v", payload.UsageCheck)
	}

	cmd := command(`{"image":"@img1","region":{"x":0,"y":0,"width":0.5,"height":0.5}}`, nil)
	cmd.Options.ProductionBrief = brief
	result, err := service.Inspect(context.Background(), cmd)
	if err != nil || result.Error != "" {
		t.Fatalf("region result = %+v err=%v", result, err)
	}
	var cropped usagePayload
	if err := json.Unmarshal([]byte(result.AgentContent), &cropped); err != nil {
		t.Fatal(err)
	}
	if cropped.UsageCheck.Purpose != "" || cropped.has("short_edge") {
		t.Fatalf("a crop was judged against the purpose: %+v", cropped.UsageCheck)
	}
}

// periodicTexture is continuous across its edges: its period divides the size.
func periodicTexture(x, y int) color.NRGBA {
	return color.NRGBA{
		R: uint8(128 + 100*math.Sin(2*math.Pi*float64(x)/64)),
		G: uint8(128 + 100*math.Sin(2*math.Pi*float64(y)/64)),
		B: uint8(128 + 60*math.Sin(2*math.Pi*float64(x+y)/64)),
		A: 255,
	}
}

// #287: under the texture purpose a seamless texture passes and one whose edges
// do not meet is flagged with the position of the break and the next step.
func TestTexturePurposeJudgesSeamlessTiling(t *testing.T) {
	brief := briefOf(t, map[string]any{"image_use": "texture_asset"})

	tileable, _ := shipped(t, pngOf(t, 1024, 1024, periodicTexture))
	payload, _, _ := inspectWith(t, tileable, `{"image":"@img1"}`, brief)
	if status, actual, _, _ := payload.check(t, "seamless"); status != "pass" {
		t.Fatalf("a seamless texture was flagged: %s %q", status, actual)
	}
	if payload.Report.Tiling == nil || len(payload.Report.Tiling.LeftRight) != 8 {
		t.Fatalf("the seam measurement is not in the report: %+v", payload.Report.Tiling)
	}

	// The last column of rows 256..767 is painted white: a step on part of the
	// left/right seam only.
	seamed, _ := shipped(t, pngOf(t, 1024, 1024, func(x, y int) color.NRGBA {
		if x == 1023 && y >= 256 && y < 768 {
			return color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		}
		return periodicTexture(x, y)
	}))
	payload, summary, _ := inspectWith(t, seamed, `{"image":"@img1"}`, brief)
	status, actual, hint, fix := payload.check(t, "seamless")
	if status != "fail" || fix != "regenerate_tileable" || !strings.Contains(actual, "left/right seam at y 256-767") {
		t.Fatalf("seamless = %s %q fix=%s", status, actual, fix)
	}
	for _, want := range []string{"seamless tileable texture", "region", actual} {
		if !strings.Contains(hint, want) {
			t.Fatalf("hint omits %q: %s", want, hint)
		}
	}
	if !strings.Contains(summary, `"check":"seamless"`) {
		t.Fatalf("summary omits the failed check: %s", summary)
	}
	// No other purpose measures seams.
	payload, _, _ = inspectWith(t, seamed, `{"image":"@img1"}`, briefOf(t, map[string]any{"image_use": "concept_art"}))
	if payload.has("seamless") || payload.Report.Tiling != nil {
		t.Fatalf("a purpose that does not tile measured seams: %+v", payload.UsageCheck)
	}
}

func cutout(rim bool) func(x, y int) color.NRGBA {
	dark := color.NRGBA{R: 60, G: 20, B: 20, A: 255}
	return func(x, y int) color.NRGBA {
		distance := math.Hypot(float64(x)+0.5-512, float64(y)+0.5-512)
		switch {
		case distance <= 300:
			return dark
		case rim && distance <= 302:
			return color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		case !rim && distance <= 302:
			edge := dark
			edge.A = uint8(255 * (302 - distance) / 2)
			return edge
		}
		return color.NRGBA{}
	}
}

// #284: under a purpose that takes a transparent background the delivery
// checklist reports the alpha channel and the edge. A white rim is flagged, a
// clean edge passes, and an opaque picture is judged by its background.
func TestIconPurposeJudgesAlphaAndEdge(t *testing.T) {
	brief := briefOf(t, map[string]any{"image_use": "icon_prop"})

	rimmed, _ := shipped(t, pngOf(t, 1024, 1024, cutout(true)))
	payload, _, _ := inspectWith(t, rimmed, `{"image":"@img1"}`, brief)
	if status, _, _, _ := payload.check(t, "alpha"); status != "pass" {
		t.Fatalf("a real cut-out failed the alpha check: %s", status)
	}
	status, actual, hint, fix := payload.check(t, "edge_fringe")
	if status != "fail" || fix != "clean_edges" || !strings.Contains(actual, "light rim") ||
		!strings.Contains(hint, "remove_background") {
		t.Fatalf("edge_fringe = %s %q hint=%q fix=%s", status, actual, hint, fix)
	}
	if payload.Report.EdgeFringe == nil || !payload.Report.EdgeFringe.Measured {
		t.Fatalf("the edge measurement is not in the report: %+v", payload.Report.EdgeFringe)
	}

	clean, _ := shipped(t, pngOf(t, 1024, 1024, cutout(false)))
	payload, _, _ = inspectWith(t, clean, `{"image":"@img1"}`, brief)
	if status, actual, _, _ := payload.check(t, "edge_fringe"); status != "pass" {
		t.Fatalf("a clean edge was flagged: %s %q", status, actual)
	}

	// An opaque icon on a plain background is acceptable to the purpose (solid
	// or transparent): the alpha item passes on the plain background and there
	// is no edge to judge.
	opaque, _ := shipped(t, pngOf(t, 1024, 1024, func(x, y int) color.NRGBA {
		if x >= 300 && x < 700 && y >= 300 && y < 700 {
			return color.NRGBA{R: 60, G: 20, B: 20, A: 255}
		}
		return color.NRGBA{R: 250, G: 250, B: 250, A: 255}
	}))
	payload, _, _ = inspectWith(t, opaque, `{"image":"@img1"}`, brief)
	if status, _, _, _ := payload.check(t, "alpha"); status != "pass" {
		t.Fatalf("a plain solid background was refused by a solid-or-transparent purpose: %s", status)
	}
	if status, _, _, _ := payload.check(t, "edge_fringe"); status != "unknown" {
		t.Fatalf("an opaque picture had its edge judged: %s", status)
	}

	// The same opaque picture fails once the person asked for transparency:
	// the brief flag turns the alpha check strict, whatever the purpose takes.
	strict := briefOf(t, map[string]any{"image_use": "icon_prop", "image_transparent": true})
	payload, summary, _ := inspectWith(t, opaque, `{"image":"@img1"}`, strict)
	status, actual, hint, fix = payload.check(t, "alpha")
	if status != "fail" || fix != "remove_background" || !strings.Contains(hint, "remove_background") ||
		!strings.Contains(actual, "alpha=false") {
		t.Fatalf("alpha = %s %q hint=%q fix=%s", status, actual, hint, fix)
	}
	// An unknown check is counted apart, never as a pass.
	if !strings.Contains(summary, `"unknown":1`) || !strings.Contains(summary, `"check":"alpha"`) {
		t.Fatalf("summary = %s", summary)
	}
	// A purpose that does not take transparency does not judge it.
	payload, _, _ = inspectWith(t, opaque, `{"image":"@img1"}`, briefOf(t, map[string]any{"image_use": "illustration"}))
	if payload.has("alpha") || payload.has("edge_fringe") {
		t.Fatalf("an opaque purpose judged transparency: %+v", payload.UsageCheck.Checks)
	}
}

// A Catalog without the purpose thresholds keeps the historical delivery
// checklist: the service must not invent a purpose.
func TestDeliveryChecklistIsUnchangedWithoutPurposeChecks(t *testing.T) {
	service := service(t, fakeInputs{data: picture(t)}, nil)
	cmd := command(`{"image":"@img1"}`, nil)
	cmd.Options.ProductionBrief = briefOf(t, map[string]any{"image_use": "illustration"})
	result, err := service.Inspect(context.Background(), cmd)
	if err != nil || result.Error != "" {
		t.Fatalf("result = %+v err=%v", result, err)
	}
	var payload usagePayload
	if err := json.Unmarshal([]byte(result.AgentContent), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.UsageCheck.Purpose != "" || payload.has("short_edge") || payload.Report.Tiling != nil {
		t.Fatalf("purpose checks appeared without a Schema: %+v", payload.UsageCheck)
	}
}

// A model the runtime cannot run is never named as the fix: the application
// lookup leaves unavailable Offerings out, and with none left the hint says so
// instead of sending the Agent to a model that would be refused (#291 review).
func TestResolutionHintNamesOnlyRunnableModels(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := product.Application().Chat
	service, err := New(Config{
		Context: schema.Context, Review: schema.ImageReview,
		Inputs: fakeInputs{data: pngOf(t, 128, 128, subjectPicture(128, 128))},
		Usage:  schema.Image2DUsage, Brief: schema.Model3DUsage.Brief,
		Offerings: func(string) (generation.Offering, bool) { return generation.Offering{}, false },
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, _, _ := inspectWith(t, service, `{"image":"@img1"}`, briefOf(t, map[string]any{"image_use": "illustration"}))
	status, _, hint, _ := payload.check(t, "short_edge")
	if status != "fail" || strings.Contains(hint, "Gemini") || !strings.Contains(hint, schema.ImageReview.Usage.NoModelsText) {
		t.Fatalf("short_edge = %s, hint %q", status, hint)
	}
}
