package generationtool_test

import (
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func anchor(name, kind string, references ...string) chat.VisualAnchor {
	return chat.VisualAnchor{ID: "anchor-" + name, Name: name, Kind: kind, References: references}
}

// #250: a call that names an identity anchor sends the anchor's pictures
// before the style lock's pictures, which in turn precede the turn's own
// references. The server states what each picture is for.
func TestIdentityAnchorReferencesPrecedeStyleAndTurnReferences(t *testing.T) {
	rig := newStyleLockRig(t, 4)
	options := styleLock(true, "ink wash", "style-a")
	options.VisualAnchors = []chat.VisualAnchor{anchor("hero", chat.VisualAnchorKindIdentity, "hero-a", "hero-b")}
	result, input := rig.run(t, options, imageHistory(),
		`{"prompt":"portrait","reference_handles":["@img1"],"anchors":["hero"]}`)
	if result.NoSubmission {
		t.Fatalf("result=%+v error=%q", result, result.Error)
	}
	want := []string{
		"/api/v1/input-assets/hero-a",
		"/api/v1/input-assets/hero-b",
		"/api/v1/input-assets/style-a",
		"/api/v1/input-assets/agent-image",
	}
	if strings.Join(rig.resolver.urls, ",") != strings.Join(want, ",") {
		t.Fatalf("reference order=%v, want %v", rig.resolver.urls, want)
	}
	prompt, _ := input["prompt"].(string)
	for _, purpose := range []string{
		"image 1: 保留「hero」的身份、外形、服装与配色",
		"image 2: 保留「hero」的身份、外形、服装与配色",
		"image 3: 沿用用户风格锁的画风",
	} {
		if !strings.Contains(prompt, purpose) {
			t.Fatalf("prompt missing %q: %q", purpose, prompt)
		}
	}
}

// #250: the model limit keeps the anchors and the style picture and drops the
// ordinary reference; the result names the dropped handle.
func TestAnchorAndStyleReferencesOutrankTheTurnLimit(t *testing.T) {
	rig := newStyleLockRig(t, 2)
	options := styleLock(true, "ink wash", "style-a")
	options.VisualAnchors = []chat.VisualAnchor{anchor("hero", chat.VisualAnchorKindIdentity, "hero-a")}
	result, _ := rig.run(t, options, imageHistory(),
		`{"prompt":"recolor","reference_handles":["@img1"],"anchors":["hero"]}`)
	if result.NoSubmission {
		t.Fatalf("result=%+v error=%q", result, result.Error)
	}
	want := []string{"/api/v1/input-assets/hero-a", "/api/v1/input-assets/style-a"}
	if strings.Join(rig.resolver.urls, ",") != strings.Join(want, ",") {
		t.Fatalf("reference order=%v, want %v", rig.resolver.urls, want)
	}
	if !strings.Contains(result.AgentContent, "丢弃了普通参考 @img1") {
		t.Fatalf("dropped-reference note missing: %s", result.AgentContent)
	}
}

// #250: a shared picture is sent once, and the identity class keeps the first
// position. The ordinary reference is not counted again.
func TestAnchorReferencesAreDeduplicatedAgainstTurnReferences(t *testing.T) {
	rig := newStyleLockRig(t, 3)
	options := styleLock(true, "ink wash", "style-a")
	options.VisualAnchors = []chat.VisualAnchor{anchor("hero", chat.VisualAnchorKindIdentity, "agent-image")}
	result, _ := rig.run(t, options, imageHistory(),
		`{"prompt":"recolor","reference_handles":["@img1"],"anchors":["hero"]}`)
	if result.NoSubmission {
		t.Fatalf("result=%+v error=%q", result, result.Error)
	}
	want := []string{"/api/v1/input-assets/agent-image", "/api/v1/input-assets/style-a"}
	if strings.Join(rig.resolver.urls, ",") != strings.Join(want, ",") {
		t.Fatalf("reference order=%v, want %v", rig.resolver.urls, want)
	}
	if strings.Contains(result.AgentContent, "reference_note") {
		t.Fatalf("a deduplicated reference must not be reported as dropped: %s", result.AgentContent)
	}
}

// #250 review: the artboard snapshot must stay the first reference picture
// (#266) even when the turn has identity and style pictures. The priority
// order applies after the required turn input.
func TestCanvasSnapshotLeadsAnchorAndStyleReferences(t *testing.T) {
	rig := newStyleLockRig(t, 4)
	options := styleLock(true, "ink wash", "style-a")
	options.VisualAnchors = []chat.VisualAnchor{anchor("hero", chat.VisualAnchorKindIdentity, "hero-a")}
	options.InputSource = rig.app.Chat.Context.CanvasInputSource
	options.Selection = []chat.Attachment{{
		Handle: "@img9", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleCanvas,
		AssetID: "board", URL: "/api/v1/input-assets/board",
		Canvas: &chat.CanvasLayout{Width: 1024, Height: 1024},
	}}
	result, input := rig.run(t, options, imageHistory(),
		`{"prompt":"repaint the board","reference_handles":["@img1"],"anchors":["hero"]}`)
	if result.NoSubmission {
		t.Fatalf("result=%+v error=%q", result, result.Error)
	}
	want := []string{
		"/api/v1/input-assets/board",
		"/api/v1/input-assets/hero-a",
		"/api/v1/input-assets/style-a",
		"/api/v1/input-assets/agent-image",
	}
	if strings.Join(rig.resolver.urls, ",") != strings.Join(want, ",") {
		t.Fatalf("reference order=%v, want %v", rig.resolver.urls, want)
	}
	prompt, _ := input["prompt"].(string)
	if !strings.Contains(prompt, "image 2: 保留「hero」的身份、外形、服装与配色") ||
		!strings.Contains(prompt, "image 3: 沿用用户风格锁的画风") {
		t.Fatalf("purposes are numbered against the final order: %q", prompt)
	}
}

// #250 review: a picture the person marked "input" keeps the front even when a
// style lock picture exists; the old code skipped the reorder whenever any
// style reference was present.
func TestInputPurposeLeadsTheListBesideStyleReferences(t *testing.T) {
	rig := newStyleLockRig(t, 4)
	options := styleLock(true, "ink wash", "style-a")
	options.AttachmentPurposes = map[string]string{"@img1": "input"}
	result, _ := rig.run(t, options, imageHistory(), `{"prompt":"repaint","reference_handles":["@img1"]}`)
	if result.NoSubmission {
		t.Fatalf("result=%+v error=%q", result, result.Error)
	}
	want := []string{"/api/v1/input-assets/agent-image", "/api/v1/input-assets/style-a"}
	if strings.Join(rig.resolver.urls, ",") != strings.Join(want, ",") {
		t.Fatalf("reference order=%v, want %v", rig.resolver.urls, want)
	}
}

// #250 review: the model limit keeps the required input and the identity
// anchor, drops the style picture and the ordinary reference, and names the
// dropped ordinary handle.
func TestCanvasAndAnchorReferencesOutrankTheTurnLimit(t *testing.T) {
	rig := newStyleLockRig(t, 2)
	options := styleLock(true, "ink wash", "style-a")
	options.VisualAnchors = []chat.VisualAnchor{anchor("hero", chat.VisualAnchorKindIdentity, "hero-a")}
	options.InputSource = rig.app.Chat.Context.CanvasInputSource
	options.Selection = []chat.Attachment{{
		Handle: "@img9", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleCanvas,
		AssetID: "board", URL: "/api/v1/input-assets/board",
		Canvas: &chat.CanvasLayout{Width: 1024, Height: 1024},
	}}
	result, _ := rig.run(t, options, imageHistory(),
		`{"prompt":"repaint the board","reference_handles":["@img1"],"anchors":["hero"]}`)
	if result.NoSubmission {
		t.Fatalf("result=%+v error=%q", result, result.Error)
	}
	want := []string{"/api/v1/input-assets/board", "/api/v1/input-assets/hero-a"}
	if strings.Join(rig.resolver.urls, ",") != strings.Join(want, ",") {
		t.Fatalf("reference order=%v, want %v", rig.resolver.urls, want)
	}
	if !strings.Contains(result.AgentContent, "丢弃了普通参考 @img1") {
		t.Fatalf("dropped-reference note missing: %s", result.AgentContent)
	}
}

// #250: a style anchor is attached by the server without being named in the
// anchor argument (the argument only accepts identity anchors), and it
// outranks the turn's ordinary references when the model takes fewer pictures.
func TestStyleAnchorReferencesAreAttachedWithoutBeingNamed(t *testing.T) {
	rig := newStyleLockRig(t, 1)
	options := styleLock(false, "")
	options.VisualAnchors = []chat.VisualAnchor{anchor("mood", chat.VisualAnchorKindStyle, "mood-a", "mood-b")}
	result, input := rig.run(t, options, imageHistory(),
		`{"prompt":"portrait","reference_handles":["@img1"]}`)
	if result.NoSubmission {
		t.Fatalf("result=%+v error=%q", result, result.Error)
	}
	// The limit is one: the style anchor's first picture keeps it and the
	// ordinary reference is dropped, named by its handle.
	if got, want := rig.resolver.urls, []string{"/api/v1/input-assets/mood-a"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("reference order=%v, want %v", got, want)
	}
	prompt, _ := input["prompt"].(string)
	if !strings.Contains(prompt, "沿用「mood」的画风、材质与笔触") {
		t.Fatalf("the style anchor purpose is missing: %q", prompt)
	}
	if !strings.Contains(result.AgentContent, "丢弃了普通参考 @img1") {
		t.Fatalf("dropped-reference note missing: %s", result.AgentContent)
	}
	if !strings.Contains(result.AgentContent, "风格参考 风格锚「mood」参考图 #2") {
		t.Fatalf("the cut style picture is not named: %s", result.AgentContent)
	}
}

// #250: a style anchor's pictures are attached without a name and both
// precede the turn's own ordinary reference; the style lock keeps its
// historical place at the front of the style class.
func TestStyleAnchorReferencesPrecedeTheTurnReferences(t *testing.T) {
	rig := newStyleLockRig(t, 4)
	options := styleLock(true, "ink wash", "style-a")
	options.VisualAnchors = []chat.VisualAnchor{anchor("mood", chat.VisualAnchorKindStyle, "mood-a", "mood-b")}
	result, _ := rig.run(t, options, imageHistory(),
		`{"prompt":"portrait","reference_handles":["@img1"]}`)
	if result.NoSubmission {
		t.Fatalf("result=%+v error=%q", result, result.Error)
	}
	want := []string{
		"/api/v1/input-assets/style-a",
		"/api/v1/input-assets/mood-a",
		"/api/v1/input-assets/mood-b",
		"/api/v1/input-assets/agent-image",
	}
	if strings.Join(rig.resolver.urls, ",") != strings.Join(want, ",") {
		t.Fatalf("reference order=%v, want %v", rig.resolver.urls, want)
	}
}

// #250 review: an anchor picture that was deleted or expired refuses this step
// with the anchor's handle and the Schema wording, not a generic generation
// failure, and nothing is submitted.
func TestUnavailableAnchorReferenceIsRefusedWithItsHandle(t *testing.T) {
	rig := newStyleLockRig(t, 3)
	reference := "/api/v1/tasks/task-1/assets/gone-1"
	rig.resolver.fail = map[string]bool{reference: true}
	options := styleLock(false, "")
	options.VisualAnchors = []chat.VisualAnchor{anchor("hero", chat.VisualAnchorKindIdentity, reference)}
	result, _ := rig.run(t, options, imageHistory(), `{"prompt":"portrait","anchors":["hero"]}`)
	if !result.NoSubmission || rig.gateway.submitted.OfferingID != "" {
		t.Fatalf("result=%+v submitted=%#v", result, rig.gateway.submitted)
	}
	if !strings.Contains(result.Error, "视觉锚参考图不可用") ||
		!strings.Contains(result.Error, "身份锚「hero」参考图 #1") {
		t.Fatalf("refusal = %q; want the anchor handle and the Schema wording", result.Error)
	}
	if strings.Contains(result.Error, "生成失败") {
		t.Fatalf("an unavailable anchor is not a generic generation failure: %q", result.Error)
	}
}

// #250 review: an auto-attached style anchor whose picture is gone is named
// by its handle too, without being written in the anchor argument.
func TestUnavailableStyleAnchorReferenceIsRefusedWithItsHandle(t *testing.T) {
	rig := newStyleLockRig(t, 3)
	reference := "/api/v1/tasks/task-1/assets/gone-style"
	rig.resolver.fail = map[string]bool{reference: true}
	options := styleLock(false, "")
	options.VisualAnchors = []chat.VisualAnchor{anchor("mood", chat.VisualAnchorKindStyle, reference)}
	result, _ := rig.run(t, options, imageHistory(), `{"prompt":"portrait"}`)
	if !result.NoSubmission || rig.gateway.submitted.OfferingID != "" {
		t.Fatalf("result=%+v submitted=%#v", result, rig.gateway.submitted)
	}
	if !strings.Contains(result.Error, "风格锚「mood」参考图 #1") {
		t.Fatalf("refusal = %q; want the style anchor handle", result.Error)
	}
	if strings.Contains(result.Error, "生成失败") {
		t.Fatalf("an unavailable anchor is not a generic generation failure: %q", result.Error)
	}
}

// #250 review: a generated picture is stored as its Task asset URL (the Agent
// can anchor what it just made without ever seeing an input-asset ID). The
// exact URL reaches the resolver, which verifies and copies it.
func TestIdentityAnchorTaskAssetURLIsSentUnchanged(t *testing.T) {
	rig := newStyleLockRig(t, 3)
	options := styleLock(false, "")
	options.VisualAnchors = []chat.VisualAnchor{
		anchor("hero", chat.VisualAnchorKindIdentity, "/api/v1/tasks/task-1/assets/gen-1"),
	}
	result, _ := rig.run(t, options, imageHistory(), `{"prompt":"portrait","anchors":["hero"]}`)
	if result.NoSubmission {
		t.Fatalf("result=%+v error=%q", result, result.Error)
	}
	want := []string{"/api/v1/tasks/task-1/assets/gen-1", "/api/v1/input-assets/agent-image"}
	if strings.Join(rig.resolver.urls, ",") != strings.Join(want, ",") {
		t.Fatalf("reference order=%v, want %v", rig.resolver.urls, want)
	}
}

// #250 review: only a stored identity anchor may be named; an unknown name or a style
// anchor is refused before any submission.
func TestAnchorArgumentRefusesUnknownAndStyleAnchors(t *testing.T) {
	cases := map[string]struct {
		options chat.TurnOptions
		want    string
	}{
		"unknown": {
			options: chat.TurnOptions{},
			want:    "找不到名为「hero」的锚点",
		},
		"style anchor": {
			options: chat.TurnOptions{VisualAnchors: []chat.VisualAnchor{anchor("hero", chat.VisualAnchorKindStyle, "hero-a")}},
			want:    "「hero」不是身份锚",
		},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			rig := newStyleLockRig(t, 3)
			options := test.options
			options.AIModel = "configured-model"
			result, _ := rig.run(t, options, nil, `{"prompt":"portrait","anchors":["hero"]}`)
			if !result.NoSubmission || !strings.Contains(result.Error, test.want) {
				t.Fatalf("result=%+v error=%q, want refusal containing %q", result, result.Error, test.want)
			}
			if rig.gateway.submitted.OfferingID != "" {
				t.Fatalf("an invalid anchor submitted %q", rig.gateway.submitted.OfferingID)
			}
			if len(rig.resolver.urls) != 0 {
				t.Fatalf("an invalid anchor resolved pictures: %v", rig.resolver.urls)
			}
		})
	}
}

