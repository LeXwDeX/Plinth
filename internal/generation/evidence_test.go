package generation

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestUpstreamExcerptStripsEverythingUpstreamControlled(t *testing.T) {
	prompt := "a velvet castle on a cliff at sunrise with banners"
	text := "rejected " + prompt + " via https://assets.example.test/dir/id-1234.glb?X-Amz-Signature=beef Authorization: Bearer abcdefghijklmnop " +
		"and key msy-abcdefgh1234 for person@example.com token=zzz999"
	got := UpstreamExcerpt(text, 0, prompt)
	for _, leaked := range []string{"velvet", "assets.example.test", "id-1234", "X-Amz", "beef", "abcdefghijklmnop", "msy-abcdefgh1234", "person@example.com", "zzz999"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("excerpt leaked %q: %q", leaked, got)
		}
	}
	for _, kept := range []string{"rejected", "[input]", "[url]", "[email]"} {
		if !strings.Contains(got, kept) {
			t.Fatalf("excerpt lost %q: %q", kept, got)
		}
	}
	if again := UpstreamExcerpt(got, 0, prompt); again != got {
		t.Fatalf("sanitizing is not idempotent: %q then %q", got, again)
	}
}

func TestUpstreamExcerptStripsATruncatedPromptEcho(t *testing.T) {
	prompt := "a velvet castle on a cliff at sunrise with banners and a long winding road"
	got := UpstreamExcerpt("invalid prompt: a velvet castle on a cliff at sunrise with ban...", 0, prompt)
	if strings.Contains(got, "velvet") || !strings.Contains(got, "[input]") {
		t.Fatalf("truncated echo kept: %q", got)
	}
	// An unrelated sentence that merely shares a few words is not an echo.
	if got := UpstreamExcerpt("a velvet rope", 0, prompt); got != "a velvet rope" {
		t.Fatalf("unrelated text changed: %q", got)
	}
}

func TestUpstreamExcerptBoundsByRunesAndDropsUnprintable(t *testing.T) {
	got := UpstreamExcerpt("bad\x00 model "+strings.Repeat("长", 1000), 0)
	if !utf8.ValidString(got) || len([]rune(got)) != UpstreamExcerptRunes || strings.ContainsRune(got, 0) {
		t.Fatalf("excerpt shape: %d runes valid=%v", len([]rune(got)), utf8.ValidString(got))
	}
	// Short vocabulary words are not treated as echoes.
	if got := UpstreamExcerpt("topology quad glb", 0, "quad", "glb"); got != "topology quad glb" {
		t.Fatalf("short values stripped: %q", got)
	}
}

func TestInputEchoesComeFromTheOfferingsTextParameters(t *testing.T) {
	offering := Offering{Parameters: []Parameter{
		{Name: "prompt", Type: "text", Semantic: SemanticPrompt},
		{Name: "note", Type: "text"},
		{Name: "topology", Type: "select"},
		{Name: "height", Type: "number"},
	}}
	input := json.RawMessage(`{"prompt":"a velvet castle","note":"for a game","topology":"quad","height":3,"undeclared":"secret words"}`)
	echoes := InputEchoes(offering, input)
	if len(echoes) != 2 || echoes[0] != "a velvet castle" || echoes[1] != "for a game" {
		t.Fatalf("echoes = %q", echoes)
	}
	if InputEchoes(offering, nil) != nil || InputEchoes(offering, json.RawMessage(`not json`)) != nil {
		t.Fatal("unreadable input must yield no echoes")
	}
}
