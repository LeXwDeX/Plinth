package generationtool_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestPendingCardPrivateSourceIdentityAndLegacyGolden(t *testing.T) {
	p := generation.PreparedInput{Capability: generation.ImageTo3D, OfferingID: "meshy/processing/rigging", OfferingRevision: "frozen", NormalizedInput: json.RawMessage(`{"source_task":"source"}`), Dependencies: []generation.PreparedInputDependency{{Parameter: "source_task", TaskID: "source"}}}
	// Captured from the unchanged 486ba44a implementation via an offline overlay.
	legacy := "0e2c846ac59f3155adede75f5334d5182936533d370656174361720776e53db5"
	if got := generationtool.ApprovalRequestKey("process_model", p); got != legacy {
		t.Fatalf("legacy pending key changed: %s", got)
	}
	p.Dependencies[0].SourceSessionID = "session-a"
	p.Dependencies[0].ProviderBinding = json.RawMessage(`{"candidate":"private-A","version":1}`)
	first := generationtool.ApprovalRequestKey("process_model", p)
	if first == "" || first == legacy {
		t.Fatal("private identity missing from pending key")
	}
	p.Dependencies[0].ProviderBinding = json.RawMessage(` {"version":1,"candidate":"private-A"} `)
	if got := generationtool.ApprovalRequestKey("process_model", p); got != first {
		t.Fatal("same canonical binding did not reuse card")
	}
	p.Dependencies[0].ProviderBinding = json.RawMessage(`{"candidate":"private-B","version":1}`)
	if got := generationtool.ApprovalRequestKey("process_model", p); got == first || got == "" {
		t.Fatal("changed candidate merged with old pending")
	}
	p.Dependencies[0].ProviderBinding = json.RawMessage(`{"candidate":"private-A","version":1}`)
	p.Dependencies[0].SourceSessionID = "session-b"
	if got := generationtool.ApprovalRequestKey("process_model", p); got == first || got == "" {
		t.Fatal("changed source session merged with old pending")
	}
	for _, bad := range []string{`null`, `{}`, `{"candidate":"a","candidate":"b"}`, `{"x":1.5}`, `{"candidate":"` + strings.Repeat("a", 17000) + `"}`} {
		p.Dependencies[0].ProviderBinding = []byte(bad)
		if got := generationtool.ApprovalRequestKey("process_model", p); got != "" {
			t.Fatal("invalid private binding received mergeable key")
		}
	}
}
