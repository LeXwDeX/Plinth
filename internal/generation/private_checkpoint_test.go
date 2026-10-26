package generation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func privateCheckpointFixture() (Lease, *Attempt, Observation) {
	old := json.RawMessage(`{"source":"frozen","credits":1}`)
	digest := sha256.Sum256(old)
	attempt := &Attempt{ID: "attempt", TaskID: "task", State: AttemptRunning, Opaque: old, UpstreamJob: &UpstreamJob{ProviderID: "provider", RemoteID: "remote"}, LastObservationKey: "old-key", LastObservationPhase: UpstreamRunning, UpdatedAt: time.Unix(1, 0)}
	lease := Lease{Aggregate: Aggregate{Task: Task{ID: "task", State: TaskRunning, Progress: 50, CurrentAttemptID: attempt.ID, Version: 7, Offering: Offering{PollInterval: time.Second}, UpdatedAt: time.Unix(1, 0)}, CurrentAttempt: attempt}, Token: "current-lease"}
	obs := Observation{Phase: UpstreamRunning, Progress: 50, Job: &UpstreamJob{ProviderID: "provider", RemoteID: "remote"}, Opaque: json.RawMessage(`{"source":"frozen","credits":3.75}`), DedupeKey: "updated-key", CheckpointBaseSHA256: hex.EncodeToString(digest[:])}
	return lease, attempt, obs
}
func TestPrivateCheckpointUpdateHasClosedAdmission(t *testing.T) {
	lease, attempt, obs := privateCheckpointFixture()
	if !staleObservation(lease.Task, attempt, obs) || !validPrivateCheckpointUpdate(lease, attempt, obs, "provider.observe") {
		t.Fatal("private evidence must remain stale for public state but eligible for the bounded update")
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Lease, **Attempt, *Observation, *string)
	}{
		{"webhook", func(_ *Lease, _ **Attempt, _ *Observation, event *string) { *event = "provider.webhook" }},
		{"start", func(_ *Lease, _ **Attempt, _ *Observation, event *string) { *event = "provider.start" }},
		{"no-lease", func(l *Lease, _ **Attempt, _ *Observation, _ *string) { l.Token = "" }},
		{"nil-attempt", func(_ *Lease, a **Attempt, _ *Observation, _ *string) { *a = nil }},
		{"nil-current-attempt", func(l *Lease, _ **Attempt, _ *Observation, _ *string) { l.CurrentAttempt = nil }},
		{"different-current-attempt", func(l *Lease, _ **Attempt, _ *Observation, _ *string) { l.Task.CurrentAttemptID = "other" }},
		{"different-task", func(_ *Lease, a **Attempt, _ *Observation, _ *string) { (*a).TaskID = "other" }},
		{"terminal-task", func(l *Lease, _ **Attempt, _ *Observation, _ *string) { l.Task.State = TaskSucceeded }},
		{"materializing", func(l *Lease, _ **Attempt, _ *Observation, _ *string) { l.Task.State = TaskMaterializing }},
		{"queued-task", func(l *Lease, _ **Attempt, _ *Observation, _ *string) { l.Task.State = TaskQueued }},
		{"terminal-attempt", func(_ *Lease, a **Attempt, _ *Observation, _ *string) { (*a).State = AttemptFailed }},
		{"submitting-attempt", func(_ *Lease, a **Attempt, _ *Observation, _ *string) { (*a).State = AttemptSubmitting }},
		{"phase-regression", func(_ *Lease, _ **Attempt, o *Observation, _ *string) { o.Phase = UpstreamAccepted }},
		{"phase-completed", func(_ *Lease, _ **Attempt, o *Observation, _ *string) { o.Phase = UpstreamCompleted }},
		{"progress-regression", func(_ *Lease, _ **Attempt, o *Observation, _ *string) { o.Progress = 49 }},
		{"progress-advance", func(_ *Lease, _ **Attempt, o *Observation, _ *string) { o.Progress = 51 }},
		{"new-job-missing", func(_ *Lease, _ **Attempt, o *Observation, _ *string) { o.Job = nil }},
		{"old-job-missing", func(_ *Lease, a **Attempt, _ *Observation, _ *string) { (*a).UpstreamJob = nil }},
		{"changed-job", func(_ *Lease, _ **Attempt, o *Observation, _ *string) { o.Job.RemoteID = "other" }},
		{"changed-provider", func(_ *Lease, _ **Attempt, o *Observation, _ *string) { o.Job.ProviderID = "other" }},
		{"new-order-sequence", func(_ *Lease, _ **Attempt, o *Observation, _ *string) { o.Sequence = 1 }},
		{"old-order-sequence", func(_ *Lease, a **Attempt, _ *Observation, _ *string) { (*a).LastObservationSequence = 1 }},
		{"new-order-time", func(_ *Lease, _ **Attempt, o *Observation, _ *string) { o.ObservedAt = time.Unix(1, 0) }},
		{"old-order-time", func(_ *Lease, a **Attempt, _ *Observation, _ *string) { (*a).LastObservationAt = time.Unix(1, 0) }},
		{"dispatch", func(_ *Lease, _ **Attempt, o *Observation, _ *string) { o.Dispatch = &DispatchReceipt{AckToken: "ack"} }},
		{"failure", func(_ *Lease, _ **Attempt, o *Observation, _ *string) { o.Failure = &Failure{Class: FailureProtocol} }},
		{"artifacts", func(_ *Lease, _ **Attempt, o *Observation, _ *string) { o.Artifacts = []ArtifactClaim{{ID: "claim"}} }},
		{"resources", func(_ *Lease, _ **Attempt, o *Observation, _ *string) { o.ResourceUpdates = []ResourceUpdate{{}} }},
		{"wrong-base", func(_ *Lease, _ **Attempt, o *Observation, _ *string) { o.CheckpointBaseSHA256 = "wrong" }},
		{"no-base", func(_ *Lease, _ **Attempt, o *Observation, _ *string) { o.CheckpointBaseSHA256 = "" }},
		{"empty-checkpoint", func(_ *Lease, _ **Attempt, o *Observation, _ *string) { o.Opaque = nil }},
		{"invalid-checkpoint", func(_ *Lease, _ **Attempt, o *Observation, _ *string) { o.Opaque = json.RawMessage(`{`) }},
		{"same-checkpoint", func(_ *Lease, a **Attempt, o *Observation, _ *string) { o.Opaque = (*a).Opaque }},
		{"empty-key", func(_ *Lease, _ **Attempt, o *Observation, _ *string) { o.DedupeKey = "" }},
		{"same-key", func(_ *Lease, a **Attempt, o *Observation, _ *string) { o.DedupeKey = (*a).LastObservationKey }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, a, o := privateCheckpointFixture()
			event := "provider.observe"
			tc.mutate(&l, &a, &o, &event)
			if validPrivateCheckpointUpdate(l, a, o, event) {
				t.Fatal("unsafe checkpoint update admitted")
			}
		})
	}
	lease, attempt, obs = privateCheckpointFixture()
	lease.Task.Progress = 0
	attempt.LastObservationPhase = UpstreamAccepted
	obs.Phase = UpstreamAccepted
	obs.Progress = 0
	if !validPrivateCheckpointUpdate(lease, attempt, obs, "provider.observe") {
		t.Fatal("unchanged accepted checkpoint should be eligible")
	}
}

