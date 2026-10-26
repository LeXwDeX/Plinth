package generation

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPrivateDependencyBindingCanonicalBounds(t *testing.T) {
	cases := []string{`null`, `[]`, `{}`, `{"x":null}`, `{"x":1.5}`, `{"x":1e0}`, `{"x":9223372036854775808}`, `{"x":1,"x":2}`, `{"x":true} {}`, `{"x":"` + strings.Repeat("x", 4097) + `"}`, strings.Repeat("{\"x\":", 10) + `true` + strings.Repeat("}", 10), `{"x":[` + strings.Repeat(`true,`, 32) + `true]}`, `{"x":"` + strings.Repeat("x", 17000) + `"}`}
	for _, raw := range cases {
		_, err := CanonicalDependencyBinding([]byte(raw))
		if err == nil {
			t.Fatalf("invalid canonical acceptance length=%d err=%v", len(raw), err)
		}
	}
	got, err := CanonicalDependencyBinding([]byte(` {"z":[1,true],"a":"v"} `))
	if err != nil || string(got) != `{"a":"v","z":[1,true]}` {
		t.Fatalf("canonical=%s err=%v", got, err)
	}
}

func TestTaskSourceSameSessionIsExplicitAndFrozen(t *testing.T) {
	for _, name := range []string{"same", "other", "empty", "missing-destination", "legacy"} {
		t.Run(name, func(t *testing.T) {
			job := UpstreamJob{ProviderID: "p", RemoteID: "remote"}
			sourceSession := "session"
			if name == "other" {
				sourceSession = "other"
			}
			if name == "empty" {
				sourceSession = ""
			}
			source := Aggregate{Task: Task{ID: "source", OwnerID: "owner", SessionID: sourceSession, State: TaskSucceeded, Offering: Offering{ID: "source-offering", ProviderID: "p"}}, CurrentAttempt: &Attempt{ID: "attempt", State: AttemptUpstreamSucceeded, UpstreamJob: &job}}
			p := Parameter{Name: "source", Type: "task", SameSession: name != "legacy"}
			m := &Module{repository: dependencySourceRepository{source: source}, providers: &dependencySourceRegistry{port: &dependencyProjection{job: job}}}
			destination := "session"
			if name == "missing-destination" {
				destination = ""
			}
			deps, err := m.resolveTaskDependencies(context.Background(), "owner", Offering{Parameters: []Parameter{p}}, []byte(`{"source":"source"}`), destination)
			valid := name == "same" || name == "legacy"
			if (err == nil) != valid {
				t.Fatalf("err=%v", err)
			}
			if valid {
				frozen := snapshotPreparedDependencies(deps)
				want := ""
				if name == "same" {
					want = "session"
				}
				if frozen[0].SourceSessionID != want {
					t.Fatal("session binding mismatch")
				}
			}
		})
	}
}

func TestLegacyDependencyBytesAndPreparedHashRemainCompatible(t *testing.T) {
	dep := PreparedInputDependency{Parameter: "source", TaskID: "source", Capability: TextTo3D, OfferingID: "o", ProviderID: "p", RemoteID: "r", UpstreamProviderID: "p", OutputAssets: []PreparedDependencyAsset{}}
	raw, err := json.Marshal(dep)
	if err != nil {
		t.Fatal(err)
	}
	old := `{"parameter":"source","task_id":"source","capability":"text-to-3d","offering_id":"o","provider_id":"p","remote_id":"r","upstream_provider_id":"p","output_assets":[]}`
	if string(raw) != old {
		t.Fatalf("legacy serialized dependency changed: %s", raw)
	}
	p := PreparedInput{OwnerID: "owner", Dependencies: []PreparedInputDependency{dep}}
	before, err := hashPreparedInput(p)
	if err != nil {
		t.Fatal(err)
	}
	var restored PreparedInputDependency
	if err := json.Unmarshal([]byte(old), &restored); err != nil {
		t.Fatal(err)
	}
	p.Dependencies = []PreparedInputDependency{restored}
	after, _ := hashPreparedInput(p)
	if after != before {
		t.Fatal("legacy hash changed")
	}
	p.Dependencies[0].ProviderBinding = json.RawMessage(`{"candidate":"a"}`)
	a, _ := hashPreparedInput(p)
	p.Dependencies[0].ProviderBinding = json.RawMessage(`{"candidate":"b"}`)
	b, _ := hashPreparedInput(p)
	if a == b || a == before {
		t.Fatal("private binding not hashed")
	}
	deps := snapshotPreparedDependencies(map[string]TaskDependency{"source": {ProviderBinding: json.RawMessage(`{"candidate":"a"}`)}})
	clone := append(json.RawMessage(nil), deps[0].ProviderBinding...)
	if !reflect.DeepEqual(clone, deps[0].ProviderBinding) {
		t.Fatal("binding snapshot lost")
	}
}

