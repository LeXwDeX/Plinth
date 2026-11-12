package chat

// The persisted 3D self-check contract (#246). One record belongs to one
// (owner, session, Task) triple. The server appends a check for every real
// read-only review call; the Agent writes the conclusion through
// record_self_check. The record is session data: it never rewrites the Task's
// terminal state and an unchecked result stays downloadable.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// Self-check check kinds. kind is what the evidence proves; tool names the
// public tool that produced it.
const (
	SelfCheckKindInspect   = "inspect"
	SelfCheckKindRender    = "render"
	SelfCheckKindCompare   = "compare"
	SelfCheckKindReference = "reference"
)

// Self-check delivery-state roles. Role is the only semantic the code reads.
const (
	SelfCheckRoleUnchecked = "unchecked"
	SelfCheckRolePassed    = "passed"
	SelfCheckRoleIssues    = "issues"
	// SelfCheckRoleUncheckable is the optional state for a model the latest
	// inspect_model could not read, for example above the limit (#281).
	SelfCheckRoleUncheckable = "uncheckable"
)

// SelfCheckMetrics is the deterministic reference comparison a
// compare_model_reference call reports. Ratios are in [0, 1]; distances are on
// a 0..255 scale. They are evidence for the Agent, never a pass/fail verdict.
type SelfCheckMetrics struct {
	SilhouetteIOU         float64 `json:"silhouette_iou"`
	BBoxAspectDelta       float64 `json:"bbox_aspect_delta"`
	DominantColorDistance float64 `json:"dominant_color_distance"`
	DominantReference     string  `json:"dominant_reference,omitempty"`
	DominantRender        string  `json:"dominant_render,omitempty"`
	ReferenceSubjectRatio float64 `json:"reference_subject_ratio"`
	RenderSubjectRatio    float64 `json:"render_subject_ratio"`
	Method                string  `json:"method,omitempty"`
}

// SelfCheckCheck is one piece of evidence the server appended. ok=false means
// the call named the model but could not finish (for example a render that
// failed); the Agent must not read that as a passed check.
type SelfCheckCheck struct {
	Round   int               `json:"round"`
	Kind    string            `json:"kind"`
	Tool    string            `json:"tool"`
	AssetID string            `json:"asset_id,omitempty"`
	Views   []string          `json:"views,omitempty"`
	Handles []string          `json:"handles,omitempty"`
	Metrics *SelfCheckMetrics `json:"metrics,omitempty"`
	// UsageFailed lists the purpose checks a successful inspect judged as
	// failed; Purpose, PurposeLabel and PurposeSource name the purpose it
	// judged against (#281). A check without a usage_check leaves them empty.
	UsageFailed   []string  `json:"usage_failed,omitempty"`
	Purpose       string    `json:"purpose,omitempty"`
	PurposeLabel  string    `json:"purpose_label,omitempty"`
	PurposeSource string    `json:"purpose_source,omitempty"`
	OK            bool      `json:"ok"`
	Error         string    `json:"error,omitempty"`
	At            time.Time `json:"at"`
}

// SelfCheckConclusion is what the Agent wrote. Status is a declared status id;
// the server derives the delivery state from its role.
type SelfCheckConclusion struct {
	Status    string   `json:"status"`
	Summary   string   `json:"summary"`
	Findings  []string `json:"findings"`
	Fixes     []string `json:"fixes"`
	Remaining []string `json:"remaining"`
	// Waiver is the Agent's reason for passing despite failed purpose checks;
	// it is kept with the conclusion so the exemption stays visible (#281).
	Waiver string `json:"waiver,omitempty"`
	// Purpose, PurposeLabel and PurposeSource are copied by the server from
	// the latest successful inspection, so the conclusion says which purpose
	// it was judged against (#281).
	Purpose       string    `json:"purpose,omitempty"`
	PurposeLabel  string    `json:"purpose_label,omitempty"`
	PurposeSource string    `json:"purpose_source,omitempty"`
	At            time.Time `json:"at"`
}

