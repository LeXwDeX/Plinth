package openaicompatible_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/openaicompatible"
)

// A model such as gpt-astra (gpt-6-astra behind the gateway) may draw the
// picture itself: the whole PNG arrives as base64 in delta.images on one SSE
// line of megabytes. That used to end the stream with "bufio.Scanner: token
// too long" and an unknown error; the line is now dropped and the turn says
// what happened.
func inlineImageStream(t *testing.T, maxLine int, body string) (agents.Completion, error) {
	t.Helper()
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})
	adapter, err := openaicompatible.New(openaicompatible.Config{
		BaseURL:           "https://configured-agent.test/v1/",
		APIKey:            "configured-secret",
		EndpointPath:      "/chat/completions",
		AuthorizationName: "Authorization",
		AuthorizationType: "Bearer",
		RequestTimeout:    time.Minute,
		ScannerMaxBytes:   maxLine,
		ErrorBodyMaxBytes: 1 << 16,
		Client:            &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter.Stream(context.Background(), agents.Request{
		Model:    "gpt-astra",
		Messages: []agents.Message{{Role: "user", Content: "redo it"}},
	}, nil)
}

func imageLine(base64Bytes int) string {
	return `data: {"id":"resp_1","object":"chat.completion.chunk","model":"gpt-6-astra","choices":[{"index":0,"delta":{"images":[{"type":"image_url","image_url":{"url":"data:image/png;base64,` +
		strings.Repeat("A", base64Bytes) + `"}}]}}]}` + "\n\n"
}

const streamEnd = `data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"

func TestAdapterSaysAnInlinePictureWasNotKeptInsteadOfFailingTheStream(t *testing.T) {
	for name, body := range map[string]string{
		"above the line limit":  imageLine(3 << 20),
		"within the line limit": imageLine(1 << 10),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := inlineImageStream(t, 1<<20, body+streamEnd)
			if !errors.Is(err, agents.ErrInlineImageOutput) {
				t.Fatalf("err = %v, want ErrInlineImageOutput", err)
			}
		})
	}
}

func TestAdapterKeepsTheTextOfAReplyThatAlsoDrewAPicture(t *testing.T) {
	text := `data: {"choices":[{"index":0,"delta":{"content":"done"}}]}` + "\n\n"
	completion, err := inlineImageStream(t, 1<<20, text+imageLine(3<<20)+streamEnd)
	if err != nil || completion.Content != "done" {
		t.Fatalf("completion = %+v, err = %v", completion, err)
	}
}

func TestAdapterStillFailsOnAnOversizedLineThatIsNotAPicture(t *testing.T) {
	long := `data: {"choices":[{"index":0,"delta":{"content":"` + strings.Repeat("x", 2<<20) + `"}}]}` + "\n\n"
	if _, err := inlineImageStream(t, 1<<20, long+streamEnd); err == nil || errors.Is(err, agents.ErrInlineImageOutput) {
		t.Fatalf("err = %v, want a read error", err)
	}
}
