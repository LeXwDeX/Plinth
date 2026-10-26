package app

import (
	"context"
	"log/slog"

	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// reportAdminBootstrap writes what the startup administrator bootstrap did
// (#319): an INFO line for every run that looked at ADMIN_PASSWORD, and an
// audit event (actions from the Schema) when it created the administrator or
// reset its password. The password never reaches the log or the audit trail.
func reportAdminBootstrap(
	ctx context.Context,
	logger *slog.Logger,
	audits administration.AuditRecorder,
	api catalog.AuthenticationAPI,
	username string,
	outcome authentication.BootstrapAdminResult,
) {
	if outcome.Action == authentication.BootstrapAdminSkipped {
		return
	}
	logger.InfoContext(ctx, "管理员初始化",
		"action", string(outcome.Action),
		"username", username,
		"user_id", outcome.UserID,
		"sessions_revoked", outcome.SessionsRevoked,
	)
	action := ""
	switch outcome.Action {
	case authentication.BootstrapAdminCreated:
		action = api.BootstrapAdminCreatedAuditAction
	case authentication.BootstrapAdminPasswordReset:
		action = api.BootstrapAdminPasswordResetAuditAction
	}
	if action == "" || audits == nil {
		return
	}
	if err := audits.RecordAudit(ctx, administration.AuditEvent{
		Action:       action,
		ActorID:      outcome.UserID,
		ActorName:    username,
		ResourceType: api.AuditResourceType,
		ResourceID:   outcome.UserID,
	}); err != nil {
		logger.WarnContext(ctx, "写入管理员初始化审计失败", "action", action, "user_id", outcome.UserID, "error", err)
	}
}
