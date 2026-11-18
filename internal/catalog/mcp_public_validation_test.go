package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
	"github.com/LeXwDeX/Plinth/internal/schema/toolschema"
)

// mcpToolOf is the declaration of a tool in an application a test edits.
func mcpToolOf(application *catalog.ApplicationSchema, name string) *catalog.MCPPublicTool {
	for index := range application.MCPPublic.Tools {
		if application.MCPPublic.Tools[index].Name == name {
			return &application.MCPPublic.Tools[index]
		}
	}
	panic("no tool " + name)
}

// #332: the default Catalog declares the external MCP service. It is a
// server-side section: nothing of it reaches the browser's public Schema.
func TestDefaultMCPPublicLoadsAndStaysServerSide(t *testing.T) {
	t.Parallel()
	application := defaultApplication(t)
	service := application.MCPPublic
	if service == nil {
		t.Fatal("default Catalog has no application.mcp_public")
	}
	if err := catalog.ValidateApplicationSchema(application); err != nil {
		t.Fatalf("default mcp_public invalid: %v", err)
	}
	if service.Path != "/mcp" || service.ProtocolVersion != "2026-07-28" || service.StatusBlock.Key != "status" {
		t.Fatalf("mcp_public = %+v", service)
	}
	if service.SignedDownloadPath != "/mcp/signed-assets/{token}" ||
		service.SignedDownloadTTLSeconds != 300 || service.SignedDownloadMaxTTLSeconds != 3600 {
		t.Fatalf("signed download declaration = %q, %d, %d", service.SignedDownloadPath,
			service.SignedDownloadTTLSeconds, service.SignedDownloadMaxTTLSeconds)
	}
	if strings.TrimSpace(service.Messages.SignedDownloadUnavailable) == "" {
		t.Fatal("mcp_public.messages.signed_download_unavailable is empty")
	}
	names := make([]string, 0, len(service.Tools))
	for _, tool := range service.Tools {
		names = append(names, tool.Name)
		// The tools that start work need the generate scope, the others read.
		wantScope := "read"
		switch tool.Name {
		case catalog.MCPToolUploadImage, catalog.MCPToolGenerateImage, catalog.MCPToolRemoveBackground,
			catalog.MCPToolCreateModel, catalog.MCPToolProcessModel:
			wantScope = "generate"
		}
		if tool.Scope != wantScope {
			t.Errorf("tool %s needs scope %q, want %q", tool.Name, tool.Scope, wantScope)
		}
		if wantScope == "read" && len(tool.Capabilities) > 0 {
			t.Errorf("reading tool %s declares capabilities %v", tool.Name, tool.Capabilities)
		}
	}
	if strings.Join(names, ",") != strings.Join(catalog.MCPPublicToolNames(), ",") {
		t.Fatalf("declared tools = %v, implemented = %v", names, catalog.MCPPublicToolNames())
	}
	// The shipped Catalog declares an account for every balance it names.
	for _, account := range service.StatusBlock.BalanceAccounts {
		if _, declared := application.Runtime.ProviderAccounts[account]; !declared {
			t.Errorf("balance account %q is not a runtime provider account", account)
		}
	}
	encoded, err := json.Marshal(application.Public())
	if err != nil {
		t.Fatal(err)
	}
	for _, internal := range []string{"mcp_public", "status_block", "balance_cache_seconds", "balance_accounts"} {
		if strings.Contains(string(encoded), internal) {
			t.Fatalf("the public Schema leaks %q", internal)
		}
	}
}

