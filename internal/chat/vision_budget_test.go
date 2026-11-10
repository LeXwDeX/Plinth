package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// visionTestSchema is the default catalog's chat schema with a compact
// purpose-based budget, so a test states only the roles it exercises.
func visionTestSchema(t *testing.T, roles []catalog.ChatVisionRoleSchema, maximumImages, maximumTokens int) catalog.ChatSchema {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := product.Application().Chat
	schema.Agent.Vision = true
	schema.Context.MaximumVisionImages = 100
	schema.Context.RecentImageLimit = 100
	schema.Context.RecentGeneratedImageLimit = 100
	schema.Context.Vision = &catalog.ChatVisionSchema{
		Roles:                    roles,
		MaximumImages:            maximumImages,
		MaximumTokens:            maximumTokens,
		MaximumBytes:             1 << 30,
		OverflowTemplate:         "\n[dropped {handles}]",
		VisionOffNote:            "\n[vision off]",
		StyleAnchorIntro:         "\n[style]",
		StyleAnchorLabelTemplate: "\n[style {index}]",
		ContactSheet: catalog.ChatVisionContactSheetSchema{
			Enabled: true, MaximumTiles: 10, Columns: 3, TilePixels: 64, LabelHeight: 12,
			JPEGQualities: []int{80}, MaximumBytes: 1 << 20, Detail: "high", TokenCost: 500,
			Intro: "sheet", LabelTemplate: "[{handle}]",
		},
		Crop: catalog.ChatVisionCropSchema{
			Enabled: true, Detail: "high", TokenCost: 500,
			RegionArgument: "region", ShowPictureArgument: "show_picture",
			MinimumSidePixels: 8, MaximumPixels: 1 << 20,
			TooLargeTemplate:     "too large {maximum}",
			PictureLabelTemplate: "\n[crop {handle} {region} {width}x{height}]",
		},
	}
	return schema
}

func visionRole(id string, sources []string, maximum, cost int, detail string) catalog.ChatVisionRoleSchema {
	return catalog.ChatVisionRoleSchema{ID: id, Sources: sources, MaximumImages: maximum, TokenCost: cost, Detail: detail}
}

func visionTestOrchestrator(schema catalog.ChatSchema, inlined map[string]string) *Orchestrator {
	return &Orchestrator{
		schema: schema,
		model:  "test-model",
		visionInline: func(_ context.Context, _ generation.OwnerID, url string) (string, bool) {
			if inlined != nil {
				if value, ok := inlined[url]; ok {
					return value, true
				}
			}
			return "data:image/png;base64," + strings.ReplaceAll(url, "/", "_"), true
		},
		visionAssetInline: func(_ context.Context, _ generation.OwnerID, assetID string) (string, bool) {
			return "data:image/png;base64," + assetID, true
		},
	}
}

// visionHistory is a user's first turn with three pictures, a generated
// result, a render pair, then the current user turn with three pictures.
func visionHistory() []Message {
	renderAttachments := []Attachment{
		{Handle: "@img5", Kind: AttachmentKindImage, Role: AttachmentRoleRender, ImageSlot: 1, TaskID: "t-render"},
		{Handle: "@img6", Kind: AttachmentKindImage, Role: AttachmentRoleRender, ImageSlot: 2, TaskID: "t-render"},
	}
	generatedAttachments := []Attachment{
		{Handle: "@img4", Kind: AttachmentKindImage, Role: AttachmentRoleGenerated, ImageSlot: 1, TaskID: "t-gen"},
	}
	return []Message{
		{ID: "u1", Role: "user", Content: "first", ImageURLs: []string{"/a1", "/a2", "/a3"}},
		{ID: "a1", Role: "assistant", Content: "generated", ImageURLs: []string{"/g1"}, Attachments: generatedAttachments},
		{ID: "a2", Role: "assistant", Content: "renders", ImageURLs: []string{"/r1", "/r2"}, Attachments: renderAttachments},
		{ID: "u2", Role: "user", Content: "now", ImageURLs: []string{"/b1", "/b2", "/b3"}},
	}
}

func planForTest(t *testing.T, orchestrator *Orchestrator, history []Message, options TurnOptions) visionPlan {
	t.Helper()
	indexed, lastUser := orchestrator.indexHistoryImages(history)
	return orchestrator.planVision(context.Background(), "owner", history, indexed, -1, lastUser, options, visionSpend{})
}

func pickedDetails(plan visionPlan) map[string]string {
	result := map[string]string{}
	for _, picks := range plan.Picks {
		for _, pick := range picks {
			result[pick.URL] = pick.Detail
		}
	}
	for _, pick := range plan.Generated {
		result[pick.URL] = pick.Detail
	}
	return result
}

