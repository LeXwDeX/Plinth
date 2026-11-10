package generationtool_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
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

// #250: only a stored identity anchor may be named; an unknown name or a style
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
