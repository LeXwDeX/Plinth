package generation

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/harness"
)

type PreparedInputID string

// PrepareInputCommand requests validation and an immutable input snapshot. It
// does not authorize a paid Task or reserve an admission slot.
type PrepareInputCommand struct {
	ParameterReasons    map[string]string
	OwnerID             OwnerID
	SessionID           string
	Capability          Capability
	OfferingID          OfferingID
	Input               json.RawMessage
	SceneID             string
	ManifestID          string
	ManifestHash        string
	SourceSceneObjectID string
	SourceSceneRevision int64
	// KeepFirstReferences lets a direct request (the task API, #329 follow-up)
	// give a reference-image list longer than the Offering takes: the first
	// pictures are kept and PreparedInput.ReferenceNote says so. Unset, such
	// an input is refused as before (Agent preparations have already cut it;
	// retries of a frozen snapshot must not change it).
	KeepFirstReferences bool
}

type PreparedInputAsset struct {
	Parameter string  `json:"parameter"`
	Index     int     `json:"index"`
	ID        AssetID `json:"id"`
	SHA256    string  `json:"sha256"`
	Size      int64   `json:"size"`
	MediaType string  `json:"media_type"`
	Role      string  `json:"role"`
}

// RemoteID freezes the adapter-resolved delivered result reference. Keep this
// serialized shape stable so historical prepared-input hashes remain readable.
type PreparedInputDependency struct {
	SourceSessionID    string                    `json:"source_session_id,omitempty"`
	ProviderBinding    json.RawMessage           `json:"provider_binding,omitempty"`
	Parameter          string                    `json:"parameter"`
	TaskID             TaskID                    `json:"task_id"`
	Capability         Capability                `json:"capability"`
	OfferingID         OfferingID                `json:"offering_id"`
	ProviderID         string                    `json:"provider_id"`
	RemoteID           string                    `json:"remote_id"`
	UpstreamProviderID string                    `json:"upstream_provider_id"`
	OutputAssets       []PreparedDependencyAsset `json:"output_assets"`
}

type PreparedDependencyAsset struct {
	ID        AssetID `json:"id"`
	Role      string  `json:"role"`
	Variant   string  `json:"variant,omitempty"`
	Ordinal   int     `json:"ordinal"`
	SHA256    string  `json:"sha256"`
	Size      int64   `json:"size"`
	MediaType string  `json:"media_type"`
}

// ParameterAdjustment is immutable approval evidence for a changed advised
// value. It contains public product text and typed values, never wire settings.
type ParameterAdjustment struct {
	Name             string          `json:"name"`
	Label            string          `json:"label"`
	RecommendedValue json.RawMessage `json:"recommended_value"`
	Value            json.RawMessage `json:"value"`
	Reason           string          `json:"reason"`
	Advice           string          `json:"advice"`
	Source           string          `json:"source"`
}

// ParameterReason associates a session explanation with its exact value.
type ParameterReason struct {
	Value  json.RawMessage
	Reason string
}

// ReferenceLimit says a preparation kept the first Count of Total reference
// pictures because Model takes no more; Template is the Schema text
// (runtime.generation_messages.reference_limit, or reference_limit_one when
// Count is 1) the note is written from.
type ReferenceLimit struct {
	Template string
	Model    string
	Count    int
	Total    int
}

// Note writes the limit with a template (the Schema text or its translation).
func (limit ReferenceLimit) Note(template string) string {
	return strings.NewReplacer("{model}", limit.Model, "{count}", strconv.Itoa(limit.Count),
		"{total}", strconv.Itoa(limit.Total)).Replace(template)
}

type PreparedInput struct {
	// ReferenceLimit is set when a KeepFirstReferences preparation kept only
	// the first reference pictures; ReferenceNote is its Schema note. Both are
	// answered once and not stored.
	ReferenceLimit              *ReferenceLimit           `json:"-"`
	ReferenceNote               string                    `json:"-"`
	NativeQuote                 *NativeQuote              `json:"native_quote,omitempty"`
	ParameterAdjustments        []ParameterAdjustment     `json:"parameter_adjustments,omitempty"`
	ID                          PreparedInputID           `json:"id"`
	OwnerID                     OwnerID                   `json:"owner_id"`
	SessionID                   string                    `json:"session_id"`
	Capability                  Capability                `json:"capability"`
	OfferingID                  OfferingID                `json:"offering_id"`
	OfferingRevision            string                    `json:"offering_revision"`
	SchemaVersion               string                    `json:"schema_version"`
	NormalizedInput             json.RawMessage           `json:"normalized_input"`
	Assets                      []PreparedInputAsset      `json:"assets"`
	Dependencies                []PreparedInputDependency `json:"dependencies"`
	SceneID                     string                    `json:"scene_id,omitempty"`
	ManifestID                  string                    `json:"manifest_id,omitempty"`
	ManifestHash                string                    `json:"manifest_hash,omitempty"`
	SourceSceneObjectID         string                    `json:"source_scene_object_id,omitempty"`
	SourceSceneRevision         int64                     `json:"source_scene_revision,omitempty"`
	SourceSceneRepresentationID string                    `json:"source_scene_representation_id,omitempty"`
	// ExtraCredits is the extra credits the frozen input incurs on top of the
	// Offering's base price (0 when none). It is derived from NormalizedInput
	// and the pinned Offering revision, and it is part of InputHash when above
	// zero, so an approval of this input is an approval of exactly this amount.
	ExtraCredits int       `json:"extra_credits,omitempty"`
	InputHash    string    `json:"input_hash"`
	CreatedAt    time.Time `json:"created_at"`
}

type SubmitPreparedCommand struct {
	Harness         harness.Binding `json:"harness,omitempty"`
	OwnerID         OwnerID
	SessionID       string
	PreparedInputID PreparedInputID
	IdempotencyKey  string
	OperationID     string
	PlanStepID      string
	// AuthorizationRef is an opaque execution-grant request ID. The SQLite
	// Create transaction verifies its separate human decision and exact input.
	AuthorizationRef string
}

type PreparedInputStore interface {
	CreatePreparedInput(context.Context, PreparedInput) error
	GetPreparedInput(context.Context, OwnerID, PreparedInputID) (PreparedInput, error)
}

// TaskAdmissionStore reads the durable bound grant, not Task.Admission (which
// is deliberately not serialized). An empty ID denotes a legacy unbound task.
type TaskAdmissionStore interface {
	AdmittedPreparedInput(context.Context, OwnerID, TaskID) (PreparedInputID, error)
}

type PreparedContextVerifier interface {
	VerifyPreparedContext(context.Context, OwnerID, string, string, string, string) error
}

// PreparedContextOutput is the server-rendered asset named by a manifest.
// Generation compares it with the actual resolved input bytes before a paid
// Task can be admitted; a manifest cannot attest to an unrelated image.
type PreparedContextOutput struct {
	AssetID AssetID
	SHA256  string
	Size    int64
}

type PreparedContextOutputVerifier interface {
	VerifiedPreparedOutput(context.Context, OwnerID, string, string, string, string) (PreparedContextOutput, error)
}

// PreparedSceneObjectVerifier validates the user's explicit source object in
// the same owner/session before a paid call. A missing implementation fails closed.
type PreparedSceneObjectVerifier interface {
	ResolvePreparedSource(context.Context, OwnerID, string, string, int64, json.RawMessage) (string, error)
}
