package chat

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/project"
)

// Process copies the item marks from the stored turn, never from the caller,
// and keeps the frozen references private to it.
func TestCopyFrozenAssetListTurnReplacesCallerMarks(t *testing.T) {
	stored := TurnOptions{
		AssetListItemID:     "aitem_1",
		AssetListReferences: []project.Reference{{TaskID: "task-1", AssetID: "asset-1"}},
	}
	options := TurnOptions{
		AssetListItemID:     "aitem_forged",
		AssetListReferences: []project.Reference{{TaskID: "task-forged", AssetID: "asset-forged"}},
	}
	copyFrozenAssetListTurn(&options, stored)
	if options.AssetListItemID != "aitem_1" || len(options.AssetListReferences) != 1 ||
		options.AssetListReferences[0] != (project.Reference{TaskID: "task-1", AssetID: "asset-1"}) {
		t.Fatalf("copied options = %+v", options)
	}
	options.AssetListReferences[0].TaskID = "changed"
	if stored.AssetListReferences[0].TaskID != "task-1" {
		t.Fatalf("the stored references share storage: %+v", stored.AssetListReferences)
	}
}
