package skillrun

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
)

type Service struct {
	store   Store
	recipes RecipeResolver
}

func NewService(store Store, recipes RecipeResolver) (*Service, error) {
	if store == nil || recipes == nil {
		return nil, fmt.Errorf("%w: store and recipe resolver required", ErrInvalid)
	}
	return &Service{store: store, recipes: recipes}, nil
}

func (s *Service) Create(ctx context.Context, cmd CreateCommand) (Run, error) {
	if !cmd.Harness.Valid() || !validID(string(cmd.OwnerID)) || !validID(cmd.SessionID) || !validID(cmd.RecipeID) ||
		!validID(cmd.ExpectedRevision) || !validID(string(cmd.SelectedOfferingID)) ||
		!validID(string(cmd.SourceAssetID)) ||
		!validID(cmd.IdempotencyKey) {
		return Run{}, ErrInvalid
	}
	recipe, ok, err := s.recipes.ResolveRecipe(ctx, cmd.RecipeID, cmd.SelectedOfferingID)
	if err != nil {
		return Run{}, err
	}
	if !ok || recipe.ID != cmd.RecipeID || recipe.Revision != cmd.ExpectedRevision {
		return Run{}, ErrConflict
	}
	if err := ValidateRecipe(recipe); err != nil {
		return Run{}, err
	}
	selectedUsed := false
	for _, step := range recipe.Steps {
		if step.OfferingID == cmd.SelectedOfferingID {
			selectedUsed = true
		}
	}
	if !selectedUsed {
		return Run{}, ErrConflict
	}
	recipeEncoded, err := json.Marshal(recipe)
	if err != nil {
		return Run{}, err
	}
	recipeDigest := sha256.Sum256(recipeEncoded)
	requestEncoded, err := json.Marshal(struct {
		OwnerID               generation.OwnerID         `json:"owner_id"`
		SessionID             string                     `json:"session_id"`
		RecipeHash            string                     `json:"recipe_hash"`
		SelectedOfferingID    generation.OfferingID      `json:"selected_offering_id"`
		SourceAssetID         generation.AssetID         `json:"source_asset_id"`
		SourceTaskID          generation.TaskID          `json:"source_task_id"`
		SourcePreparedInputID generation.PreparedInputID `json:"source_prepared_input_id"`
	}{cmd.OwnerID, cmd.SessionID, hex.EncodeToString(recipeDigest[:]), cmd.SelectedOfferingID, cmd.SourceAssetID,
		cmd.SourceTaskID, cmd.SourcePreparedInputID})
	if err != nil {
		return Run{}, err
	}
	if !cmd.Harness.IsZero() {
		requestEncoded, err = json.Marshal(struct {
			Request json.RawMessage `json:"request"`
			Harness harness.Binding `json:"harness"`
		}{requestEncoded, cmd.Harness})
		if err != nil {
			return Run{}, err
		}
	}
	requestDigest := sha256.Sum256(requestEncoded)
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return Run{}, err
	}
	now := time.Now().UTC()
	run := Run{Harness: cmd.Harness,
		ID: "skill_" + hex.EncodeToString(nonce[:]), OwnerID: cmd.OwnerID,
		SessionID: cmd.SessionID, RecipeID: recipe.ID, RecipeRevision: recipe.Revision,
		RecipeHash: hex.EncodeToString(recipeDigest[:]), SourceAssetID: cmd.SourceAssetID,
		SelectedOfferingID: cmd.SelectedOfferingID,
		SourceTaskID:       cmd.SourceTaskID, SourcePreparedInputID: cmd.SourcePreparedInputID,
		IdempotencyKey: cmd.IdempotencyKey, RequestHash: hex.EncodeToString(requestDigest[:]),
		CreatedAt: now, UpdatedAt: now,
	}
	for _, spec := range recipe.Steps {
		run.Steps = append(run.Steps, Step{Spec: spec, Phase: Planned, Review: Unreviewed})
	}
	return s.store.CreateSkillRun(ctx, run)
}

func (s *Service) Get(ctx context.Context, owner generation.OwnerID, runID string) (Run, error) {
	return s.store.GetSkillRun(ctx, owner, runID)
}

// GetByTask finds the run whose current step projects this Task.
func (s *Service) GetByTask(ctx context.Context, owner generation.OwnerID, session string, taskID generation.TaskID) (Run, error) {
	if !validID(string(owner)) || !validID(session) || !validID(string(taskID)) {
		return Run{}, ErrInvalid
	}
	return s.store.GetSkillRunByTask(ctx, owner, session, taskID)
}

// GetByIdempotency reads an existing run without resolving today's recipe or
// sources. A recovered paid call must retain its original execution identity.
func (s *Service) GetByIdempotency(ctx context.Context, owner generation.OwnerID, key string) (Run, error) {
	if !validID(string(owner)) || !validID(key) {
		return Run{}, ErrInvalid
	}
	return s.store.GetSkillRunByIdempotency(ctx, owner, key)
}

func (s *Service) List(ctx context.Context, owner generation.OwnerID, session, after string, limit int) ([]Run, error) {
	if !validID(string(owner)) || !validID(session) || limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	return s.store.ListSkillRuns(ctx, owner, session, after, limit)
}

