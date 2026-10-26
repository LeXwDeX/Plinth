package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/pendingapproval"
)

// An operation identity is derived by the server from the immutable snapshot.
// A caller cannot reuse a decision for a different prepared input or plan step.
func standaloneOperationID(id generation.PreparedInputID) string {
	return "task:" + string(id)
}

// StandaloneOperationID is the same identity for packages that submit a
// prepared input on the caller's behalf, such as a retry.
func StandaloneOperationID(id generation.PreparedInputID) string { return standaloneOperationID(id) }

func (s *Server) taskSessionOwned(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID, sessionID string) bool {
	if strings.TrimSpace(sessionID) == "" {
		s.writeMessageProblem(writer, http.StatusBadRequest, "session_required", "error.session_required")
		return false
	}
	owned, err := s.chat.OwnsSession(request.Context(), string(owner), sessionID)
	if err != nil {
		s.writeChatInternalError(writer, err)
		return false
	}
	if !owned {
		s.writeMessageProblem(writer, http.StatusNotFound, "not_found", "error.task_not_found")
		return false
	}
	return true
}

// taskHumanPrincipal verifies a real, current session cookie. The ordinary
// IdentityResolver may also accept Bearer or development credentials; neither
// may be promoted to human approval by presenting an owner ID in JSON.
func (s *Server) taskHumanPrincipal(request *http.Request, owner generation.OwnerID) (executiongrant.Principal, bool) {
	if s.authentication == nil || strings.TrimSpace(request.Header.Get("Authorization")) != "" {
		return executiongrant.Principal{}, false
	}
	if _, err := request.Cookie(s.application.Authentication.Cookie.Name); err != nil {
		return executiongrant.Principal{}, false
	}
	user, exists, err := s.authentication.CurrentUser(request.Context(), request)
	if err != nil || !exists || user.ID == "" || user.ID != string(owner) {
		return executiongrant.Principal{}, false
	}
	return executiongrant.Principal{OwnerID: user.ID, ActorID: user.ID, Source: executiongrant.HumanBrowser}, true
}

func (s *Server) prepareTask(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	var body struct {
		Capability          generation.Capability `json:"capability"`
		OfferingID          generation.OfferingID `json:"offering_id"`
		SessionID           string                `json:"session_id"`
		Input               json.RawMessage       `json:"input"`
		SceneID             string                `json:"scene_id,omitempty"`
		ManifestID          string                `json:"manifest_id,omitempty"`
		ManifestHash        string                `json:"manifest_hash,omitempty"`
		SourceSceneObjectID string                `json:"source_scene_object_id,omitempty"`
		SourceSceneRevision int64                 `json:"source_scene_revision,omitempty"`
	}
	if err := s.decodeJSON(writer, request, &body); err != nil {
		return
	}
	if !s.taskSessionOwned(writer, request, owner, body.SessionID) {
		return
	}
	prepared, err := s.generation.PrepareInput(request.Context(), generation.PrepareInputCommand{
		OwnerID: owner, SessionID: body.SessionID, Capability: body.Capability,
		OfferingID: body.OfferingID, Input: body.Input, SceneID: body.SceneID,
		ManifestID: body.ManifestID, ManifestHash: body.ManifestHash,
		SourceSceneObjectID: body.SourceSceneObjectID, SourceSceneRevision: body.SourceSceneRevision,
		// A direct request keeps the first reference pictures a single-picture
		// model takes, as the Agent does, and is told so (#329 follow-up).
		KeepFirstReferences: true,
	})
	if err != nil {
		s.writeDomainError(writer, err)
		return
	}
	// Return only the decision-relevant public identity. Private dependency
	// and Provider checkpoint material stays inside the prepared store.
	view := map[string]any{
		"prepared_input_id": prepared.ID,
		"input_hash":        prepared.InputHash,
		"session_id":        prepared.SessionID,
		"capability":        prepared.Capability,
		"offering_id":       prepared.OfferingID,
		"offering_revision": prepared.OfferingRevision,
	}
	if prepared.ReferenceLimit != nil {
		// The template and the model name in the reader's language, as on the approval card.
		limit := *prepared.ReferenceLimit
		limit.Model = s.texts.TextIn(request.Context(), limit.Model)
		view["reference_note"] = limit.Note(s.texts.TextIn(request.Context(), limit.Template))
	}
	writeJSON(writer, http.StatusCreated, frozenQuoteWire(view, prepared.NativeQuote))
}

