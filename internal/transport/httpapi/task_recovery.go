package httpapi

import (
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// Only a server-attested receipt can make a failed task recoverable. The HTTP
// caller cannot supply bytes, URLs, storage keys, scope or replacement slots.
func (s *Server) getTaskRecovery(w http.ResponseWriter, r *http.Request, owner generation.OwnerID) {
	view, err := s.generation.RecoveryEligibility(r.Context(), owner, generation.TaskID(r.PathValue("taskID")))
	if err != nil {
		s.writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) prepareTaskRecovery(w http.ResponseWriter, r *http.Request, owner generation.OwnerID) {
	var body struct {
		ReceiptDigest string `json:"receipt_digest"`
	}
	if err := s.decodeJSONWithLimit(w, r, &body, 1024); err != nil {
		return
	}
	taskID := generation.TaskID(r.PathValue("taskID"))
	task, err := s.generation.Get(r.Context(), owner, taskID)
	if err != nil {
		s.writeDomainError(w, err)
		return
	}
	if !s.taskSessionOwned(w, r, owner, task.SessionID) {
		return
	}
	prepared, view, err := s.generation.PrepareRecovery(r.Context(), owner, taskID, body.ReceiptDigest)
	if err != nil {
		s.writeDomainError(w, err)
		return
	}
	// The existing grant decision and task submission endpoints remain the only
	// execution path. Repeated preparation returns the same identity or task.
	response := map[string]any{"recovery": view}
	status := http.StatusOK
	if prepared.ID != "" {
		response["prepared_input_id"] = prepared.ID
		response["input_hash"] = prepared.InputHash
		response["session_id"] = prepared.SessionID
		response["capability"] = prepared.Capability
		response["offering_id"] = prepared.OfferingID
		response["offering_revision"] = prepared.OfferingRevision
		status = http.StatusCreated
	}
	writeJSON(w, status, response)
}

// getTaskRefetch reports whether the owner's failed Task can have its accepted
// upstream result fetched again (#295). Other owners get 404.
func (s *Server) getTaskRefetch(w http.ResponseWriter, r *http.Request, owner generation.OwnerID) {
	view, err := s.generation.RefetchEligibility(r.Context(), owner, generation.TaskID(r.PathValue("taskID")))
	if err != nil {
		s.writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// startTaskRefetch starts the refetch, or returns the one already running or
// done. The body is ignored: the caller cannot name a job, URL, Offering or
// slot. Nothing is submitted upstream and nothing is charged.
func (s *Server) startTaskRefetch(w http.ResponseWriter, r *http.Request, owner generation.OwnerID) {
	result, err := s.generation.RefetchUpstreamResult(r.Context(), owner, generation.TaskID(r.PathValue("taskID")), generation.RefetchByOwner)
	if err != nil {
		if generation.IsCode(err, generation.ErrConflict) && result.Refetch.Reason != "" {
			// Not refetchable: the Schema text for the reason is chosen by the client.
			if trace := traceFromWriter(w); trace != nil {
				trace.noteProblem("upstream_refetch_unavailable", string(result.Refetch.Reason), []error{err})
			}
			writeJSON(w, http.StatusConflict, result)
			return
		}
		s.writeDomainError(w, err)
		return
	}
	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, result)
}
