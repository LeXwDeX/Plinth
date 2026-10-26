package catalog

// Provider webhook configuration and payload contracts.
import (
	"errors"
	"fmt"
	"strings"
)

func validateProviderWebhooks(
	schemas []ProviderWebhookSchema,
	runtime RuntimeSchema,
) error {
	if len(schemas) == 0 {
		return errors.New("不能为空")
	}
	seenIDs := make(map[string]struct{}, len(schemas))
	seenRouteShapes := make(map[string]struct{}, len(schemas))
	for index, schema := range schemas {
		if strings.TrimSpace(schema.ID) == "" {
			return fmt.Errorf("[%d].ID 不能为空", index)
		}
		if _, exists := seenIDs[schema.ID]; exists {
			return fmt.Errorf("[%d].ID 重复: %q", index, schema.ID)
		}
		seenIDs[schema.ID] = struct{}{}
		if err := validateProviderWebhook(schema, runtime); err != nil {
			return fmt.Errorf("[%d]: %w", index, err)
		}
		routeShape := strings.ReplaceAll(
			schema.Route,
			"{"+schema.PathParameter+"}",
			"{}",
		)
		if _, exists := seenRouteShapes[routeShape]; exists {
			return fmt.Errorf("[%d].route 与已有回调路由等价: %q", index, schema.Route)
		}
		seenRouteShapes[routeShape] = struct{}{}
	}
	return nil
}

func validateProviderWebhook(
	schema ProviderWebhookSchema,
	runtime RuntimeSchema,
) error {
	for name, value := range map[string]string{
		"provider_id":          schema.ProviderID,
		"route":                schema.Route,
		"path_parameter":       schema.PathParameter,
		"path_secret_setting":  schema.PathSecretSetting,
		"key_setting":          schema.KeySetting,
		"event_type":           schema.EventType,
		"api.timestamp_layout": schema.API.TimestampLayout,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s 不能为空", name)
		}
	}
	if !strings.HasPrefix(schema.Route, "/") ||
		!configuredRouteParametersMatch(
			schema.Route,
			[]string{schema.PathParameter},
		) {
		return errors.New("route 必须且只能声明 path_parameter")
	}
	for _, settingName := range []string{
		schema.PathSecretSetting,
		schema.KeySetting,
	} {
		setting, exists := runtime.Configuration.Settings[settingName]
		if !exists || !setting.Secret {
			return fmt.Errorf(
				"secret setting %q 不存在或未标记 secret",
				settingName,
			)
		}
	}
	if len(schema.KeyHeaders) == 0 {
		return errors.New("key_headers 不能为空")
	}
	seenHeaders := make(map[string]struct{}, len(schema.KeyHeaders))
	for _, header := range schema.KeyHeaders {
		normalized := strings.ToLower(strings.TrimSpace(header))
		if normalized == "" ||
			strings.ContainsAny(normalized, " \t\r\n:") {
			return fmt.Errorf("key_headers 包含非法 header %q", header)
		}
		if _, exists := seenHeaders[normalized]; exists {
			return fmt.Errorf("key_headers 包含重复 header %q", header)
		}
		seenHeaders[normalized] = struct{}{}
	}
	if len(schema.PayloadJobIDFields) == 0 {
		return errors.New("payload_job_id_fields 不能为空")
	}
	payloadJobIDFields := make(
		map[string]string,
		len(schema.PayloadJobIDFields),
	)
	for index, field := range schema.PayloadJobIDFields {
		payloadJobIDFields[fmt.Sprintf("field_%d", index)] = field
	}
	if err := validateUniqueWireNames(
		"payload_job_id_fields",
		payloadJobIDFields,
	); err != nil {
		return err
	}
	if err := validateUniqueWireNames(
		"api.fields",
		map[string]string{
			"error":     schema.API.Fields.Error,
			"received":  schema.API.Fields.Received,
			"timestamp": schema.API.Fields.Timestamp,
			"status":    schema.API.Fields.Status,
			"service":   schema.API.Fields.Service,
		},
	); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"not_found":    schema.API.Values.NotFound,
		"unauthorized": schema.API.Values.Unauthorized,
		"bad_request":  schema.API.Values.BadRequest,
		"unavailable":  schema.API.Values.Unavailable,
		"ok":           schema.API.Values.OK,
		"service":      schema.API.Values.Service,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("api.values.%s 不能为空", name)
		}
	}
	statuses := map[string]int{
		"ok":           schema.API.Statuses.OK,
		"bad_request":  schema.API.Statuses.BadRequest,
		"unauthorized": schema.API.Statuses.Unauthorized,
		"not_found":    schema.API.Statuses.NotFound,
		"unavailable":  schema.API.Statuses.Unavailable,
	}
	seenStatuses := make(map[int]string, len(statuses))
	for name, status := range statuses {
		if status < 100 || status > 599 {
			return fmt.Errorf("api.statuses.%s 无效: %d", name, status)
		}
		if previous, exists := seenStatuses[status]; exists {
			return fmt.Errorf(
				"api.statuses.%s 与 %s 重复: %d",
				name,
				previous,
				status,
			)
		}
		seenStatuses[status] = name
	}
	return nil
}