// SelfCheckRecord is the persisted record. Revision is the store's
// compare-and-swap version and is not part of the JSON document; the status
// column is a denormalised copy of the conclusion status.
type SelfCheckRecord struct {
	TaskID          string               `json:"task_id"`
	ModelAssetID    string               `json:"model_asset_id,omitempty"`
	Checks          []SelfCheckCheck     `json:"checks"`
	Conclusion      *SelfCheckConclusion `json:"conclusion,omitempty"`
	CorrectionsUsed int                  `json:"corrections_used"`
	// CorrectionKeys are the paid steps already counted, by stable operation
	// or call identity (#246). They make a correction exactly-once across a
	// restarted turn or an approval the background sweep retries.
	CorrectionKeys []string  `json:"correction_keys,omitempty"`
	Revision       int64     `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// SelfCheckCounters is the derived state the tools and the prompt report.
type SelfCheckCounters struct {
	StatusID         string
	StatusRole       string
	StatusLabel      string
	RoundsUsed       int
	RoundsLimit      int
	RendersInRound   int
	RendersLimit     int
	CorrectionsUsed  int
	CorrectionsLimit int
}

// SelfCheckTask is one finished 3D result the prompt lists. Model is the
// public model name; Background marks a result the person stopped waiting for.
type SelfCheckTask struct {
	TaskID     string
	Model      string
	Background bool
	ResultAt   time.Time
}

// selfCheckJSON is the wire shape stored in record_json. Kept explicit so the
// revision column never leaks into the document.
type selfCheckJSON struct {
	TaskID          string               `json:"task_id"`
	ModelAssetID    string               `json:"model_asset_id,omitempty"`
	Checks          []SelfCheckCheck     `json:"checks"`
	Conclusion      *SelfCheckConclusion `json:"conclusion,omitempty"`
	CorrectionsUsed int                  `json:"corrections_used"`
	CorrectionKeys  []string             `json:"correction_keys,omitempty"`
	CreatedAt       time.Time            `json:"created_at"`
	UpdatedAt       time.Time            `json:"updated_at"`
}

// EncodeSelfCheckRecord renders the record_json document. An empty check list
// is encoded as [] rather than null, so readers never branch on null.
func EncodeSelfCheckRecord(record SelfCheckRecord) (string, error) {
	checks := record.Checks
	if checks == nil {
		checks = []SelfCheckCheck{}
	}
	encoded, err := json.Marshal(selfCheckJSON{
		TaskID:          record.TaskID,
		ModelAssetID:    record.ModelAssetID,
		Checks:          checks,
		Conclusion:      record.Conclusion,
		CorrectionsUsed: record.CorrectionsUsed,
		CorrectionKeys:  record.CorrectionKeys,
		CreatedAt:       record.CreatedAt,
		UpdatedAt:       record.UpdatedAt,
	})
	if err != nil {
		return "", fmt.Errorf("encode self-check record: %w", err)
	}
	return string(encoded), nil
}

// DecodeSelfCheckRecord reads the record_json document. Revision is filled by
// the store from its column.
func DecodeSelfCheckRecord(raw string, revision int64) (SelfCheckRecord, error) {
	var document selfCheckJSON
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		return SelfCheckRecord{}, fmt.Errorf("decode self-check record: %w", err)
	}
	record := SelfCheckRecord{
		TaskID:          document.TaskID,
		ModelAssetID:    document.ModelAssetID,
		Checks:          document.Checks,
		Conclusion:      document.Conclusion,
		CorrectionsUsed: document.CorrectionsUsed,
		CorrectionKeys:  document.CorrectionKeys,
		Revision:        revision,
		CreatedAt:       document.CreatedAt,
		UpdatedAt:       document.UpdatedAt,
	}
	if record.Checks == nil {
		record.Checks = []SelfCheckCheck{}
	}
	return record, nil
}

// SelfCheckStatus returns the Schema status declaration by id.
func SelfCheckStatus(schema catalog.ChatSelfCheckSchema, id string) (catalog.ChatSelfCheckStatusSchema, bool) {
	for _, status := range schema.Statuses {
		if status.ID == id {
			return status, true
		}
	}
	return catalog.ChatSelfCheckStatusSchema{}, false
}

// SelfCheckStatusByRole returns the declared status of one role. Every role the
// code reads is required by the Catalog validator.
func SelfCheckStatusByRole(schema catalog.ChatSelfCheckSchema, role string) (catalog.ChatSelfCheckStatusSchema, bool) {
	for _, status := range schema.Statuses {
		if status.Role == role {
			return status, true
		}
	}
	return catalog.ChatSelfCheckStatusSchema{}, false
}

// SelfCheckDerivedStatus reports the delivery state of a record: its written
// conclusion's status, or the declared unchecked state.
func SelfCheckDerivedStatus(schema catalog.ChatSelfCheckSchema, record SelfCheckRecord) catalog.ChatSelfCheckStatusSchema {
	if record.Conclusion != nil {
		if status, found := SelfCheckStatus(schema, record.Conclusion.Status); found {
			return status
		}
	}
	if status, found := SelfCheckStatusByRole(schema, SelfCheckRoleUnchecked); found {
		return status
	}
	return catalog.ChatSelfCheckStatusSchema{ID: SelfCheckRoleUnchecked, Role: SelfCheckRoleUnchecked}
}

// SelfCheckCounts derives the counters every tool result and the prompt show.
func SelfCheckCounts(schema catalog.ChatSelfCheckSchema, record SelfCheckRecord) SelfCheckCounters {
	status := SelfCheckDerivedStatus(schema, record)
	rounds := 0
	lastInspect := -1
	for index, check := range record.Checks {
		if check.Kind == SelfCheckKindInspect {
			rounds++
			lastInspect = index
		}
	}
	renders := 0
	for index, check := range record.Checks {
		if index <= lastInspect {
			continue
		}
		switch check.Kind {
		case SelfCheckKindRender, SelfCheckKindCompare, SelfCheckKindReference:
			renders++
		}
	}
	return SelfCheckCounters{
		StatusID:         status.ID,
		StatusRole:       status.Role,
		StatusLabel:      status.Label,
		RoundsUsed:       rounds,
		RoundsLimit:      schema.Limits.MaxRoundsPerResult,
		RendersInRound:   renders,
		RendersLimit:     schema.Limits.MaxRendersPerRound,
		CorrectionsUsed:  record.CorrectionsUsed,
		CorrectionsLimit: schema.Limits.MaxCorrectionsPerResult,
	}
}

// SelfCheckRequiredSatisfied reports whether the record has the evidence the
// Schema requires for a passed conclusion.
func SelfCheckRequiredSatisfied(schema catalog.ChatSelfCheckSchema, record SelfCheckRecord) bool {
	inspect, renderOrReference := false, false
	for _, check := range record.Checks {
		if !check.OK {
			continue
		}
		switch check.Kind {
		case SelfCheckKindInspect:
			inspect = true
		case SelfCheckKindRender, SelfCheckKindCompare, SelfCheckKindReference:
			renderOrReference = true
		}
	}
	if schema.RequiredChecks.Inspect && !inspect {
		return false
	}
	if schema.RequiredChecks.RenderOrReference && !renderOrReference {
		return false
	}
	return true
}

// SelfCheckLatestInspect returns the record's last inspect check and the last
// successful one. Either may be absent.
func SelfCheckLatestInspect(record SelfCheckRecord) (latest, latestOK *SelfCheckCheck) {
	for index := range record.Checks {
		check := &record.Checks[index]
		if check.Kind != SelfCheckKindInspect {
			continue
		}
		latest = check
		if check.OK {
			latestOK = check
		}
	}
	return latest, latestOK
}

// SelfCheckConclusionError names a refused conclusion. Kind is one of
// unknown_status, not_writable, needs_check, usage_failed,
// uncheckable_unproven, remaining_required, remaining_forbidden or too_long.
// For usage_failed, Field lists the failed checks.
type SelfCheckConclusionError struct {
	Kind    string
	Status  string
	Field   string
	Length  int
	Maximum int
}

func (err *SelfCheckConclusionError) Error() string {
	return "chat: self-check conclusion refused: " + err.Kind
}

// ValidateSelfCheckConclusion applies the Schema's conclusion rules. The
// reason is returned as data so the tool can word it with Schema text.
func ValidateSelfCheckConclusion(
	schema catalog.ChatSelfCheckSchema,
	record SelfCheckRecord,
	conclusion SelfCheckConclusion,
) *SelfCheckConclusionError {
	status, found := SelfCheckStatus(schema, conclusion.Status)
	if !found {
		return &SelfCheckConclusionError{Kind: "unknown_status", Status: conclusion.Status}
	}
	if status.Role == SelfCheckRoleUnchecked {
		return &SelfCheckConclusionError{Kind: "not_writable", Status: conclusion.Status}
	}
	if status.Role == SelfCheckRolePassed && !SelfCheckRequiredSatisfied(schema, record) {
		return &SelfCheckConclusionError{Kind: "needs_check", Status: conclusion.Status}
	}
	latest, latestOK := SelfCheckLatestInspect(record)
	if status.Role == SelfCheckRolePassed && schema.RequiredChecks.UsageClean && latestOK != nil &&
		len(latestOK.UsageFailed) > 0 && strings.TrimSpace(conclusion.Waiver) == "" {
		return &SelfCheckConclusionError{
			Kind: "usage_failed", Status: conclusion.Status, Field: strings.Join(latestOK.UsageFailed, ", "),
		}
	}
	if status.Role == SelfCheckRoleUncheckable && (latest == nil || latest.OK) {
		return &SelfCheckConclusionError{Kind: "uncheckable_unproven", Status: conclusion.Status}
	}
	if status.Role == SelfCheckRolePassed && len(conclusion.Remaining) > 0 {
		return &SelfCheckConclusionError{Kind: "remaining_forbidden", Status: conclusion.Status}
	}
	if status.Role == SelfCheckRoleIssues && len(conclusion.Remaining) == 0 {
		return &SelfCheckConclusionError{Kind: "remaining_required", Status: conclusion.Status}
	}
	limit := schema.Conclusion
	if length := utf8.RuneCountInString(strings.TrimSpace(conclusion.Waiver)); length > limit.MaxItemCharacters {
		return &SelfCheckConclusionError{Kind: "too_long", Field: "waiver", Length: length, Maximum: limit.MaxItemCharacters}
	}
	if length := utf8.RuneCountInString(strings.TrimSpace(conclusion.Summary)); length > limit.MaxSummaryCharacters {
		return &SelfCheckConclusionError{Kind: "too_long", Field: "summary", Length: length, Maximum: limit.MaxSummaryCharacters}
	}
	for field, items := range map[string][]string{
		"findings": conclusion.Findings, "fixes": conclusion.Fixes, "remaining": conclusion.Remaining,
	} {
		if limit.MaxItems > 0 && len(items) > limit.MaxItems {
			return &SelfCheckConclusionError{Kind: "too_long", Field: field, Length: len(items), Maximum: limit.MaxItems}
		}
		for index, item := range items {
			if length := utf8.RuneCountInString(strings.TrimSpace(item)); length > limit.MaxItemCharacters {
				return &SelfCheckConclusionError{
					Kind: "too_long", Field: fmt.Sprintf("%s[%d]", field, index),
					Length: length, Maximum: limit.MaxItemCharacters,
				}
			}
		}
	}
	return nil
}
