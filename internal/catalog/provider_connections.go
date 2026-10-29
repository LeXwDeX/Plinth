package catalog

// Provider connection lookup and protocol configuration validation.
import (
	"errors"
	"fmt"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
	"strings"
)

// ProviderProtocolEnum 是受支持的连接协议封闭枚举；工厂只按 protocol
// 分支，未知协议在加载期拒绝（启动失败）。
var ProviderProtocolEnum = map[string]struct{}{
	"image-sync":        {},
	"meshy":             {},
	"tripo":             {},
	"hyper3d.rodin":     {},
	"matting":           {},
	"local-model":       {},
	"local-recovery":    {},
	"mock":              {},
	"openai-compatible": {},
	"http-json":         {},
}

// providerConnectionByID 按 ID 查找声明连接。
func providerConnectionByID(
	connections []ProviderConnectionSchema,
	id string,
) (ProviderConnectionSchema, bool) {
	for _, connection := range connections {
		if connection.ID == id {
			return connection, true
		}
	}
	return ProviderConnectionSchema{}, false
}

// ProviderConnectionByID 按 ID 查找声明连接（导出给 app 装配层使用）。
func ProviderConnectionByID(
	connections []ProviderConnectionSchema,
	id string,
) (ProviderConnectionSchema, bool) {
	return providerConnectionByID(connections, id)
}

// ProtocolByAdapterKey 构建 adapter_key → protocol 映射（供发布路由按协议
// 校验）；同一 adapter_key 只允许出现在一条连接中。
func ProtocolByAdapterKey(
	connections []ProviderConnectionSchema,
) map[string]string {
	result := make(map[string]string, len(connections))
	for _, connection := range connections {
		if connection.AdapterKey == "" {
			continue
		}
		result[connection.AdapterKey] = connection.Protocol
	}
	return result
}

// ConnectionCapabilitiesByAdapterKey 构建 adapter_key → 声明能力集合
// （无条件 + 条件能力）的映射：发布路由校验用它判定 Offering capability
// 是否被连接声明，取代按协议硬编码的能力名单。
func ConnectionCapabilitiesByAdapterKey(
	connections []ProviderConnectionSchema,
) map[string][]schemacontract.Capability {
	result := make(map[string][]schemacontract.Capability, len(connections))
	for _, connection := range connections {
		if connection.AdapterKey == "" {
			continue
		}
		capabilities := append(
			[]schemacontract.Capability(nil),
			connection.Capabilities...,
		)
		for _, group := range connection.ConditionalCapabilities {
			capabilities = append(capabilities, group.Capabilities...)
		}
		result[connection.AdapterKey] = capabilities
	}
	return result
}

