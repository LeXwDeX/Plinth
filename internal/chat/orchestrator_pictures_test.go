package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func freshPictureOrchestrator() *Orchestrator {
	return &Orchestrator{
		schema: catalog.ChatSchema{
			Roles: catalog.ChatRolesSchema{Tool: "tool", User: "user"},
			Agent: catalog.ChatAgentSchema{Vision: true},
			Context: catalog.ChatContextSchema{
				TextPartKind:                   "text",
				ImagePartKind:                  "image",
				HistoryImageViewIntro:          "history intro",
				HistoryImageViewLabelTemplate:  "\n[history {handle}]",
				HistoryImageUnreadableTemplate: "\n[history unreadable {handle}]",
				ResultImageViewIntro:           "fresh intro",
				ResultImageViewLabelTemplate:   "\n[fresh {handle}]",
				ResultImageUnreadableTemplate:  "\n[fresh unreadable {handle}]",
			},
		},
		visionInline: func(context.Context, generation.OwnerID, string) (string, bool) {
			return "data:image/png;base64,AAAA", true
		},
	}
}

func TestFreshPicturesUseTheResultCopy(t *testing.T) {
	orchestrator := freshPictureOrchestrator()
	reply := agents.Message{Role: "tool", Content: "result"}
	orchestrator.attachToolPictures(context.Background(), "owner", &reply,
		[]ToolPicture{{Handle: "@img1", URL: "/api/v1/tasks/t/assets/a", Fresh: true}})
	if !reply.FreshPictures {
		t.Fatal("a fresh picture did not mark the reply")
	}
	joined := ""
	for _, part := range reply.Parts {
		joined += part.Text
	}
	if !strings.Contains(joined, "fresh @img1") || strings.Contains(joined, "history @img1") {
		t.Fatalf("fresh label = %q", joined)
	}
	messages := orchestrator.withToolPictures([]agents.Message{reply})
	if len(messages) != 2 || messages[1].Parts[0].Text != "fresh intro" {
		t.Fatalf("picture intro = %+v", messages)
	}
}

func TestHistoryPicturesKeepTheHistoryCopy(t *testing.T) {
	orchestrator := freshPictureOrchestrator()
	reply := agents.Message{Role: "tool", Content: "lookup"}
	orchestrator.attachToolPictures(context.Background(), "owner", &reply,
		[]ToolPicture{{Handle: "@img2", URL: "/api/v1/tasks/t/assets/b"}})
	if reply.FreshPictures {
		t.Fatal("a history picture was marked fresh")
	}
	messages := orchestrator.withToolPictures([]agents.Message{reply})
	if len(messages) != 2 || messages[1].Parts[0].Text != "history intro" {
		t.Fatalf("picture intro = %+v", messages)
	}
}
