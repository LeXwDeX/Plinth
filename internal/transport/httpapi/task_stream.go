package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func (s *Server) legacyTaskStream(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	scope := taskReadScope(principal)
	configured := s.application.TaskStream
	externalType := request.PathValue(
		configured.API.PathParameters.Type,
	)
	taskID := generation.TaskID(request.PathValue(
		configured.API.PathParameters.TaskID,
	))
	capabilityID, exists := taskStreamCapability(
		configured.Aliases,
		externalType,
	)
	if !exists {
		s.writeTaskStreamHTTPError(
			writer,
			http.StatusBadRequest,
			strings.ReplaceAll(
				configured.Presentation.UnsupportedType,
				"{type}",
				externalType,
			),
		)
		return
	}
	capability, exists := taskStreamPresentation(
		s.application.Capabilities,
		capabilityID,
	)
	if !exists || capability.LegacyAPI == nil {
		s.writeTaskStreamHTTPError(
			writer,
			http.StatusBadRequest,
			configured.Presentation.StreamFailed,
		)
		return
	}

	task, err := s.generation.GetScoped(
		request.Context(),
		scope,
		taskID,
	)
	if err != nil || task.Capability != capabilityID {
		s.writeTaskStreamHTTPError(
			writer,
			http.StatusNotFound,
			configured.Presentation.TaskNotFound,
		)
		return
	}
	payload, err := s.projectTaskStream(
		requestLocale(request),
		task,
		capability,
		externalType,
	)
	if err != nil {
		s.writeTaskStreamHTTPError(
			writer,
			http.StatusInternalServerError,
			configured.Presentation.StreamFailed,
		)
		return
	}

	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)

	// 声明式 opt-in（§6.2 触点 6）：缺省保持旧契约（终态立即断流）；
	// 显式 completion=post_success 时，SUCCEEDED 且 resources_pending 的
	// 任务继续按版本轮询，资源全部落地（或截止收尾）后断流。
	postSuccessOptIn := false
	if configured.API.Query.Completion != "" {
		postSuccessOptIn = request.URL.Query().Get(configured.API.Query.Completion) ==
			"post_success"
	}

	encoded, ok := writeTaskStreamEvent(
		writer,
		configured.Events.Message,
		payload,
	)
	if !ok {
		return
	}
	flushHTTPWriter(writer)
	if task.State.Terminal() && !postSuccessStreamActive(task, postSuccessOptIn) {
		return
	}

	ticker := time.NewTicker(time.Duration(
		configured.Timing.PollIntervalMilliseconds,
	) * time.Millisecond)
	defer ticker.Stop()
	last := encoded
	for {
		select {
		case <-request.Context().Done():
			return
		case <-ticker.C:
			version, versionErr := s.generation.GetVersionScoped(request.Context(), scope, taskID)
			if versionErr != nil {
				s.writeTaskStreamEventError(writer, http.StatusNotFound, configured.Presentation.TaskNotFound)
				return
			}
			if version == task.Version {
				continue
			}
			task, err = s.generation.GetScoped(
				request.Context(),
				scope,
				taskID,
			)
			if err != nil {
				s.writeTaskStreamEventError(
					writer,
					http.StatusNotFound,
					configured.Presentation.TaskNotFound,
				)
				return
			}
			payload, err = s.projectTaskStream(
				requestLocale(request),
				task,
				capability,
				externalType,
			)
			if err != nil {
				s.writeTaskStreamEventError(
					writer,
					http.StatusInternalServerError,
					configured.Presentation.StreamFailed,
				)
				return
			}
			next, marshalErr := json.Marshal(payload)
			if marshalErr != nil {
				s.writeTaskStreamEventError(
					writer,
					http.StatusInternalServerError,
					configured.Presentation.StreamFailed,
				)
				return
			}
			if !bytes.Equal(last, next) {
				if _, ok = writeTaskStreamEvent(
					writer,
					configured.Events.Message,
					payload,
				); !ok {
					return
				}
				flushHTTPWriter(writer)
				last = next
			}
			if task.State.Terminal() && !postSuccessStreamActive(task, postSuccessOptIn) {
				return
			}
		}
	}
}