func validateProviderConnections(schema RuntimeSchema) error {
	seenIDs := map[string]struct{}{}
	seenAdapterKeys := map[string]struct{}{}
	for _, connection := range schema.ProviderConnections {
		if strings.TrimSpace(connection.ID) == "" {
			return errors.New("连接缺少 id")
		}
		if _, exists := seenIDs[connection.ID]; exists {
			return fmt.Errorf("连接 id %q 重复", connection.ID)
		}
		seenIDs[connection.ID] = struct{}{}
		if _, known := ProviderProtocolEnum[connection.Protocol]; !known {
			return fmt.Errorf(
				"连接 %q 使用未知协议 %q",
				connection.ID,
				connection.Protocol,
			)
		}
		if connection.Protocol == "local-model" {
			if connection.BaseURLSetting != "" || connection.APIKeySetting != "" || connection.AuthHeader != "" || connection.AuthPrefix != "" || connection.RequestTimeoutSeconds != 0 || connection.AllowHTTPAssets || len(connection.ConditionalCapabilities) != 0 || connection.ErrorMapping != nil || connection.StatusMapping != nil || len(connection.Capabilities) != 1 || connection.Capabilities[0] != schemacontract.FormatConversion {
				return fmt.Errorf("local-model connection %q must declare only local format conversion without network settings", connection.ID)
			}
		}
		if connection.Protocol == "local-recovery" {
			if connection.BaseURLSetting != "" || connection.APIKeySetting != "" || connection.AuthHeader != "" || connection.AuthPrefix != "" || connection.RequestTimeoutSeconds != 0 || connection.AllowHTTPAssets || len(connection.ConditionalCapabilities) != 0 || connection.ErrorMapping != nil || connection.StatusMapping != nil || len(connection.Capabilities) != 1 || connection.Capabilities[0] != schemacontract.AssetRecovery {
				return fmt.Errorf("local-recovery connection %q must declare only receipt recovery without network settings", connection.ID)
			}
		}
		if connection.Protocol == "mock" {
			continue
		}
		if strings.TrimSpace(connection.AdapterKey) == "" {
			return fmt.Errorf("连接 %q 缺少 adapter_key", connection.ID)
		}
		if _, exists := seenAdapterKeys[connection.AdapterKey]; exists {
			return fmt.Errorf(
				"连接 %q 的 adapter_key %q 与其他连接重复",
				connection.ID,
				connection.AdapterKey,
			)
		}
		seenAdapterKeys[connection.AdapterKey] = struct{}{}
		for _, setting := range []string{
			connection.BaseURLSetting,
			connection.APIKeySetting,
		} {
			if setting == "" {
				continue
			}
			if _, exists := schema.Configuration.Settings[setting]; !exists {
				return fmt.Errorf(
					"连接 %q 引用了未知运行设置 %q",
					connection.ID,
					setting,
				)
			}
		}
		if connection.RequestTimeoutSeconds < 0 {
			return fmt.Errorf(
				"连接 %q 的 request_timeout_seconds 不能为负数",
				connection.ID,
			)
		}
		capabilities := map[schemacontract.Capability]struct{}{}
		for _, capability := range connection.Capabilities {
			if !capability.Valid() {
				return fmt.Errorf(
					"连接 %q 声明了未知 capability %q",
					connection.ID,
					capability,
				)
			}
			capabilities[capability] = struct{}{}
		}
		for index, group := range connection.ConditionalCapabilities {
			if len(group.Capabilities) == 0 || len(group.AllOfSettings) == 0 {
				return fmt.Errorf(
					"连接 %q 的 conditional_capabilities[%d] 必须同时声明 capabilities 与 all_of_settings",
					connection.ID,
					index,
				)
			}
			for _, capability := range group.Capabilities {
				if !capability.Valid() {
					return fmt.Errorf(
						"连接 %q 的 conditional_capabilities[%d] 声明了未知 capability %q",
						connection.ID,
						index,
						capability,
					)
				}
				if _, exists := capabilities[capability]; exists {
					return fmt.Errorf(
						"连接 %q 的 capability %q 同时出现在无条件与条件声明中",
						connection.ID,
						capability,
					)
				}
			}
			for _, setting := range group.AllOfSettings {
				if _, exists := schema.Configuration.Settings[setting]; !exists {
					return fmt.Errorf(
						"连接 %q 的 conditional_capabilities[%d] 引用了未知运行设置 %q",
						connection.ID,
						index,
						setting,
					)
				}
			}
		}
		if err := validateErrorMapping(connection.ID, connection.ErrorMapping); err != nil {
			return err
		}
		if err := validateStatusMapping(connection.ID, connection.StatusMapping); err != nil {
			return err
		}
		if err := validateResultRefetch(connection); err != nil {
			return err
		}
	}
	return nil
}

// resultRefetchProtocols are the protocols whose accepted results live behind
// an upstream job the adapter can query again (Observe). Synchronous image,
// local and chat protocols hand over one-shot results or have no upstream.
var resultRefetchProtocols = map[string]bool{"meshy": true, "tripo": true, "hyper3d.rodin": true, "matting": true}

func validateResultRefetch(connection ProviderConnectionSchema) error {
	if connection.ResultRefetch == nil {
		return nil
	}
	if !resultRefetchProtocols[connection.Protocol] {
		return fmt.Errorf("连接 %q 的协议 %q 不能声明 result_refetch", connection.ID, connection.Protocol)
	}
	if err := connection.ResultRefetch.Validate(); err != nil {
		return fmt.Errorf("连接 %q: %w", connection.ID, err)
	}
	return nil
}

// ResultRefetchPolicy returns the refetch declaration of the connection that
// serves adapterKey, if it declares one (#295).
func (c *Catalog) ResultRefetchPolicy(adapterKey string) (schemacontract.ResultRefetchPolicy, bool) {
	for _, connection := range c.application.Runtime.ProviderConnections {
		if connection.AdapterKey == adapterKey && connection.ResultRefetch != nil {
			return *connection.ResultRefetch, true
		}
	}
	return schemacontract.ResultRefetchPolicy{}, false
}

func validateErrorMapping(connectionID string, mapping *schemacontract.ErrorMapping) error {
	if mapping == nil {
		return nil
	}
	for index, rangeRule := range mapping.HTTPStatusRanges {
		if rangeRule.Min <= 0 || rangeRule.Max < rangeRule.Min {
			return fmt.Errorf(
				"连接 %q 的 error_mapping.http_status_ranges[%d] 区间无效",
				connectionID,
				index,
			)
		}
		if !schemacontract.FailureClass(rangeRule.Class).Valid() {
			return fmt.Errorf(
				"连接 %q 的 error_mapping.http_status_ranges[%d] 使用未知失败分类 %q",
				connectionID,
				index,
				rangeRule.Class,
			)
		}
	}
	declaredCodes := map[string]struct{}{}
	for source, rule := range mapping.UpstreamStatusMap {
		if strings.TrimSpace(source) == "" ||
			!schemacontract.FailureClass(rule.Class).Valid() {
			return fmt.Errorf(
				"连接 %q 的 error_mapping.upstream_status_map[%q] 无效",
				connectionID,
				source,
			)
		}
	}
	for code, rule := range mapping.UpstreamCodeMap {
		if strings.TrimSpace(code) == "" ||
			!schemacontract.FailureClass(rule.Class).Valid() {
			return fmt.Errorf(
				"连接 %q 的 error_mapping.upstream_code_map[%q] 无效",
				connectionID,
				code,
			)
		}
		declaredCodes[code] = struct{}{}
	}
	for index, matcher := range mapping.MessageMatchers {
		if !schemacontract.FailureClass(matcher.Class).Valid() ||
			(matcher.Equals == "" &&
				len(matcher.ContainsAll) == 0 &&
				len(matcher.ContainsAny) == 0) {
			return fmt.Errorf(
				"连接 %q 的 error_mapping.message_matchers[%d] 无效",
				connectionID,
				index,
			)
		}
		if status := matcher.HTTPStatus; status != nil && (status.Min < 100 || status.Max > 599 || status.Min > status.Max) {
			return fmt.Errorf(
				"连接 %q 的 error_mapping.message_matchers[%d].http_status 必须是 100-599 内 min<=max 的区间",
				connectionID,
				index,
			)
		}
	}
	return nil
}

