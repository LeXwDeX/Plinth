package generationtool

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/project"
)

// The item context frozen with a server-started turn reaches the reference
// resolver through the scope; no tool argument can set it (#328).
func TestCommandReferenceScopeCarriesTheFrozenItemContext(t *testing.T) {
	references := []project.Reference{{TaskID: "task-1", AssetID: "asset-1"}}
	scope := commandReferenceScope(chat.ToolCommand{
		OwnerID:   "owner-a",
		SessionID: "session-item",
		Options: chat.TurnOptions{
			ProjectID:           "prj_1",
			AssetListItemID:     "aitem_1",
			AssetListReferences: references,
		},
	})
	if scope.Owner != "owner-a" || scope.SessionID != "session-item" || scope.ProjectID != "prj_1" ||
		scope.AssetListItemID != "aitem_1" || len(scope.AssetListReferences) != 1 ||
		scope.AssetListReferences[0] != references[0] {
		t.Fatalf("scope = %+v", scope)
	}
	// An ordinary turn carries no item context, so its references keep the
	// session/project rule.
	plain := commandReferenceScope(chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a"})
	if plain.AssetListItemID != "" || len(plain.AssetListReferences) != 0 {
		t.Fatalf("plain scope = %+v", plain)
	}
}
