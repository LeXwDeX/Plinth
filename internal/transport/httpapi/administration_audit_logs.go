package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

type administrationAuditLogHandler func(
	http.ResponseWriter,
	*http.Request,
	access.Principal,
)

func (server *Server) registerAdministrationAuditLogContracts() {
	route := server.application.Runtime.HTTP.LegacyRoutes.AdminAuditLogs
	server.mux.HandleFunc(
		"GET "+route,
		server.withAdministrationAuditLogPrincipal(
			server.listAdministrationAuditLogs,
		),
	)
}

func (server *Server) withAdministrationAuditLogPrincipal(
	next administrationAuditLogHandler,
) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		principal, err := server.identity.Identify(request)
		if err != nil {
			server.writeAdministrationAuditLogError(
				writer,
				http.StatusUnauthorized,
				server.application.Authentication.Messages.Unauthenticated,
			)
			return
		}
		permission := access.Permission(
			server.application.Administration.AuditLogs.Permission,
		)
		if !principal.Can(permission) {
			server.writeAdministrationAuditLogError(
				writer,
				http.StatusForbidden,
				server.application.Authentication.Messages.Forbidden,
			)
			return
		}
		next(writer, request, principal)
	}
}

func (server *Server) listAdministrationAuditLogs(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	schema := server.application.Administration.AuditLogs
	queryFields := schema.API.Query
	values := request.URL.Query()
	query := administration.AuditQuery{
		Page: max(
			1,
			administrationAuditLogInteger(
				values.Get(queryFields.Page),
				schema.Limits.DefaultPage,
			),
		),
		PageSize: max(
			1,
			administrationAuditLogInteger(
				values.Get(queryFields.PageSize),
				schema.Limits.DefaultPageSize,
			),
		),
		UserID: values.Get(queryFields.UserID),
		Action: values.Get(queryFields.Action),
		From: administrationAuditLogTime(
			values.Get(queryFields.From),
		),
		To: administrationAuditLogTime(
			values.Get(queryFields.To),
		),
	}
	page, err := server.administrationAudit.
		QueryAuditEvents(request.Context(), query)
	if err != nil {
		server.logger.Error(
			"list administration audit logs",
			"error",
			err,
		)
		server.writeAdministrationAuditLogError(
			writer,
			http.StatusInternalServerError,
			schema.Messages.InternalError,
		)
		return
	}
	events := make([]map[string]any, 0, len(page.Events))
	for _, event := range page.Events {
		events = append(
			events,
			administrationAuditLogWire(schema.API.Fields, event),
		)
	}
	server.recordAdministrationReadAudit(
		request,
		principal,
		schema.Audit,
	)
	fields := schema.API.Fields
	server.writeAdministrationAuditLogJSON(
		writer,
		http.StatusOK,
		map[string]any{
			fields.Data:     events,
			fields.Total:    page.Total,
			fields.Page:     page.Page,
			fields.PageSize: page.PageSize,
		},
	)
}

func administrationAuditLogWire(
	fields catalog.AdministrationAuditLogFieldSchema,
	event administration.AuditEvent,
) map[string]any {
	detail := "{}"
	if event.Detail != nil {
		if encoded, err := json.Marshal(redactExternalAddresses(event.Detail)); err == nil {
			detail = string(encoded)
		}
	}
	resourceID := event.ResourceID
	if hasExternalAddress(resourceID) {
		resourceID = ""
	}
	userAgent := event.UserAgent
	if hasExternalAddress(userAgent) {
		userAgent = ""
	}
	return map[string]any{
		fields.ID:           event.ID,
		fields.Action:       event.Action,
		fields.UserID:       event.ActorID,
		fields.UserName:     event.ActorName,
		fields.ResourceType: event.ResourceType,
		fields.ResourceID:   resourceID,
		fields.Detail:       detail,
		fields.IPAddress:    event.IPAddress,
		fields.UserAgent:    userAgent,
		fields.CreatedAt:    event.CreatedAt,
	}
}

func administrationAuditLogInteger(raw string, fallback int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	end := 0
	if raw[0] == '+' || raw[0] == '-' {
		end = 1
	}
	for end < len(raw) && raw[end] >= '0' && raw[end] <= '9' {
		end++
	}
	if end == 0 ||
		(end == 1 && (raw[0] == '+' || raw[0] == '-')) {
		return fallback
	}
	value, err := strconv.Atoi(raw[:end])
	if err != nil {
		return fallback
	}
	if value == 0 {
		return fallback
	}
	return value
}

func administrationAuditLogTime(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02",
	} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			normalized := parsed.UTC()
			return &normalized
		}
	}
	return nil
}

func (server *Server) writeAdministrationAuditLogError(
	writer http.ResponseWriter,
	status int,
	message string,
) {
	field := server.application.Administration.AuditLogs.API.Fields.Error
	server.writeAdministrationAuditLogJSON(
		writer,
		status,
		map[string]any{field: message},
	)
}

func (server *Server) writeAdministrationAuditLogJSON(
	writer http.ResponseWriter,
	status int,
	payload map[string]any,
) {
	writer.Header().Set(
		"Cache-Control",
		server.application.Runtime.HTTP.PrivateCacheControl,
	)
	// writeJSON is the single JSON exit: it adds the request ID to every
	// error body (#118) and keeps the response locale marking.
	writeJSON(writer, status, payload)
}
