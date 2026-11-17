package catalog

import (
	"encoding/json"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// The tools of the external MCP service the code implements (#332). The Schema
// decides which of them are served and how they are described; a tool the code
// does not implement cannot be declared. The names are the fixed vocabulary of
// the code that dispatches them; MCPPublicToolNames lists them in order.
const (
	MCPToolListServices  = "list_services"
	MCPToolGetStatus     = "get_status"
	MCPToolGetTask       = "get_task"
	MCPToolListTasks     = "list_tasks"
	MCPToolGetTaskAssets = "get_task_assets"
	// The tools that start work (scope generate). upload_image only stores a
	// picture; the others create a Task through the generation kernel.
	MCPToolUploadImage      = "upload_image"
	MCPToolGenerateImage    = "generate_image"
	MCPToolRemoveBackground = "remove_background"
	MCPToolCreateModel      = "create_model"
	MCPToolProcessModel     = "process_model"
)

// MCPPublicToolNames is the tools the code implements, in declaration order.
func MCPPublicToolNames() []string {
	return []string{
		MCPToolListServices, MCPToolGetStatus, MCPToolGetTask, MCPToolListTasks, MCPToolGetTaskAssets,
		MCPToolUploadImage, MCPToolGenerateImage, MCPToolRemoveBackground, MCPToolCreateModel, MCPToolProcessModel,
	}
}

// The property names of the status block the code writes. The Schema declares
// the block as a JSON Schema, and validation requires these names in it, so
// the declared contract and the emitted block cannot drift apart.
const (
	MCPStatusStepMode       = "step_mode"
	MCPStatusStepModeLabel  = "step_mode_label"
	MCPStatusStepCap        = "step_cap"
	MCPStatusAsset          = "asset"
	MCPStatusTotalUsedSteps = "total_used_steps"
	MCPStatusBalances       = "balances"
	MCPStatusStepCosts      = "step_costs"
	MCPStatusFailure        = "failure"
)

// MCPPublicSchema is application.mcp_public (#332): the external MCP service.
// It is served by its own listener of the process, never by the main web
// server, and only on an address the runtime configuration names. Its
// identity is a personal access key (application.access_keys) and nothing
// else; the 3D steps follow application.step_policy.
//
// The section is optional so older Catalog documents stay valid. Without it,
// or with enabled false, no listener starts. The whole section is server-side:
// nothing of it reaches the browser's public Schema.
type MCPPublicSchema struct {
	// Enabled turns the service on. The listener also needs an address in the
	// runtime configuration (mcp_public_listen_addr).
	Enabled bool `json:"enabled"`
	// Path is the JSON-RPC endpoint (POST). DownloadPath is the asset
	// download route of the same listener (GET), with {taskID} and {assetID}.
	Path         string `json:"path"`
	DownloadPath string `json:"download_path"`
	// ProtocolVersion is the MCP wire revision the endpoint speaks.
	ProtocolVersion string `json:"protocol_version"`
	// ServerName is the name server/discover reports; Instructions is the
	// usage note it carries (the status block, the step modes).
	ServerName   string `json:"server_name"`
	Instructions string `json:"instructions"`
	// MaxRequestBytes bounds a request body, MaxResponseBytes a JSON
	// response. The timeouts are those of the listener's http.Server.
	MaxRequestBytes          int64 `json:"max_request_bytes"`
	MaxResponseBytes         int64 `json:"max_response_bytes"`
	ReadHeaderTimeoutSeconds int   `json:"read_header_timeout_seconds"`
	ReadTimeoutSeconds       int   `json:"read_timeout_seconds"`
	WriteTimeoutSeconds      int   `json:"write_timeout_seconds"`
	IdleTimeoutSeconds       int   `json:"idle_timeout_seconds"`
	// PollIntervalSeconds is the polling interval a task result suggests
	// while the task is not finished.
	PollIntervalSeconds int `json:"poll_interval_seconds"`
	// ListPageSize and ListMaxPageSize are the default and largest page of
	// list_tasks.
	ListPageSize    int `json:"list_page_size"`
	ListMaxPageSize int `json:"list_max_page_size"`
	// ServiceGroups groups the capabilities list_services shows. A capability
	// outside every group is not offered through the service.
	ServiceGroups []MCPPublicServiceGroup `json:"service_groups"`
	StatusBlock   MCPPublicStatusBlock    `json:"status_block"`
	// Assets names the assets the generation tools work on, Generation the rules
	// of starting a Task through them, Upload the picture upload and Audit how
	// those calls are told apart in the product audit.
	Assets     MCPPublicAssets     `json:"assets"`
	Generation MCPPublicGeneration `json:"generation"`
	Upload     MCPPublicUpload     `json:"upload"`
	Audit      MCPPublicAudit      `json:"audit"`
	Tools      []MCPPublicTool     `json:"tools"`
	Messages   MCPPublicMessages   `json:"messages"`
}

// MCPPublicAssets is how a caller's asset name becomes a chat session (#332):
// a name maps, per person, to one session that is made the first time the name
// is used and marked as coming from MCP.
type MCPPublicAssets struct {
	// NameMaxLength is the longest asset name, in characters, once whitespace
	// is folded.
	NameMaxLength int `json:"name_max_length"`
	// TitleTemplate is the session's title ({name} is the asset name);
	// DefaultTitle the title of the person's default asset, the one a call that
	// names no asset works on.
	TitleTemplate string `json:"title_template"`
	DefaultTitle  string `json:"default_title"`
}

// MCPPublicGeneration is what the tools that start a Task share.
type MCPPublicGeneration struct {
	// IdempotencyKeyPrefix is put before the caller's idempotency_key to make
	// the key of the Task and of its grant, so a caller's key can never meet the
	// key another entry (the browser, the Agent) made: the person's keys are one
	// namespace.
	IdempotencyKeyPrefix string `json:"idempotency_key_prefix"`
	// SourceVariantPreference orders the saved model files of a source Task
	// when a processing service takes a file and the caller named none, the
	// main model first (as the Agent's processing tools do).
	SourceVariantPreference []string `json:"source_variant_preference"`
}

// MCPPublicUpload bounds upload_image. The picture goes through the same
// ingestion as a browser upload; this is the cap of the base64 route.
type MCPPublicUpload struct {
	// MaxBytes bounds the decoded picture. The base64 text of it, and the rest
	// of the request, must fit max_request_bytes.
	MaxBytes int64 `json:"max_bytes"`
	// MediaTypes are the picture types the tool accepts. The type is read from
	// the bytes, never taken from the caller.
	MediaTypes        []string `json:"media_types"`
	FilenameMaxLength int      `json:"filename_max_length"`
}

// MCPPublicAudit names how a Task or a picture the external service made is
// marked in the product audit: the same actions the browser writes, with the
// entry and the access key in the detail.
type MCPPublicAudit struct {
	EntryField     string `json:"entry_field"`
	EntryValue     string `json:"entry_value"`
	AccessKeyField string `json:"access_key_field"`
}

// MCPPublicServiceGroup is one group of list_services.
type MCPPublicServiceGroup struct {
	ID           string                      `json:"id"`
	Label        string                      `json:"label"`
	Capabilities []schemacontract.Capability `json:"capabilities"`
}

// MCPPublicTool declares one tool. InputSchema describes the arguments (closed
// objects only); OutputSchema describes the tool's own result. The status
// block is added to the served output schema under StatusBlock.Key.
type MCPPublicTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Scope is the access key scope the tool needs (read or generate).
	Scope string `json:"scope"`
	// Capabilities are the capabilities a tool that starts a Task may start,
	// among them the ones whose earlier failed attempts of a session count when
	// the step is checked for a repeated charge. Empty for a tool that starts
	// none.
	Capabilities []schemacontract.Capability `json:"capabilities,omitempty"`
	InputSchema  json.RawMessage             `json:"input_schema"`
	OutputSchema json.RawMessage             `json:"output_schema"`
}