// Each purpose has its own places and detail; one role taking all its places
// does not shrink another role's.
func TestVisionRolesKeepTheirOwnQuotasAndDetails(t *testing.T) {
	schema := visionTestSchema(t, []catalog.ChatVisionRoleSchema{
		visionRole("turn_reference", []string{"turn"}, 2, 100, "high"),
		visionRole("self_check_render", []string{"render"}, 2, 100, "high"),
		visionRole("recent_result", []string{"generated"}, 1, 100, "high"),
		visionRole("history", []string{"history"}, 1, 10, "low"),
	}, 10, 10000)
	orchestrator := visionTestOrchestrator(schema, nil)
	plan := planForTest(t, orchestrator, visionHistory(), TurnOptions{})

	if len(plan.Picks[0]) != 1 || len(plan.Picks[3]) != 2 || len(plan.Generated) != 3 {
		t.Fatalf("places = history %d turn %d assistant %d", len(plan.Picks[0]), len(plan.Picks[3]), len(plan.Generated))
	}
	details := pickedDetails(plan)
	if details["data:image/png;base64,_b2"] != "high" || details["data:image/png;base64,_b3"] != "high" {
		t.Fatalf("the newest turn pictures must win the turn places: %+v", details)
	}
	if details["data:image/png;base64,_a3"] != "low" {
		t.Fatalf("history must stay at its own low detail: %+v", details)
	}
	for _, url := range []string{"data:image/png;base64,_r1", "data:image/png;base64,_r2", "data:image/png;base64,_g1"} {
		if details[url] != "high" {
			t.Fatalf("render/result %s detail = %q", url, details[url])
		}
	}
	// turn 2*100 + render 2*100 + generated 1*100 + history 1*10.
	if plan.Spend.Images != 6 || plan.Spend.Tokens != 510 {
		t.Fatalf("spend = %+v", plan.Spend)
	}
}

// A bound drops the lower-priority roles first and names their handles.
func TestVisionBudgetDropsByDeclaredPriorityWithOverflowNote(t *testing.T) {
	roles := []catalog.ChatVisionRoleSchema{
		visionRole("turn_reference", []string{"turn"}, 2, 100, "high"),
		visionRole("self_check_render", []string{"render"}, 2, 100, "high"),
		visionRole("recent_result", []string{"generated"}, 1, 100, "high"),
		visionRole("history", []string{"history"}, 1, 10, "low"),
	}
	// The image bound leaves one assistant place: the render role comes first
	// and the generated result is dropped.
	orchestrator := visionTestOrchestrator(visionTestSchema(t, roles, 4, 10000), nil)
	plan := planForTest(t, orchestrator, visionHistory(), TurnOptions{})
	if plan.Spend.Images != 4 || len(plan.Generated) != 2 {
		t.Fatalf("spend = %+v generated = %+v", plan.Spend, plan.Generated)
	}
	note := orchestrator.schema.Context.Vision.OverflowTemplate
	note = strings.ReplaceAll(note, "{handles}", strings.Join(plan.Dropped, " "))
	if !strings.Contains(note, "@img4") {
		t.Fatalf("dropped = %v; the generated handle must be named", plan.Dropped)
	}

	// A token bound drops the render pair and the result while the
	// higher-priority turn and the lower-cost history still fit.
	orchestrator = visionTestOrchestrator(visionTestSchema(t, roles, 10, 250), nil)
	plan = planForTest(t, orchestrator, visionHistory(), TurnOptions{})
	details := pickedDetails(plan)
	for _, url := range []string{"data:image/png;base64,_r1", "data:image/png;base64,_r2"} {
		if _, shown := details[url]; shown {
			t.Fatalf("render %s must be dropped by the token bound: %+v", url, details)
		}
	}
	if details["data:image/png;base64,_b3"] != "high" || details["data:image/png;base64,_a3"] != "low" {
		t.Fatalf("higher-priority turn and history must stay: %+v", details)
	}
	if len(plan.Dropped) == 0 {
		t.Fatal("a token bound must name the dropped handles")
	}
}

// The bytes bound refuses a picture whose inlined form would overflow it.
func TestVisionBudgetDropsPicturesOverTheByteBound(t *testing.T) {
	schema := visionTestSchema(t, []catalog.ChatVisionRoleSchema{
		visionRole("turn_reference", []string{"turn"}, 2, 100, "high"),
		visionRole("recent_result", []string{"generated"}, 1, 100, "high"),
		visionRole("history", []string{"history"}, 1, 10, "low"),
	}, 10, 10000)
	schema.Context.Vision.MaximumBytes = 60
	orchestrator := visionTestOrchestrator(schema, nil)
	plan := planForTest(t, orchestrator, visionHistory(), TurnOptions{})
	if plan.Spend.Bytes > 60 || len(plan.Picks[3]) == 0 {
		t.Fatalf("bytes = %d picks = %+v", plan.Spend.Bytes, plan.Picks)
	}
	if len(plan.Dropped) == 0 {
		t.Fatal("the byte bound must name the dropped pictures")
	}
}

