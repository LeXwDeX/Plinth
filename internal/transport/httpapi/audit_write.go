package httpapi

import (
	"net/http"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// recordProductAudit 是 HTTP 产品层唯一的审计写入口。领域动作和资源类型
// 由调用模块的 Product Schema 提供；这里仅负责翻译请求元数据并执行
// best-effort 持久化，审计故障不得回滚已经成功的产品操作。
func (server *Server) recordProductAudit(
	request *http.Request,
	event administration.AuditEvent,
) {
	headers := server.application.Administration.AuditLogs.RequestHeaders
	event.IPAddress = request.Header.Get(headers.ForwardedFor)
	if event.IPAddress == "" {
		event.IPAddress = request.Header.Get(headers.RealIP)
	}
	event.UserAgent = request.Header.Get(headers.UserAgent)
	if err := server.administrationAudit.RecordAudit(
		request.Context(),
		event,
	); err != nil {
		server.logger.WarnContext(
			request.Context(),
			"写入产品审计事件失败",
			"action", event.Action,
			"user_id", event.ActorID,
			"resource_type", event.ResourceType,
			"resource_id", event.ResourceID,
			"error", err,
		)
	}
}

// recordAdministrationReadAudit 为管理读路径留痕。读此前完全不写审计，
// 管理员读了哪些用户、任务、余额、审计记录都不可追溯；动作名与资源类型
// 来自 Product Schema 的 audit.read_action / audit.resource_type。
func (server *Server) recordAdministrationReadAudit(
	request *http.Request,
	principal access.Principal,
	audit catalog.AdministrationReadAuditSchema,
	resourceIDs ...string,
) {
	event := administration.AuditEvent{
		Action:       audit.ReadAction,
		ActorID:      principal.ID(),
		ActorName:    principal.Claims.Name,
		ResourceType: audit.ResourceType,
	}
	if len(resourceIDs) > 0 {
		if resourceID := strings.TrimSpace(resourceIDs[0]); resourceID != "" {
			event.ResourceID = resourceID
		}
	}
	server.recordProductAudit(request, event)
}