type privateCheckpointRepository struct {
	Repository
	commit Commit
	err    error
}

func (r *privateCheckpointRepository) Commit(_ context.Context, c Commit) error {
	r.commit = c
	return r.err
}

type privateCheckpointClock struct{ now time.Time }

func (c privateCheckpointClock) Now() time.Time { return c.now }
func TestPrivateCheckpointCommitOnlyChangesPrivateEvidenceAndScheduling(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "current-lease", true: "old-lease"}[conflict], func(t *testing.T) {
			lease, attempt, obs := privateCheckpointFixture()
			before := *attempt
			repository := &privateCheckpointRepository{}
			if conflict {
				repository.err = RepositoryConflict
			}
			now := time.Unix(100, 0)
			m := &Module{repository: repository, clock: privateCheckpointClock{now}}
			err := m.applyObservation(context.Background(), lease, attempt, obs, "provider.observe", nil)
			if conflict && !IsCode(err, ErrConflict) || !conflict && err != nil {
				t.Fatalf("lease conflict=%v: %v", conflict, err)
			}
			c := repository.commit
			if c.LeaseToken != lease.Token || c.ExpectedVersion != lease.Task.Version || !c.ClearLease || c.ReplaceClaims || c.PersistResources || c.DispatchReceipt != nil || len(c.Claims) != 0 || len(c.NewAssets) != 0 || len(c.Events) != 0 {
				t.Fatalf("checkpoint commit escaped fence or changed public data: %+v", c)
			}
			if c.Task.Version != lease.Task.Version+1 || c.Task.NextActionAt != now.Add(time.Second) || c.Task.UpdatedAt != now {
				t.Fatal("missing version/lease-expiry time or poll scheduling")
			}
			actualTask := c.Task
			actualTask.Version = lease.Task.Version
			actualTask.NextActionAt = lease.Task.NextActionAt
			actualTask.UpdatedAt = lease.Task.UpdatedAt
			if !reflect.DeepEqual(actualTask, lease.Task) {
				t.Fatal("public task lifecycle or metadata changed")
			}
			actualAttempt := *c.Attempt
			if !bytes.Equal(actualAttempt.Opaque, obs.Opaque) || actualAttempt.LastObservationKey != obs.DedupeKey {
				t.Fatal("private checkpoint not committed")
			}
			actualAttempt.Opaque = before.Opaque
			actualAttempt.LastObservationKey = before.LastObservationKey
			actualAttempt.UpdatedAt = before.UpdatedAt
			if !reflect.DeepEqual(actualAttempt, before) || !reflect.DeepEqual(*attempt, before) {
				t.Fatal("checkpoint update changed job/order/lifecycle or mutated caller before lease CAS")
			}
		})
	}
}
func TestPrivateCheckpointBaseNeverUsesPublicJSON(t *testing.T) {
	_, _, obs := privateCheckpointFixture()
	raw, err := json.Marshal(obs)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(obs.CheckpointBaseSHA256)) || bytes.Contains(raw, []byte("checkpoint")) {
		t.Fatalf("private checkpoint base exposed: %s", raw)
	}
	var decoded Observation
	if json.Unmarshal([]byte(`{"checkpoint_base_sha256":"forged","CheckpointBaseSHA256":"forged"}`), &decoded) != nil || decoded.CheckpointBaseSHA256 != "" {
		t.Fatal("public JSON supplied private checkpoint authority")
	}
}
