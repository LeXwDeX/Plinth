package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

type administrationUsageHandler func(
	http.ResponseWriter,
	*http.Request,
	access.Principal,
)

func (server *Server) registerAdministrationUsageContract() {
	routes := server.application.Runtime.HTTP.LegacyRoutes
	server.mux.HandleFunc(
		"GET "+routes.AdminUsage,
		server.withAdministrationUsagePrincipal(
			server.listAdministrationUsage,
		),
	)
	server.mux.HandleFunc(
		"GET "+routes.AdminBalanceSummary,
		server.withAdministrationUsagePrincipal(
			server.getAdministrationBalanceSummary,
		),
	)
}

func (server *Server) getAdministrationBalanceSummary(
	writer http.ResponseWriter,
	request *http.Request,
	_ access.Principal,
) {
	schema := server.application.Administration.Usage
	summary, err := server.administrationBalanceSummary.Summary(
		request.Context(),
	)
	if err != nil {
		server.logger.Error(
			"get administration balance summary",
			"error",
			err,
		)
		server.writeAdministrationUsageError(
			writer,
			http.StatusInternalServerError,
			schema.Messages.InternalError,
		)
		return
	}
	fields := schema.API.BalanceFields
	history := make([]map[string]any, 0, len(summary.History))
	for _, snapshot := range summary.History {
		history = append(history, map[string]any{
			fields.Balance: snapshot.Balance,
			fields.CapturedAt: snapshot.CapturedAt.UTC().
				Format(time.RFC3339Nano),
		})
	}
	writeJSON(
		writer,
		http.StatusOK,
		map[string]any{
			fields.CurrentBalance: summary.CurrentBalance,
			fields.History:        history,
		},
	)
}

func (server *Server) withAdministrationUsagePrincipal(
	next administrationUsageHandler,
) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		principal, err := server.identity.Identify(request)
		if err != nil {
			server.writeAdministrationUsageError(
				writer,
				http.StatusUnauthorized,
				server.application.Authentication.Messages.Unauthenticated,
			)
			return
		}
		permission := access.Permission(
			server.application.Administration.Usage.Permission,
		)
		if !principal.Can(permission) {
			server.writeAdministrationUsageError(
				writer,
				http.StatusForbidden,
				server.application.Authentication.Messages.Forbidden,
			)
			return
		}
		next(writer, request, principal)
	}
}

func (server *Server) listAdministrationUsage(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	schema := server.application.Administration.Usage
	queryFields := schema.API.Query
	values := request.URL.Query()
	overview, err := server.administrationUsage.Overview(
		request.Context(),
		administration.UsageRequest{
			Days: administrationAuditLogInteger(
				values.Get(queryFields.Days),
				schema.Defaults.Days,
			),
			StartDate: values.Get(queryFields.StartDate),
			EndDate:   values.Get(queryFields.EndDate),
		},
	)
	if err != nil {
		if errors.Is(err, administration.ErrUsageInvalidRange) {
			server.writeAdministrationUsageError(
				writer,
				http.StatusBadRequest,
				schema.Messages.InvalidRange,
			)
			return
		}
		server.logger.Error(
			"list administration usage",
			"error",
			err,
		)
		server.writeAdministrationUsageError(
			writer,
			http.StatusInternalServerError,
			schema.Messages.InternalError,
		)
		return
	}
	records := make([]map[string]any, 0, len(overview.Users))
	for _, summary := range overview.Users {
		records = append(
			records,
			administrationUsageWire(schema.API.Fields, summary),
		)
	}
	server.recordAdministrationReadAudit(request, principal, schema.Audit)
	writeJSON(
		writer,
		http.StatusOK,
		map[string]any{
			schema.API.Fields.Data:  records,
			schema.API.Fields.Total: administrationUsageTotalWire(schema.API.Fields, overview.Total),
		},
	)
}

// administrationUsageTotalWire states the total of all listed users the way a
// user's row states its own (#327): the credit estimate, or per-unit amounts
// once a Provider-native quote is among them.
func administrationUsageTotalWire(
	fields catalog.AdministrationUsageFieldSchema,
	total administration.UsageTotal,
) map[string]any {
	if len(total.PriceTotals) > 0 {
		return map[string]any{"price_totals": total.PriceTotals}
	}
	return map[string]any{fields.EstimatedCredits: total.EstimatedCredits}
}

func administrationUsageWire(
	fields catalog.AdministrationUsageFieldSchema,
	summary administration.UsageSummary,
) map[string]any {
	result := map[string]any{
		fields.UserID:           summary.UserID,
		fields.UserName:         summary.UserName,
		fields.UserEmail:        summary.UserEmail,
		fields.TotalTasks:       summary.TotalTasks,
		fields.SucceededTasks:   summary.SucceededTasks,
		fields.FailedTasks:      summary.FailedTasks,
		fields.EstimatedCredits: summary.EstimatedCredits,
		fields.LastActiveAt: summary.LastActiveAt.UTC().
			Format(time.RFC3339Nano),
		fields.ByType: summary.ByType,
	}
	if len(summary.PriceTotals) > 0 {
		delete(result, fields.EstimatedCredits)
		delete(result, fields.ByType)
		result["price_totals"] = summary.PriceTotals
	}
	return result
}

func (server *Server) writeAdministrationUsageError(
	writer http.ResponseWriter,
	status int,
	message string,
) {
	writeJSON(
		writer,
		status,
		map[string]any{
			server.application.Administration.Usage.API.Fields.Error: message,
		},
	)
}