// A model profile narrows the budget, and supports_vision=false sends no
// picture at all with the off note.
func TestVisionBudgetModelProfileNarrowsAndDisables(t *testing.T) {
	off := false
	roles := []catalog.ChatVisionRoleSchema{
		visionRole("turn_reference", []string{"turn"}, 2, 100, "high"),
		visionRole("recent_result", []string{"generated"}, 1, 100, "high"),
		visionRole("history", []string{"history"}, 1, 10, "low"),
	}
	schema := visionTestSchema(t, roles, 10, 10000)
	schema.Context.Vision.ModelProfiles = []catalog.ChatVisionModelProfileSchema{
		{ModelPrefixes: []string{"Text-Only"}, SupportsVision: &off},
		{ModelPrefixes: []string{"narrow"}, MaximumImages: 1, MaximumTokens: 100},
	}
	orchestrator := visionTestOrchestrator(schema, nil)
	plan := planForTest(t, orchestrator, visionHistory(), TurnOptions{Model: "text-only-v2"})
	if !plan.Off || len(plan.Picks) != 0 || len(plan.Generated) != 0 {
		t.Fatalf("vision off plan = %+v", plan)
	}
	narrow := planForTest(t, orchestrator, visionHistory(), TurnOptions{Model: "NARROW-mini"})
	if narrow.Spend.Images != 1 || len(narrow.Picks[3]) != 1 {
		t.Fatalf("narrow plan spend = %+v picks = %+v", narrow.Spend, narrow.Picks)
	}
}

// Without a vision block the historical behavior is unchanged: a render takes
// a recent-result place, and every picture uses image_detail.
func TestLegacyVisionTreatsRendersAsRecentResults(t *testing.T) {
	schema := visionTestSchema(t, nil, 10, 10000)
	schema.Context.Vision = nil
	schema.Context.ImageDetail = "low"
	schema.Context.RecentGeneratedImageLimit = 1
	orchestrator := visionTestOrchestrator(schema, nil)
	plan := planForTest(t, orchestrator, visionHistory(), TurnOptions{})
	if len(plan.Generated) != 1 || plan.Generated[0].URL != "data:image/png;base64,_r2" {
		t.Fatalf("legacy assistant pictures = %+v; the newest render takes the single recent-result place", plan.Generated)
	}
	for _, pick := range plan.Generated {
		if pick.Detail != "low" {
			t.Fatalf("legacy detail = %q", pick.Detail)
		}
	}
}

// With the purpose budget, a render no longer takes the generated result's
// place.
func TestSelfCheckRendersDoNotTakeTheRecentResultSlots(t *testing.T) {
	schema := visionTestSchema(t, []catalog.ChatVisionRoleSchema{
		visionRole("self_check_render", []string{"render"}, 4, 100, "high"),
		visionRole("recent_result", []string{"generated"}, 2, 100, "high"),
		visionRole("history", []string{"history"}, 4, 10, "low"),
	}, 10, 10000)
	schema.Context.RecentGeneratedImageLimit = 1
	orchestrator := visionTestOrchestrator(schema, nil)
	plan := planForTest(t, orchestrator, visionHistory(), TurnOptions{})
	shown := map[string]bool{}
	for _, pick := range plan.Generated {
		shown[pick.URL] = true
	}
	if !shown["data:image/png;base64,_g1"] {
		t.Fatalf("the generated result lost its own place to a render: %+v", plan.Generated)
	}
	if !shown["data:image/png;base64,_r1"] || !shown["data:image/png;base64,_r2"] {
		t.Fatalf("the renders must keep their own places: %+v", plan.Generated)
	}
}

// A selected index keeps its historical meaning: only the selected pictures
// are shown, in the declared selection order.
func TestSelectedVisionImagesStayOnTheirOwnRole(t *testing.T) {
	schema := visionTestSchema(t, []catalog.ChatVisionRoleSchema{
		visionRole("turn_reference", []string{"turn"}, 1, 100, "high"),
		visionRole("recent_result", []string{"generated"}, 1, 100, "high"),
		visionRole("history", []string{"history"}, 4, 10, "low"),
	}, 10, 10000)
	orchestrator := visionTestOrchestrator(schema, nil)
	history := visionHistory()
	indexed, lastUser := orchestrator.indexHistoryImages(history)
	plan := orchestrator.planVision(context.Background(), "owner", history, indexed, -1, lastUser,
		TurnOptions{ReferenceImageIndices: []int{1, 7}}, visionSpend{})
	if len(plan.Picks[0]) != 1 || plan.Picks[0][0].URL != "data:image/png;base64,_a1" {
		t.Fatalf("selected picks = %+v", plan.Picks)
	}
	if len(plan.Picks[3]) != 0 {
		t.Fatalf("unselected current pictures leaked in: %+v", plan.Picks[3])
	}
}
