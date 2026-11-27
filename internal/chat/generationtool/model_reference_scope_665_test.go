package generationtool_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #665: compare_model_reference reads the source Task's frozen reference as
// the Task's own input, but a picture it names by any other asset ID only
// through the session-scoped read: one outside the session ends as a failed
// view with the scope refusal, and nothing is rendered for it.
func TestCompareModelReferenceScopesAPictureNamedByID665(t *testing.T) {
	glb := reviewModelGLB(t, 1, 1)
	task := referenceTask("task-a", "owner-a", "session-a", glb)
	gateway := newReviewGateway(task)
	assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-glb": glb})
	pictures := &reviewPictures{
		pictures: map[string][]byte{"ref-asset": referencePNG(t, 32, 32),
			"own-picture": referencePNG(t, 32, 32), "sibling-picture": referencePNG(t, 32, 32)},
		scoped: func(command chat.ToolCommand, id string) bool {
			return command.SessionID == "session-a" && id == "own-picture"
		},
	}
	executor := referenceExecutor(t, gateway, assets, pictures, newReviewSelfCheckStore(), nil)

	frozen, err := executor.CompareModelReference(context.Background(),
		reviewCommand("compare_model_reference", `{"model":"task-a","image":"ref-asset","view":"front"}`))
	if err != nil || frozen.Error != "" || len(frozen.ImageURLs) != 1 || len(pictures.agentOpened) != 0 {
		t.Fatalf("frozen reference = %+v err=%v scoped reads=%v", frozen, err, pictures.agentOpened)
	}
	own, err := executor.CompareModelReference(context.Background(),
		reviewCommand("compare_model_reference", `{"model":"task-a","image":"own-picture","view":"front"}`))
	if err != nil || own.Error != "" || len(own.ImageURLs) != 1 || !slices.Equal(pictures.agentOpened, []string{"own-picture"}) {
		t.Fatalf("in-scope picture = %+v err=%v scoped reads=%v", own, err, pictures.agentOpened)
	}
	sibling, err := executor.CompareModelReference(context.Background(),
		reviewCommand("compare_model_reference", `{"model":"task-a","image":"sibling-picture","view":"front"}`))
	if err != nil || len(sibling.ImageURLs) != 0 || !strings.Contains(sibling.AgentContent, "out of scope sibling-picture") {
		t.Fatalf("sibling picture = %+v err=%v", sibling, err)
	}
}