// postSuccessStreamActive 判定终态任务是否继续续流：仅 opt-in 且
// SUCCEEDED 且仍有 pending 资源且未过截止时刻（deadline 收尾后资源态
// 变为 incomplete，自然停止续流；这里的时刻兜底覆盖 finalizer 尚未
// 跑到的窗口）。
func postSuccessStreamActive(task generation.Task, optIn bool) bool {
	if !optIn || task.State != generation.TaskSucceeded {
		return false
	}
	if !generation.ResourcesPending(task.Resources) {
		return false
	}
	if task.FinishedAt == nil {
		return true
	}
	deadlineSeconds := 0
	if task.Offering.PostSuccess != nil {
		deadlineSeconds = task.Offering.PostSuccess.DeadlineSeconds
	}
	return time.Now().Before(task.FinishedAt.Add(
		time.Duration(deadlineSeconds) * time.Second,
	))
}

func (s *Server) projectTaskStream(
	locale string,
	task generation.Task,
	capability catalog.CapabilityPresentation,
	externalType string,
) (map[string]any, error) {
	response := map[string]any{}
	if err := json.Unmarshal(task.Input, &response); err != nil {
		return nil, err
	}
	legacy := capability.LegacyAPI
	if len(legacy.AssetAliases) > 0 {
		presentationOffering := task.Offering
		if current, exists := s.generation.Offering(task.Offering.ID); exists {
			presentationOffering.Parameters = current.Parameters
		}
		restoreLegacyAssetAliases(
			response,
			presentationOffering,
			legacy.AssetAliases,
			s.assetItemRoute,
		)
	}
	restoreLegacyTaskAliases(response, legacy.TaskAliases, task.Offering.ID)
	restoreLegacyFields(response, legacy.Fields, task.Offering.ID)
	projectLegacyOutputAssets(
		response,
		task,
		legacy.OutputProjections,
		legacy.AssetDownload,
		s.taskAssetRoute,
	)
	projectLegacyResultAssets(
		response,
		task,
		*legacy,
		s.taskAssetRoute,
	)

	fields := s.application.TaskStream.API.Fields
	response[fields.TaskID] = string(task.ID)
	response[fields.Type] = externalType
	response[fields.Status] = s.publicTaskState(task.State)
	response[fields.Progress] = task.Progress
	response[fields.CreatedAt] = task.CreatedAt.UnixMilli()
	response[fields.UpdatedAt] = task.UpdatedAt.UnixMilli()
	if task.Failure != nil {
		response[fields.TaskError] = map[string]string{
			fields.Message: s.publicFailure(locale, task.Failure).Detail,
		}
	}
	redactExternalAddresses(response)
	return response, nil
}

func taskStreamCapability(
	aliases []catalog.TaskStreamCapabilityAlias,
	externalType string,
) (generation.Capability, bool) {
	for _, alias := range aliases {
		if alias.ExternalType == externalType {
			return alias.Capability, true
		}
	}
	return "", false
}

func taskStreamPresentation(
	capabilities []catalog.CapabilityPresentation,
	capability generation.Capability,
) (catalog.CapabilityPresentation, bool) {
	for _, configured := range capabilities {
		if configured.ID == capability {
			return configured, true
		}
	}
	return catalog.CapabilityPresentation{}, false
}

func (s *Server) writeTaskStreamHTTPError(
	writer http.ResponseWriter,
	status int,
	message string,
) {
	writeJSON(writer, status, map[string]string{
		s.application.TaskStream.API.Fields.Error: traceFromWriter(writer).localeText(message),
	})
}

func (s *Server) writeTaskStreamEventError(
	writer http.ResponseWriter,
	status int,
	message string,
) {
	fields := s.application.TaskStream.API.Fields
	_, _ = writeTaskStreamEvent(
		writer,
		s.application.TaskStream.Events.Error,
		map[string]any{
			fields.StatusCode: status,
			fields.Message:    traceFromWriter(writer).localeText(message),
		},
	)
	flushHTTPWriter(writer)
}

func writeTaskStreamEvent(
	writer io.Writer,
	event string,
	payload any,
) ([]byte, bool) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, false
	}
	if _, err = io.WriteString(
		writer,
		"event: "+event+"\ndata: "+string(encoded)+"\n\n",
	); err != nil {
		return nil, false
	}
	return encoded, true
}

func flushHTTPWriter(writer http.ResponseWriter) {
	if flusher, ok := writer.(http.Flusher); ok {
		flusher.Flush()
	}
}
