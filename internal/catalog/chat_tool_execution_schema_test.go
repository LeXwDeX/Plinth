package catalog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

func TestDefaultChatToolsDeclareRuntimeExecutionContracts(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	if len(app.Chat.Tools) != 14 || len(app.Chat.AgentTools) != 11 {
		t.Fatalf("chat tool inventory = generation:%d controls:%d", len(app.Chat.Tools), len(app.Chat.AgentTools))
	}
	if err := catalog.ValidateApplicationSchema(app); err != nil {
		t.Fatalf("default execution metadata invalid: %v", err)
	}
	for _, tool := range app.Chat.Tools {
		switch tool.Name {
		case "create_model_3d", "create_model_3d_from_image", "create_gaussian_splat", "retexture_model", "process_model", "plan_model_processing", "convert_model_format",
			"create_creative_lab_design", "build_creative_lab_item":
		default:
			continue
		}
		if len(tool.Execution.Domains) != 1 || tool.Execution.Domains[0] != "model-3d" {
			t.Fatalf("3D tool %s must use the workspace's model-3d domain, got %v", tool.Name, tool.Execution.Domains)
		}
	}
	public := app.Public()
	if len(public.Chat.Tools) != len(app.Chat.Tools) {
		t.Fatalf("public tools = %d; want %d", len(public.Chat.Tools), len(app.Chat.Tools))
	}
}

func TestCatalogAllowsNewGenerationToolNameWithRegisteredCapability(t *testing.T) {
	payload, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	var app catalog.ApplicationSchema
	if err := json.Unmarshal(document["application"], &app); err != nil {
		t.Fatal(err)
	}
	app.Chat.Tools[0].Name = "generate_image_variant"
	encoded, err := json.Marshal(app)
	if err != nil {
		t.Fatal(err)
	}
	document["application"] = encoded
	payload, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Load(bytes.NewReader(payload)); err != nil {
		t.Fatalf("renamed generation tool rejected: %v", err)
	}
}

func TestCatalogAllowsImageToolForProcessingClassifiedCapability(t *testing.T) {
	payload, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	var app catalog.ApplicationSchema
	if err := json.Unmarshal(document["application"], &app); err != nil {
		t.Fatal(err)
	}
	app.Chat.Tools[1].Name = "cutout_assets"
	encoded, err := json.Marshal(app)
	if err != nil {
		t.Fatal(err)
	}
	document["application"] = encoded
	payload, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Load(bytes.NewReader(payload)); err != nil {
		t.Fatalf("image generation tool for processing-classified capability rejected: %v", err)
	}
}

func TestChatToolExecutionValidationRejectsUnsafeOrUnregisteredMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*catalog.ApplicationSchema)
	}{
		{"paid task cannot drop consent", func(a *catalog.ApplicationSchema) { a.Chat.Tools[0].Execution.Consent = "none" }},
		{"paid task cannot downgrade effect", func(a *catalog.ApplicationSchema) { a.Chat.Tools[0].Execution.Effects = []string{"READ"} }},
		{"generation implementation is fixed", func(a *catalog.ApplicationSchema) { a.Chat.Tools[0].Execution.ImplementationKey = "arbitrary.execute" }},
		{"generation capability closure", func(a *catalog.ApplicationSchema) { a.Chat.Tools[0].Execution.Capabilities = nil }},
		{"fake capability is rejected", func(a *catalog.ApplicationSchema) {
			a.Chat.Tools[0].Execution.Capabilities = []schemacontract.Capability{"unregistered-capability"}
		}},
		{"domains are validated", func(a *catalog.ApplicationSchema) { a.Chat.Tools[0].Execution.Domains = []string{"admin"} }},
		{"legacy model domain is rejected", func(a *catalog.ApplicationSchema) { a.Chat.Tools[2].Execution.Domains = []string{"model"} }},
		{"cross-list duplicate tool names", func(a *catalog.ApplicationSchema) { a.Chat.AgentTools[0].Name = a.Chat.Tools[0].Name }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			app := c.Application()
			tt.mutate(&app)
			if err := catalog.ValidateApplicationSchema(app); err == nil {
				t.Fatal("ValidateApplicationSchema unexpectedly accepted invalid execution metadata")
			}
		})
	}
}