// PendingGrant returns the oldest owner/session-scoped grant awaiting human
// consent. The repository verifies the persisted run, step, and grant identities.
func (s *Service) PendingGrant(ctx context.Context, owner generation.OwnerID, session string) (PendingGrant, error) {
	if !validID(string(owner)) || !validID(session) {
		return PendingGrant{}, ErrInvalid
	}
	return s.store.PendingGrant(ctx, owner, session)
}

func (s *Service) AttachPrepared(ctx context.Context, cmd PreparedCommand) (Run, error) {
	return s.store.AttachSkillPrepared(ctx, cmd)
}
func (s *Service) AttachGrant(ctx context.Context, cmd GrantCommand) (Run, error) {
	return s.store.AttachSkillGrant(ctx, cmd)
}
func (s *Service) BindTask(ctx context.Context, cmd TaskCommand) (Run, error) {
	return s.store.BindSkillTask(ctx, cmd)
}
func (s *Service) RecordOutput(ctx context.Context, cmd OutputCommand) (Run, error) {
	return s.store.RecordSkillOutput(ctx, cmd)
}
func (s *Service) RestartSkillStep(ctx context.Context, cmd RestartCommand) (Run, error) {
	if !validID(string(cmd.OwnerID)) || !validID(cmd.RunID) || !validID(cmd.StepKey) || cmd.ExpectedAttempt < 0 ||
		(cmd.FailedTaskID == "") == (cmd.DeniedGrantID == "") {
		return Run{}, ErrInvalid
	}
	return s.store.RestartSkillStep(ctx, cmd)
}
func (s *Service) Review(ctx context.Context, cmd ReviewCommand) (Run, error) {
	return s.store.ReviewSkillOutput(ctx, cmd)
}

// ResolveExecution returns the exact source and paid-step identity after a
// fresh owner-scoped read. Dependent steps cannot consume an unreviewed or
// rejected output. AttachPrepared repeats this test in its write transaction.
func (s *Service) ResolveExecution(ctx context.Context, owner generation.OwnerID, runID, stepKey string) (Execution, error) {
	run, err := s.Get(ctx, owner, runID)
	if err != nil {
		return Execution{}, err
	}
	for _, step := range run.Steps {
		if step.Spec.Key != stepKey {
			continue
		}
		source := run.SourceAssetID
		if step.Spec.SourceStepKey != "" {
			found := false
			for _, prior := range run.Steps {
				if prior.Spec.Key == step.Spec.SourceStepKey && prior.Phase == OutputReady &&
					prior.Review == Accepted && prior.OutputAssetID != "" {
					source, found = prior.OutputAssetID, true
					break
				}
			}
			if !found {
				return Execution{}, ErrConflict
			}
		}
		op, key, planStep := StepIdentityAt(run.ID, stepKey, step.Attempt)
		return Execution{Harness: run.Harness, RunID: run.ID, OwnerID: owner, SessionID: run.SessionID,
			Step: step, SourceAssetID: source, OperationID: op,
			IdempotencyKey: key, PlanStepID: planStep}, nil
	}
	return Execution{}, ErrMissing
}

func ValidateRecipe(recipe Recipe) error {
	if !validID(recipe.ID) || !validID(recipe.Revision) || len(recipe.Steps) == 0 || len(recipe.Steps) > 16 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, step := range recipe.Steps {
		if !validID(step.Key) || !validID(step.Label) || !validID(string(step.Capability)) ||
			!validID(string(step.OfferingID)) || !validID(step.OfferingRevision) ||
			!validID(step.PromptTemplate) || !validID(step.OutputRole) || seen[step.Key] {
			return ErrInvalid
		}
		if (step.PromptParameter == "") != (step.SourceParameter == "") ||
			(step.PromptParameter != "" && (step.PromptParameter == step.SourceParameter ||
				!validID(step.PromptParameter) || !validID(step.SourceParameter))) {
			return ErrInvalid
		}
		for key, value := range step.StaticInput {
			if !validID(key) || key == step.PromptParameter || key == step.SourceParameter ||
				len(value) == 0 || !json.Valid(value) {
				return ErrInvalid
			}
		}
		if step.SourceStepKey != "" && !seen[step.SourceStepKey] {
			return ErrInvalid
		}
		seen[step.Key] = true
	}
	return nil
}

func validID(value string) bool { return value != "" && value == strings.TrimSpace(value) }

// StepIdentity yields stable keys from a persisted run and recipe step. The
// same keys are reused after a crash; a retry must never invent a paid step.
func StepIdentity(runID, stepKey string) (operationID, idempotencyKey, planStepID string) {
	return StepIdentityAt(runID, stepKey, 0)
}

// StepIdentityAt is the identity of one attempt of a step. Attempt 0 is the
// original step and hashes exactly as before, so stored runs keep their keys.
// A later attempt exists only after the earlier one ended without a result
// (its Task failed with a retryable, unambiguous failure, or its grant was
// denied); it never reuses the paid identity of the attempt it replaces.
func StepIdentityAt(runID, stepKey string, attempt int) (operationID, idempotencyKey, planStepID string) {
	material := runID + "\x00" + stepKey
	if attempt > 0 {
		material += "\x00" + strconv.Itoa(attempt)
	}
	sum := sha256.Sum256([]byte(material))
	part := hex.EncodeToString(sum[:])
	return "skill_op_" + part, "skill_submit_" + part, "skill_step_" + part
}
