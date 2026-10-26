package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"
)

// clientErrorReport is what the browser sends for an error boundary,
// window.onerror or an unhandled rejection (#233).
type clientErrorReport struct {
	Kind      string `json:"kind"`
	Message   string `json:"message"`
	Stack     string `json:"stack"`
	Page      string `json:"page"`
	Revision  string `json:"revision"`
	RequestID string `json:"request_id"`
}

func truncateText(value string, maximum int) string {
	if utf8.RuneCountInString(value) <= maximum {
		return value
	}
	runes := []rune(value)
	return string(runes[:maximum]) + "…"
}

// anonymousClientErrorsKey is the shared bucket for the global cap on
// signed-out reports; client buckets are prefixed so they never collide with
// an owner ID.
const anonymousClientErrorsKey = "anonymous\x00*"

func (s *Server) reportClientError(writer http.ResponseWriter, request *http.Request) {
	limits := s.application.Runtime.HTTP.ClientErrorReport
	principal, err := s.identity.Identify(request)
	anonymous := err != nil
	if anonymous && limits.AnonymousGlobalPerMinute <= 0 {
		writeProblem(writer, http.StatusUnauthorized, "unauthenticated", s.application.Authentication.Messages.Unauthenticated)
		return
	}
	bodyLimit, maximum := limits.MaxBodyBytes, limits.MaxTextCharacters
	if anonymous {
		// #317: signed-out pages (login, an expired session) report under
		// tighter limits; see docs/self-deploy-security.md for the abuse model.
		bodyLimit, maximum = limits.AnonymousMaxBodyBytes, limits.AnonymousMaxTextCharacters
	}
	var report clientErrorReport
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, bodyLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil || decoder.Decode(&struct{}{}) != io.EOF || report.Kind == "" {
		writeProblem(writer, http.StatusBadRequest, "invalid_client_error", s.message("error.client_report_invalid"), err)
		return
	}
	allowed := false
	if anonymous {
		client := "anonymous\x00" + rateLimitClientKey(requestPeerAddress(request))
		allowed = s.clientErrors.allow(client, limits.AnonymousPerClientPerMinute) &&
			s.clientErrors.allow(anonymousClientErrorsKey, limits.AnonymousGlobalPerMinute)
	} else {
		allowed = s.clientErrors.allow(principal.ID(), limits.PerOwnerPerMinute)
	}
	if !allowed {
		writeProblem(writer, http.StatusTooManyRequests, "client_error_rate_limited", s.message("error.client_report_rate_limited"))
		return
	}
	attributes := []any{}
	if anonymous {
		// The page is kept as a path only: a signed-out URL may carry a
		// return target or a one-time token in its query or fragment.
		report.Page = pagePath(report.Page)
		attributes = append(attributes, "anonymous", true)
	} else {
		attributes = append(attributes, "owner_id", principal.ID())
	}
	if trace := traceFromWriter(writer); trace != nil {
		attributes = append(attributes, "request_id", trace.id)
	}
	attributes = append(attributes,
		"kind", truncateText(report.Kind, 64),
		"message", truncateText(report.Message, maximum),
		"stack", truncateText(report.Stack, maximum),
		"page", truncateText(report.Page, 512),
		"client_revision", truncateText(report.Revision, 64),
		"related_request_id", truncateText(report.RequestID, 64),
		"user_agent", truncateText(request.UserAgent(), 256),
	)
	s.logger.ErrorContext(request.Context(), "前端错误", attributes...)
	writer.WriteHeader(http.StatusNoContent)
}

// pagePath drops the query and fragment of a reported page.
func pagePath(page string) string {
	if index := strings.IndexAny(page, "?#"); index >= 0 {
		return page[:index]
	}
	return page
}