// MCPPublicStatusBlock declares the facts every tool response carries so the
// calling Agent can manage its own 3D steps: the step mode, the steps used by
// the asset and by the person through the service, the balances of the 3D
// providers, the approximate cost of a step and, for a failed task, why it
// failed and what to do. Reading it never counts a step and never charges.
type MCPPublicStatusBlock struct {
	// Key is the one fixed key of structuredContent that holds the block.
	Key string `json:"key"`
	// Schema is the block as a JSON Schema; its properties are the names the
	// code writes (the MCPStatus constants).
	Schema json.RawMessage `json:"schema"`
	// BalanceAccounts are the provider accounts (runtime.provider_accounts)
	// whose balance the block reads (the 3D providers), in display order. One
	// the deployment does not declare is left out of the block.
	BalanceAccounts []string `json:"balance_accounts"`
	// MaxAssets bounds the assets get_status lists.
	MaxAssets int `json:"max_assets"`
	// CostTaskWindow is how many of an asset's latest tasks name the services
	// whose per-step cost the block shows.
	CostTaskWindow int `json:"cost_task_window"`
	// Summary is the one short line appended to the text content.
	Summary MCPPublicStatusSummary `json:"summary"`
}

// MCPPublicStatusSummary is the copy of the status line. Template carries
// {mode}, {asset_steps}, {total_steps} and {balances}; TemplateNoAsset is the
// same without the asset. BalanceItem carries {label}, {amount} and
// {unit_label}; BalanceUnknown carries {label}. Unknown is the word for a
// number that could not be read.
type MCPPublicStatusSummary struct {
	Template        string `json:"template"`
	TemplateNoAsset string `json:"template_no_asset"`
	BalanceItem     string `json:"balance_item"`
	BalanceUnknown  string `json:"balance_unknown"`
	Unknown         string `json:"unknown"`
	Separator       string `json:"separator"`
}

