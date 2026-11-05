package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
)

// decodeApprovalDecision reads the person's approve/deny decision of a task
// grant or a production plan. The request member is the one the Schema
// declares (chat.api.fields.decision), the same declaration the browser
// builds its body from, so renaming it in the Catalog keeps both ends in step
// (#120). The body must be a JSON object holding only that member: any other
// member is refused like an unknown field, and a value other than approve or
// deny is invalid input. It writes the problem response itself and reports
// whether a decision was read.
func (s *Server) decodeApprovalDecision(writer http.ResponseWriter, request *http.Request) (executiongrant.Decision, bool) {
	var body map[string]json.RawMessage
	if err := s.decodeJSON(writer, request, &body); err != nil {
		return "", false
	}
	field := s.application.Chat.API.Fields.Decision
	for member := range body {
		if member != field {
			s.writeMessageProblem(writer, http.StatusBadRequest, "invalid_json", "error.invalid_json")
			return "", false
		}
	}
	var decision executiongrant.Decision
	if raw, present := body[field]; present && field != "" {
		if err := json.Unmarshal(raw, &decision); err != nil {
			s.writeMessageProblem(writer, http.StatusBadRequest, "invalid_json", "error.invalid_json")
			return "", false
		}
	}
	if decision != executiongrant.Approve && decision != executiongrant.Deny {
		s.writeMessageProblem(writer, http.StatusBadRequest, "invalid_input", "error.invalid_input")
		return "", false
	}
	return decision, true
}
