package app

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/llmsettings"
)

// Chat model connections (#322 Scope 2). The deployment's own connection is
// the chat.agent settings (LLM_BASE_URL, LLM_API_KEY, LLM_MODEL,
// LLM_CONTEXT_WINDOW_TOKENS). The runtime environment lists extra connection
// IDs in llm_settings.connections.list_setting (LLM_CONNECTIONS); connection
// <ID> reads exactly <ID>_BASE_URL, <ID>_API_KEY, <ID>_MODEL and
// <ID>_CONTEXT_WINDOW_TOKENS. No connection reads another one's variables and
// nothing falls back: with one gateway for every model, each address is set
// explicitly to the same URL.

// ChatConnectionConfig is one extra connection as the environment declares
// it. APIKey is a secret: it only reaches the connection's own adapter.
type ChatConnectionConfig struct {
	ID           string
	BaseURL      string
	APIKey       string
	Model        string
	WindowTokens int
}

func loadExtraChatConnections(
	schema catalog.ApplicationSchema,
	values map[string]string,
	lookup func(string) string,
) ([]ChatConnectionConfig, error) {
	settings := schema.LLMSettings
	if settings == nil {
		return nil, nil
	}
	contract := settings.Connections
	listName := settingEnvironmentName(schema, contract.ListSetting)
	declared := map[string]bool{}
	for _, setting := range schema.Runtime.Configuration.Settings {
		for _, environment := range setting.Environment {
			declared[environment.Name] = true
		}
	}
	var result []ChatConnectionConfig
	seen := map[string]bool{contract.DefaultID: true}
	for _, raw := range strings.Split(values[contract.ListSetting], ",") {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if !contract.ValidID(id) {
			return nil, fmt.Errorf("%s: 连接 ID %q 无效（须匹配 %s）", listName, id, contract.IDPattern)
		}
		if seen[id] {
			return nil, fmt.Errorf("%s: 连接 ID %q 重复或与默认连接 %s 相同", listName, id, contract.DefaultID)
		}
		seen[id] = true
		names := contract.EnvironmentNames(id)
		for _, name := range names {
			if declared[name] {
				return nil, fmt.Errorf("%s: 连接 %s 会读取已由其他设置使用的变量 %s，请换一个连接 ID", listName, id, name)
			}
		}
		connection := ChatConnectionConfig{
			ID:      id,
			BaseURL: strings.TrimSpace(lookup(names[0])),
			APIKey:  strings.TrimSpace(lookup(names[1])),
			Model:   strings.TrimSpace(lookup(names[2])),
		}
		if raw := strings.TrimSpace(lookup(names[3])); raw != "" {
			window, err := strconv.Atoi(raw)
			if err != nil || window < 1 {
				return nil, fmt.Errorf("%s 必须是正整数（token 数）: %q", names[3], raw)
			}
			connection.WindowTokens = window
		}
		result = append(result, connection)
	}
	if len(result)+1 > contract.MaxConnections {
		return nil, fmt.Errorf("%s: 最多 %d 个对话模型连接（含默认连接）", listName, contract.MaxConnections)
	}
	return result, nil
}

// chatConnection is a connection with its values, for building its adapter.
type chatConnection struct {
	ChatConnectionConfig
	Environment [4]string
}

// chatConnections lists the deployment's connection first, then the extra
// ones in the order LLM_CONNECTIONS names them.
func chatConnections(config Config, schema catalog.ApplicationSchema) []chatConnection {
	agent := schema.Chat.Agent
	defaultID := "LLM"
	if schema.LLMSettings != nil {
		defaultID = schema.LLMSettings.Connections.DefaultID
	}
	result := []chatConnection{{
		ChatConnectionConfig: ChatConnectionConfig{
			ID:           defaultID,
			BaseURL:      configuredRuntimeSetting(config, schema, agent.BaseURLSetting),
			APIKey:       configuredRuntimeSetting(config, schema, agent.APIKeySetting),
			Model:        configuredRuntimeSetting(config, schema, agent.ModelSetting),
			WindowTokens: config.ContextWindowTokens,
		},
		Environment: [4]string{
			settingEnvironmentName(schema, agent.BaseURLSetting),
			settingEnvironmentName(schema, agent.APIKeySetting),
			settingEnvironmentName(schema, agent.ModelSetting),
			settingEnvironmentName(schema, agent.ContextWindowSetting),
		},
	}}
	for _, extra := range config.ExtraChatConnections {
		result = append(result, chatConnection{ChatConnectionConfig: extra, Environment: schema.LLMSettings.Connections.EnvironmentNames(extra.ID)})
	}
	return result
}

// llmSettingsConnections reduces the connections to whether each variable is
// set; values never leave this package. Mock mode needs no address or key.
func llmSettingsConnections(connections []chatConnection, credentialsRequired bool) []llmsettings.Connection {
	result := make([]llmsettings.Connection, 0, len(connections))
	for _, connection := range connections {
		result = append(result, llmsettings.Connection{
			ID:                  connection.ID,
			Model:               connection.Model,
			BaseURLSet:          connection.BaseURL != "",
			APIKeySet:           connection.APIKey != "",
			CredentialsRequired: credentialsRequired,
			WindowOverride:      connection.WindowTokens,
			Environment:         connection.Environment,
		})
	}
	return result
}

// missingSettings names the connection's variables without a value.
func (connection chatConnection) missingSettings() []string {
	var missing []string
	for index, value := range []string{connection.BaseURL, connection.APIKey, connection.Model} {
		if value == "" {
			missing = append(missing, connection.Environment[index])
		}
	}
	return missing
}