func validateStatusMapping(connectionID string, mapping *schemacontract.StatusMapping) error {
	if mapping == nil {
		return nil
	}
	for status, rule := range mapping.UpstreamStatus {
		if strings.TrimSpace(status) == "" {
			return fmt.Errorf(
				"连接 %q 的 status_mapping 含空状态词",
				connectionID,
			)
		}
		switch rule.Phase {
		case "accepted", "running", "completed", "failed", "canceled":
		default:
			return fmt.Errorf(
				"连接 %q 的 status_mapping[%q] 使用未知相位 %q",
				connectionID,
				status,
				rule.Phase,
			)
		}
		if rule.Class != "" && !schemacontract.FailureClass(rule.Class).Valid() {
			return fmt.Errorf(
				"连接 %q 的 status_mapping[%q] 使用未知失败分类 %q",
				connectionID,
				status,
				rule.Class,
			)
		}
		if rule.MaxConsecutive < 0 ||
			(rule.MaxConsecutive > 0 &&
				((rule.Phase != "accepted" && rule.Phase != "running") || rule.Class == "")) {
			return fmt.Errorf(
				"连接 %q 的 status_mapping[%q] 的 max_consecutive 只能用于带失败分类的 accepted/running 相位且不得为负",
				connectionID,
				status,
			)
		}
	}
	return nil
}

// validateProviderEnvironmentNames holds the Provider configuration contract
// (#252): a connection's base URL and API key settings are read first from
// the setting name in upper case (meshy_api_key -> MESHY_API_KEY), and any
// other environment source is a deprecated alias. The API key has no alias
// to another Provider's or the Agent's key, so a Provider is enabled only by
// its own key (an old name of that same key, such as MATTING_API_TOKEN, may
// remain as a deprecated alias). A base URL alias may not be another
// setting's canonical variable either (another connection's or the Agent's
// LLM_BASE_URL): an empty GEMINI_BASE_URL would otherwise send the Gemini key
// to the Agent's vendor. There is no exception; each connection reads only its
// own address.
func validateProviderEnvironmentNames(schema RuntimeSchema) error {
	canonicalOwners := map[string]string{}
	for name, setting := range schema.Configuration.Settings {
		if len(setting.Environment) > 0 {
			canonicalOwners[setting.Environment[0].Name] = name
		}
	}
	for _, connection := range schema.ProviderConnections {
		for kind, name := range map[string]string{"base_url_setting": connection.BaseURLSetting, "api_key_setting": connection.APIKeySetting} {
			if name == "" {
				continue
			}
			setting, exists := schema.Configuration.Settings[name]
			if !exists {
				continue
			}
			canonical := strings.ToUpper(name)
			if len(setting.Environment) == 0 || setting.Environment[0].Name != canonical || setting.Environment[0].Deprecated {
				return fmt.Errorf("连接 %q 的 %s %q 必须首先读取环境变量 %s", connection.ID, kind, name, canonical)
			}
			for _, source := range setting.Environment[1:] {
				if !source.Deprecated {
					return fmt.Errorf("连接 %q 的 %s %q 的别名 %s 必须标记 deprecated", connection.ID, kind, name, source.Name)
				}
				prefix := strings.ToUpper(strings.TrimSuffix(name, "_api_key")) + "_"
				if kind == "api_key_setting" && !strings.HasPrefix(source.Name, prefix) {
					return fmt.Errorf("连接 %q 的 API key 设置 %q 不能读取其他用途的变量 %s：Provider 只由自己的 key 启用", connection.ID, name, source.Name)
				}
				if owner, borrowed := canonicalOwners[source.Name]; borrowed && owner != name {
					return fmt.Errorf("连接 %q 的 %s %q 不能读取运行设置 %q 的规范变量 %s：本连接的 API key 会被发往该地址", connection.ID, kind, name, owner, source.Name)
				}
			}
		}
	}
	return nil
}
