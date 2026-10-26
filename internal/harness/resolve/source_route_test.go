package resolve

import (
	"context"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"testing"
)

func TestExplicitSameProviderAssetRouteRemainsSelected(t *testing.T) {
	s, _, _ := fixture(t)
	choice := choose(t, s, "remesh")
	choice.Sources = []harness.Source{{Parameter: "source_asset", TaskID: "model-task", AssetID: "model-asset"}}
	selected, err := s.Update(context.Background(), "owner", "session", 0, choice)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.ResolveSelection(context.Background(), selected)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != StatusReady || string(snapshot.Input["source_asset"]) != `"model-asset"` || len(snapshot.Input["source_task"]) != 0 {
		t.Fatalf("explicit asset route changed or refused: %+v", snapshot)
	}
}
