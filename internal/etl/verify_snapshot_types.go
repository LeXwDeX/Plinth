package etl

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

// This is a deliberately bounded projection, never a full Docker inspect:
// Config.Env, credentials and arbitrary command/configuration fields are absent.
type proofIdentity struct {
	Host           string `json:"host"`
	Daemon         string `json:"daemon"`
	ContainerID    string `json:"container_id"`
	ComposeProject string `json:"compose_project"`
	ComposeService string `json:"compose_service"`
	ImageID        string `json:"image_id"`
}
type proofMount struct {
	Type        string `json:"type"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
}
type proofBind struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
}
type proofChange struct {
	Change string `json:"change"`
	Path   string `json:"path"`
}
type proofEvidence struct {
	Format        string `json:"format"`
	CaptureEpoch  string `json:"capture_epoch"`
	CapturedAtUTC string `json:"captured_at_utc"`
	Consistency   struct {
		Kind      string `json:"kind"`
		Reference string `json:"reference"`
	} `json:"consistency"`
	Identity        proofIdentity  `json:"identity"`
	State           string         `json:"state"`
	StoragePrefix   string         `json:"storage_prefix"`
	Mounts          *[]proofMount  `json:"mounts"`
	Binds           *[]proofBind   `json:"binds"`
	Volumes         *[]string      `json:"volumes"`
	UpperDir        string         `json:"upper_dir"`
	MountInfo       *[]string      `json:"mountinfo"`
	ExportPaths     *[]string      `json:"export_paths"`
	Diff            *[]proofChange `json:"diff"`
	InventorySHA256 string         `json:"inventory_sha256"`
	ManifestSHA256  string         `json:"manifest_sha256"`
	DatabaseSHA256  string         `json:"database_sha256"`
}
type proofScope struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	ContainerPath string `json:"container_path"`
	Origin        string `json:"origin"`
	SourceRoot    string `json:"source_root"`
	SnapshotPath  string `json:"snapshot_path"`
}
type proofBindings struct {
	Format           string        `json:"format"`
	ExpectedIdentity proofIdentity `json:"expected_identity"`
	Scopes           []proofScope  `json:"scopes"`
}
type proofCriterion struct {
	ID       string   `json:"id"`
	Status   string   `json:"status"`
	Reason   string   `json:"reason"`
	Evidence []string `json:"evidence"`
}
type proofBucket struct {
	Domain    string `json:"domain"`
	Kind      string `json:"kind"`
	Reference string `json:"reference,omitempty"`
	Count     int64  `json:"count"`
}
type proofReport struct {
	Format               string             `json:"format"`
	GeneratedAtUTC       string             `json:"generated_at_utc"`
	Decision             string             `json:"decision"`
	SnapshotIdentity     string             `json:"snapshot_identity,omitempty"`
	IdentityTrusted      bool               `json:"live_source_authenticated"`
	SourceLossApproval   bool               `json:"source_loss_approval"`
	AtomicSnapshotProven bool               `json:"atomic_source_snapshot_proven"`
	Evidence             map[string]string  `json:"evidence"`
	Criteria             []proofCriterion   `json:"criteria"`
	Collections          []proofCriterion   `json:"collection_evidence"`
	Buckets              []proofBucket      `json:"reference_buckets"`
	DiagnosticRollup     []diagnosticRollup `json:"diagnostic_rollup"`
	SourceCounts         map[string]int     `json:"source_counts,omitempty"`
	ScopeTrees           map[string]string  `json:"scope_trees"`
	Boundary             string             `json:"boundary"`
	Notice               string             `json:"notice"`
}

func newProofReport(stamp string) proofReport {
	r := proofReport{Notice: "本报告不含任何源损失批准；审批权在正式生产负责人。", Format: "art-ai-snapshot-completeness-v1", GeneratedAtUTC: stamp, Decision: "STOP", Evidence: map[string]string{}, Buckets: []proofBucket{}, DiagnosticRollup: []diagnosticRollup{}, ScopeTrees: map[string]string{}, Boundary: "supplied_evidence_and_explicit_trees_only;not_source_loss_authorization"}
	for _, id := range []string{"D1", "D2", "D3", "D4"} {
		r.Collections = append(r.Collections, proofCriterion{ID: id, Status: "unknown", Reason: "not_checked", Evidence: []string{}})
	}
	for _, id := range []string{"C1", "C2", "C3", "C4", "C5", "C6"} {
		r.Criteria = append(r.Criteria, proofCriterion{ID: id, Status: "unknown", Reason: "not_checked", Evidence: []string{}})
	}
	return r
}
func (r *proofReport) criterion(id, status, reason string, refs ...string) {
	for i := range r.Criteria {
		if r.Criteria[i].ID == id {
			r.Criteria[i] = proofCriterion{ID: id, Status: status, Reason: reason, Evidence: append([]string{}, refs...)}
			return
		}
	}
}
func (r *proofReport) collection(id, status, reason string, refs ...string) {
	for i := range r.Collections {
		if r.Collections[i].ID == id {
			r.Collections[i] = proofCriterion{ID: id, Status: status, Reason: reason, Evidence: append([]string{}, refs...)}
			return
		}
	}
}
func proofRef(value string) string { return "sha256:" + diagnosticHash([]byte(value)) }
func strictProofJSON(raw []byte, target any) error {
	if !utf8.Valid(raw) {
		return errors.New("invalid_unicode")
	}
	// Bound recursion and reject duplicate decoded keys before typed decoding.
	d := json.NewDecoder(bytes.NewReader(raw))
	remaining := 100000
	var value func(int) error
	value = func(depth int) error {
		remaining--
		if depth > 64 || remaining < 0 {
			return errors.New("json_limit")
		}
		token, err := d.Token()
		if err != nil {
			return err
		}
		if delim, ok := token.(json.Delim); ok {
			switch delim {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					k, err := d.Token()
					if err != nil {
						return err
					}
					key, ok := k.(string)
					if !ok || seen[key] || key != strings.ToLower(key) {
						return errors.New("duplicate_field")
					}
					seen[key] = true
					remaining--
					if err = value(depth + 1); err != nil {
						return err
					}
				}
				end, err := d.Token()
				if err != nil || end != json.Delim('}') {
					return errors.New("invalid_json")
				}
			case '[':
				for d.More() {
					if err = value(depth + 1); err != nil {
						return err
					}
				}
				end, err := d.Token()
				if err != nil || end != json.Delim(']') {
					return errors.New("invalid_json")
				}
			default:
				return errors.New("invalid_json")
			}
		}
		return nil
	}
	if err := value(1); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing_json")
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	return nil
}
