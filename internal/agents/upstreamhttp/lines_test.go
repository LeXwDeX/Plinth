package upstreamhttp_test

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents/upstreamhttp"
)

// A line over the bound does not end the stream: it is reported as oversized
// with its head, and the lines around it are read normally.
func TestLinesSkipAnOversizedLineAndKeepItsHead(t *testing.T) {
	long := "data: " + strings.Repeat("x", 4000)
	lines := upstreamhttp.NewLines(strings.NewReader("event: one\n"+long+"\r\nlast line without a newline"), 1000)
	var got []string
	var heads []string
	for lines.Scan() {
		if lines.Oversized() {
			got = append(got, "<oversized>")
			heads = append(heads, string(lines.Head()))
			if lines.Text() != "" {
				t.Fatalf("an oversized line has text %q", lines.Text())
			}
			continue
		}
		if lines.Head() != nil {
			t.Fatal("a normal line has a head")
		}
		got = append(got, lines.Text())
	}
	if lines.Err() != nil {
		t.Fatal(lines.Err())
	}
	if strings.Join(got, "|") != "event: one|<oversized>|last line without a newline" {
		t.Fatalf("lines = %q", got)
	}
	if len(heads) != 1 || !strings.HasPrefix(heads[0], "data: xxx") || len(heads[0]) != 512 {
		t.Fatalf("head = %d bytes %.20q", len(heads[0]), heads[0])
	}
}

// A line that is exactly the bound is not oversized, and a read error is
// reported by Err rather than swallowed.
func TestLinesBoundAndReadError(t *testing.T) {
	lines := upstreamhttp.NewLines(strings.NewReader(strings.Repeat("a", 10)+"\n"+strings.Repeat("b", 11)+"\n"), 11)
	var oversized []bool
	for lines.Scan() {
		oversized = append(oversized, lines.Oversized())
	}
	// "aaaaaaaaaa\n" is 11 bytes with its ending: within the bound; the next is 12.
	if len(oversized) != 2 || oversized[0] || !oversized[1] {
		t.Fatalf("oversized = %v", oversized)
	}
	broken := upstreamhttp.NewLines(io.MultiReader(strings.NewReader("partial"), errReader{}), 100)
	for broken.Scan() {
	}
	if !errors.Is(broken.Err(), errBoom) {
		t.Fatalf("err = %v", broken.Err())
	}
}

var errBoom = errors.New("boom")

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errBoom }
