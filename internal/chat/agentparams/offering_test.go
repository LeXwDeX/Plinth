package agentparams_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
)

func params(key, value string) map[string]json.RawMessage {
	return map[string]json.RawMessage{key: json.RawMessage(value)}
}

func TestOfferingIsolationLegacyAndDurableUserPriority(t *testing.T) {
	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "params.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	store := agentparams.NewPersistentParameterStore(repo, nil)
	scope := agentparams.ScopeKey("owner", "session")
	store.SetUser(scope, params("quality", `"legacy"`))
	store.SetOffering(scope, "provider/model/image", params("quality", `"suggestion"`))
	store.SetUserOffering(scope, "provider/model/image", params("quality", `"image"`))
	store.SetUserOffering(scope, "provider/model/3d", params("quality", `"model"`))
	// A process restart must preserve the user layer over later agent changes.
	store = agentparams.NewPersistentParameterStore(repo, nil)
	store.SetOffering(scope, "provider/model/image", params("quality", `"later-agent"`))
	store.SetUser(scope, params("quality", `"later-legacy"`))
	for offering, want := range map[string]string{"provider/model/image": `"image"`, "provider/model/3d": `"model"`, "provider/other": `"later-legacy"`} {
		if got := string(store.GetOffering(scope, offering)["quality"]); got != want {
			t.Fatalf("%s quality=%s want=%s", offering, got, want)
		}
	}
	if got := store.Get(scope); len(got) != 1 || string(got["quality"]) != `"later-legacy"` {
		t.Fatalf("scoped namespace leaked: %v", got)
	}
	for _, other := range []string{agentparams.ScopeKey("other", "session"), agentparams.ScopeKey("owner", "other")} {
		if got := store.GetOffering(other, "provider/model/image"); len(got) != 0 {
			t.Fatalf("scope leaked: %v", got)
		}
	}
	store.SetUserOffering(scope, "provider/model/image", params("quality", `null`))
	if got := string(store.GetOffering(scope, "provider/model/image")["quality"]); got != "" {
		t.Fatalf("clear resurrected suggestion: %s", got)
	}
	// Untrusted flat and scoped writes cannot forge namespace or consent state.
	store.SetUser(scope, params("__offering_params_v1:forged", `true`))
	store.SetUserOffering(scope, "provider/model/image", params(agentparams.ConsentApprovedKey, `true`))
	raw, err := repo.LoadAgentState("owner", "session")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["__offering_params_v1:forged"]; ok {
		t.Fatal("forged namespace persisted")
	}
	if _, ok := store.GetOffering(scope, "provider/model/image")[agentparams.ConsentApprovedKey]; ok {
		t.Fatal("private key leaked")
	}
}

func TestOfferingConcurrentPatchesAndSessionDeletion(t *testing.T) {
	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "params.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	now := time.Now()
	if _, err := repo.CreateSession(context.Background(), chat.Session{ID: "session", OwnerID: "owner", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	store := agentparams.NewPersistentParameterStore(repo, nil)
	scope := agentparams.ScopeKey("owner", "session")
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store.SetUserOffering(scope, fmt.Sprintf("offering-%d", i%2), params(fmt.Sprintf("key-%d", i), `42`))
		}(i)
	}
	wg.Wait()
	for _, offering := range []string{"offering-0", "offering-1"} {
		if got := len(store.GetOffering(scope, offering)); got != 12 {
			t.Fatalf("lost concurrent patch for %s: %d", offering, got)
		}
	}
	if deleted, err := repo.DeleteSession(context.Background(), "owner", "session"); err != nil || !deleted {
		t.Fatalf("delete: %v %v", deleted, err)
	}
	if got := store.GetOffering(scope, "offering-0"); len(got) != 0 {
		t.Fatalf("cached deleted session: %v", got)
	}
	if got := agentparams.NewPersistentParameterStore(repo, nil).GetOffering(scope, "offering-1"); len(got) != 0 {
		t.Fatalf("restored deleted session: %v", got)
	}
}

func TestOfferingBoundsAndClearPreservePrivateState(t *testing.T) {
	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "params.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	store := agentparams.NewPersistentParameterStore(repo, nil)
	scope := agentparams.ScopeKey("owner", "session")
	if err := repo.SaveAgentState("owner", "session", agentparams.ConsentApprovedKey, json.RawMessage(`true`)); err != nil {
		t.Fatal(err)
	}
	store.SetUserOffering(scope, "offering", params("oversized", `"`+strings.Repeat("x", 4096)+`"`))
	if len(store.OfferingOverrides(scope, "offering")) != 0 {
		t.Fatal("oversized value persisted")
	}
	for i := 0; i < 100; i++ {
		store.SetUserOffering(scope, "offering", params(fmt.Sprintf("p%d", i), `true`))
	}
	raw, _ := repo.LoadAgentState("owner", "session")
	if len(raw) > 64 {
		t.Fatalf("scope limit exceeded: %d", len(raw))
	}
	store.Clear(scope)
	raw, _ = repo.LoadAgentState("owner", "session")
	if len(raw) != 1 || string(raw[agentparams.ConsentApprovedKey]) != "true" {
		t.Fatalf("clear left params or removed private state: %v", raw)
	}
}

