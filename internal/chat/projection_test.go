package chat_test

import (
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

func TestProjectionUsesConfiguredPendingMarkerAndToolCapability(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	projected := chat.ProjectMessage(
		chat.ProjectionPolicy{
			PendingContent: "configured-pending",
			ToolCapabilities: map[string]string{
				"configured-refine": "configured-image-to-image",
			},
		},
		chat.Message{
			ID:        "message-a",
			Role:      "configured-assistant",
			Content:   "configured-pending",
			ToolCalls: []chat.ToolCall{{Name: "configured-refine"}},
			CreatedAt: now,
		},
	)

	if projected.Content != "" || !projected.IsPending {
		t.Fatalf("projected = %#v", projected)
	}
	if len(projected.PendingTaskTypes) != 1 ||
		projected.PendingTaskTypes[0] != "configured-image-to-image" {
		t.Fatalf("pending task types = %#v", projected.PendingTaskTypes)
	}
	if projected.CreatedAtMilliseconds != now.UnixMilli() {
		t.Fatalf(
			"createdAt = %d",
			projected.CreatedAtMilliseconds,
		)
	}
}

func TestProjectionKeepsImagesAsStableSemanticValues(t *testing.T) {
	projected := chat.ProjectMessage(
		chat.ProjectionPolicy{PendingContent: "pending"},
		chat.Message{
			ID:      "message-b",
			Role:    "configured-user",
			Content: "hello",
			ImageURLs: []string{
				"/configured/image-a",
				"/configured/image-b",
			},
			CreatedAt: time.Unix(1, 0).UTC(),
		},
	)

	if projected.IsPending ||
		projected.Content != "hello" ||
		len(projected.Images) != 2 ||
		projected.Images[1].URL != "/configured/image-b" {
		t.Fatalf("projected = %#v", projected)
	}
}
