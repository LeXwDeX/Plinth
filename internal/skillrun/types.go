package skillrun

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
)

var (
	ErrInvalid  = errors.New("skill run: invalid request")
	ErrMissing  = errors.New("skill run: not found")
	ErrConflict = errors.New("skill run: identity or revision conflict")
)

// StepPhase tracks only orchestration checkpoints. The Generation Task remains
// the sole authority for queued/running/materializing/terminal execution state.
type StepPhase string

const (
	Planned      StepPhase = "planned"
	Prepared     StepPhase = "prepared"
	GrantPending StepPhase = "grant_pending"
	TaskBound    StepPhase = "task_bound"
	OutputReady  StepPhase = "output_ready"
)

type ReviewStatus string

const (
	Unreviewed ReviewStatus = "unreviewed"
	Accepted   ReviewStatus = "accepted"
	Rejected   ReviewStatus = "rejected"
)

// Recipe is an immutable, versioned execution definition supplied by Product
// Schema. SkillRun does not guess prompts or translate a static gallery card
// into a paid workflow. A runtime resolver must supply a real recipe.
type Recipe struct {
	ID       string     `json:"id"`
	Revision string     `json:"revision"`
	Steps    []StepSpec `json:"steps"`
}

type StepSpec struct {
	Key              string                     `json:"key"`
	Label            string                     `json:"label"`
	Capability       generation.Capability      `json:"capability"`
	OfferingID       generation.OfferingID      `json:"offering_id"`
	OfferingRevision string                     `json:"offering_revision"`
	PromptTemplate   string                     `json:"prompt_template"`
	PromptParameter  string                     `json:"prompt_parameter,omitempty"`
	SourceParameter  string                     `json:"source_parameter,omitempty"`
	StaticInput      map[string]json.RawMessage `json:"static_input,omitempty"`
	OutputRole       string                     `json:"output_role"`
	// SourceStepKey is empty when the original source asset is used. A step
	// may consume only an earlier, locally materialized step output.
	SourceStepKey string `json:"source_step_key,omitempty"`
	// Optional marks a step the run is complete without; it still starts
	// only when a person asks for it (#65). The first step is never optional.
	Optional bool `json:"optional,omitempty"`
}

type Step struct {
	Spec StepSpec `json:"spec"`
	// Attempt counts restarts of the step: 0 for the original, then one more
	// for every restart after a failure or a denial. It selects the step's
	// paid identity (StepIdentityAt).
	Attempt           int                        `json:"attempt"`
	Phase             StepPhase                  `json:"phase"`
	PreparedInputID   generation.PreparedInputID `json:"prepared_input_id,omitempty"`
	PreparedInputHash string                     `json:"prepared_input_hash,omitempty"`
	GrantRequestID    string                     `json:"grant_request_id,omitempty"`
	TaskID            generation.TaskID          `json:"task_id,omitempty"`
	OutputAssetID     generation.AssetID         `json:"output_asset_id,omitempty"`
	Review            ReviewStatus               `json:"review"`
	ReviewedAt        *time.Time                 `json:"reviewed_at,omitempty"`
	// ReplacementAssetID is an input asset the person put in place of the
	// step's output (#65). The attempt, grant and task stay as they were.
	ReplacementAssetID generation.AssetID `json:"replacement_asset_id,omitempty"`
}

type Run struct {
	Harness               harness.Binding            `json:"harness,omitempty"`
	ID                    string                     `json:"id"`
	OwnerID               generation.OwnerID         `json:"-"`
	SessionID             string                     `json:"session_id"`
	RecipeID              string                     `json:"recipe_id"`
	RecipeRevision        string                     `json:"recipe_revision"`
	RecipeHash            string                     `json:"recipe_hash"`
	SelectedOfferingID    generation.OfferingID      `json:"selected_offering_id"`
	SourceAssetID         generation.AssetID         `json:"source_asset_id"`
	SourceTaskID          generation.TaskID          `json:"source_task_id,omitempty"`
	SourcePreparedInputID generation.PreparedInputID `json:"source_prepared_input_id,omitempty"`
	IdempotencyKey        string                     `json:"-"`
	RequestHash           string                     `json:"-"`
	Steps                 []Step                     `json:"steps"`
	CreatedAt             time.Time                  `json:"created_at"`
	UpdatedAt             time.Time                  `json:"updated_at"`
}

type CreateCommand struct {
	Harness               harness.Binding `json:"harness,omitempty"`
	OwnerID               generation.OwnerID
	SessionID             string
	RecipeID              string
	ExpectedRevision      string
	SelectedOfferingID    generation.OfferingID
	SourceAssetID         generation.AssetID
	SourceTaskID          generation.TaskID
	SourcePreparedInputID generation.PreparedInputID
	IdempotencyKey        string
	// SubjectType picks the recipe's subject constraint template (#243). An
	// empty value lets the resolver use its declared default.
	SubjectType string `json:"subject_type,omitempty"`
}

