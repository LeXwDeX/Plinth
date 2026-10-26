package generation

import (
	"encoding/json"
	"github.com/LeXwDeX/Plinth/internal/schema"
	"reflect"
	"testing"
	"time"
)

func retentionPolicy() schema.TaskRetentionPolicy {
	return schema.TaskRetentionPolicy{Enabled: true, MinimumAgeSeconds: 1, TaskStates: []string{"SUCCEEDED", "FAILED", "CANCELED"}, TaskTimeField: "finished_at", InputTimeField: "created_at", ReferenceMode: "preserve_all"}
}
func TestRetentionFixedPointAndSharedFiles(t *testing.T) {
	in := RetentionInventory{SnapshotHash: "snapshot", SchemaHash: "schema", Entities: []RetentionEntity{
		{Kind: "task", ID: "a", State: "SUCCEEDED", Time: "2020-01-01T00:00:00Z"},
		{Kind: "task", ID: "b", State: "FAILED", Time: "2020-01-01T00:00:00Z"},
		{Kind: "task", ID: "c", State: "CANCELED", Time: "2020-01-01T00:00:00Z"},
		{Kind: "input", ID: "d", Time: "2020-01-01T00:00:00Z"},
	}, References: []RetentionReference{{From: "task:b", To: "task:a", Reason: "depends"}, {From: "", To: "task:b", Reason: "retained audit"}, {From: "task:c", To: "task:c", Reason: "own attempt"}}, Blobs: []RetentionBlob{{File: RetentionFile{Key: "shared"}, Groups: []string{"task:a", "task:c"}}, {File: RetentionFile{Key: "unique"}, Groups: []string{"input:d"}}}}
	m, e := PlanRetention(in, retentionPolicy(), time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC), time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(m.Tasks, []string{"c"}) || !reflect.DeepEqual(m.Inputs, []string{"d"}) || len(m.Files) != 1 || m.Files[0].Key != "unique" || len(m.BackupFiles) != 2 {
		t.Fatalf("unsafe plan: %+v", m)
	}
	b, _ := json.Marshal(m)
	h, _ := m.Digest()
	if _, e = DecodeRetentionManifest(b, h); e != nil {
		t.Fatal(e)
	}
	for _, bad := range [][]byte{append(b, '\n'), append([]byte(`{"version":1,`), b[1:]...)} {
		if _, e = DecodeRetentionManifest(bad, h); e == nil {
			t.Fatal("noncanonical or duplicate fields accepted")
		}
	}
	m.Tasks = append(m.Tasks, "a")
	b, _ = json.Marshal(m)
	if _, e = DecodeRetentionManifest(b, h); e == nil {
		t.Fatal("modified plan accepted under old hash")
	}
}
func TestRetentionRefusesDisabledPolicyAndFreshCutoff(t *testing.T) {
	p := retentionPolicy()
	p.Enabled = false
	if _, e := PlanRetention(RetentionInventory{}, p, time.Now().Add(-time.Hour), time.Now()); e == nil {
		t.Fatal("disabled policy accepted")
	}
	p.Enabled = true
	if _, e := PlanRetention(RetentionInventory{}, p, time.Now(), time.Now()); e == nil {
		t.Fatal("fresh cutoff accepted")
	}
}
