package catalog

// Task event and stream configuration contracts.
import (
	"errors"
	"fmt"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
	"slices"
	"strings"
)

// validateTaskEvents 强制白名单非空且显式（P3 交付口径）：至少声明一个
// 事件类型；类型非空且不重复；每个类型至少声明一个公开字段且字段不重复。
func validateTaskEvents(schema TaskEventsRuntimeSchema) error {
	if len(schema.PublicProjection) == 0 {
		return errors.New("task_events.public_projection 必须显式声明公开事件类型白名单")
	}
	seenTypes := make(map[string]struct{}, len(schema.PublicProjection))
	for index, entry := range schema.PublicProjection {
		if strings.TrimSpace(entry.Type) == "" {
			return fmt.Errorf("task_events.public_projection[%d].type 不能为空", index)
		}
		if _, duplicate := seenTypes[entry.Type]; duplicate {
			return fmt.Errorf("task_events.public_projection 重复声明事件类型 %q", entry.Type)
		}
		seenTypes[entry.Type] = struct{}{}
		if len(entry.Fields) == 0 {
			return fmt.Errorf("task_events.public_projection[%q] 必须显式声明至少一个公开字段", entry.Type)
		}
		seenFields := make(map[string]struct{}, len(entry.Fields))
		for _, field := range entry.Fields {
			if strings.TrimSpace(field) == "" {
				return fmt.Errorf("task_events.public_projection[%q] 含空字段名", entry.Type)
			}
			if _, duplicate := seenFields[field]; duplicate {
				return fmt.Errorf("task_events.public_projection[%q] 重复声明字段 %q", entry.Type, field)
			}
			seenFields[field] = struct{}{}
		}
	}
	return nil
}

func validateTaskStream(
	schema TaskStreamSchema,
	runtime RuntimeSchema,
	capabilities map[schemacontract.Capability]struct{},
) error {
	fields := map[string]string{
		"task_id":     schema.API.Fields.TaskID,
		"type":        schema.API.Fields.Type,
		"status":      schema.API.Fields.Status,
		"progress":    schema.API.Fields.Progress,
		"created_at":  schema.API.Fields.CreatedAt,
		"updated_at":  schema.API.Fields.UpdatedAt,
		"task_error":  schema.API.Fields.TaskError,
		"message":     schema.API.Fields.Message,
		"status_code": schema.API.Fields.StatusCode,
		"error":       schema.API.Fields.Error,
	}
	seenFields := make(map[string]string, len(fields))
	for name, field := range fields {
		if strings.TrimSpace(field) == "" {
			return fmt.Errorf("api.fields.%s 不能为空", name)
		}
		if previous, exists := seenFields[field]; exists {
			return fmt.Errorf(
				"api.fields.%s 与 %s 重复为 %q",
				previous,
				name,
				field,
			)
		}
		seenFields[field] = name
	}

	typeParameter := strings.TrimSpace(schema.API.PathParameters.Type)
	taskParameter := strings.TrimSpace(schema.API.PathParameters.TaskID)
	for name, parameter := range map[string]string{
		"type":    typeParameter,
		"task_id": taskParameter,
	} {
		if parameter == "" || strings.ContainsAny(parameter, "{} /?#") {
			return fmt.Errorf("api.path_parameters.%s 无效", name)
		}
	}
	if typeParameter == taskParameter {
		return errors.New("api.path_parameters.type 与 task_id 不能相同")
	}
	// W6：声明了 completion 查询参数名时必须是合法 wire 标识符；空值
	// 表示未声明 opt-in（保持旧契约）。
	if completion := strings.TrimSpace(schema.API.Query.Completion); completion != "" &&
		strings.ContainsAny(completion, "{} /?#&=") {
		return errors.New("api.query.completion 无效")
	}
	parameters := configuredRouteParameters(runtime.HTTP.LegacyRoutes.TaskStream)
	if len(parameters) != 2 ||
		!slices.Contains(parameters, typeParameter) ||
		!slices.Contains(parameters, taskParameter) {
		return errors.New(
			"runtime.http.legacy_routes.task_stream 必须且只能声明 type 与 task_id 参数",
		)
	}

	if len(schema.Aliases) == 0 {
		return errors.New("aliases 不能为空")
	}
	aliases := make(map[string]struct{}, len(schema.Aliases))
	for index, alias := range schema.Aliases {
		externalType := strings.TrimSpace(alias.ExternalType)
		if externalType == "" ||
			strings.ContainsAny(externalType, "{} /?#") {
			return fmt.Errorf("aliases[%d].external_type 无效", index)
		}
		if _, exists := aliases[externalType]; exists {
			return fmt.Errorf("aliases external_type 重复: %q", externalType)
		}
		if _, exists := capabilities[alias.Capability]; !exists {
			return fmt.Errorf(
				"aliases[%d].capability 引用了未知能力 %q",
				index,
				alias.Capability,
			)
		}
		aliases[externalType] = struct{}{}
	}

	for name, event := range map[string]string{
		"message": schema.Events.Message,
		"error":   schema.Events.Error,
	} {
		if strings.TrimSpace(event) == "" ||
			strings.ContainsAny(event, "\r\n") {
			return fmt.Errorf("events.%s 无效", name)
		}
	}
	if schema.Timing.PollIntervalMilliseconds <= 0 ||
		schema.Timing.PollIntervalMilliseconds > 60_000 {
		return errors.New(
			"timing.poll_interval_milliseconds 必须在 1 到 60000 之间",
		)
	}
	if !strings.Contains(schema.Presentation.UnsupportedType, "{type}") {
		return errors.New("presentation.unsupported_type 必须包含 {type}")
	}
	if strings.TrimSpace(schema.Presentation.StreamFailed) == "" {
		return errors.New("presentation.stream_failed 不能为空")
	}
	if strings.TrimSpace(schema.Presentation.TaskNotFound) == "" {
		return errors.New("presentation.task_not_found 不能为空")
	}
	return nil
}
