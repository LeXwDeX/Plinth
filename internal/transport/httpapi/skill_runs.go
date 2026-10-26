package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
)

func (s *Server) createSkillRun(w http.ResponseWriter, r *http.Request, owner generation.OwnerID) {
	var body struct {
		SessionID             string                     `json:"session_id"`
		RecipeID              string                     `json:"recipe_id"`
		RecipeRevision        string                     `json:"recipe_revision"`
		OfferingID            generation.OfferingID      `json:"offering_id"`
		SourceAssetID         generation.AssetID         `json:"source_asset_id"`
		SourceTaskID          generation.TaskID          `json:"source_task_id,omitempty"`
		SourcePreparedInputID generation.PreparedInputID `json:"source_prepared_input_id,omitempty"`
	}
	if err := s.decodeJSON(w, r, &body); err != nil {
		return
	}
	if !s.taskSessionOwned(w, r, owner, body.SessionID) {
		return
	}
	run, err := s.skillRuns.Create(r.Context(), skillrun.CreateCommand{
		OwnerID: owner, SessionID: body.SessionID, RecipeID: body.RecipeID,
		ExpectedRevision: body.RecipeRevision, SelectedOfferingID: body.OfferingID,
		SourceAssetID: body.SourceAssetID, SourceTaskID: body.SourceTaskID,
		SourcePreparedInputID: body.SourcePreparedInputID,
		IdempotencyKey:        r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		s.writeSkillRunError(w, err)
		return
	}
	w.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(w, http.StatusCreated, run)
}

func (s *Server) listSkillRuns(w http.ResponseWriter, r *http.Request, owner generation.OwnerID) {
	session := r.URL.Query().Get("session_id")
	if !s.taskSessionOwned(w, r, owner, session) {
		return
	}
	limit := 20
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			s.writeSkillRunError(w, skillrun.ErrInvalid)
			return
		}
		limit = parsed
	}
	runs, err := s.skillRuns.List(r.Context(), owner, session, r.URL.Query().Get("after"), limit)
	if err != nil {
		s.writeSkillRunError(w, err)
		return
	}
	w.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) getSkillRun(w http.ResponseWriter, r *http.Request, owner generation.OwnerID) {
	run, err := s.skillRuns.Get(r.Context(), owner, r.PathValue("runID"))
	if err != nil {
		s.writeSkillRunError(w, err)
		return
	}
	w.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) advanceSkillStep(w http.ResponseWriter, r *http.Request, owner generation.OwnerID) {
	var body struct {
		Action string `json:"action"`
	}
	if err := s.decodeJSON(w, r, &body); err != nil {
		return
	}
	runID, stepKey := r.PathValue("runID"), r.PathValue("stepKey")
	var run skillrun.Run
	var result any
	var err error
	switch body.Action {
	case "prepare":
		var prepared generation.PreparedInput
		run, prepared, err = s.skillRuns.PrepareStep(r.Context(), owner, runID, stepKey)
		result = map[string]any{"prepared_input_id": prepared.ID, "input_hash": prepared.InputHash,
			"offering_id": prepared.OfferingID, "offering_revision": prepared.OfferingRevision}
	case "grant":
		principal, ok := s.taskHumanPrincipal(r, owner)
		if !ok {
			s.writeMessageProblem(w, http.StatusForbidden, "forbidden", "error.forbidden")
			return
		}
		var grant executiongrant.Grant
		run, grant, err = s.skillRuns.RequestGrant(r.Context(), principal, runID, stepKey)
		result = map[string]any{"request_id": grant.RequestID, "status": grant.Status,
			"request_version": grant.RequestVersion}
	case "submit":
		var accepted generation.Accepted
		run, accepted, err = s.skillRuns.SubmitStep(r.Context(), owner, runID, stepKey)
		result = map[string]any{"task_id": accepted.TaskID, "replayed": accepted.Replayed}
	case "sync":
		run, err = s.skillRuns.SyncOutput(r.Context(), owner, runID, stepKey)
	default:
		s.writeSkillRunError(w, skillrun.ErrInvalid)
		return
	}
	if err != nil {
		s.writeSkillRunError(w, err)
		return
	}
	w.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(w, http.StatusOK, map[string]any{"run": run, "result": result})
}

func (s *Server) reviewSkillStep(w http.ResponseWriter, r *http.Request, owner generation.OwnerID) {
	var body struct {
		Decision skillrun.ReviewStatus `json:"decision"`
	}
	if err := s.decodeJSON(w, r, &body); err != nil {
		return
	}
	if body.Decision != skillrun.Accepted && body.Decision != skillrun.Rejected {
		s.writeSkillRunError(w, skillrun.ErrInvalid)
		return
	}
	run, err := s.skillRuns.Review(r.Context(), skillrun.ReviewCommand{OwnerID: owner,
		RunID: r.PathValue("runID"), StepKey: r.PathValue("stepKey"), Decision: body.Decision})
	if err != nil {
		s.writeSkillRunError(w, err)
		return
	}
	w.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) writeSkillRunError(w http.ResponseWriter, err error) {
	noteCause(w, err)
	var generationError *generation.Error
	switch {
	case errors.Is(err, skillrun.ErrInvalid), errors.Is(err, executiongrant.ErrInvalid):
		s.writeMessageProblem(w, http.StatusBadRequest, "invalid_input", "error.invalid_input")
	case errors.Is(err, skillrun.ErrMissing):
		s.writeMessageProblem(w, http.StatusNotFound, "not_found", "error.not_found")
	case errors.Is(err, skillrun.ErrConflict), errors.Is(err, executiongrant.ErrConflict),
		errors.Is(err, executiongrant.ErrState):
		s.writeMessageProblem(w, http.StatusConflict, "conflict", "error.conflict")
	case errors.As(err, &generationError):
		s.writeDomainError(w, err)
	default:
		s.writeMessageProblem(w, http.StatusServiceUnavailable, "unavailable", "error.internal")
	}
}