type changingPrivateBinding struct {
	dependencyProjection
	binding      json.RawMessage
	bindingCalls int
}

func (p *changingPrivateBinding) ResolveDependencyBinding(DependencyBindingRequest) (json.RawMessage, error) {
	p.bindingCalls++
	return append(json.RawMessage(nil), p.binding...), nil
}

func TestPrivateBindingPrepareSubmitAndLeaseRejectMutationBeforeDispatch(t *testing.T) {
	for _, name := range []string{"unchanged", "binding-changed", "missing-binding", "malformed-binding", "session-changed", "source-attempt-changed", "missing-port"} {
		t.Run(name, func(t *testing.T) {
			job := UpstreamJob{ProviderID: "meshy", RemoteID: "remote"}
			repo := &reviewSessionRepository{assetDependencyPreparedRepository: assetDependencyPreparedRepository{dependencySourceRepository: dependencySourceRepository{source: Aggregate{Task: Task{ID: "source", OwnerID: "owner", SessionID: "destination", State: TaskSucceeded, Capability: TextTo3D, Offering: Offering{ID: "prototype", ProviderID: "meshy"}}, CurrentAttempt: &Attempt{ID: "attempt", State: AttemptUpstreamSucceeded, UpstreamJob: &job}}}}}
			port := &changingPrivateBinding{dependencyProjection: dependencyProjection{job: job}, binding: json.RawMessage(`{"candidate":"a"}`)}
			offering := Offering{ID: "build", Revision: "new", ProviderID: "meshy", Capability: Rigging, Parameters: []Parameter{{Name: "source", Type: "task", SameSession: true, TaskSourceBinding: "provider", Required: true, SameProvider: true, TaskOfferings: []OfferingID{"prototype"}}}}
			registry := &dependencySourceRegistry{port: port}
			m, err := New(Dependencies{Repository: repo, Catalog: assetDependencyCatalog{offering: offering}, Providers: registry, Assets: &taskAssetReadStore{body: "unused"}})
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := m.PrepareInput(context.Background(), PrepareInputCommand{OwnerID: "owner", SessionID: "destination", Capability: Rigging, OfferingID: offering.ID, Input: []byte(`{"source":"source"}`)})
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "binding-changed":
				port.binding = []byte(`{"candidate":"b"}`)
			case "missing-binding":
				port.binding = nil
			case "malformed-binding":
				port.binding = []byte(`{"x":null}`)
			case "session-changed":
				repo.source.Task.SessionID = "other"
			case "source-attempt-changed":
				repo.source.CurrentAttempt.State = AttemptRunning
			case "missing-port":
				registry.port = &dependencyProjection{job: job}
			}
			_, err = m.SubmitPrepared(context.Background(), SubmitPreparedCommand{OwnerID: "owner", SessionID: "destination", PreparedInputID: prepared.ID, IdempotencyKey: "step", AuthorizationRef: "approved", OperationID: "op"})
			if (err == nil) != (name == "unchanged") {
				t.Fatalf("error=%v", err)
			}
			if name != "unchanged" && repo.creates != 0 {
				t.Fatal("changed source admitted Task")
			}
			// The leased-start admission guard uses the exact same snapshot comparison.
			deps, resolveErr := m.resolveTaskDependencies(context.Background(), "owner", offering, []byte(`{"source":"source"}`), "destination")
			if name == "binding-changed" && resolveErr == nil && reflect.DeepEqual(snapshotPreparedDependencies(deps), prepared.Dependencies) {
				t.Fatal("leased comparison lost binding mutation")
			}
		})
	}
}
