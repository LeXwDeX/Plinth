package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/schema/toolschema"
)

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
	names := make([]string, 0, len(service.Tools))
	for _, tool := range service.Tools {
		names = append(names, tool.Name)
		if tool.Scope != "read" {
			t.Errorf("tool %s needs scope %q; only reading tools exist so far", tool.Name, tool.Scope)
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
		for _, name := range []string{"step_mode", "asset", "total_used_steps", "balances", "step_costs", "failure"} {
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
		"no protocol version":   func(a *catalog.ApplicationSchema) { a.MCPPublic.ProtocolVersion = " " },
		"no server name":        func(a *catalog.ApplicationSchema) { a.MCPPublic.ServerName = "" },
		"no instructions":       func(a *catalog.ApplicationSchema) { a.MCPPublic.Instructions = "" },
		"zero request limit":    func(a *catalog.ApplicationSchema) { a.MCPPublic.MaxRequestBytes = 0 },
		"huge response limit":   func(a *catalog.ApplicationSchema) { a.MCPPublic.MaxResponseBytes = 1 << 40 },
		"zero read timeout":     func(a *catalog.ApplicationSchema) { a.MCPPublic.ReadTimeoutSeconds = 0 },
		"zero write timeout":    func(a *catalog.ApplicationSchema) { a.MCPPublic.WriteTimeoutSeconds = 0 },
		"zero poll interval":    func(a *catalog.ApplicationSchema) { a.MCPPublic.PollIntervalSeconds = 0 },
		"page above maximum":    func(a *catalog.ApplicationSchema) { a.MCPPublic.ListPageSize = a.MCPPublic.ListMaxPageSize + 1 },
		"no service groups":     func(a *catalog.ApplicationSchema) { a.MCPPublic.ServiceGroups = nil },
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
		"no unauthenticated message":  func(a *catalog.ApplicationSchema) { a.MCPPublic.Messages.Unauthenticated = "" },
		"scope message lacks holder":  func(a *catalog.ApplicationSchema) { a.MCPPublic.Messages.InsufficientScope = "no" },
		"detail message lacks holder": func(a *catalog.ApplicationSchema) { a.MCPPublic.Messages.InvalidArguments = "no" },
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
	second := source.Application()
	if second.MCPPublic.Tools[0].InputSchema[2] == 'X' || second.MCPPublic.ServiceGroups[0].Capabilities[0] == "mutated" ||
		second.MCPPublic.StatusBlock.BalanceAccounts[0] == "mutated" {
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
		service.Messages.TaskNotFound, service.Messages.Internal, service.StatusBlock.Summary.Template,
		service.StatusBlock.Summary.TemplateNoAsset, service.StatusBlock.Summary.BalanceUnknown,
		service.StatusBlock.Summary.Unknown, service.Tools[0].Description, service.Tools[len(service.Tools)-1].Description,
	} {
		if translated := localizer.Text("en", text); translated == text || strings.TrimSpace(translated) == "" {
			t.Errorf("no English overlay for %q", text)
		}
	}
	if got := localizer.Text("en", service.Messages.InsufficientScope); !strings.Contains(got, "{scope}") {
		t.Fatalf("the English scope message lost its placeholder: %q", got)
	}
}
