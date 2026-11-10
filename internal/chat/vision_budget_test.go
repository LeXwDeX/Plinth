package chat

import (
	"context"
	"slices"
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
		Roles:                      roles,
		MaximumImages:              maximumImages,
		MaximumTokens:              maximumTokens,
		MaximumBytes:               1 << 30,
		OverflowTemplate:           "\n[dropped {handles}]",
		VisionOffNote:              "\n[vision off]",
		StyleAnchorIntro:           "\n[style]",
		StyleAnchorLabelTemplate:   "\n[style {index}]",
		StyleAnchorDroppedTemplate: "[style dropped {index}]",
		UnnamedPictureLabel:        "picture",
		ContactSheet: catalog.ChatVisionContactSheetSchema{
			Enabled: true, MaximumTiles: 10, Columns: 3, TilePixels: 64, LabelHeight: 12,
			JPEGQualities: []int{80}, MaximumBytes: 1 << 20, Detail: "high", TokenCost: 500,
			Intro: "sheet", LabelTemplate: "[{handle}]",
		},
		Crop: catalog.ChatVisionCropSchema{
			Enabled: true, Detail: "high", TokenCost: 500,
			RegionArgument: "region", ShowPictureArgument: "show_picture",
			MinimumSidePixels: 8, MaximumPixels: 1 << 20,
			TooLargeTemplate:             "too large {maximum}",
			PictureLabelTemplate:         "\n[crop {handle} {region} {width}x{height}]",
			OriginalPictureLabelTemplate: "\n[whole {handle} {width}x{height}]",
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
	return orchestrator.planVision(context.Background(), "owner", "session", history, indexed, -1, lastUser, options, visionSpend{})
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
	plan := orchestrator.planVision(context.Background(), "owner", "session", history, indexed, -1, lastUser,
		TurnOptions{ReferenceImageIndices: []int{1, 7}}, visionSpend{})
	if len(plan.Picks[0]) != 1 || plan.Picks[0][0].URL != "data:image/png;base64,_a1" {
		t.Fatalf("selected picks = %+v", plan.Picks)
	}
	if len(plan.Picks[3]) != 0 {
		t.Fatalf("unselected current pictures leaked in: %+v", plan.Picks[3])
	}
}

// The exported resolvers expose the same declared bounds the wiring uses: the
// token bound (not the picture places) comes from maximum_tokens and is
// narrowed by a matching model profile (#247).
func TestEffectiveVisionBoundsExposeTokensAndImages(t *testing.T) {
	roles := []catalog.ChatVisionRoleSchema{
		visionRole("turn_reference", []string{"turn"}, 2, 100, "high"),
		visionRole("recent_result", []string{"generated"}, 2, 100, "high"),
		visionRole("history", []string{"history"}, 2, 10, "low"),
	}
	schema := visionTestSchema(t, roles, 6, 12000)
	schema.Context.Vision.ModelProfiles = []catalog.ChatVisionModelProfileSchema{
		{ModelPrefixes: []string{"narrow"}, MaximumImages: 3, MaximumTokens: 4000},
	}
	if images, tokens := EffectiveVisionImages(schema, "other-model"), EffectiveVisionTokens(schema, "other-model"); images != 6 || tokens != 12000 {
		t.Fatalf("declared bounds = images %d tokens %d; want 6 and 12000", images, tokens)
	}
	if images, tokens := EffectiveVisionImages(schema, "NARROW-mini"), EffectiveVisionTokens(schema, "NARROW-mini"); images != 3 || tokens != 4000 {
		t.Fatalf("narrowed bounds = images %d tokens %d; want 3 and 4000", images, tokens)
	}
	// The legacy fallback declares no token bound.
	schema.Context.Vision = nil
	if tokens := EffectiveVisionTokens(schema, "other-model"); tokens != 0 {
		t.Fatalf("legacy token bound = %d; want 0", tokens)
	}
}

// A model profile with supports_vision=false reports no budget at all: the
// pictures and the vision tokens are not applicable, not merely narrowed (#247
// review).
func TestEffectiveVisionBoundsAreZeroWhenVisionIsOff(t *testing.T) {
	off := false
	roles := []catalog.ChatVisionRoleSchema{
		visionRole("turn_reference", []string{"turn"}, 2, 100, "high"),
		visionRole("recent_result", []string{"generated"}, 2, 100, "high"),
		visionRole("history", []string{"history"}, 2, 10, "low"),
	}
	schema := visionTestSchema(t, roles, 6, 12000)
	schema.Context.Vision.ModelProfiles = []catalog.ChatVisionModelProfileSchema{
		{ModelPrefixes: []string{"text-only"}, SupportsVision: &off},
	}
	if images, tokens := EffectiveVisionImages(schema, "Text-Only-v2"), EffectiveVisionTokens(schema, "Text-Only-v2"); images != 0 || tokens != 0 {
		t.Fatalf("vision-off bounds = images %d tokens %d; want 0 and 0", images, tokens)
	}
	// The Agent's own switch off reports the same.
	schema.Agent.Vision = false
	if images, tokens := EffectiveVisionImages(schema, "other-model"), EffectiveVisionTokens(schema, "other-model"); images != 0 || tokens != 0 {
		t.Fatalf("agent vision-off bounds = images %d tokens %d; want 0 and 0", images, tokens)
	}
}

// The legacy fallback keeps the historical split: the user pictures spend
// maximum_vision_images while generated and render pictures keep their own
// recent_generated_image_limit and never take an user place (#247 review).
func TestLegacyGeneratedPicturesKeepTheirOwnQuota(t *testing.T) {
	schema := visionTestSchema(t, nil, 10, 10000)
	schema.Context.Vision = nil
	schema.Context.ImageDetail = "low"
	schema.Context.MaximumVisionImages = 3
	schema.Context.RecentImageLimit = 3
	schema.Context.RecentGeneratedImageLimit = 1
	orchestrator := visionTestOrchestrator(schema, nil)
	plan := planForTest(t, orchestrator, visionHistory(), TurnOptions{})
	if len(plan.Picks[3]) != 3 || len(plan.Picks[0]) != 0 {
		t.Fatalf("user picks = %+v; the three newest user pictures take maximum_vision_images", plan.Picks)
	}
	if len(plan.Generated) != 1 || plan.Generated[0].URL != "data:image/png;base64,_r2" {
		t.Fatalf("generated = %+v; the newest render must keep its own recent place", plan.Generated)
	}
	if plan.Spend.Images != 4 {
		t.Fatalf("spend = %+v; generated pictures do not spend the user pool", plan.Spend)
	}
}

// Naming reference indices only narrows the user pictures, as before: the
// generated pictures of the history are still shown (#247 review).
func TestLegacyReferenceIndicesKeepTheGeneratedPictures(t *testing.T) {
	schema := visionTestSchema(t, nil, 10, 10000)
	schema.Context.Vision = nil
	schema.Context.ImageDetail = "low"
	schema.Context.RecentGeneratedImageLimit = 2
	orchestrator := visionTestOrchestrator(schema, nil)
	history := visionHistory()
	indexed, lastUser := orchestrator.indexHistoryImages(history)
	plan := orchestrator.planVision(context.Background(), "owner", "session", history, indexed, -1, lastUser,
		TurnOptions{ReferenceImageIndices: []int{1}}, visionSpend{})
	if len(plan.Picks[3]) != 0 || plan.Picks[0][0].URL != "data:image/png;base64,_a1" {
		t.Fatalf("user picks = %+v; only the named reference is shown", plan.Picks)
	}
	if len(plan.Generated) != 2 || plan.Generated[0].URL != "data:image/png;base64,_r1" ||
		plan.Generated[1].URL != "data:image/png;base64,_r2" {
		t.Fatalf("generated = %+v; the historical scan must survive reference indices", plan.Generated)
	}
}

// #250: an identity anchor may store the canonical Task asset URL of a
// generated result. VisionAnchorInline receives the owner, the session and that
// exact reference; the picture enters at the role's detail. A reference that
// cannot be read is named in the overflow note, never silently skipped.
func TestVisualAnchorTaskAssetURLIsInlinedAtHighDetail(t *testing.T) {
	roles := []catalog.ChatVisionRoleSchema{
		visionRole("style_anchor", []string{"style"}, 2, 50, "high"),
	}
	schema := visionTestSchema(t, roles, 10, 10000)
	reference := "/api/v1/tasks/task-1/assets/gen-1"
	var gotOwner, gotSession, gotReference string
	orchestrator := visionTestOrchestrator(schema, nil)
	orchestrator.visionAnchorInline = func(_ context.Context, owner generation.OwnerID, sessionID, value string) (string, bool) {
		gotOwner, gotSession, gotReference = string(owner), sessionID, value
		return "data:image/png;base64,anchor-task", true
	}
	options := TurnOptions{VisualAnchors: []VisualAnchor{{
		Name: "hero", Kind: "identity", References: []string{reference},
	}}}
	history := []Message{{Role: "user", Content: "draw"}}
	indexed, lastUser := orchestrator.indexHistoryImages(history)
	plan := orchestrator.planVision(context.Background(), "owner", "session", history, indexed, -1, lastUser,
		options, visionSpend{})
	if gotOwner != "owner" || gotSession != "session" || gotReference != reference {
		t.Fatalf("anchor inline got owner=%q session=%q reference=%q", gotOwner, gotSession, gotReference)
	}
	if len(plan.Style) != 1 || plan.Style[0].URL != "data:image/png;base64,anchor-task" ||
		plan.Style[0].Detail != "high" || !strings.Contains(plan.Style[0].Label, "hero") {
		t.Fatalf("anchor style picks = %+v; want the Task URL inlined at high detail", plan.Style)
	}
	if len(plan.Dropped) != 0 {
		t.Fatalf("a readable anchor must not be reported as dropped: %v", plan.Dropped)
	}

	orchestrator.visionAnchorInline = func(context.Context, generation.OwnerID, string, string) (string, bool) {
		return "", false
	}
	plan = orchestrator.planVision(context.Background(), "owner", "session", history, indexed, -1, lastUser,
		options, visionSpend{})
	if len(plan.Style) != 0 {
		t.Fatalf("an unreadable anchor must not spend a place: %+v", plan.Style)
	}
	if !slices.Contains(plan.Dropped, "身份锚「hero」参考图 #1") {
		t.Fatalf("dropped = %v; the unreadable anchor must be named by its handle", plan.Dropped)
	}
}

// #250: an anchor stored as an input asset ID keeps the #247 path; the anchor
// inliner receives the raw ID and the session.
func TestVisualAnchorInputAssetIDUsesTheAnchorInliner(t *testing.T) {
	roles := []catalog.ChatVisionRoleSchema{
		visionRole("style_anchor", []string{"style"}, 2, 50, "high"),
	}
	schema := visionTestSchema(t, roles, 10, 10000)
	var gotReference, gotSession string
	orchestrator := visionTestOrchestrator(schema, nil)
	orchestrator.visionAnchorInline = func(_ context.Context, _ generation.OwnerID, sessionID, value string) (string, bool) {
		gotReference, gotSession = value, sessionID
		return "data:image/png;base64,anchor-asset", true
	}
	options := TurnOptions{VisualAnchors: []VisualAnchor{{
		Name: "hero", Kind: "identity", References: []string{"asset-1"},
	}}}
	history := []Message{{Role: "user", Content: "draw"}}
	indexed, lastUser := orchestrator.indexHistoryImages(history)
	plan := orchestrator.planVision(context.Background(), "owner", "session", history, indexed, -1, lastUser,
		options, visionSpend{})
	if gotReference != "asset-1" || gotSession != "session" || len(plan.Style) != 1 ||
		plan.Style[0].URL != "data:image/png;base64,anchor-asset" {
		t.Fatalf("reference=%q session=%q style=%+v", gotReference, gotSession, plan.Style)
	}
}

// #250: without the anchor inliner an anchor picture is still named in the
// overflow note; the style lock's own silence is unchanged.
func TestVisualAnchorWithoutInlinerIsNamedInTheOverflow(t *testing.T) {
	roles := []catalog.ChatVisionRoleSchema{
		visionRole("style_anchor", []string{"style"}, 2, 50, "high"),
	}
	schema := visionTestSchema(t, roles, 10, 10000)
	orchestrator := visionTestOrchestrator(schema, nil)
	orchestrator.visionAnchorInline = nil
	options := TurnOptions{
		StyleLock: StyleLock{Enabled: true, References: []string{"lock-1"}},
		VisualAnchors: []VisualAnchor{{
			Name: "hero", Kind: "identity", References: []string{"asset-1"},
		}},
	}
	history := []Message{{Role: "user", Content: "draw"}}
	indexed, lastUser := orchestrator.indexHistoryImages(history)
	plan := orchestrator.planVision(context.Background(), "owner", "session", history, indexed, -1, lastUser,
		options, visionSpend{})
	if len(plan.Style) != 1 || plan.Style[0].URL != "data:image/png;base64,lock-1" {
		t.Fatalf("style picks = %+v; the readable lock picture keeps its place", plan.Style)
	}
	if !slices.Contains(plan.Dropped, "身份锚「hero」参考图 #1") {
		t.Fatalf("dropped = %v; the anchor must be named", plan.Dropped)
	}
	if slices.Contains(plan.Dropped, "[style dropped 1]") {
		t.Fatalf("dropped = %v; the readable style lock is not dropped", plan.Dropped)
	}
}

// The style anchor role spends its own places at its own detail, is inlined
// through VisionAssetInline, and names a refused reference with the Schema's
// style_anchor_dropped_template (#247 review).
func TestStyleAnchorRoleUsesItsQuotaDetailAndSchemaText(t *testing.T) {
	roles := []catalog.ChatVisionRoleSchema{
		visionRole("turn_reference", []string{"turn"}, 1, 100, "high"),
		visionRole("style_anchor", []string{"style"}, 2, 50, "low"),
		visionRole("recent_result", []string{"generated"}, 1, 100, "high"),
		visionRole("history", []string{"history"}, 1, 10, "low"),
	}
	schema := visionTestSchema(t, roles, 10, 10000)
	orchestrator := visionTestOrchestrator(schema, nil)
	options := TurnOptions{StyleLock: StyleLock{
		Enabled: true, References: []string{"asset-1", "asset-2", "asset-3"},
	}}
	history := visionHistory()
	indexed, lastUser := orchestrator.indexHistoryImages(history)
	plan := orchestrator.planVision(context.Background(), "owner", "session", history, indexed, -1, lastUser,
		options, visionSpend{})
	if len(plan.Style) != 2 {
		t.Fatalf("style picks = %+v; the role's two places bound the lock's three references", plan.Style)
	}
	for index, wantURL := range []string{"data:image/png;base64,asset-1", "data:image/png;base64,asset-2"} {
		pick := plan.Style[index]
		if pick.URL != wantURL || pick.Detail != "low" || pick.RoleID != "style_anchor" {
			t.Fatalf("style pick %d = %+v", index, pick)
		}
	}
	if !slices.Contains(plan.Dropped, "[style dropped 3]") {
		t.Fatalf("dropped = %v; the refused reference must use the Schema text", plan.Dropped)
	}
	for index, wantLabel := range []string{"\n[style 1]", "\n[style 2]"} {
		if plan.Style[index].Label != wantLabel {
			t.Fatalf("style label %d = %q; want %q", index, plan.Style[index].Label, wantLabel)
		}
	}
	parts := orchestrator.plannedStyleParts(plan)
	if len(parts) != 5 || parts[0].Text != "\n[style]" {
		t.Fatalf("style parts = %+v; want an intro and two labelled pictures", parts)
	}
	// Without the asset inliner the lock cannot be read: the references are
	// skipped without spending a place, and every other role keeps its own.
	orchestrator.visionAssetInline = nil
	plan = orchestrator.planVision(context.Background(), "owner", "session", history, indexed, -1, lastUser,
		options, visionSpend{})
	if len(plan.Style) != 0 || plan.Spend.Images != 3 {
		t.Fatalf("plan without inliner = style %+v spend %+v", plan.Style, plan.Spend)
	}
}
