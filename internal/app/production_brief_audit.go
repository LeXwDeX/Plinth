package app

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// productionBriefStore is the chat service's shared brief write path. The
// Agent tool and the HTTP PUT both replace the whole brief through it; only the
// audit call differs because the tool has no HTTP request metadata.
type productionBriefStore interface {
	Session(context.Context, string, string) (chat.Session, bool, error)
	SetProductionBrief(context.Context, string, string, map[string]json.RawMessage) (chat.ProductionBrief, error)
}

// updateProductionBriefAudited replaces the brief of the Agent tool's own
// session and records a real change with the same product audit event as the
// HTTP PUT (#103): the action and resource type come from the Schema, the
// resource is the session, and the detail holds only the changed field names.
// Audit is best effort: a write failure is logged and never fails a brief that
// was already stored.
func updateProductionBriefAudited(
	ctx context.Context,
	store productionBriefStore,
	audits administration.AuditRecorder,
	logger *slog.Logger,
	action string,
	resourceType string,
	ownerID string,
	sessionID string,
	brief map[string]json.RawMessage,
) error {
	previous, found, err := store.Session(ctx, ownerID, sessionID)
	if err != nil {
		return err
	}
	if !found {
		return chat.ErrSessionNotFound
	}
	stored, err := store.SetProductionBrief(ctx, ownerID, sessionID, brief)
	if err != nil {
		return err
	}
	changed := chat.ChangedProductionBriefFields(previous.Brief, stored)
	if len(changed) == 0 || audits == nil {
		return nil
	}
	event := administration.AuditEvent{
		Action:       action,
		ActorID:      ownerID,
		ActorName:    previous.OwnerName,
		ResourceType: resourceType,
		ResourceID:   sessionID,
		Detail:       map[string]any{"fields": changed},
	}
	if err := audits.RecordAudit(ctx, event); err != nil && logger != nil {
		logger.WarnContext(
			ctx,
			"写入产品审计事件失败",
			"action", action,
			"user_id", ownerID,
			"resource_type", resourceType,
			"resource_id", sessionID,
			"error", err,
		)
	}
	return nil
}
