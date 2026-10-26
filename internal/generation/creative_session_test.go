package generation

import (
	"context"
	"encoding/json"
	"testing"
)

type reviewSessionRepository struct {
	assetDependencyPreparedRepository
}

func (r *reviewSessionRepository) OwnsSession(_ context.Context, owner, session string) (bool, error) {
	return owner == "owner" && session == "destination", nil
}
func TestReviewCreativeTaskSourceSameSessionMustReject(t *testing.T) {
	job := UpstreamJob{ProviderID: "meshy", RemoteID: "prototype-remote"}
	repo := &reviewSessionRepository{assetDependencyPreparedRepository: assetDependencyPreparedRepository{dependencySourceRepository: dependencySourceRepository{source: Aggregate{Task: Task{ID: "source", OwnerID: "owner", SessionID: "other-session", State: TaskSucceeded, Capability: "creative-lab-prototype", Offering: Offering{ID: "prototype", ProviderID: "meshy"}}, CurrentAttempt: &Attempt{State: AttemptUpstreamSucceeded, UpstreamJob: &job}}}}}
	var p Parameter
	if err := json.Unmarshal([]byte(`{"name":"source_task","type":"task","required":true,"same_provider":true,"same_session":true,"task_source_kind":"upstream_job","task_capabilities":["creative-lab-prototype"],"task_offerings":["prototype"]}`), &p); err != nil {
		t.Fatal(err)
	}
	destination := Offering{ID: "build", ProviderID: "meshy", Capability: "creative-lab-build", Parameters: []Parameter{p}}
	registry := &dependencySourceRegistry{port: &dependencyProjection{job: job}}
	m, err := New(Dependencies{Repository: repo, Catalog: assetDependencyCatalog{offering: destination}, Providers: registry, Assets: &taskAssetReadStore{body: "unused"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.PrepareInput(context.Background(), PrepareInputCommand{OwnerID: "owner", SessionID: "destination", Capability: "creative-lab-build", OfferingID: "build", Input: json.RawMessage(`{"source_task":"source"}`)})
	if !IsCode(err, ErrInvalidInput) {
		t.Fatalf("same_session source from another conversation was frozen: %v", err)
	}
}
