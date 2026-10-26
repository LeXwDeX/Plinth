package generation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

func minimum(value int) *int { return &value }

func TestConditionalRequiredOutputUsesFrozenInput(t *testing.T) {
	offering := Offering{Outputs: []AssetRequirement{
		{Role: "image", Variant: "front", Delivery: AssetDeliveryRequired, Minimum: minimum(1), RequiredWhen: &schema.ParameterCondition{Parameter: "front_prompt", Operator: "present"}},
		{Role: "image", Variant: "left", Delivery: AssetDeliveryRequired, Minimum: minimum(1), RequiredWhen: &schema.ParameterCondition{Parameter: "left_prompt", Operator: "present"}},
	}}
	resolved, err := resolveConditionalOutputs(offering, json.RawMessage(`{"front_prompt":"make green","left_prompt":""}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateClaims(resolved.Outputs, []ArtifactClaim{{ID: "front", Role: "image", Variant: "front", Locator: "https://example.test/front.jpg"}}); err != nil {
		t.Fatalf("selected front rejected: %v", err)
	}
	if resolved.Outputs[1].Minimum == nil || *resolved.Outputs[1].Minimum != 0 {
		t.Fatal("unselected left should not block")
	}
	resolved, err = resolveConditionalOutputs(offering, json.RawMessage(`{"front_prompt":"make green","left_prompt":"add logo"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateClaims(resolved.Outputs, []ArtifactClaim{{ID: "front", Role: "image", Variant: "front", Locator: "https://example.test/front.jpg"}}); err == nil || !strings.Contains(err.Error(), "left") {
		t.Fatalf("missing selected left accepted: %v", err)
	}
}

// A select-driven format freezes exactly the slot of the chosen value; the
// other format's slot can neither block success nor stand in for it.
func TestConditionalRequiredOutputFollowsFrozenSelectValue(t *testing.T) {
	offering := Offering{Outputs: []AssetRequirement{
		{Role: "model", Variant: "glb", Delivery: AssetDeliveryRequired, Minimum: minimum(1), RequiredWhen: &schema.ParameterCondition{Parameter: "output_format", Operator: "equals", Value: json.RawMessage(`"glb"`)}},
		{Role: "bundle", Variant: "zip", Delivery: AssetDeliveryRequired, Minimum: minimum(1), RequiredWhen: &schema.ParameterCondition{Parameter: "output_format", Operator: "equals", Value: json.RawMessage(`"zip"`)}},
	}}
	glb := ArtifactClaim{ID: "glb", Role: "model", Variant: "glb", Locator: "https://example.test/model.glb"}
	zip := ArtifactClaim{ID: "zip", Role: "bundle", Variant: "zip", Locator: "https://example.test/bundle.zip"}
	resolved, err := resolveConditionalOutputs(offering, json.RawMessage(`{"output_format":"glb"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateClaims(resolved.Outputs, []ArtifactClaim{glb}); err != nil {
		t.Fatalf("requested GLB rejected: %v", err)
	}
	if err := validateClaims(resolved.Outputs, []ArtifactClaim{zip}); err == nil {
		t.Fatal("ZIP stood in for the requested GLB")
	}
	resolved, err = resolveConditionalOutputs(offering, json.RawMessage(`{"output_format":"zip"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateClaims(resolved.Outputs, []ArtifactClaim{zip}); err != nil {
		t.Fatalf("requested ZIP rejected: %v", err)
	}
	if err := validateClaims(resolved.Outputs, []ArtifactClaim{glb}); err == nil {
		t.Fatal("GLB stood in for the requested ZIP")
	}
}

func TestRequirementForClaimPrefersMostSpecificMatch(t *testing.T) {
	requirements := []AssetRequirement{
		{Role: "model", VariantPrefix: "basic_animations.", Delivery: AssetDeliveryBackground, Minimum: minimum(0)},
		{Role: "model", VariantPrefix: "rigged_character", Delivery: AssetDeliveryRequired, Minimum: minimum(1)},
		{Role: "model", Variant: "rigged_character_glb_url", Delivery: AssetDeliveryRequired, Minimum: minimum(1), Group: "primary"},
	}
	exact, ok := requirementForClaim(requirements, "model", "rigged_character_glb_url")
	if !ok || exact.Variant != "rigged_character_glb_url" || exact.Group != "primary" {
		t.Fatalf("exact match = %#v ok=%v", exact, ok)
	}
	prefix, ok := requirementForClaim(requirements, "model", "rigged_character_fbx_url")
	if !ok || prefix.VariantPrefix != "rigged_character" {
		t.Fatalf("prefix match = %#v ok=%v", prefix, ok)
	}
	background, ok := requirementForClaim(requirements, "model", "basic_animations.idle")
	if !ok || background.VariantPrefix != "basic_animations." {
		t.Fatalf("background match = %#v ok=%v", background, ok)
	}
	if _, ok := requirementForClaim(requirements, "model", "unknown_variant"); ok {
		t.Fatal("unmatched variant contracted")
	}
	if _, ok := requirementForClaim(requirements, "thumbnail", ""); ok {
		t.Fatal("unmatched role contracted")
	}
}

func TestRequirementForClaimLegacyMatchesAnyVariant(t *testing.T) {
	requirements := []AssetRequirement{{Role: "model", Minimum: minimum(1)}}
	claim, ok := requirementForClaim(requirements, "model", "glb")
	if !ok || claim.Role != "model" {
		t.Fatalf("legacy match = %#v ok=%v", claim, ok)
	}
}

func TestRequirementForClaimLongerPrefixWins(t *testing.T) {
	requirements := []AssetRequirement{
		{Role: "model", VariantPrefix: "animation_", Minimum: minimum(1)},
		{Role: "model", VariantPrefix: "animation_extra_", Minimum: minimum(1)},
	}
	claim, ok := requirementForClaim(requirements, "model", "animation_extra_glb_url")
	if !ok || claim.VariantPrefix != "animation_extra_" {
		t.Fatalf("longest prefix = %#v ok=%v", claim, ok)
	}
}

func TestRequirementEffectiveMinimumPreservesLegacyCoercion(t *testing.T) {
	cases := map[string]struct {
		requirement AssetRequirement
		expected    int
	}{
		"nil legacy":           {AssetRequirement{Role: "model"}, 1},
		"zero legacy":          {AssetRequirement{Role: "model", Minimum: minimum(0)}, 1},
		"explicit legacy":      {AssetRequirement{Role: "model", Minimum: minimum(2)}, 2},
		"explicit required":    {AssetRequirement{Role: "model", Delivery: AssetDeliveryRequired, Minimum: minimum(3)}, 3},
		"conditional required": {AssetRequirement{Role: "texture", VariantPrefix: "material.", Delivery: AssetDeliveryRequired, Minimum: minimum(0)}, 0},
		"background zero":      {AssetRequirement{Role: "model", Delivery: AssetDeliveryBackground, Minimum: minimum(0)}, 1},
		"on-demand zero":       {AssetRequirement{Role: "model", Delivery: AssetDeliveryOnDemand, Minimum: minimum(0)}, 1},
		"negative defensive":   {AssetRequirement{Role: "model", Minimum: minimum(-2)}, 1},
	}
	for name, testCase := range cases {
		if got := requirementEffectiveMinimum(testCase.requirement); got != testCase.expected {
			t.Errorf("%s = %d, want %d", name, got, testCase.expected)
		}
	}
}

func TestConditionalRequiredClaimAllowsAbsenceButChecksReturnedArtifact(t *testing.T) {
	requirements := []AssetRequirement{
		{Role: "model", Variant: "glb", Delivery: AssetDeliveryRequired, Minimum: minimum(1)},
		{Role: "texture", VariantPrefix: "material.", Delivery: AssetDeliveryRequired, Minimum: minimum(0)},
	}
	primary := ArtifactClaim{ID: "model", Role: "model", Variant: "glb", Inline: []byte("model")}
	if err := validateClaims(requirements, []ArtifactClaim{primary}); err != nil {
		t.Fatalf("absent texture must be allowed: %v", err)
	}
	assets := []Asset{{ID: "model", Role: "model", Variant: "glb"}}
	if err := assignRequiredOutputs(requirements, assets); err != nil {
		t.Fatalf("absent texture must not block success: %v", err)
	}
	if !assets[0].Required {
		t.Fatal("primary model must remain required")
	}
	withTexture := []ArtifactClaim{primary, {ID: "texture", Role: "texture", Variant: "material.base", Inline: []byte("texture")}}
	if err := validateClaims(requirements, withTexture); err != nil {
		t.Fatalf("returned texture must be admitted for materialization: %v", err)
	}
}

func TestRequirementBlocksSuccess(t *testing.T) {
	if !requirementBlocksSuccess(AssetRequirement{Role: "model"}) {
		t.Fatal("legacy must block")
	}
	if !requirementBlocksSuccess(AssetRequirement{Role: "model", Delivery: AssetDeliveryRequired}) {
		t.Fatal("required must block")
	}
	if requirementBlocksSuccess(AssetRequirement{Role: "model", Delivery: AssetDeliveryBackground}) {
		t.Fatal("background must not block")
	}
	if requirementBlocksSuccess(AssetRequirement{Role: "model", Delivery: AssetDeliveryOnDemand}) {
		t.Fatal("on-demand must not block")
	}
}

// TestValidateClaimsKeepsLegacyMessageByteForByte 覆盖 §3.4：旧语义
// （delivery 为空）下的成功门槛错误信息与现状逐字节等价。
func TestValidateClaimsKeepsLegacyMessageByteForByte(t *testing.T) {
	requirements := []AssetRequirement{{Role: "model", Minimum: minimum(2)}}
	claims := []ArtifactClaim{
		{ID: "a", Role: "model", Locator: "https://assets.test/a.glb"},
	}
	err := validateClaims(requirements, claims)
	if err == nil || err.Error() !=
		`上游产物不完整："model" 至少需要 2 个，实际为 1 个` {
		t.Fatalf("legacy message = %#v", err)
	}
}

func TestValidateClaimsCountsPerVariantOneToOne(t *testing.T) {
	requirements := []AssetRequirement{
		{Role: "model", Variant: "hero", Delivery: AssetDeliveryRequired, Minimum: minimum(1)},
		{Role: "model", Variant: "side", Delivery: AssetDeliveryRequired, Minimum: minimum(1)},
	}
	twoHeroes := []ArtifactClaim{
		{ID: "h1", Role: "model", Variant: "hero", Locator: "https://assets.test/h1"},
		{ID: "h2", Role: "model", Variant: "hero", Locator: "https://assets.test/h2"},
	}
	err := validateClaims(requirements, twoHeroes)
	if err == nil || !strings.Contains(err.Error(), "side") {
		t.Fatalf("slot mixing error = %v", err)
	}
	balanced := append(twoHeroes, ArtifactClaim{
		ID: "s1", Role: "model", Variant: "side", Locator: "https://assets.test/s1",
	})
	if err := validateClaims(requirements, balanced); err != nil {
		t.Fatalf("balanced claims rejected: %v", err)
	}
}

func TestValidateClaimsIgnoresMissingOptionalDeliveries(t *testing.T) {
	requirements := []AssetRequirement{
		{Role: "model", VariantPrefix: "hero_", Delivery: AssetDeliveryRequired, Minimum: minimum(1)},
		{Role: "model", VariantPrefix: "extras.", Delivery: AssetDeliveryBackground, Minimum: minimum(0)},
		{Role: "model", Variant: "preview", Delivery: AssetDeliveryOnDemand, Minimum: minimum(0)},
	}
	claims := []ArtifactClaim{
		{ID: "h1", Role: "model", Variant: "hero_glb_url", Locator: "https://assets.test/h1"},
	}
	if err := validateClaims(requirements, claims); err != nil {
		t.Fatalf("optional missing must not fail: %v", err)
	}
}

func TestValidateClaimsReportsGroupAtomically(t *testing.T) {
	requirements := []AssetRequirement{
		{Role: "model", Variant: "hero_a", Delivery: AssetDeliveryRequired, Minimum: minimum(1), Group: "pair"},
		{Role: "model", Variant: "hero_b", Delivery: AssetDeliveryRequired, Minimum: minimum(1), Group: "pair"},
	}
	claims := []ArtifactClaim{
		{ID: "a", Role: "model", Variant: "hero_a", Locator: "https://assets.test/a"},
	}
	err := validateClaims(requirements, claims)
	if err == nil ||
		!strings.Contains(err.Error(), "hero_b") ||
		!strings.Contains(err.Error(), "pair") {
		t.Fatalf("group atomic error = %v", err)
	}
}

// TestAssignRequiredOutputsKeepsLegacyMessageByteForByte 覆盖 §3.4：
// 旧语义下持久资产分配错误信息与现状逐字节等价。
func TestAssignRequiredOutputsKeepsLegacyMessageByteForByte(t *testing.T) {
	requirements := []AssetRequirement{{Role: "model", Minimum: minimum(1)}}
	err := assignRequiredOutputs(requirements, nil)
	if err == nil || err.Error() !=
		`持久资产不完整："model" 至少需要 1 个，实际为 0 个` {
		t.Fatalf("legacy message = %#v", err)
	}
}

func TestAssignRequiredOutputsMarksFirstByOrdinalThenID(t *testing.T) {
	requirements := []AssetRequirement{
		{Role: "model", VariantPrefix: "hero_", Delivery: AssetDeliveryRequired, Minimum: minimum(2)},
		{Role: "model", VariantPrefix: "extras.", Delivery: AssetDeliveryBackground, Minimum: minimum(0)},
	}
	assets := []Asset{
		{ID: "asset-z", Role: "model", Variant: "hero_second", Ordinal: 2},
		{ID: "asset-b", Role: "model", Variant: "hero_first", Ordinal: 1},
		{ID: "asset-c", Role: "model", Variant: "hero_third", Ordinal: 3},
		{ID: "asset-x", Role: "model", Variant: "extras.idle", Ordinal: 4},
	}
	if err := assignRequiredOutputs(requirements, assets); err != nil {
		t.Fatal(err)
	}
	required := map[AssetID]bool{}
	for _, asset := range assets {
		required[asset.ID] = asset.Required
	}
	if !required["asset-b"] || !required["asset-z"] || required["asset-c"] {
		t.Fatalf("required flags = %#v", required)
	}
	if required["asset-x"] {
		t.Fatal("background asset marked required")
	}
}

func TestAssignRequiredOutputsDoesNotShareAssetsAcrossSlots(t *testing.T) {
	requirements := []AssetRequirement{
		{Role: "model", Variant: "hero", Delivery: AssetDeliveryRequired, Minimum: minimum(1)},
		{Role: "model", Variant: "side", Delivery: AssetDeliveryRequired, Minimum: minimum(1)},
	}
	assets := []Asset{{ID: "hero-1", Role: "model", Variant: "hero", Ordinal: 1}}
	err := assignRequiredOutputs(requirements, assets)
	if err == nil || !strings.Contains(err.Error(), "side") {
		t.Fatalf("cross-slot error = %v", err)
	}
	if !assets[0].Required {
		t.Fatal("hero slot unassigned")
	}
}

func TestAssignRequiredOutputsFailsShortRequiredPrefix(t *testing.T) {
	requirements := []AssetRequirement{
		{Role: "model", VariantPrefix: "rigged_character", Delivery: AssetDeliveryRequired, Minimum: minimum(1)},
	}
	assets := []Asset{
		{ID: "idle-1", Role: "model", Variant: "basic_animations.idle", Ordinal: 1},
	}
	err := assignRequiredOutputs(requirements, assets)
	if err == nil ||
		!strings.Contains(err.Error(), "rigged_character") {
		t.Fatalf("collection-only error = %v", err)
	}
	if assets[0].Required {
		t.Fatal("collection asset marked required for primary slot")
	}
}

func TestAssetDeliveryDerivesFromDeclarations(t *testing.T) {
	outputs := []AssetRequirement{
		{Role: "model", VariantPrefix: "rigged_character", Delivery: AssetDeliveryRequired, Minimum: minimum(1)},
		{Role: "model", VariantPrefix: "basic_animations.", Delivery: AssetDeliveryBackground, Minimum: minimum(0)},
	}
	if got := AssetDelivery(outputs, "model", "rigged_character_glb_url"); got != AssetDeliveryRequired {
		t.Fatalf("primary delivery = %q", got)
	}
	if got := AssetDelivery(outputs, "model", "basic_animations.idle"); got != AssetDeliveryBackground {
		t.Fatalf("collection delivery = %q", got)
	}
	if got := AssetDelivery(outputs, "model", "mystery"); got != "" {
		t.Fatalf("unmatched delivery = %q", got)
	}
	legacy := []AssetRequirement{{Role: "model", Minimum: minimum(1)}}
	if got := AssetDelivery(legacy, "model", "glb"); got != "" {
		t.Fatalf("legacy delivery = %q", got)
	}
}
