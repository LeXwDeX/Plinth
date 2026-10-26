package sqlite

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
)

func TestOfferingPatchSQLiteRollsBackValueReasonAndAutoClear(t *testing.T) {
	for _, failure := range []string{"reason", "clear"} {
		t.Run(failure, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "params.db")
			repo, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = repo.Close() }()
			scope := agentparams.ScopeKey("owner", "session")
			store := agentparams.NewPersistentParameterStore(repo, nil)
			value := func(v string) map[string]json.RawMessage {
				return map[string]json.RawMessage{"quality": json.RawMessage(v)}
			}
			if err = repo.SaveAgentState("owner", "session", "quality", json.RawMessage(`"legacy"`)); err != nil {
				t.Fatal(err)
			}
			if err = repo.SaveAgentState("owner", "session", agentparams.ConsentApprovedKey, json.RawMessage(`true`)); err != nil {
				t.Fatal(err)
			}
			if err = store.SetAgentOffering(scope, "o", value(`"old"`), map[string]string{"quality": "old reason"}); err != nil {
				t.Fatal(err)
			}
			if err = store.SetUserOffering(scope, "other", value(`"other"`)); err != nil {
				t.Fatal(err)
			}
			before, err := repo.LoadAgentState("owner", "session")
			if err != nil {
				t.Fatal(err)
			}
			if failure == "reason" {
				// Sorted patch writes the value first; aborting its reason must roll it back.
				_, err = repo.db.Exec(`CREATE TRIGGER reject_reason BEFORE UPDATE ON go_agent_state WHEN NEW.state_key LIKE '%:reason:%' BEGIN SELECT RAISE(ABORT, 'injected reason failure'); END`)
			} else {
				// Clear writes the user null and deletes the reason before this Agent delete.
				_, err = repo.db.Exec(`CREATE TRIGGER reject_clear BEFORE DELETE ON go_agent_state WHEN OLD.state_key LIKE '%:agent:%' BEGIN SELECT RAISE(ABORT, 'injected clear failure'); END`)
			}
			if err != nil {
				t.Fatal(err)
			}
			if failure == "reason" {
				err = store.SetAgentOffering(scope, "o", value(`"new"`), map[string]string{"quality": "new reason"})
			} else {
				err = store.SetUserOffering(scope, "o", value(`null`))
			}
			if err == nil {
				t.Fatal("partial persistence reported success")
			}
			after, err := repo.LoadAgentState("owner", "session")
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("patch was not atomic: before=%v after=%v err=%v", before, after, err)
			}
			if state := store.OfferingStates(scope, "o")["quality"]; string(state.Value) != `"old"` || state.Reason != "old reason" {
				t.Fatalf("failed patch published: %+v", state)
			}
			if err = repo.Close(); err != nil {
				t.Fatal(err)
			}
			repo, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			store = agentparams.NewPersistentParameterStore(repo, nil)
			if state := store.OfferingStates(scope, "o")["quality"]; string(state.Value) != `"old"` || state.Reason != "old reason" {
				t.Fatalf("restart split pair: %+v", state)
			}
			trigger := "reject_" + failure
			if _, err = repo.db.Exec("DROP TRIGGER " + trigger); err != nil {
				t.Fatal(err)
			}
			if err = store.SetUserOffering(scope, "o", value(`null`)); err != nil {
				t.Fatal(err)
			}
			if err = repo.Close(); err != nil {
				t.Fatal(err)
			}
			repo, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			store = agentparams.NewPersistentParameterStore(repo, nil)
			state := store.OfferingStates(scope, "o")["quality"]
			if state.Source != "default" || len(state.Value) != 0 || state.Reason != "" || len(store.GetOffering(scope, "o")) != 0 {
				t.Fatalf("clear resurrected state: %+v", state)
			}
			if got := string(store.GetOffering(scope, "other")["quality"]); got != `"other"` {
				t.Fatal("other offering changed")
			}
			raw, _ := repo.LoadAgentState("owner", "session")
			if string(raw[agentparams.ConsentApprovedKey]) != "true" {
				t.Fatal("private state changed")
			}
			if err = store.SetAgentOffering(scope, "o", value(`"later"`), map[string]string{"quality": "later reason"}); err != nil {
				t.Fatal(err)
			}
			if state = store.OfferingStates(scope, "o")["quality"]; string(state.Value) != `"later"` || state.Reason != "later reason" {
				t.Fatalf("new auto suggestion rejected: %+v", state)
			}
		})
	}
}

func TestOfferingPatchSQLiteRejectsStaleSnapshotWithoutOverwritingConcurrentState(t *testing.T) {
	repo, err := Open(filepath.Join(t.TempDir(), "params.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	before, err := repo.LoadAgentState("owner", "s")
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.SaveAgentState("owner", "s", "concurrent", json.RawMessage(`true`)); err != nil {
		t.Fatal(err)
	}
	if err = repo.PatchOfferingState("owner", "s", before, map[string]json.RawMessage{"new": json.RawMessage(`1`)}, []string{"concurrent"}); err == nil {
		t.Fatal("stale snapshot accepted")
	}
	after, err := repo.LoadAgentState("owner", "s")
	if err != nil || len(after) != 1 || string(after["concurrent"]) != "true" {
		t.Fatalf("concurrent state lost: %v %v", after, err)
	}
}