func TestIdentityAnchorSurvivesTheImageTo3DLimit(t *testing.T) {
	rig := newStyleLockRig(t, 1)
	var tool catalog.ChatToolSchema
	for _, candidate := range rig.app.Chat.Tools {
		if candidate.Name == "create_model_3d_from_image" {
			tool = candidate
		}
	}
	if tool.Name == "" {
		t.Fatal("create_model_3d_from_image is not declared")
	}
	rig.tool = tool
	offering := generation.Offering{
		ID: "configured/i23d", ModelID: "configured-model", Capability: generation.ImageTo3D,
		Domain: generation.DomainModel3D, Enabled: true, Default: true,
		PollInterval: time.Second, Timeout: time.Second,
		Parameters: []generation.Parameter{
			{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt, Required: true},
			{Name: "images", Type: "asset-array", Semantic: generation.SemanticReferenceImage, MaxItems: 1},
		},
	}
	rig.gateway.offerings = []generation.Offering{offering}
	rig.gateway.tasks = []generation.Task{{State: generation.TaskSucceeded, Offering: offering}}
	options := styleLock(false, "")
	options.AIModel = "configured-model"
	options.VisualAnchors = []chat.VisualAnchor{anchor("hero", chat.VisualAnchorKindIdentity, "hero-a")}
	result, input := rig.run(t, options, imageHistory(),
		`{"prompt":"a ranger","reference_handles":["@img1"],"anchors":["hero"]}`)
	t.Logf("result=%+v\ninput=%v", result, input)
	if result.NoSubmission {
		t.Fatalf("result=%+v error=%q", result, result.Error)
	}
	if strings.Join(rig.resolver.urls, ",") != "/api/v1/input-assets/hero-a" {
		t.Fatalf("references=%v", rig.resolver.urls)
	}
	if !strings.Contains(result.AgentContent, "丢弃了普通参考 @img1") {
		t.Fatalf("dropped-reference note missing: %s", result.AgentContent)
	}
}
