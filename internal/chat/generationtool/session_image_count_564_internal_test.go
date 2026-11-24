package generationtool

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #564: the session's image count is one value across models, but each model
// declares its own range for the count parameter. A count set on a model with
// a wider range must reach a narrower one clamped to that model's declared
// maximum, not be written as is and refused at input normalization, where the
// Agent cannot change it.
func TestSessionImageCountIsClampedToTheTargetOfferingsRange(t *testing.T) {
	executor := &Executor{
		imageCount: func(ownerID, sessionID string) (int, bool) { return 15, true },
	}
	command := chat.ToolCommand{Tool: catalog.ChatToolSchema{Name: "refine_image"}, SessionID: "session-1"}

	gpt := countOffering("openai/gpt-image-2/text-to-image", "n", "user")
	if values := buildInputValues(t, executor, command, gpt); values["n"] != float64(10) {
		t.Fatalf("n = %v, want the session's 15 clamped to the declared maximum 10", values["n"])
	}

	seedream := countOffering("jimeng/seedream-4-5/text-to-image", "max_images", "user")
	seedream.Parameters[0].Maximum = pointerFloat(15)
	if values := buildInputValues(t, executor, command, seedream); values["max_images"] != float64(15) {
		t.Fatalf("max_images = %v, want the session's 15 inside the declared range", values["max_images"])
	}

	// A count inside the range is written unchanged.
	executor.imageCount = func(ownerID, sessionID string) (int, bool) { return 3, true }
	if values := buildInputValues(t, executor, command, gpt); values["n"] != float64(3) {
		t.Fatalf("n = %v, want the session's 3", values["n"])
	}
}
