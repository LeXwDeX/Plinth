package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func (s *Server) legacyTaskHistory(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	taskHistory := s.application.TaskHistory
	query := taskHistory.API.Query
	queryValues := request.URL.Query()
	page := boundedPositiveInteger(
		queryValues.Get(query.Page),
		1,
		1,
		int(^uint(0)>>1),
	)
	httpRuntime := s.application.Runtime.HTTP
	pageSize := boundedPositiveInteger(
		queryValues.Get(query.PageSize),
		httpRuntime.DefaultPageSize,
		1,
		httpRuntime.MaximumPageSize,
	)
	recentRaw := strings.TrimSpace(queryValues.Get(query.Recent))
	recent := 0
	if recentRaw != "" {
		recent = boundedPositiveInteger(
			recentRaw,
			0,
			1,
			httpRuntime.MaximumRecentTasks,
		)
	}

	capability := generation.Capability(
		strings.TrimSpace(queryValues.Get(query.Type)),
	)
	if string(capability) == taskHistory.Values.AllCapability {
		capability = ""
	}
	if capability != "" && !capability.Valid() {
		s.writeLegacyTaskHistory(
			writer,
			nil,
			0,
			page,
			pageSize,
			recentRaw != "",
		)
		return
	}

	states, stateKnown := s.internalStatesForPublic(
		strings.TrimSpace(queryValues.Get(query.Status)),
	)
	if !stateKnown {
		s.writeLegacyTaskHistory(
			writer,
			nil,
			0,
			page,
			pageSize,
			recentRaw != "",
		)
		return
	}

	// 与 GET /api/v1/tasks 一致（#154、#158）：历史列表对所有角色只返回本人任务；
	// 管理员查看他人任务走 /api/admin/generations（按 Schema 写审计），按 ID 的
	// 详情读取另在 auditCrossOwnerTaskRead 留痕。
	scope := generation.OwnTasks(generation.OwnerID(principal.ID()))
	if !principal.Can(access.PermissionTasksReadOwn) && !principal.Can(access.PermissionTasksReadAny) {
		writeProblem(
			writer,
			http.StatusForbidden,
			"forbidden",
			s.application.Authentication.Messages.Forbidden,
		)
		return
	}

	limit := pageSize
	offset := generation.PageOffset(page, pageSize)
	if recentRaw != "" {
		limit = recent
		offset = 0
	}
	listQuery := generation.ListQuery{
		States:      states,
		Capability:  capability,
		SearchID:    strings.TrimSpace(queryValues.Get(query.Search)),
		Offset:      offset,
		Limit:       limit,
		ActiveFirst: true,
	}
	if queryValues.Get(query.HideFailures) ==
		taskHistory.Values.Enabled &&
		strings.TrimSpace(queryValues.Get(query.Status)) == "" {
		listQuery.ExcludedStates = []generation.TaskState{generation.TaskFailed}
	}

	result, err := s.generation.List(request.Context(), scope, listQuery)
	if err != nil {
		s.writeDomainError(writer, err)
		return
	}
	data := make([]map[string]any, 0, len(result.Tasks))
	for _, task := range result.Tasks {
		data = append(data, s.projectLegacyTaskListItem(task))
	}
	s.writeLegacyTaskHistory(
		writer,
		data,
		result.Total,
		page,
		pageSize,
		recentRaw != "",
	)
}

func (s *Server) writeLegacyTaskHistory(
	writer http.ResponseWriter,
	data []map[string]any,
	total int,
	page int,
	pageSize int,
	recent bool,
) {
	if data == nil {
		data = []map[string]any{}
	}
	writer.Header().Set(
		"Cache-Control",
		s.application.Runtime.HTTP.PrivateCacheControl,
	)
	if recent {
		writeJSON(writer, http.StatusOK, map[string]any{
			s.application.TaskHistory.API.Fields.Data: data,
		})
		return
	}
	fields := s.application.TaskHistory.API.Fields
	writeJSON(writer, http.StatusOK, map[string]any{
		fields.Data:     data,
		fields.Total:    total,
		fields.Page:     page,
		fields.PageSize: pageSize,
	})
}

func (s *Server) projectLegacyTaskListItem(
	task generation.Task,
) map[string]any {
	parameters := task.Offering.Parameters
	if current, exists := s.generation.Offering(task.Offering.ID); exists {
		parameters = current.Parameters
	}
	fields := s.application.TaskHistory.API.Fields
	item := map[string]any{
		fields.ID:       task.ID,
		fields.TaskType: string(task.Capability),
		fields.Status:   s.publicTaskState(task.State),
		fields.Progress: task.Progress,
		fields.CreatedAt: task.CreatedAt.UTC().
			Format("2006-01-02T15:04:05.000Z"),
	}
	prompt := semanticTextInput(
		task.Input,
		parameters,
		generation.SemanticPrompt,
	)
	if prompt != "" {
		item[fields.Prompt] = prompt
	}
	if task.State == generation.TaskSucceeded {
	preview:
		for _, configuredRole := range s.application.TaskHistory.Assets.
			PreviewRoles {
			previewRole := strings.TrimSpace(configuredRole)
			for _, asset := range task.Assets {
				if asset.Role != previewRole ||
					!strings.HasPrefix(
						strings.ToLower(asset.MediaType),
						"image/",
					) {
					continue
				}
				item[fields.ThumbnailURL] = s.taskAssetRoute.Render(
					string(task.ID),
					string(asset.ID),
				)
				break preview
			}
		}
	}
	return item
}

func semanticTextInput(
	inputJSON json.RawMessage,
	parameters []generation.Parameter,
	semantic generation.ParameterSemantic,
) string {
	var input map[string]any
	if err := json.Unmarshal(inputJSON, &input); err != nil {
		return ""
	}
	for _, parameter := range parameters {
		if parameter.Semantic != semantic {
			continue
		}
		if value, ok := input[parameter.Name].(string); ok {
			return value
		}
	}
	return ""
}

func (s *Server) publicTaskState(state generation.TaskState) string {
	for _, configured := range s.application.TaskStates {
		if configured.InternalState == string(state) {
			return configured.PublicState
		}
	}
	return string(state)
}

func (s *Server) internalStatesForPublic(publicState string) ([]generation.TaskState, bool) {
	if publicState == "" {
		return nil, true
	}
	states := make([]generation.TaskState, 0)
	for _, configured := range s.application.TaskStates {
		if configured.PublicState == publicState {
			candidate := generation.TaskState(configured.InternalState)
			if candidate.Valid() {
				states = append(states, candidate)
			}
		}
	}
	return states, len(states) > 0
}

func boundedPositiveInteger(
	raw string,
	fallback int,
	minimum int,
	maximum int,
) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}