func (s *Server) requestTaskGrant(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	principal, ok := s.taskHumanPrincipal(request, owner)
	if !ok {
		s.writeMessageProblem(writer, http.StatusForbidden, "forbidden", "error.forbidden")
		return
	}
	if s.executionGrants == nil {
		s.writeMessageProblem(writer, http.StatusServiceUnavailable, "unavailable", "error.unavailable")
		return
	}
	var body struct {
		PreparedInputID generation.PreparedInputID `json:"prepared_input_id"`
		PlanID          string                     `json:"plan_id,omitempty"`
		PlanStepID      string                     `json:"plan_step_id,omitempty"`
	}
	if err := s.decodeJSON(writer, request, &body); err != nil {
		return
	}
	prepared, err := s.generation.GetPreparedInput(request.Context(), owner, body.PreparedInputID)
	if err != nil {
		s.writeDomainError(writer, err)
		return
	}
	if !s.taskSessionOwned(writer, request, owner, prepared.SessionID) {
		return
	}
	key := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	operationID := standaloneOperationID(prepared.ID)
	planStepID := ""
	binding := harness.Binding{}
	if body.PlanID != "" || body.PlanStepID != "" {
		if body.PlanID == "" || body.PlanStepID == "" || s.processing == nil {
			s.writeMessageProblem(writer, http.StatusBadRequest, "invalid_input", "error.invalid_input")
			return
		}
		planIdentity, err := s.processing.ExecutionGrantIdentity(request.Context(), owner,
			body.PlanID, string(prepared.ID), body.PlanStepID)
		if err != nil {
			s.writeProcessingError(writer, err)
			return
		}
		if planIdentity.SessionID != prepared.SessionID ||
			(key != "" && key != planIdentity.IdempotencyKey) {
			s.writeMessageProblem(writer, http.StatusConflict, "conflict", "error.conflict")
			return
		}
		key = planIdentity.IdempotencyKey
		operationID = planIdentity.OperationID
		planStepID = planIdentity.PlanStepID
		binding = planIdentity.Harness
	}
	grant, err := s.executionGrants.Prepare(request.Context(), executiongrant.PrepareCommand{
		Harness:   binding,
		Principal: principal, SessionID: prepared.SessionID,
		OfferingID: string(prepared.OfferingID), OfferingRevision: prepared.OfferingRevision,
		PreparedInputID: string(prepared.ID), PreparedInputHash: prepared.InputHash,
		IdempotencyKey: key,
		OperationID:    operationID,
		PlanStepID:     planStepID,
	})
	if err != nil {
		s.writeTaskGrantError(writer, err)
		return
	}
	// The session approval mode may approve this step without a person. The
	// response status tells the client whether a decision is still needed.
	grant, err = s.executionGrants.ApplyPolicy(request.Context(), grant)
	if err != nil {
		s.writeTaskGrantError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, frozenQuoteWire(map[string]any{
		"request_id":         grant.RequestID,
		"status":             grant.Status,
		"request_version":    grant.RequestVersion,
		"prepared_input_id":  grant.PreparedInputID,
		"approved_by_policy": grant.ApprovedByPolicy(),
	}, prepared.NativeQuote))
}

func (s *Server) decideTaskGrant(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	principal, ok := s.taskHumanPrincipal(request, owner)
	if !ok {
		s.writeMessageProblem(writer, http.StatusForbidden, "forbidden", "error.forbidden")
		return
	}
	if s.executionGrants == nil {
		s.writeMessageProblem(writer, http.StatusServiceUnavailable, "unavailable", "error.unavailable")
		return
	}
	var body struct {
		Decision executiongrant.Decision `json:"decision"`
	}
	if err := s.decodeJSON(writer, request, &body); err != nil {
		return
	}
	if body.Decision != executiongrant.Approve && body.Decision != executiongrant.Deny {
		s.writeMessageProblem(writer, http.StatusBadRequest, "invalid_input", "error.invalid_input")
		return
	}
	requestID := request.PathValue("requestID")
	if s.pendingApprovals != nil {
		// An Agent approval whose turn already ended is decided, submitted once
		// and reported into its session by the pending-approval service. Any
		// other grant is not "handled" and is decided the ordinary way below.
		outcome, handled, err := s.pendingApprovals.Decide(request.Context(), principal, requestID, body.Decision)
		if err != nil {
			s.writeTaskGrantError(writer, err)
			return
		}
		if handled {
			writeJSON(writer, http.StatusOK, pendingOutcomeWire(outcome))
			return
		}
	}
	grant, err := s.executionGrants.Decide(request.Context(), principal, requestID, body.Decision)
	if err != nil {
		s.writeTaskGrantError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"request_id":      grant.RequestID,
		"status":          grant.Status,
		"request_version": grant.RequestVersion,
	})
}

func (s *Server) writeTaskGrantError(writer http.ResponseWriter, err error) {
	noteCause(writer, err)
	switch {
	case errors.Is(err, executiongrant.ErrInvalid):
		s.writeMessageProblem(writer, http.StatusBadRequest, "invalid_input", "error.invalid_input")
	case errors.Is(err, executiongrant.ErrExpired):
		s.writeMessageProblem(writer, http.StatusConflict, "pending_approval_expired", "error.pending_approval_expired")
	case errors.Is(err, pendingapproval.ErrUnavailable):
		s.writeMessageProblem(writer, http.StatusConflict, "pending_approval_unavailable", "error.pending_approval_unavailable")
	case errors.Is(err, executiongrant.ErrConflict), errors.Is(err, executiongrant.ErrState):
		s.writeMessageProblem(writer, http.StatusConflict, "conflict", "error.conflict")
	case errors.Is(err, sql.ErrNoRows):
		s.writeMessageProblem(writer, http.StatusNotFound, "not_found", "error.task_not_found")
	default:
		// Every rejection carries a Schema message (`detail`) the approval card
		// shows; a bare 500 body would leave the card with a generic text.
		s.writeDomainError(writer, err)
	}
}
