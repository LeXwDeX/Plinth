package httpapi

import (
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/administration"
)

type administrationBackfillStatusHandler func(
	http.ResponseWriter,
	*http.Request,
	access.Principal,
)

func (server *Server) registerAdministrationBackfillStatusContract() {
	route := server.application.Runtime.HTTP.LegacyRoutes.
		AdminBackfillStatus
	server.mux.HandleFunc(
		"POST "+route,
		server.withAdministrationBackfillStatusPrincipal(
			server.postAdministrationBackfillStatus,
		),
	)
}

func (server *Server) withAdministrationBackfillStatusPrincipal(
	next administrationBackfillStatusHandler,
) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		principal, err := server.identity.Identify(request)
		if err != nil {
			server.writeAdministrationBackfillStatusError(
				writer,
				http.StatusUnauthorized,
				server.application.Authentication.Messages.Unauthenticated,
			)
			return
		}
		permission := access.Permission(
			server.application.Administration.BackfillStatus.Permission,
		)
		if !principal.Can(permission) {
			server.writeAdministrationBackfillStatusError(
				writer,
				http.StatusForbidden,
				server.application.Authentication.Messages.Forbidden,
			)
			return
		}
		next(writer, request, principal)
	}
}

func (server *Server) postAdministrationBackfillStatus(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	schema := server.application.Administration.BackfillStatus
	fields := schema.API.Fields
	result, err := server.administrationBackfillStatus.Backfill(
		request.Context(),
	)
	if err != nil {
		server.logger.Error(
			"legacy administration status backfill failed",
			"error",
			err,
		)
		server.writeAdministrationBackfillStatusError(
			writer,
			http.StatusInternalServerError,
			schema.Messages.InternalError,
		)
		return
	}
	server.recordProductAudit(request, administration.AuditEvent{
		Action:       schema.Audit.TriggerAction,
		ActorID:      principal.ID(),
		ActorName:    principal.Claims.Name,
		ResourceType: schema.Audit.ResourceType,
		Detail: map[string]any{
			"total":      result.Total,
			"sync_fixed": result.SyncFixed,
			"unchanged":  result.Unchanged,
		},
	})
	if result.Total == 0 {
		writeJSON(writer, http.StatusOK, map[string]any{
			fields.Message: schema.Messages.NoPending,
			fields.Fixed:   0,
		})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		fields.Message:    schema.Messages.Complete,
		fields.Total:      result.Total,
		fields.SyncFixed:  result.SyncFixed,
		fields.AsyncFixed: result.AsyncFixed,
		fields.Unchanged:  result.Unchanged,
	})
}

func (server *Server) writeAdministrationBackfillStatusError(
	writer http.ResponseWriter,
	status int,
	message string,
) {
	writeJSON(writer, status, map[string]any{
		server.application.Administration.BackfillStatus.API.Fields.Error: message,
	})
}
