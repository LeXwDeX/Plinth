package catalog

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestNativePricingCatalogConsentAndClone(t *testing.T) {
	var raw map[string]any
	if json.Unmarshal(defaultCatalog, &raw) != nil {
		t.Fatal("fixture")
	}
	for _, v := range raw["offerings"].([]any) {
		o := v.(map[string]any)
		if o["provider_id"] == "openai" {
			o["credits_metered"] = true
		}
		if o["id"] != "openai/gpt-image-2/text-to-image" {
			continue
		}
		o["credits_metered"] = true
		o["estimated_credits"] = 0
		delete(o, "estimated_credits_unit_parameter")
		o["native_pricing"] = map[string]any{"version": 1, "unit": "hyper3d.credit", "unit_label": "Hyper3D credits", "base_amount": "0.5", "components": []any{map[string]any{"id": "texture", "label": "Texture", "amount": "2.5"}}, "consent_parameter": "native_consent", "max_authorized_extra_amount": "2.5"}
		o["parameters"] = append(o["parameters"].([]any), map[string]any{"name": "native_consent", "type": "boolean", "default": false})
	}
	body, _ := json.Marshal(raw)
	c, err := Load(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	id := schema.OfferingID("openai/gpt-image-2/text-to-image")
	if _, _, err = c.Normalize(schema.TextToImage, id, json.RawMessage(`{"prompt":"x"}`)); err == nil {
		t.Fatal("extra price without consent")
	}
	o, input, err := c.Normalize(schema.TextToImage, id, json.RawMessage(`{"prompt":"x","native_consent":true}`))
	if err != nil {
		t.Fatal(err)
	}
	q, err := o.EvaluateNativeQuote(input)
	if err != nil || q.TotalAmount != "3" {
		t.Fatalf("%+v %v", q, err)
	}
	o.NativePricing.Components[0].Amount = "0.5"
	again, _ := c.Get(id)
	if again.NativePricing.Components[0].Amount != "2.5" {
		t.Fatal("Catalog native pricing not detached")
	}
	if again.UserParameterAllowed(again.Parameters[len(again.Parameters)-1]) {
		t.Fatal("native consent editable")
	}
}

func TestNativePricingDeclarationRejectsAmbiguousContract(t *testing.T) {
	base := schema.Offering{ID: "test", CreditsMetered: true, NativePricing: &schema.NativePricing{Version: 1, Unit: "provider.credit", UnitLabel: "Provider credits", BaseAmount: "0.5"}}
	for _, mutate := range []func(*schema.Offering){func(o *schema.Offering) { o.EstimatedCredits = 1 }, func(o *schema.Offering) { o.CreditsMetered = false }, func(o *schema.Offering) { o.EstimatedCreditsUnitParameter = "n" }, func(o *schema.Offering) { o.Cost = &schema.CostDeclaration{} }, func(o *schema.Offering) { o.CostPolicy = &schema.CostPolicy{} }, func(o *schema.Offering) { o.NativePricing.Version = 2 }, func(o *schema.Offering) { o.NativePricing.BaseAmount = "0.0000001" }, func(o *schema.Offering) { o.NativePricing.Unit = "legacy.credits" }} {
		body, _ := json.Marshal(base)
		var o schema.Offering
		_ = json.Unmarshal(body, &o)
		mutate(&o)
		if validateNativePricing(o) == nil {
			t.Fatalf("bad native declaration accepted %+v", o)
		}
	}
	for _, kind := range []string{"task", "asset-array", "string-array"} {
		o := base
		p := *base.NativePricing
		o.NativePricing = &p
		o.NativePricing.Components = []schema.NativePriceComponent{{ID: "extra", Label: "Extra", Amount: "0.5", When: &schema.ParameterCondition{Parameter: "source", Operator: "truthy"}}}
		o.NativePricing.ConsentParameter = "consent"
		o.NativePricing.MaxAuthorizedExtraAmount = "0.5"
		o.Parameters = []schema.Parameter{{Name: "source", Type: kind, Default: json.RawMessage(`[]`)}, {Name: "consent", Type: "boolean"}}
		if validateNativePricing(o) == nil {
			t.Fatalf("non-scalar price source %s accepted", kind)
		}
	}
}