// MCPPublicMessages is the copy of the service's answers.
type MCPPublicMessages struct {
	Unauthenticated string `json:"unauthenticated"`
	// InsufficientScope carries {scope}; InvalidArguments carries {detail}.
	InsufficientScope string `json:"insufficient_scope"`
	InvalidArguments  string `json:"invalid_arguments"`
	TaskNotFound      string `json:"task_not_found"`
	AssetNotFound     string `json:"asset_not_found"`
	NotFound          string `json:"not_found"`
	MethodNotAllowed  string `json:"method_not_allowed"`
	RequestTooLarge   string `json:"request_too_large"`
	Internal          string `json:"internal"`

	// The answers of the tools that start work. NeedsPerson carries {task_id};
	// ServiceUnavailable and ServiceMismatch {service}; TooManyInFlight
	// {seconds}; AssetNameInvalid {max}; UploadTooLarge
	// {max}; UploadTypeNotAllowed {types}. A step that hit the asset's 3D step
	// cap is worded by step_policy.messages.cap_reached.
	NeedsPerson          string `json:"needs_person"`
	ServiceUnavailable   string `json:"service_unavailable"`
	ServiceMismatch      string `json:"service_mismatch"`
	NoService            string `json:"no_service"`
	InputRefused         string `json:"input_refused"`
	SourceNotFound       string `json:"source_not_found"`
	SourceNotReady       string `json:"source_not_ready"`
	SourceIncompatible   string `json:"source_incompatible"`
	ImageNotFound        string `json:"image_not_found"`
	AssetNameInvalid     string `json:"asset_name_invalid"`
	AssetProjectMismatch string `json:"asset_project_mismatch"`
	ProjectNotFound      string `json:"project_not_found"`
	IdempotencyConflict  string `json:"idempotency_conflict"`
	InProgress           string `json:"in_progress"`
	TooManyInFlight      string `json:"too_many_in_flight"`
	UploadTooLarge       string `json:"upload_too_large"`
	UploadInvalid        string `json:"upload_invalid"`
	UploadTypeNotAllowed string `json:"upload_type_not_allowed"`
}

// ToolOutputSchema is the output schema a tool is served with: its own result
// schema with the status block under the Schema's key, required. A caller gets
// an error when the tool is not declared or a stored schema is not an object.
func (m MCPPublicSchema) ToolOutputSchema(name string) (json.RawMessage, bool, error) {
	for _, tool := range m.Tools {
		if tool.Name != name {
			continue
		}
		var output map[string]any
		if err := json.Unmarshal(tool.OutputSchema, &output); err != nil {
			return nil, true, err
		}
		var block any
		if err := json.Unmarshal(m.StatusBlock.Schema, &block); err != nil {
			return nil, true, err
		}
		properties, _ := output["properties"].(map[string]any)
		if properties == nil {
			properties = map[string]any{}
		}
		properties[m.StatusBlock.Key] = block
		output["properties"] = properties
		required, _ := output["required"].([]any)
		output["required"] = append(required, m.StatusBlock.Key)
		encoded, err := json.Marshal(output)
		return encoded, true, err
	}
	return nil, false, nil
}
