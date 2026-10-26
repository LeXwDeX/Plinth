package catalog_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/catalog/catalogtest"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestSplatEnabledAndRepresentationIsolated(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	o, ok := c.Get("tripo/image-to-splat")
	if !ok || !o.Enabled || o.Default || o.Capability != schema.ImageToSplat || o.EstimatedCredits != 30 {
		t.Fatalf("invalid Splat offering: %+v", o)
	}
	if err = tripo.ValidateOffering(o); err != nil {
		t.Fatal(err)
	}
	if _, _, err = c.Normalize(schema.ImageToSplat, o.ID, json.RawMessage(`{"image":"owned-image"}`)); err != nil {
		t.Fatalf("Splat input rejected: %v", err)
	}
	if len(c.List(schema.ImageToSplat)) != 1 {
		t.Fatal("Splat must have one runnable declaration")
	}
	for _, other := range c.List("") {
		for _, p := range other.Parameters {
			for _, cap := range p.TaskCapabilities {
				if cap == schema.ImageToSplat {
					t.Fatalf("Splat leaked into mesh source %s", other.ID)
				}
			}
			for _, id := range p.TaskOfferings {
				if id == o.ID {
					t.Fatalf("Splat leaked into source %s", other.ID)
				}
			}
		}
	}
}

func TestSplatDedicatedSelectorIsPublicClonedAndUniquelyRouted(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	found := false
	for _, capability := range app.Public().Capabilities {
		if capability.ID != "image-to-splat" {
			continue
		}
		found = true
		if capability.WorkspaceModelSelector == nil || *capability.WorkspaceModelSelector {
			t.Fatal("Splat entered generic model selector")
		}
	}
	if !found {
		t.Fatal("public capability missing")
	}
	publicToolFound := false
	for _, tool := range app.Public().Chat.Tools {
		if tool.Name == "create_gaussian_splat" {
			publicToolFound = true
			if !tool.RequireUniqueOffering || tool.Capability != "image-to-splat" {
				t.Fatalf("public route lost uniqueness: %+v", tool)
			}
		}
	}
	if !publicToolFound {
		t.Fatal("public dedicated tool missing")
	}
	for _, capability := range app.Capabilities {
		if capability.ID == "image-to-splat" {
			*capability.WorkspaceModelSelector = true
		}
	}
	for _, capability := range c.Application().Capabilities {
		if capability.ID == "image-to-splat" && *capability.WorkspaceModelSelector {
			t.Fatal("selector pointer aliases catalog")
		}
	}
	for index, tool := range app.Chat.Tools {
		if tool.Name != "create_gaussian_splat" {
			continue
		}
		if !tool.RequireUniqueOffering || !tool.IgnoreUserModel || tool.RequireUserModel || tool.CrossDomainModelFallback {
			t.Fatalf("tool selection=%+v", tool)
		}
		app.Chat.Tools[index].RequireUserModel = true
		if catalog.ValidateApplicationSchema(app) == nil {
			t.Fatal("ambiguous unique/user selection accepted")
		}
		return
	}
	t.Fatal("dedicated tool missing")
}
func TestSplatCatalogRejectsRepresentationBypass(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"missing profile": func(o map[string]any) { delete(o, "delivery_profile") },
		"model role":      func(o map[string]any) { o["role"] = "model" },
		"glb variant":     func(o map[string]any) { o["variant"] = "glb" },
		"generic MIME":    func(o map[string]any) { o["media_types"] = []any{"application/octet-stream"} },
		"optional":        func(o map[string]any) { o["delivery"] = "optional" },
		"no minimum":      func(o map[string]any) { delete(o, "minimum") },
		"zero minimum":    func(o map[string]any) { o["minimum"] = 0 },
		"oversize":        func(o map[string]any) { o["max_bytes"] = 33554433 },
	} {
		t.Run(name, func(t *testing.T) {
			var d map[string]any
			if err := json.Unmarshal(catalogtest.DefaultCatalogBytes(t), &d); err != nil {
				t.Fatal(err)
			}
			for _, v := range d["offerings"].([]any) {
				o := v.(map[string]any)
				if o["id"] == "tripo/image-to-splat" {
					mutate(o["outputs"].([]any)[0].(map[string]any))
				}
			}
			b, _ := json.Marshal(d)
			if _, err := catalog.Load(bytes.NewReader(b)); err == nil {
				t.Fatal("invalid profile declaration accepted")
			}
		})
	}
}
func TestSplatAdapterRejectsMeshWireAndRetries(t *testing.T) {
	for name, mutate := range map[string]func(*schema.Offering){
		"model field": func(o *schema.Offering) {
			var p map[string]any
			_ = json.Unmarshal(o.ProviderOptions, &p)
			p["model"] = "v3.1"
			o.ProviderOptions, _ = json.Marshal(p)
		},
		"retry":               func(o *schema.Offering) { o.Retry.MaxAttempts = 2 },
		"idempotent":          func(o *schema.Offering) { o.IdempotentSubmission = true },
		"duplicate parameter": func(o *schema.Offering) { o.Parameters = append(o.Parameters, o.Parameters[0]) },
		"asset array":         func(o *schema.Offering) { o.Parameters[0].Type = "asset-array" },
	} {
		t.Run(name, func(t *testing.T) {
			c, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			o, _ := c.Get("tripo/image-to-splat")
			mutate(&o)
			if err = tripo.ValidateOffering(o); err == nil {
				t.Fatal("unsupported wire accepted")
			}
		})
	}
}
