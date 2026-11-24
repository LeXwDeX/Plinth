package upstreamhttp_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents/upstreamhttp"
)

// #555: an upstream that echoes a picture does not put its bytes in the log:
// every data URI is cut out of the excerpt, and the excerpt is bounded.
func TestResponseExcerptCutsDataURIsAndBoundsTheBody(t *testing.T) {
	body := "Bad Gateway: data:image/png;base64,QUJDREVGRw== " + strings.Repeat("x", 400)
	excerpt := upstreamhttp.ResponseExcerpt([]byte(body))
	if !strings.Contains(excerpt, "[data-uri]") || strings.Contains(excerpt, "QUJDREVGRw") {
		t.Fatalf("excerpt = %q", excerpt)
	}
	if len(excerpt) > upstreamhttp.ResponseExcerptBytes {
		t.Fatalf("excerpt is %d bytes", len(excerpt))
	}
}
