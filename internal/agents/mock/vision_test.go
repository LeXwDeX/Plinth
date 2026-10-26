package mock_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
)

// #322: on the mock stack a model whose name says text-only refuses pictures
// like an upstream without image input, so the administrator's connection
// test can show both findings without a real model.
func TestMockTextOnlyModelRefusesPictures(t *testing.T) {
	adapter := usageAdapter(t)
	picture := agents.Request{Model: "mock-text-only", Messages: []agents.Message{{Role: "user", Parts: []agents.ContentPart{
		{Kind: "text", Text: "look"}, {Kind: "image_url", URL: "data:image/png;base64,AAAA"},
	}}}}
	if _, err := adapter.Complete(context.Background(), picture); !errors.Is(err, agents.ErrImagesRejected) {
		t.Fatalf("text-only with a picture = %v", err)
	}
	if _, err := adapter.Stream(context.Background(), picture, nil); !errors.Is(err, agents.ErrImagesRejected) {
		t.Fatalf("text-only stream with a picture = %v", err)
	}
	text := agents.Request{Model: "mock-text-only", Messages: []agents.Message{{Role: "user", Content: "hi"}}}
	if _, err := adapter.Complete(context.Background(), text); err != nil {
		t.Fatalf("text-only without a picture = %v", err)
	}
	picture.Model = "deepseek"
	if _, err := adapter.Complete(context.Background(), picture); err != nil {
		t.Fatalf("a model with image input = %v", err)
	}
}