type StepCommand struct {
	OwnerID generation.OwnerID
	RunID   string
	StepKey string
	// OperationID and IdempotencyKey bind one logical paid submission. They
	// must be persisted before a grant is requested and survive process death.
	OperationID    string
	IdempotencyKey string
}

type PreparedCommand struct {
	StepCommand
	PreparedInputID   generation.PreparedInputID
	PreparedInputHash string
}

type GrantCommand struct {
	StepCommand
	GrantRequestID string
}

type TaskCommand struct {
	StepCommand
	TaskID generation.TaskID
}

type OutputCommand struct {
	OwnerID generation.OwnerID
	RunID   string
	StepKey string
	AssetID generation.AssetID
}

// RestartCommand starts the next attempt of a step that ended without a
// result. Exactly one of FailedTaskID and DeniedGrantRequestID names what the
// attempt being replaced ended with; the store verifies it against the ledger.
type RestartCommand struct {
	OwnerID         generation.OwnerID
	RunID           string
	StepKey         string
	ExpectedAttempt int
	FailedTaskID    generation.TaskID
	DeniedGrantID   string
	// RejectedReview replaces a succeeded output the automatic consistency
	// check rejected (#243). Exactly one of FailedTaskID, DeniedGrantID and
	// this flag names why the attempt is replaced.
	RejectedReview bool
}

// ReplaceCommand puts an input asset of the owner in place of one step's
// output. The store refuses it while the step's approval or Task is still in
// flight, so a paid result is never overwritten while it is being made.
type ReplaceCommand struct {
	OwnerID generation.OwnerID
	RunID   string
	StepKey string
	AssetID generation.AssetID
}

type ReviewCommand struct {
	OwnerID  generation.OwnerID
	RunID    string
	StepKey  string
	Decision ReviewStatus
}

// PendingGrant is the exact human-consent identity for one prepared recipe
// step. It carries no provider execution state; Generation remains its owner.
type PendingGrant struct {
	RunID             string
	StepKey           string
	GrantRequestID    string
	PreparedInputID   generation.PreparedInputID
	PreparedInputHash string
	OfferingID        generation.OfferingID
	OfferingRevision  string
	OperationID       string
	IdempotencyKey    string
}

// Execution is a frozen orchestration instruction for one recipe step. The
// caller still uses Generation.PrepareInput and ExecutionGrant; no paid work is
// issued by this package.
type Execution struct {
	Harness        harness.Binding `json:"harness,omitempty"`
	RunID          string
	OwnerID        generation.OwnerID
	SessionID      string
	Step           Step
	SourceAssetID  generation.AssetID
	OperationID    string
	IdempotencyKey string
	PlanStepID     string
}

type RecipeResolver interface {
	// The resolver validates the human-selected Offering and the subject type
	// against the recipe's Product Schema contract, then materializes concrete
	// step Offering IDs, revisions and prompts. This keeps recipes
	// provider-independent until creation. An empty subject type means the
	// declaration's default; unknown types are refused.
	ResolveRecipe(context.Context, string, generation.OfferingID, string) (Recipe, bool, error)
}

type Store interface {
	CreateSkillRun(context.Context, Run) (Run, error)
	GetSkillRun(context.Context, generation.OwnerID, string) (Run, error)
	GetSkillRunByIdempotency(context.Context, generation.OwnerID, string) (Run, error)
	GetSkillRunByTask(context.Context, generation.OwnerID, string, generation.TaskID) (Run, error)
	ListSkillRuns(context.Context, generation.OwnerID, string, string, int) ([]Run, error)
	PendingGrant(context.Context, generation.OwnerID, string) (PendingGrant, error)
	AttachSkillPrepared(context.Context, PreparedCommand) (Run, error)
	AttachSkillGrant(context.Context, GrantCommand) (Run, error)
	BindSkillTask(context.Context, TaskCommand) (Run, error)
	RecordSkillOutput(context.Context, OutputCommand) (Run, error)
	ReviewSkillOutput(context.Context, ReviewCommand) (Run, error)
	// RestartSkillStep moves a failed or denied step to its next attempt. It
	// is a compare-and-set on the attempt number: a repeated request for an
	// attempt that was already replaced changes nothing.
	RestartSkillStep(context.Context, RestartCommand) (Run, error)
	// ReplaceSkillStepOutput records a replacement picture for one step.
	ReplaceSkillStepOutput(context.Context, ReplaceCommand) (Run, error)
}

// InputTemplate and provider parameters are deliberately absent from this
// ledger: actual input bytes are frozen by Generation.PreparedInput. Integrators
// must create that record from the recipe and verify the source asset before
// AttachSkillPrepared; the repository repeats the ownership/content checks.
