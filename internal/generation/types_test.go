package generation

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCapabilityValidationOwnsSyntaxNotCatalogContents(t *testing.T) {
	t.Parallel()

	for _, capability := range []Capability{
		TextToImage,
		"future-capability",
		"a",
		"model-3d-v2",
	} {
		if !capability.Valid() {
			t.Errorf("expected %q to be a valid capability identifier", capability)
		}
	}
	for _, capability := range []Capability{
		"",
		"FutureCapability",
		"-future",
		"future-",
		"future--capability",
		"future_capability",
		Capability(strings.Repeat("a", 65)),
	} {
		if capability.Valid() {
			t.Errorf("expected %q to be rejected", capability)
		}
	}
}

func TestCapabilityDomainValidationOwnsSyntaxNotCatalogContents(t *testing.T) {
	t.Parallel()

	for _, domain := range []CapabilityDomain{
		DomainImage,
		"future-domain",
	} {
		if !domain.Valid() {
			t.Errorf("expected %q to be a valid domain identifier", domain)
		}
	}
	for _, domain := range []CapabilityDomain{"", "FutureDomain", "future_domain"} {
		if domain.Valid() {
			t.Errorf("expected %q to be rejected", domain)
		}
	}
}

// B8 按数量计费：声明 unit 参数时按用户选择张数乘算单件价；
// 缺失/非法输入按单件价（展示口径），提交期额外积分仍走成本评估器。
func TestOfferingEstimateTotalCreditsMultipliesUnitParameter(t *testing.T) {
	offering := Offering{
		EstimatedCredits:              6,
		EstimatedCreditsUnitParameter: "n",
	}
	cases := []struct {
		name  string
		input string
		want  int
	}{
		{"multiplies", `{"n":10}`, 60},
		{"clamps below one", `{"n":0}`, 6},
		{"missing", `{}`, 6},
		{"undecodable", `not-json`, 6},
		{"wrong type", `{"n":"many"}`, 6},
	}
	plain := Offering{EstimatedCredits: 6}
	for _, tc := range cases {
		if got := offering.EstimateTotalCredits(json.RawMessage(tc.input)); got != tc.want {
			t.Fatalf("%s: got %d want %d", tc.name, got, tc.want)
		}
	}
	if got := plain.EstimateTotalCredits(json.RawMessage(`{"n":10}`)); got != 6 {
		t.Fatalf("plain offering got %d want 6", got)
	}
}