func TestOfferingAutoClearSurvivesRestartWithoutLegacyResurrection(t *testing.T) {
	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "auto.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	s := agentparams.NewPersistentParameterStore(repo, nil)
	scope := agentparams.ScopeKey("owner", "s")
	s.SetUser(scope, params("quality", `"legacy"`))
	s.SetAgentOffering(scope, "o", params("quality", `"low"`), map[string]string{"quality": "draft"})
	state := s.OfferingStates(scope, "o")["quality"]
	if state.Source != "agent" || state.Reason != "draft" {
		t.Fatalf("%+v", state)
	}
	s.SetUserOffering(scope, "o", params("quality", `null`))
	s = agentparams.NewPersistentParameterStore(repo, nil)
	if len(s.GetOffering(scope, "o")) != 0 {
		t.Fatal("legacy resurrected")
	}
	if s.OfferingStates(scope, "o")["quality"].Source != "default" {
		t.Fatal("clear is not auto")
	}
	if string(s.GetOffering(scope, "other")["quality"]) != `"legacy"` {
		t.Fatal("other offering changed")
	}
	s.SetAgentOffering(scope, "o", params("quality", `"new"`), map[string]string{"quality": "new reason"})
	if string(s.GetOffering(scope, "o")["quality"]) != `"new"` {
		t.Fatal("auto blocked new agent edit")
	}
	s.SetUserOffering(scope, "o", params("quality", `"human"`))
	s.SetAgentOffering(scope, "o", params("quality", `"agent"`), map[string]string{"quality": "reason"})
	if s.OfferingStates(scope, "o")["quality"].Source != "user" {
		t.Fatal("agent replaced fixed user ownership")
	}
}

func TestBoundedAgentSuggestionNeverSeparatesItsReasonFromValue(t *testing.T) {
	s := agentparams.NewPersistentParameterStore(nil, nil)
	scope := agentparams.ScopeKey("owner", "s")
	s.SetAgentOffering(scope, "o", params("quality", `"old"`), map[string]string{"quality": "old reason"})
	if err := s.SetAgentOffering(scope, "o", params("quality", `"new"`), map[string]string{"quality": strings.Repeat("x", 4096)}); err == nil {
		t.Fatal("bounded rejection reported success")
	}
	state := s.OfferingStates(scope, "o")["quality"]
	if string(state.Value) != `"old"` || state.Reason != "old reason" {
		t.Fatalf("pair split: %+v", state)
	}
}

type failingOfferingPersister struct {
	*sqlite.Repository
	failRead, failPatch bool
}

func (p *failingOfferingPersister) LoadAgentState(owner, scope string) (map[string]json.RawMessage, error) {
	if p.failRead {
		return nil, errors.New("injected read failure")
	}
	return p.Repository.LoadAgentState(owner, scope)
}
func (p *failingOfferingPersister) PatchOfferingState(owner, scope string, expected, values map[string]json.RawMessage, deleted []string) error {
	if p.failPatch {
		return errors.New("injected patch failure")
	}
	return p.Repository.PatchOfferingState(owner, scope, expected, values, deleted)
}
func TestOfferingPersistenceFailureNeverPublishesFallbackMemory(t *testing.T) {
	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "params.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	persistence := &failingOfferingPersister{Repository: repo}
	s := agentparams.NewPersistentParameterStore(persistence, nil)
	scope := agentparams.ScopeKey("owner", "s")
	if err = s.SetAgentOffering(scope, "o", params("quality", `"old"`), map[string]string{"quality": "old reason"}); err != nil {
		t.Fatal(err)
	}
	persistence.failPatch = true
	if err = s.SetAgentOffering(scope, "o", params("quality", `"new"`), map[string]string{"quality": "new reason"}); err == nil {
		t.Fatal("write error swallowed")
	}
	persistence.failRead = true
	state := s.OfferingStates(scope, "o")["quality"]
	if string(state.Value) != `"old"` || state.Reason != "old reason" {
		t.Fatalf("memory published failed write: %+v", state)
	}
	if err = s.SetUserOffering(scope, "o", params("quality", `null`)); err == nil {
		t.Fatal("read error used stale memory for mutation")
	}
}
