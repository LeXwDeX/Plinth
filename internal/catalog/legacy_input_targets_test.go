package catalog

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestLegacyInputTargetsValidateEnabledOfferingsWithoutBlockingDisabledCandidates(t *testing.T) {
	app := ApplicationSchema{Capabilities: []CapabilityPresentation{{
		ID: schema.Retexture,
		LegacyAPI: &LegacyTaskAPISchema{
			Fields:    []LegacyFieldSchema{{Name: "model_url", TargetParameter: "source_url"}},
			Multipart: &LegacyMultipartTaskSchema{TargetParameter: "source_asset", Cardinality: "one"},
		},
	}}}
	valid := schema.Offering{
		ID: "provider/enabled", Enabled: true, Capability: schema.Retexture,
		Parameters: []schema.Parameter{
			{Name: "source_url", Type: "text"},
			{Name: "source_asset", Type: "asset", MaxBytes: 1024, Extensions: []string{"glb"}},
		},
	}
	candidate := schema.Offering{ID: "provider/candidate", Capability: schema.Retexture}
	for _, tc := range []struct {
		name     string
		validate func(ApplicationSchema, generationMap) error
	}{
		{"field", validateLegacyFieldTargets},
		{"multipart", validateLegacyMultipartTargets},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.validate(app, generationMap{valid.ID: valid, candidate.ID: candidate}); err != nil {
				t.Fatalf("disabled candidate blocked an executable legacy contract: %v", err)
			}
			if err := tc.validate(app, generationMap{candidate.ID: candidate}); err != nil {
				t.Fatalf("a disabled-only capability has no executable target to validate: %v", err)
			}
			candidate.Enabled = true
			if err := tc.validate(app, generationMap{valid.ID: valid, candidate.ID: candidate}); err == nil {
				t.Fatal("enabled candidate accepted missing legacy parameters")
			}
			candidate.Enabled = false
			if err := tc.validate(app, generationMap{}); err == nil {
				t.Fatal("legacy target with no declared offering accepted")
			}
		})
	}
}

// #313 review, folded into #330: Meshy Remesh no longer has resize_height,
// auto_size or convert_format_only, yet its legacy fields still forwarded them
// "always", so an old client sending one got legacy_input_not_applicable. A
// field forwarded always must name a parameter of every enabled Offering it
// applies to, whether it renames the field or keeps its name; an
// if-supported field may be absent.
func TestLegacyFieldsForwardedAlwaysMustExistOnTheOffering(t *testing.T) {
	enabled := schema.Offering{ID: "meshy/processing/remesh", Enabled: true, Capability: schema.Remesh,
		Parameters: []schema.Parameter{{Name: "topology", Type: "text"}}}
	offerings := generationMap{enabled.ID: enabled}
	appWith := func(field LegacyFieldSchema) ApplicationSchema {
		return ApplicationSchema{Capabilities: []CapabilityPresentation{{ID: schema.Remesh, LegacyAPI: &LegacyTaskAPISchema{Fields: []LegacyFieldSchema{field}}}}}
	}
	if err := validateLegacyFieldTargets(appWith(LegacyFieldSchema{Name: "topology", Forwarding: "always", OfferingIDs: []schema.OfferingID{enabled.ID}}), offerings); err != nil {
		t.Fatalf("an existing parameter was rejected: %v", err)
	}
	if err := validateLegacyFieldTargets(appWith(LegacyFieldSchema{Name: "resize_height", Forwarding: "always", OfferingIDs: []schema.OfferingID{enabled.ID}}), offerings); err == nil {
		t.Fatal("a field forwarded always to a parameter the Offering no longer has was accepted")
	}
	if err := validateLegacyFieldTargets(appWith(LegacyFieldSchema{Name: "resolution", Forwarding: "if-supported", OfferingIDs: []schema.OfferingID{enabled.ID}}), offerings); err != nil {
		t.Fatalf("an if-supported field may be absent: %v", err)
	}
	// The shipped Remesh contract no longer lists the removed fields.
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, capability := range product.Application().Capabilities {
		if capability.ID != schema.Remesh || capability.LegacyAPI == nil {
			continue
		}
		for _, field := range capability.LegacyAPI.Fields {
			switch field.Name {
			case "resize_height", "auto_size", "convert_format_only":
				t.Fatalf("legacy remesh still forwards %q", field.Name)
			}
		}
	}
}