// #332: generate_image declares the optional mask argument of an image-to-image
// call, and the three mask refusals carry their placeholders.
func TestDefaultMCPGenerateImageMaskDeclaration(t *testing.T) {
	t.Parallel()
	application := defaultApplication(t)
	tool := mcpToolOf(&application, catalog.MCPToolGenerateImage)
	input, err := toolschema.Parse(tool.InputSchema, toolschema.Options{ClosedObjects: true, RootObject: true, FreeFormObjects: true})
	if err != nil {
		t.Fatal(err)
	}
	mask, declared := input.Properties["mask"]
	if !declared || len(mask.Types) != 1 || mask.Types[0] != "string" {
		t.Fatalf("generate_image mask = %#v (declared %v)", mask, declared)
	}
	service := application.MCPPublic
	if !strings.Contains(service.Messages.MaskInvalid, "{max}") {
		t.Fatalf("mask_invalid = %q", service.Messages.MaskInvalid)
	}
	for _, holder := range []string{"{mask_width}", "{mask_height}", "{image_width}", "{image_height}"} {
		if !strings.Contains(service.Messages.MaskSizeMismatch, holder) {
			t.Fatalf("mask_size_mismatch = %q lacks %s", service.Messages.MaskSizeMismatch, holder)
		}
	}
	if strings.TrimSpace(service.Messages.MaskNotSupported) == "" {
		t.Fatal("mcp_public.messages.mask_not_supported is empty")
	}
}

