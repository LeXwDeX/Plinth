package generation

import (
	"context"
	"encoding/json"
	"testing"
)

// branchScopeRepository is the pre-#186 dependency fixture plus the read-only
// branch scope the SQLite repository now provides. It deliberately leaves the
// stored source Task in its ancestor session: only the dependency the Provider
// sees is bound to the requesting session.
type branchScopeRepository struct {
	assetDependencyPreparedRepository
	allowed map[string]bool
}

func (r *branchScopeRepository) OwnsSession(_ context.Context, owner, session string) (bool, error) {
	return owner == "owner", nil
}

func (r *branchScopeRepository) TaskInSession(_ context.Context, owner OwnerID, session string, task TaskID) (bool, error) {
	return owner == "owner" && r.allowed[session+"\x00"+string(task)], nil
}

func branchSourceFixture(t *testing.T) (Offering, *branchScopeRepository, ProviderRegistry, UpstreamJob) {
	t.Helper()
	job := UpstreamJob{ProviderID: "meshy", RemoteID: "prototype-remote"}
	repository := &branchScopeRepository{
		assetDependencyPreparedRepository: assetDependencyPreparedRepository{
			dependencySourceRepository: dependencySourceRepository{source: Aggregate{
				Task: Task{ID: "source", OwnerID: "owner", SessionID: "parent",
					State: TaskSucceeded, Capability: "creative-lab-prototype",
					Offering: Offering{ID: "prototype", ProviderID: "meshy"}},
				CurrentAttempt: &Attempt{State: AttemptUpstreamSucceeded, UpstreamJob: &job},
			}},
		},
		allowed: map[string]bool{},
	}
	var parameter Parameter
	if err := json.Unmarshal([]byte(`{"name":"source_task","type":"task","required":true,"same_provider":true,"same_session":true,"task_source_kind":"upstream_job","task_capabilities":["creative-lab-prototype"],"task_offerings":["prototype"]}`), &parameter); err != nil {
		t.Fatal(err)
	}
	destination := Offering{ID: "build", ProviderID: "meshy", Capability: "creative-lab-build",
		Parameters: []Parameter{parameter}}
	return destination, repository, &dependencySourceRegistry{port: &dependencyProjection{job: job}}, job
}

func TestBranchAncestorTaskIsFrozenUnderTheBranchEffectiveSession(t *testing.T) {
	for _, test := range []struct {
		name    string
		session string
		allowed bool
	}{
		{"direct parent", "branch", true},
		{"grandparent", "grandbranch", true},
		{"sibling", "sibling", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			destination, repository, registry, _ := branchSourceFixture(t)
			if test.allowed {
				repository.allowed[test.session+"\x00source"] = true
			}
			module, err := New(Dependencies{
				Repository: repository,
				Catalog:    assetDependencyCatalog{offering: destination},
				Providers:  registry,
				Assets:     &taskAssetReadStore{body: "unused"},
			})
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := module.PrepareInput(context.Background(), PrepareInputCommand{
				OwnerID: "owner", SessionID: test.session, Capability: "creative-lab-build",
				OfferingID: "build", Input: json.RawMessage(`{"source_task":"source"}`),
			})
			if test.allowed {
				if err != nil {
					t.Fatalf("ancestor source was not frozen: %v", err)
				}
				if len(prepared.Dependencies) != 1 || prepared.Dependencies[0].TaskID != "source" ||
					prepared.Dependencies[0].SourceSessionID != test.session {
					t.Fatalf("dependency effective session = %+v", prepared.Dependencies)
				}
				if repository.source.Task.SessionID != "parent" {
					t.Fatalf("stored ancestor task session changed: %q", repository.source.Task.SessionID)
				}
				return
			}
			if !IsCode(err, ErrInvalidInput) {
				t.Fatalf("sibling source was accepted: %v", err)
			}
		})
	}
}

func TestBranchSubmitAcceptsAnAncestorSourceTask(t *testing.T) {
	for _, test := range []struct {
		name    string
		session string
		allowed bool
	}{
		{"direct parent", "branch", true},
		{"grandparent", "grandbranch", true},
		{"sibling", "sibling", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			destination, repository, registry, _ := branchSourceFixture(t)
			if test.allowed {
				repository.allowed[test.session+"\x00source"] = true
			}
			module, err := New(Dependencies{
				Repository: repository,
				Catalog:    assetDependencyCatalog{offering: destination},
				Providers:  registry,
				Assets:     &taskAssetReadStore{body: "unused"},
			})
			if err != nil {
				t.Fatal(err)
			}
			accepted, err := module.Submit(context.Background(), SubmitCommand{
				OwnerID: "owner", SessionID: test.session, Capability: "creative-lab-build",
				OfferingID: "build", Input: json.RawMessage(`{"source_task":"source"}`),
				IdempotencyKey: "branch-submit-key",
			})
			if test.allowed {
				if err != nil || accepted.TaskID == "" || repository.creates != 1 {
					t.Fatalf("ancestor source submit = %+v creates=%d err=%v", accepted, repository.creates, err)
				}
				return
			}
			if !IsCode(err, ErrInvalidInput) || repository.creates != 0 {
				t.Fatalf("sibling source submit = %+v creates=%d err=%v", accepted, repository.creates, err)
			}
		})
	}
}

func TestBranchScopeDoesNotWidenWithoutRefs(t *testing.T) {
	destination, repository, registry, _ := branchSourceFixture(t)
	module, err := New(Dependencies{
		Repository: repository,
		Catalog:    assetDependencyCatalog{offering: destination},
		Providers:  registry,
		Assets:     &taskAssetReadStore{body: "unused"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := module.PrepareInput(context.Background(), PrepareInputCommand{
		OwnerID: "owner", SessionID: "branch", Capability: "creative-lab-build",
		OfferingID: "build", Input: json.RawMessage(`{"source_task":"source"}`),
	}); !IsCode(err, ErrInvalidInput) {
		t.Fatalf("source without a branch ref was accepted: %v", err)
	}
	if allowed, err := module.TaskInSession(context.Background(), "owner", "branch", "source"); err != nil || allowed {
		t.Fatalf("unscoped TaskInSession = %v err=%v", allowed, err)
	}
}
