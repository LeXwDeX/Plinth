package generation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// Retention is an offline, explicitly approved database operation. It never
// invokes a Provider or changes a surviving public task's state.
const RetentionManifestVersion = 1

var ErrRetentionReceiptNotFound = errors.New("retention receipt not found")

var ErrRetentionChanged = errors.New("retention snapshot changed; prepare and approve a new manifest")

type RetentionEntity struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	State     string `json:"state,omitempty"`
	Time      string `json:"time"`
	Protected bool   `json:"protected"`
}

func (e RetentionEntity) Key() string { return e.Kind + ":" + e.ID }

type RetentionReference struct {
	From   string `json:"from"` // empty means a row outside the deletion closure
	To     string `json:"to"`
	Reason string `json:"reason"`
}

type RetentionFile struct {
	Key    string `json:"key"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

type RetentionBlob struct {
	File   RetentionFile
	Groups []string // empty group protects the blob independently of candidates
}

type RetentionInventory struct {
	SnapshotHash string
	SchemaHash   string
	Entities     []RetentionEntity
	References   []RetentionReference
	Blobs        []RetentionBlob
}

type RetentionDecision struct {
	Kind    string   `json:"kind"`
	ID      string   `json:"id"`
	Reasons []string `json:"reasons"`
}

type RetentionManifest struct {
	Version        int                                `json:"version"`
	Database       string                             `json:"database"`
	Storage        string                             `json:"storage"`
	DatabaseDevice uint64                             `json:"database_device"`
	DatabaseInode  uint64                             `json:"database_inode"`
	StorageDevice  uint64                             `json:"storage_device"`
	StorageInode   uint64                             `json:"storage_inode"`
	Policy         schemacontract.TaskRetentionPolicy `json:"policy"`
	Cutoff         string                             `json:"cutoff"`
	SnapshotHash   string                             `json:"snapshot_sha256"`
	SchemaHash     string                             `json:"schema_sha256"`
	Tasks          []string                           `json:"task_ids"`
	Inputs         []string                           `json:"input_ids"`
	Files          []RetentionFile                    `json:"delete_files"`
	BackupFiles    []RetentionFile                    `json:"backup_files"`
	Retained       []RetentionDecision                `json:"retained"`
}

func (m RetentionManifest) Digest() (string, error) {
	b, e := json.Marshal(m)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

// PlanRetention computes a fixed point. If an external reference retains A,
// references from A retain B on the next pass; candidate-owned rows do not
// retain their own deletion group. Shared CAS bytes require zero survivors.
func PlanRetention(in RetentionInventory, policy schemacontract.TaskRetentionPolicy, cutoff, now time.Time) (RetentionManifest, error) {
	m := RetentionManifest{Version: RetentionManifestVersion, Policy: policy, Cutoff: cutoff.UTC().Format(time.RFC3339Nano), SnapshotHash: in.SnapshotHash, SchemaHash: in.SchemaHash, Tasks: []string{}, Inputs: []string{}, Files: []RetentionFile{}, BackupFiles: []RetentionFile{}, Retained: []RetentionDecision{}}
	if err := policy.Validate(); err != nil {
		return m, err
	}
	if !policy.Enabled {
		return m, errors.New("task retention is disabled")
	}
	if cutoff.IsZero() || now.IsZero() || cutoff.After(now.Add(-time.Duration(policy.MinimumAgeSeconds)*time.Second)) {
		return m, errors.New("explicit cutoff violates retention minimum age")
	}
	selected := map[string]bool{}
	reasons := map[string][]string{}
	known := map[string]bool{}
	for _, e := range in.Entities {
		k := e.Key()
		if known[k] || e.ID == "" || (e.Kind != "task" && e.Kind != "input") {
			return m, errors.New("invalid retention entity")
		}
		known[k] = true
		stamp, err := time.Parse(time.RFC3339Nano, e.Time)
		if err != nil {
			reasons[k] = []string{"missing_or_invalid_retention_time"}
			continue
		}
		switch {
		case e.Protected:
			reasons[k] = []string{"active_or_incomplete"}
		case !stamp.Before(cutoff):
			reasons[k] = []string{"not_before_cutoff"}
		case e.Kind == "task" && !slices.Contains(policy.TaskStates, e.State):
			reasons[k] = []string{"state_outside_policy"}
		default:
			selected[k] = true
		}
	}
	for _, r := range in.References {
		if !known[r.To] || (r.From != "" && !known[r.From]) {
			return m, errors.New("incomplete retention reference inventory")
		}
	}
	for changed := true; changed; {
		changed = false
		for _, r := range in.References {
			if selected[r.To] && !selected[r.From] {
				delete(selected, r.To)
				reasons[r.To] = append(reasons[r.To], r.Reason)
				changed = true
			}
		}
	}
	for _, e := range in.Entities {
		if selected[e.Key()] {
			if e.Kind == "task" {
				m.Tasks = append(m.Tasks, e.ID)
			} else {
				m.Inputs = append(m.Inputs, e.ID)
			}
		} else {
			v := reasons[e.Key()]
			sort.Strings(v)
			m.Retained = append(m.Retained, RetentionDecision{e.Kind, e.ID, slices.Compact(v)})
		}
	}
	seen := map[string]bool{}
	for _, b := range in.Blobs {
		if b.File.Key == "" || seen[b.File.Key] || len(b.Groups) == 0 {
			return m, errors.New("invalid retention blob inventory")
		}
		seen[b.File.Key] = true
		m.BackupFiles = append(m.BackupFiles, b.File)
		remove := true
		for _, g := range b.Groups {
			if g != "" && !known[g] {
				return m, fmt.Errorf("unknown blob reference group")
			}
			if !selected[g] {
				remove = false
			}
		}
		if remove {
			m.Files = append(m.Files, b.File)
		}
	}
	sort.Strings(m.Tasks)
	sort.Strings(m.Inputs)
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Key < m.Files[j].Key })
	sort.Slice(m.BackupFiles, func(i, j int) bool { return m.BackupFiles[i].Key < m.BackupFiles[j].Key })
	sort.Slice(m.Retained, func(i, j int) bool { a, b := m.Retained[i], m.Retained[j]; return a.Kind+":"+a.ID < b.Kind+":"+b.ID })
	return m, nil
}

// Approval is supplied by an interactive human decision at the command
// boundary. An actor name or a manifest digest alone is not approval.
type RetentionApproval struct {
	ManifestHash string `json:"manifest_sha256"`
	Actor        string `json:"actor"`
	Nonce        string `json:"nonce"`
	Purpose      string `json:"purpose"`
	ApprovedAt   string `json:"approved_at"`
}

type RetentionReceipt struct {
	ManifestHash       string            `json:"manifest_sha256"`
	Approval           RetentionApproval `json:"approval"`
	BackupDirectory    string            `json:"backup_directory"`
	BackupDatabaseHash string            `json:"backup_database_sha256"`
	AfterSnapshotHash  string            `json:"after_snapshot_sha256"`
	CommittedAt        string            `json:"committed_at"`
}

// RetentionSession owns the database write reservation for its entire lifetime.
// The caller must also own the storage's exclusive maintenance lease.
type RetentionSession interface {
	Inventory(context.Context) (RetentionInventory, error)
	BackupAndVerify(context.Context, string) (string, error)
	DeleteAndRecord(context.Context, RetentionManifest, RetentionReceipt) (RetentionReceipt, error)
	Receipt(context.Context, string) (RetentionReceipt, error)
	ReserveCleanup(context.Context) error
	Close() error
}