// #332 step 7b: the default Catalog declares inspect_image right after
// inspect_model, with the platform checklist vocabulary, a pixel cap inside the
// platform limit, and an output whose only required root property is the status
// block.
func TestDefaultMCPInspectImageDeclaration(t *testing.T) {
	t.Parallel()
	application := defaultApplication(t)
	service := application.MCPPublic
	names := make([]string, 0, len(service.Tools))
	for _, tool := range service.Tools {
		names = append(names, tool.Name)
	}
	if len(names) != 12 || names[5] != catalog.MCPToolInspectModel || names[6] != catalog.MCPToolInspectImage {
		t.Fatalf("tools = %v", names)
	}
	if service.InspectImage.MaxPixels != 20000000 ||
		service.InspectImage.MaxPixels > application.Chat.ImageReview.Limits.MaxPixels {
		t.Fatalf("inspect_image.max_pixels = %d", service.InspectImage.MaxPixels)
	}
	tool := mcpToolOf(&application, catalog.MCPToolInspectImage)
	if tool.Scope != "read" || len(tool.Capabilities) != 0 {
		t.Fatalf("inspect_image declaration = %+v", tool)
	}
	input, declared, err := service.ParsedInputSchema(catalog.MCPToolInspectImage)
	if err != nil || !declared {
		t.Fatalf("declared=%v err=%v", declared, err)
	}
	for _, name := range []string{"image", "task_id", "asset_id", "checklist", "purpose"} {
		if _, present := input.Properties[name]; !present {
			t.Errorf("inspect_image.input_schema lacks %q", name)
		}
	}
	if len(input.Required) != 0 {
		t.Errorf("inspect_image requires %v; the picture is named by image or task_id in the handler", input.Required)
	}
	enum := input.Properties["checklist"].Enum
	if len(enum) != 2 || enum[0] != "delivery" || enum[1] != "3d_input" {
		t.Errorf("checklist enum = %v", enum)
	}
	served, _, err := service.ToolOutputSchema(catalog.MCPToolInspectImage)
	if err != nil {
		t.Fatal(err)
	}
	output, err := toolschema.Parse(served, toolschema.Options{RootObject: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(output.Required) != 1 || output.Required[0] != service.StatusBlock.Key {
		t.Errorf("the output requires %v; only the status block may be required", output.Required)
	}
	usage := output.Properties["usage_check"]
	for _, name := range []string{"checklist", "checks", "purpose", "purpose_label", "purpose_source", "note"} {
		if _, present := usage.Properties[name]; !present {
			t.Errorf("usage_check lacks %q", name)
		}
	}
	report := output.Properties["report"]
	if report == nil {
		t.Fatal("the output has no report")
	}
	for _, name := range []string{"width", "height", "short_edge", "subject_bbox", "tiling", "edge_fringe"} {
		if _, present := report.Properties[name]; !present {
			t.Errorf("report lacks %q", name)
		}
	}
	if len(report.Required) == 0 {
		t.Error("report requires no facts")
	}
	for _, name := range []string{"tiling", "edge_fringe"} {
		found := false
		for _, required := range report.Required {
			found = found || required == name
		}
		if found {
			t.Errorf("report requires the optional %q", name)
		}
	}
}

// #332 step 7c-1: inspect_model declares an explicit purpose like
// inspect_image, and the English copy says what it does.
func TestDefaultMCPInspectModelDeclarationHasPurpose(t *testing.T) {
	t.Parallel()
	application := defaultApplication(t)
	service := application.MCPPublic
	input, declared, err := service.ParsedInputSchema(catalog.MCPToolInspectModel)
	if err != nil || !declared {
		t.Fatalf("declared=%v err=%v", declared, err)
	}
	purpose, present := input.Properties["purpose"]
	if !present {
		t.Fatal("inspect_model.input_schema lacks purpose")
	}
	if len(purpose.Types) != 1 || purpose.Types[0] != "string" ||
		purpose.MinLength == nil || *purpose.MinLength != 1 ||
		purpose.MaxLength == nil || *purpose.MaxLength != 200 {
		t.Fatalf("purpose schema = %+v", purpose)
	}
	if !strings.Contains(purpose.Description, "chat.model3d_usage") || !strings.Contains(purpose.Description, "只影响本次") {
		t.Fatalf("purpose description = %q", purpose.Description)
	}
	if len(input.Required) != 1 || input.Required[0] != "task_id" {
		t.Fatalf("inspect_model requires %v; only task_id may be required", input.Required)
	}
	if input.AdditionalProperties == nil || *input.AdditionalProperties {
		t.Fatal("inspect_model arguments must stay a closed object")
	}
	tool := mcpToolOf(&application, catalog.MCPToolInspectModel)
	localizer, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	if translated := localizer.Text("en", tool.Description); !strings.Contains(translated, "purpose") {
		t.Fatalf("the English inspect_model description omits purpose: %q", translated)
	}
}

// The status block's JSON Schema is embedded in every tool's served output
// schema under the one key, and is required there; the tool's own schema
// stays as declared.
func TestToolOutputSchemaCarriesTheStatusBlock(t *testing.T) {
	t.Parallel()
	service := defaultApplication(t).MCPPublic
	for _, tool := range service.Tools {
		served, found, err := service.ToolOutputSchema(tool.Name)
		if err != nil || !found {
			t.Fatalf("%s: found=%v err=%v", tool.Name, found, err)
		}
		parsed, err := toolschema.Parse(served, toolschema.Options{RootObject: true})
		if err != nil {
			t.Fatalf("%s: served schema invalid: %v", tool.Name, err)
		}
		if _, ok := parsed.Properties["status"]; !ok || len(parsed.Required) == 0 || parsed.Required[len(parsed.Required)-1] != "status" {
			t.Fatalf("%s: served schema lacks the required status block: %s", tool.Name, served)
		}
		for _, name := range []string{"step_mode", "asset", "total_used_steps", "balances", "step_costs", "failure", "inspection"} {
			if _, ok := parsed.Properties["status"].Properties[name]; !ok {
				t.Fatalf("%s: status block lacks %q", tool.Name, name)
			}
		}
	}
	if _, found, _ := service.ToolOutputSchema("no_such_tool"); found {
		t.Fatal("an undeclared tool has an output schema")
	}
}

func TestMCPPublicRejectsInvalidDeclarations(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*catalog.ApplicationSchema){
		"needs access keys": func(a *catalog.ApplicationSchema) {
			a.AccessKeys = nil
			a.Runtime.HTTP.Routes.AccountAccessKeys, a.Runtime.HTTP.Routes.AccountAccessKey = "", ""
		},
		"needs the step policy": func(a *catalog.ApplicationSchema) { a.StepPolicy = nil },
		"no balance cache":      func(a *catalog.ApplicationSchema) { a.StepPolicy.BalanceCacheSeconds = 0 },
		"no balance timeout":    func(a *catalog.ApplicationSchema) { a.StepPolicy.BalanceReadTimeoutSeconds = 0 },
		"huge balance cache":    func(a *catalog.ApplicationSchema) { a.StepPolicy.BalanceCacheSeconds = 4000 },
		"path with parameter":   func(a *catalog.ApplicationSchema) { a.MCPPublic.Path = "/mcp/{id}" },
		"path without slash":    func(a *catalog.ApplicationSchema) { a.MCPPublic.Path = "mcp" },
		"path trailing slash":   func(a *catalog.ApplicationSchema) { a.MCPPublic.Path = "/mcp/" },
		"root path":             func(a *catalog.ApplicationSchema) { a.MCPPublic.Path = "/" },
		"download without ids":  func(a *catalog.ApplicationSchema) { a.MCPPublic.DownloadPath = "/mcp/assets" },
		"download wrong ids":    func(a *catalog.ApplicationSchema) { a.MCPPublic.DownloadPath = "/mcp/{task}/{asset}" },
		"same routes":           func(a *catalog.ApplicationSchema) { a.MCPPublic.DownloadPath = "/mcp" },
		"signed download without token": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.SignedDownloadPath = "/mcp/signed-assets"
		},
		"signed download wrong parameters": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.SignedDownloadPath = "/mcp/signed-assets/{taskID}/{assetID}"
		},
		"signed download without slash": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.SignedDownloadPath = "mcp/signed-assets/{token}"
		},
		"signed download is the endpoint": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.SignedDownloadPath = "/mcp"
		},
		"signed download is the download path": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.SignedDownloadPath = "/mcp/tasks/{taskID}/assets/{assetID}"
		},
		"signed download outside the endpoint": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.SignedDownloadPath = "/assets/{token}"
		},
		"signed download ttl too short": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.SignedDownloadTTLSeconds = 29
		},
		"signed download ttl above its maximum": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.SignedDownloadTTLSeconds = a.MCPPublic.SignedDownloadMaxTTLSeconds + 1
		},
		"signed download maximum too long": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.SignedDownloadMaxTTLSeconds = 86401
		},
		"no signed download message": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.Messages.SignedDownloadUnavailable = " "
		},
		"no protocol version": func(a *catalog.ApplicationSchema) { a.MCPPublic.ProtocolVersion = " " },
		"no server name":      func(a *catalog.ApplicationSchema) { a.MCPPublic.ServerName = "" },
		"no instructions":     func(a *catalog.ApplicationSchema) { a.MCPPublic.Instructions = "" },
		"zero request limit":  func(a *catalog.ApplicationSchema) { a.MCPPublic.MaxRequestBytes = 0 },
		"huge response limit": func(a *catalog.ApplicationSchema) { a.MCPPublic.MaxResponseBytes = 1 << 40 },
		"zero read timeout":   func(a *catalog.ApplicationSchema) { a.MCPPublic.ReadTimeoutSeconds = 0 },
		"zero write timeout":  func(a *catalog.ApplicationSchema) { a.MCPPublic.WriteTimeoutSeconds = 0 },
		"zero poll interval":  func(a *catalog.ApplicationSchema) { a.MCPPublic.PollIntervalSeconds = 0 },
		"page above maximum":  func(a *catalog.ApplicationSchema) { a.MCPPublic.ListPageSize = a.MCPPublic.ListMaxPageSize + 1 },
		"no service groups":   func(a *catalog.ApplicationSchema) { a.MCPPublic.ServiceGroups = nil },
		"unknown capability": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.ServiceGroups[0].Capabilities = append(a.MCPPublic.ServiceGroups[0].Capabilities, "no-such-capability")
		},
		"repeated capability": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.ServiceGroups[1].Capabilities = append(a.MCPPublic.ServiceGroups[1].Capabilities, a.MCPPublic.ServiceGroups[0].Capabilities[0])
		},
		"bad group id":     func(a *catalog.ApplicationSchema) { a.MCPPublic.ServiceGroups[0].ID = "Bad Id" },
		"bad status key":   func(a *catalog.ApplicationSchema) { a.MCPPublic.StatusBlock.Key = "Status Block" },
		"empty status key": func(a *catalog.ApplicationSchema) { a.MCPPublic.StatusBlock.Key = "" },
		"status not a schema": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.StatusBlock.Schema = json.RawMessage(`{"type":"object","oneOf":[]}`)
		},
		"status misses a name": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.StatusBlock.Schema = json.RawMessage(strings.Replace(string(a.MCPPublic.StatusBlock.Schema), `"step_cap"`, `"step_limit"`, 1))
		},
		"status nested misses a name": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.StatusBlock.Schema = json.RawMessage(strings.Replace(string(a.MCPPublic.StatusBlock.Schema), `"read_at"`, `"seen_at"`, 1))
		},
		"status misses the inspection": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.StatusBlock.Schema = json.RawMessage(strings.Replace(string(a.MCPPublic.StatusBlock.Schema), `"inspection":`, `"check":`, 1))
		},
		"inspection purpose misses a name": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.StatusBlock.Schema = json.RawMessage(strings.Replace(string(a.MCPPublic.StatusBlock.Schema), `"label": {"type": "string", "description": "用途名称"}`, `"name": {"type": "string", "description": "用途名称"}`, 1))
		},
		"inspection failed check misses its name": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.StatusBlock.Schema = json.RawMessage(strings.Replace(string(a.MCPPublic.StatusBlock.Schema), `"check":`, `"name":`, 1))
		},
		"blank balance account": func(a *catalog.ApplicationSchema) { a.MCPPublic.StatusBlock.BalanceAccounts = []string{"meshy", " "} },
		"repeated balance account": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.StatusBlock.BalanceAccounts = []string{"meshy", "meshy"}
		},
		"no balance accounts": func(a *catalog.ApplicationSchema) { a.MCPPublic.StatusBlock.BalanceAccounts = nil },
		"zero asset window":   func(a *catalog.ApplicationSchema) { a.MCPPublic.StatusBlock.MaxAssets = 0 },
		"summary lacks holder": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.StatusBlock.Summary.Template = "no placeholders"
		},
		"summary no unknown": func(a *catalog.ApplicationSchema) { a.MCPPublic.StatusBlock.Summary.Unknown = "" },
		"no tools":           func(a *catalog.ApplicationSchema) { a.MCPPublic.Tools = nil },
		"unknown tool":       func(a *catalog.ApplicationSchema) { a.MCPPublic.Tools[0].Name = "delete_everything" },
		"repeated tool":      func(a *catalog.ApplicationSchema) { a.MCPPublic.Tools[1].Name = a.MCPPublic.Tools[0].Name },
		"no description":     func(a *catalog.ApplicationSchema) { a.MCPPublic.Tools[0].Description = " " },
		"unknown scope":      func(a *catalog.ApplicationSchema) { a.MCPPublic.Tools[0].Scope = "admin" },
		"open argument object": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.Tools[0].InputSchema = json.RawMessage(`{"type":"object","properties":{}}`)
		},
		"owner argument": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.Tools[0].InputSchema = json.RawMessage(`{"type":"object","properties":{"owner_id":{"type":"string"}},"additionalProperties":false}`)
		},
		"unsupported keyword": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.Tools[0].InputSchema = json.RawMessage(`{"type":"object","properties":{"a":{"type":"string","pattern":"^x"}},"additionalProperties":false}`)
		},
		"output not an object": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.Tools[0].OutputSchema = json.RawMessage(`{"type":"array","items":{"type":"string"}}`)
		},
		"output owns the status key": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.Tools[0].OutputSchema = json.RawMessage(`{"type":"object","properties":{"status":{"type":"string"}}}`)
		},
		"no asset name bound":    func(a *catalog.ApplicationSchema) { a.MCPPublic.Assets.NameMaxLength = 0 },
		"huge asset name bound":  func(a *catalog.ApplicationSchema) { a.MCPPublic.Assets.NameMaxLength = 1000 },
		"title without the name": func(a *catalog.ApplicationSchema) { a.MCPPublic.Assets.TitleTemplate = "MCP asset" },
		"no default title":       func(a *catalog.ApplicationSchema) { a.MCPPublic.Assets.DefaultTitle = " " },
		"no key prefix":          func(a *catalog.ApplicationSchema) { a.MCPPublic.Generation.IdempotencyKeyPrefix = "" },
		"key prefix with space":  func(a *catalog.ApplicationSchema) { a.MCPPublic.Generation.IdempotencyKeyPrefix = "m cp:" },
		"key prefix too long": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.Generation.IdempotencyKeyPrefix = strings.Repeat("p", 33)
		},
		"no variant preference": func(a *catalog.ApplicationSchema) { a.MCPPublic.Generation.SourceVariantPreference = nil },
		"no upload cap":         func(a *catalog.ApplicationSchema) { a.MCPPublic.Upload.MaxBytes = 0 },
		"upload cap above the request bound": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.Upload.MaxBytes = a.MCPPublic.MaxRequestBytes
		},
		"no upload types": func(a *catalog.ApplicationSchema) { a.MCPPublic.Upload.MediaTypes = nil },
		"upload type not a picture": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.Upload.MediaTypes = []string{"model/gltf-binary"}
		},
		"upload type not allowed": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.Upload.MediaTypes = []string{"image/tiff"}
		},
		"no audit entry": func(a *catalog.ApplicationSchema) { a.MCPPublic.Audit.EntryValue = "" },
		"generating tool with scope read": func(a *catalog.ApplicationSchema) {
			mcpToolOf(a, catalog.MCPToolCreateModel).Scope = "read"
		},
		"generating tool without capabilities": func(a *catalog.ApplicationSchema) {
			mcpToolOf(a, catalog.MCPToolCreateModel).Capabilities = nil
		},
		"generating tool with an unknown capability": func(a *catalog.ApplicationSchema) {
			tool := mcpToolOf(a, catalog.MCPToolCreateModel)
			tool.Capabilities = append(tool.Capabilities, "no-such-capability")
		},
		"generating tool with a capability no group serves": func(a *catalog.ApplicationSchema) {
			tool := mcpToolOf(a, catalog.MCPToolGenerateImage)
			tool.Capabilities = append(tool.Capabilities, "text-to-motion")
		},
		"upload tool with capabilities": func(a *catalog.ApplicationSchema) {
			mcpToolOf(a, catalog.MCPToolUploadImage).Capabilities = []schemacontract.Capability{"text-to-image"}
		},
		"generating tool without its idempotency key": func(a *catalog.ApplicationSchema) {
			tool := mcpToolOf(a, catalog.MCPToolCreateModel)
			tool.InputSchema = json.RawMessage(strings.Replace(string(tool.InputSchema), `"idempotency_key"`, `"retry_token"`, -1))
		},
		"idempotency key not required": func(a *catalog.ApplicationSchema) {
			tool := mcpToolOf(a, catalog.MCPToolRemoveBackground)
			tool.InputSchema = json.RawMessage(strings.Replace(string(tool.InputSchema), `"image", "idempotency_key"`, `"image"`, 1))
		},
		"idempotency key too long for the Task key": func(a *catalog.ApplicationSchema) {
			tool := mcpToolOf(a, catalog.MCPToolGenerateImage)
			tool.InputSchema = json.RawMessage(strings.Replace(string(tool.InputSchema), `"maxLength": 128`, `"maxLength": 199`, 1))
		},
		"process operation outside the capabilities": func(a *catalog.ApplicationSchema) {
			tool := mcpToolOf(a, catalog.MCPToolProcessModel)
			tool.Capabilities = tool.Capabilities[:2]
		},
		"closed parameters object": func(a *catalog.ApplicationSchema) {
			tool := mcpToolOf(a, catalog.MCPToolCreateModel)
			tool.InputSchema = json.RawMessage(strings.Replace(string(tool.InputSchema),
				`"parameters": {"type": "object", "additionalProperties": true,`, `"parameters": {"type": "object", "additionalProperties": true, "properties": {"x": {"type": "string"}},`, 1))
		},
		"no needs-person message":      func(a *catalog.ApplicationSchema) { a.MCPPublic.Messages.NeedsPerson = "" },
		"needs-person lacks the task":  func(a *catalog.ApplicationSchema) { a.MCPPublic.Messages.NeedsPerson = "ask a person" },
		"in-flight message lacks time": func(a *catalog.ApplicationSchema) { a.MCPPublic.Messages.TooManyInFlight = "later" },
		"no unauthenticated message":   func(a *catalog.ApplicationSchema) { a.MCPPublic.Messages.Unauthenticated = "" },
		"no rate-limited message":      func(a *catalog.ApplicationSchema) { a.MCPPublic.Messages.RateLimited = "" },
		"rate-limited lacks the wait":  func(a *catalog.ApplicationSchema) { a.MCPPublic.Messages.RateLimited = "slow down" },
		"no throttled message":         func(a *catalog.ApplicationSchema) { a.MCPPublic.Messages.AuthThrottled = " " },
		"throttled lacks the wait":     func(a *catalog.ApplicationSchema) { a.MCPPublic.Messages.AuthThrottled = "try later" },
		"scope message lacks holder":   func(a *catalog.ApplicationSchema) { a.MCPPublic.Messages.InsufficientScope = "no" },
		"detail message lacks holder":  func(a *catalog.ApplicationSchema) { a.MCPPublic.Messages.InvalidArguments = "no" },
		"zero MCP picture pixel cap":   func(a *catalog.ApplicationSchema) { a.MCPPublic.InspectImage.MaxPixels = 0 },
		"picture pixel cap above the platform cap": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.InspectImage.MaxPixels = a.Chat.ImageReview.Limits.MaxPixels + 1
		},
		"picture tool without the platform byte limit": func(a *catalog.ApplicationSchema) {
			a.Chat.ImageReview.Limits.MaxBytes = 0
		},
		"picture checklist enum drift": func(a *catalog.ApplicationSchema) {
			tool := mcpToolOf(a, catalog.MCPToolInspectImage)
			tool.InputSchema = json.RawMessage(strings.Replace(string(tool.InputSchema),
				`"delivery", "3d_input"`, `"delivery"`, 1))
		},
		"picture checklist differs from the platform tool": func(a *catalog.ApplicationSchema) {
			for index := range a.Chat.AgentTools {
				if a.Chat.AgentTools[index].Name != catalog.MCPToolInspectImage {
					continue
				}
				a.Chat.AgentTools[index].Parameters = json.RawMessage(strings.Replace(
					string(a.Chat.AgentTools[index].Parameters), `"delivery", "3d_input"`, `"delivery"`, 1))
			}
		},
		"no picture-not-ready message": func(a *catalog.ApplicationSchema) { a.MCPPublic.Messages.ImageNotReady = " " },
		"picture-too-large without the pixels": func(a *catalog.ApplicationSchema) {
			a.MCPPublic.Messages.ImageTooLarge = "too large, {bytes} bytes"
		},
		"picture busy without the wait": func(a *catalog.ApplicationSchema) { a.MCPPublic.Messages.ImageBusy = "busy" },
	} {
		application := defaultApplication(t)
		mutate(&application)
		if err := catalog.ValidateApplicationSchema(application); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A Catalog written before the section existed loads unchanged, even though a
// shared en bundle translates the section.
func TestHistoricalCatalogWithoutMCPPublicStaysValid(t *testing.T) {
	t.Parallel()
	application := defaultApplication(t)
	application.MCPPublic = nil
	if err := catalog.ValidateApplicationSchema(application); err != nil {
		t.Fatalf("historical Catalog without mcp_public rejected: %v", err)
	}
	if _, err := catalog.NewTextLocalizer(application); err != nil {
		t.Fatalf("text localizer without mcp_public: %v", err)
	}
}

// Application() hands out independent copies: a caller that edits a tool
// schema cannot change the Catalog.
func TestMCPPublicCopiesAreIndependent(t *testing.T) {
	t.Parallel()
	source, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	first := source.Application()
	first.MCPPublic.Tools[0].InputSchema[2] = 'X'
	first.MCPPublic.ServiceGroups[0].Capabilities[0] = "mutated"
	first.MCPPublic.StatusBlock.BalanceAccounts[0] = "mutated"
	first.MCPPublic.Generation.SourceVariantPreference[0] = "mutated"
	first.MCPPublic.Upload.MediaTypes[0] = "mutated"
	mcpToolOf(&first, catalog.MCPToolCreateModel).Capabilities[0] = "mutated"
	second := source.Application()
	if second.MCPPublic.Tools[0].InputSchema[2] == 'X' || second.MCPPublic.ServiceGroups[0].Capabilities[0] == "mutated" ||
		second.MCPPublic.StatusBlock.BalanceAccounts[0] == "mutated" || second.MCPPublic.Generation.SourceVariantPreference[0] == "mutated" ||
		second.MCPPublic.Upload.MediaTypes[0] == "mutated" || mcpToolOf(&second, catalog.MCPToolCreateModel).Capabilities[0] == "mutated" {
		t.Fatal("Catalog Application() shares MCP service declarations with a previous copy")
	}
}

// The service's Agent-facing copy has an English overlay, applied by the same
// text localizer as the rest of the Schema.
func TestMCPPublicCopyIsLocalized(t *testing.T) {
	t.Parallel()
	application := defaultApplication(t)
	localizer, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	service := application.MCPPublic
	for _, text := range []string{
		service.Instructions, service.Messages.Unauthenticated, service.Messages.InsufficientScope,
		service.Messages.TaskNotFound, service.Messages.Internal, service.Messages.RateLimited, service.Messages.AuthThrottled,
		service.Messages.SignedDownloadUnavailable, service.Messages.ModelUnavailable,
		service.Messages.ImageNotReady, service.Messages.ImageUnreadable, service.Messages.ImageTooLarge,
		service.Messages.ImageUnavailable, service.Messages.ImageBusy, service.Messages.AssetNotImage,
		service.Messages.MaskInvalid, service.Messages.MaskSizeMismatch, service.Messages.MaskNotSupported,
		service.StatusBlock.Summary.Template,
		service.StatusBlock.Summary.TemplateNoAsset, service.StatusBlock.Summary.BalanceUnknown,
		service.StatusBlock.Summary.Unknown, service.Tools[0].Description, service.Tools[len(service.Tools)-1].Description,
		mcpToolOf(&application, catalog.MCPToolGetTaskAssets).Description,
		mcpToolOf(&application, catalog.MCPToolInspectModel).Description,
		mcpToolOf(&application, catalog.MCPToolInspectImage).Description,
	} {
		if translated := localizer.Text("en", text); translated == text || strings.TrimSpace(translated) == "" {
			t.Errorf("no English overlay for %q", text)
		}
	}
	if got := localizer.Text("en", service.Messages.InsufficientScope); !strings.Contains(got, "{scope}") {
		t.Fatalf("the English scope message lost its placeholder: %q", got)
	}
	for name, message := range map[string]string{"rate_limited": service.Messages.RateLimited, "auth_throttled": service.Messages.AuthThrottled} {
		if got := localizer.Text("en", message); !strings.Contains(got, "{seconds}") {
			t.Fatalf("the English %s message lost its placeholder: %q", name, got)
		}
	}
}
