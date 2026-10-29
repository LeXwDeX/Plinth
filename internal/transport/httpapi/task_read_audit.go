package httpapi

import (
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// auditCrossOwnerTaskRead 是按任务 ID 读取任务的统一审计点：持有
// tasks.read.any 的管理员成功读到非本人任务后，写一条 Schema 声明的
// runtime.task_audit.read_other_action。读本人任务、普通用户（跨 owner 本就 404）
// 和读取失败都不写。长连接（旧兼容 SSE）只在建立时调用一次，轮询期间不重复记。
// 归属判定只对持有 read-any 的调用者多一次读取。
func (s *Server) auditCrossOwnerTaskRead(
	request *http.Request,
	principal access.Principal,
	taskID generation.TaskID,
) {
	if !principal.Can(access.PermissionTasksReadAny) {
		return
	}
	task, err := s.generation.GetScoped(request.Context(), generation.AllTasks(), taskID)
	if err != nil || string(task.OwnerID) == principal.ID() {
		return
	}
	audit := s.application.Runtime.TaskAudit
	s.recordProductAudit(request, administration.AuditEvent{
		Action:       audit.ReadOtherAction,
		ActorID:      principal.ID(),
		ActorName:    principal.Claims.Name,
		ResourceType: audit.ResourceType,
		ResourceID:   string(taskID),
		Detail: map[string]any{
			audit.DetailFields.TaskID:  string(taskID),
			audit.DetailFields.OwnerID: string(task.OwnerID),
		},
	})
}
