package generation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type dependencySourceRepository struct {
	Repository
	source Aggregate
	err    error
}

func (r dependencySourceRepository) Load(context.Context, TaskID) (Aggregate, error) {
	return r.source, r.err
}

type dependencySourceRegistry struct {
	port     ProviderPort
	missing  bool
	offering Offering
}

func (r *dependencySourceRegistry) Provider(o Offering) (ProviderPort, bool) {
	r.offering = o
	return r.port, !r.missing
}

type dependencyProjection struct {
	ProviderPort
	job   UpstreamJob
	err   error
	calls int
}

func (p *dependencyProjection) ResolveDependencyJob(r DependencyJobRequest) (UpstreamJob, error) {
	p.calls++
	// The kernel gives the adapter a copy of opaque checkpoint bytes.
	if len(r.Opaque) > 0 {
		r.Opaque[0] = '!'
	}
	return p.job, p.err
}

func TestDependencyResolutionValidatesSourceAndProjection(t *testing.T) {
	for _, tc := range []string{"valid", "legacy attempt state", "failed source", "pending source", "unknown source", "other owner", "wrong capability", "wrong provider", "failed attempt", "pending attempt", "unknown attempt", "missing adapter", "empty projection", "wrong projection provider", "bad source job provider", "resolver error"} {
		t.Run(tc, func(t *testing.T) {
			job := UpstreamJob{ProviderID: "source-provider", RemoteID: "private-preview"}
			source := Aggregate{Task: Task{ID: "source", OwnerID: "owner", State: TaskSucceeded, Capability: TextTo3D, Offering: Offering{ID: "frozen-source", ProviderID: "source-provider", AdapterKey: "source-adapter"}}, CurrentAttempt: &Attempt{State: AttemptUpstreamSucceeded, UpstreamJob: &job, Opaque: json.RawMessage(`{"checkpoint":true}`)}}
			repo := dependencySourceRepository{source: source}
			projection := &dependencyProjection{job: UpstreamJob{ProviderID: "source-provider", RemoteID: "private-delivered"}}
			registry := &dependencySourceRegistry{port: projection}
			switch tc {
			case "legacy attempt state":
				repo.source.CurrentAttempt.State = ""
			case "failed source":
				repo.source.Task.State = TaskFailed
			case "pending source":
				repo.source.Task.State = TaskRunning
			case "unknown source":
				repo.err = RepositoryNotFound
			case "other owner":
				repo.source.Task.OwnerID = "other"
			case "wrong capability":
				repo.source.Task.Capability = TextToImage
			case "wrong provider":
				repo.source.Task.Offering.ProviderID = "other"
			case "failed attempt":
				repo.source.CurrentAttempt.State = AttemptFailed
			case "pending attempt":
				repo.source.CurrentAttempt.State = AttemptRunning
			case "unknown attempt":
				repo.source.CurrentAttempt.State = AttemptSubmissionUnknown
			case "missing adapter":
				registry.missing = true
			case "empty projection":
				projection.job.RemoteID = "  "
			case "wrong projection provider":
				projection.job.ProviderID = "other"
			case "bad source job provider":
				repo.source.CurrentAttempt.UpstreamJob.ProviderID = "other"
			case "resolver error":
				projection.err = errors.New("private remote ID in provider error")
			}
			m := &Module{repository: repo, providers: registry}
			deps, err := m.resolveTaskDependencies(context.Background(), "owner", Offering{ProviderID: "source-provider", AdapterKey: "destination-adapter", Parameters: []Parameter{{Name: "source", Type: "task", TaskCapabilities: []Capability{TextTo3D}, SameProvider: true}}}, json.RawMessage(`{"source":"source"}`), "")
			if tc == "valid" || tc == "legacy attempt state" {
				if err != nil || deps["source"].UpstreamJob.RemoteID != "private-delivered" || registry.offering.AdapterKey != "source-adapter" {
					t.Fatalf("deps=%#v offering=%#v err=%v", deps, registry.offering, err)
				}
			} else {
				want := ErrInvalidInput
				if tc == "missing adapter" {
					want = ErrUnavailable
				}
				if !IsCode(err, want) || strings.Contains(err.Error(), "private") {
					t.Fatalf("unsafe/wrong error=%v", err)
				}
			}
			if string(repo.source.CurrentAttempt.Opaque) != `{"checkpoint":true}` {
				t.Fatal("opaque mutated")
			}
		})
	}
}

func TestAdmissionGuardMissingPortCannotBypassPreparedOrInMemoryAdmission(t *testing.T) {
	for _, tc := range []struct {
		name     string
		task     Task
		prepared bool
		fail     bool
	}{
		{"legacy", Task{}, false, false},
		{"in-memory admission", Task{Admission: &ExecutionAdmission{}}, false, true},
		{"prepared repository", Task{}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &Module{repository: dependencySourceRepository{}}
			if tc.prepared {
				m.preparedStore = unimplementedPreparedStore{}
			}
			err := m.verifyAdmittedDependencies(context.Background(), tc.task, nil)
			if (err != nil) != tc.fail {
				t.Fatalf("error=%v", err)
			}
			if tc.fail && !IsCode(err, ErrUnavailable) {
				t.Fatalf("missing port must remain recoverable, got %v", err)
			}
		})
	}
}

type unimplementedPreparedStore struct{ PreparedInputStore }

type fixedClock time.Time

func (c fixedClock) Now() time.Time { return time.Time(c) }

// #325: past the Schema's declared age the Provider no longer keeps the
// referenced job, so preparation, a prepared submission and dispatch all
// refuse it before anything reaches the upstream, with the expiry as cause.
func TestDependencyResolutionRefusesAnExpiredUpstreamJob(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		age     time.Duration
		maxAge  int64
		expired bool
	}{
		{"fresh", 47 * time.Hour, 172800, false},
		{"expired", 48 * time.Hour, 172800, true},
		{"undeclared", 30 * 24 * time.Hour, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := UpstreamJob{ProviderID: "source-provider", RemoteID: "private-preview"}
			source := Aggregate{Task: Task{ID: "source", OwnerID: "owner", State: TaskSucceeded, Capability: TextTo3D, CreatedAt: now.Add(-tc.age),
				Offering: Offering{ID: "frozen-source", ProviderID: "source-provider", AdapterKey: "source-adapter"}},
				CurrentAttempt: &Attempt{State: AttemptUpstreamSucceeded, UpstreamJob: &job}}
			registry := &dependencySourceRegistry{port: &dependencyProjection{job: job}}
			m := &Module{repository: dependencySourceRepository{source: source}, providers: registry, clock: fixedClock(now)}
			_, err := m.resolveTaskDependencies(context.Background(), "owner", Offering{ProviderID: "source-provider", Parameters: []Parameter{{
				Name: "source", Type: "task", TaskCapabilities: []Capability{TextTo3D}, SameProvider: true, UpstreamJobMaxAgeSeconds: tc.maxAge,
			}}}, json.RawMessage(`{"source":"source"}`), "")
			if tc.expired {
				if !IsCode(err, ErrInvalidInput) || !errors.Is(err, ErrSourceExpired) || strings.Contains(err.Error(), "private") {
					t.Fatalf("err = %v; want ErrInvalidInput wrapping ErrSourceExpired", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
		})
	}
}
