package upstreamhttp_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/upstreamhttp"
)

// #563: the refusal's error is logged as it is by the turn and by the retry
// loop, so its text carries only the sanitized, bounded excerpt of the body:
// no echo of the prompt, no data URI, nothing past ResponseExcerptBytes. The
// classes still read the whole body (a 502 stays transient).
func TestRefusalErrorCarriesOnlyTheSanitizedExcerpt(t *testing.T) {
	request := agents.Request{Messages: []agents.Message{{Role: "user", Parts: []agents.ContentPart{
		{Kind: "text", Text: "secret prompt text"},
		{Kind: "image", URL: "data:image/png;base64,AAAA"},
	}}}}
	body := "Bad Gateway: upstream refused secret prompt text data:image/png;base64,AAAA " +
		strings.Repeat("x", 64<<10)
	err := upstreamhttp.Refusal{
		Status: 502, Body: []byte(body), HasImages: true,
		Echoes: upstreamhttp.RequestTexts(request),
	}.Err()
	if err == nil {
		t.Fatal("the refusal returned no error")
	}
	text := err.Error()
	if !strings.Contains(text, "Bad Gateway") {
		t.Fatalf("the error lost the upstream's wording: %q", text)
	}
	for _, secret := range []string{"secret prompt text", "AAAA", strings.Repeat("x", upstreamhttp.ResponseExcerptBytes+1)} {
		if strings.Contains(text, secret) {
			t.Fatalf("the error carries %q: %q", secret[:min(len(secret), 40)], text[:min(len(text), 300)])
		}
	}
	if !errors.Is(err, agents.ErrTransient) {
		t.Fatalf("a 502 refusal is no longer transient: %v", err)
	}
}
