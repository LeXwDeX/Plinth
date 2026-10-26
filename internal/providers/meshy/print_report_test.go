package meshy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

const reportMIME = "application/vnd.art-ai.print-report+json"
const analysisWire = `{"_version":"v1","status":"error","issue_count":1,"error_count":1,"warning_count":0,"metrics":{"is_watertight":false,"volume":-2.5,"non_manifold_edges":0,"degenerate_faces":0,"holes":0},"evaluated_at":1700000001000}`

func reportOffering(kind string) generation.Offering {
	one := 1
	endpoint := "print/analyze"
	variant := "printability.v1.json"
	if kind == "repair" {
		endpoint = "print/repair"
		variant = "print-repair.v1.json"
	}
	options := map[string]any{"endpoint": endpoint, "api_version": "v1", "result_reports": []map[string]string{{"kind": kind, "role": "report", "variant": variant}}}
	outputs := []generation.AssetRequirement{{Role: "report", Variant: variant, Minimum: &one, MediaTypes: []string{reportMIME}, MaxBytes: 32 << 10}}
	if kind == "repair" {
		options["result_assets"] = []map[string]string{{"path": "model_urls", "role": "model", "mode": "map"}}
		outputs = append(outputs, generation.AssetRequirement{Role: "model", Variant: "glb", Minimum: &one, MediaTypes: []string{"model/gltf-binary"}, MaxBytes: 1 << 20})
	}
	raw, _ := json.Marshal(options)
	return generation.Offering{ProviderOptions: raw, Outputs: outputs}
}

func TestPrintNestedEndpointContract(t *testing.T) {
	for _, endpoint := range []string{"print/analyze", "print/repair", "print/multicolor", "text-to-3d"} {
		options, _ := json.Marshal(map[string]string{"endpoint": endpoint, "api_version": "v1"})
		got, version, err := wireRoute(generation.Offering{ProviderOptions: options})
		if err != nil || got != endpoint || version != "v1" {
			t.Errorf("safe endpoint %q: %q %q %v", endpoint, got, version, err)
		}
	}
	for _, endpoint := range []string{"/print/analyze", "print/analyze/", "print//analyze", "print/../analyze", "./print", "print/.", "print/%2fanalyze", "print/%252fanalyze", "https://evil/print", "print\\analyze", "print/analyze?q=1", "print/analyze#x"} {
		options, _ := json.Marshal(map[string]string{"endpoint": endpoint, "api_version": "v1"})
		if _, _, err := wireRoute(generation.Offering{ProviderOptions: options}); err == nil {
			t.Errorf("accepted unsafe endpoint %q", endpoint)
		}
	}
}
func TestPrintReportClaimIsNormalizedBoundedAndNotRaw(t *testing.T) {
	offering := reportOffering("analysis")
	if err := ValidateOffering(offering); err != nil {
		t.Fatalf("report-only Offering: %v", err)
	}
	var response taskResponse
	if err := json.Unmarshal([]byte(`{"status":"SUCCEEDED","id":"private-remote","signed_url":"https://private.example/SECRET","printability":`+analysisWire+`}`), &response); err != nil {
		t.Fatal(err)
	}
	claims, err := response.claims("attempt", offering)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims=%#v err=%v", claims, err)
	}
	claim := claims[0]
	if claim.Role != "report" || claim.Variant != "printability.v1.json" || claim.MediaType != reportMIME || len(claim.Inline) == 0 || claim.Locator != "" || len(claim.Metadata) != 0 {
		t.Fatalf("claim=%#v", claim)
	}
	for _, secret := range []string{"SECRET", "private-remote", "private.example", "signed_url"} {
		if strings.Contains(string(claim.Inline), secret) {
			t.Fatalf("report leaked %q", secret)
		}
	}
	var doc map[string]any
	if err := json.Unmarshal(claim.Inline, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["format"] != "art-ai.print-report.v1" || doc["kind"] != "analysis" {
		t.Fatalf("envelope=%s", claim.Inline)
	}
}

func TestPrintReportRepairUsesHonestOptionalDetails(t *testing.T) {
	for name, body := range map[string]string{"absent": `{}`, "null": `{"print_repair_report":null}`, "reported": `{"print_repair_report":{"route":"thin_wall","applied":false,"faces_before":12,"faces_after":12,"thin_area_ratio_before":0.08,"thin_area_ratio_after":0.08}}`} {
		t.Run(name, func(t *testing.T) {
			var response taskResponse
			if err := json.Unmarshal([]byte(body), &response); err != nil {
				t.Fatal(err)
			}
			claims, err := response.claims("attempt", reportOffering("repair"))
			if err != nil || len(claims) != 1 {
				t.Fatalf("claims=%#v err=%v", claims, err)
			}
			raw := string(claims[0].Inline)
			if name == "reported" {
				if !strings.Contains(raw, `"details_status":"reported"`) || !strings.Contains(raw, `"applied":false`) || !strings.Contains(raw, `"faces_before":12`) {
					t.Fatal(raw)
				}
			} else if !strings.Contains(raw, `"details_status":"not_reported"`) || strings.Contains(raw, "faces_before") || strings.Contains(raw, "applied") {
				t.Fatal(raw)
			}
		})
	}
}
func TestPrintReportRejectsMalformedWireAndUnsafeDeclaration(t *testing.T) {
	for name, body := range map[string]string{
		"missing": `{}`, "null": `{"printability":null}`, "wrong type": `{"printability":[]}`,
		"unknown field":    `{"printability":` + strings.TrimSuffix(analysisWire, "}") + `,"secret":"https://SECRET"}}`,
		"duplicate top":    `{"printability":` + analysisWire + `,"printability":` + analysisWire + `}`,
		"duplicate nested": `{"printability":` + strings.Replace(analysisWire, `"status":"error"`, `"status":"error","status":"error"`, 1) + `}`,
		"unknown version":  `{"printability":` + strings.Replace(analysisWire, `"v1"`, `"v2"`, 1) + `}`,
		"bad integer":      `{"printability":` + strings.Replace(analysisWire, `"issue_count":1`, `"issue_count":1.1`, 1) + `}`,
	} {
		t.Run(name, func(t *testing.T) {
			var response taskResponse
			if json.Unmarshal([]byte(body), &response) != nil {
				t.Fatal("fixture malformed")
			}
			claims, err := response.claims("attempt", reportOffering("analysis"))
			if err == nil || len(claims) != 0 || strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("claims=%#v err=%v", claims, err)
			}
		})
	}
	for name, mutate := range map[string]func(*generation.Offering){"oversize": func(o *generation.Offering) { o.Outputs[0].MaxBytes = 65537 }, "no limit": func(o *generation.Offering) { o.Outputs[0].MaxBytes = 0 }, "wrong MIME": func(o *generation.Offering) { o.Outputs[0].MediaTypes = []string{"application/json"} }, "optional": func(o *generation.Offering) { o.Outputs[0].Delivery = "background" }, "wrong kind endpoint": func(o *generation.Offering) {
		o.ProviderOptions = json.RawMessage(`{"endpoint":"remesh","api_version":"v1","result_reports":[{"kind":"analysis","role":"report","variant":"printability.v1.json"}]}`)
	}} {
		t.Run(name, func(t *testing.T) {
			offering := reportOffering("analysis")
			mutate(&offering)
			if ValidateOffering(offering) == nil {
				t.Fatal("unsafe report declaration accepted")
			}
		})
	}
}

func TestPrintReportDeclarationAndWireBudgetsFailClosed(t *testing.T) {
	for _, projection := range []string{`{"kind":"analysis","role":"report","variant":"printability.v1.json","path":"secret"}`, `{"kind":"repair","kind":"analysis","role":"report","variant":"printability.v1.json"}`} {
		offering := reportOffering("analysis")
		offering.ProviderOptions = json.RawMessage(`{"endpoint":"print/analyze","api_version":"v1","result_reports":[` + projection + `]}`)
		if err := ValidateOffering(offering); err == nil {
			t.Fatal("ambiguous report declaration accepted")
		}
	}
	for _, version := range []string{"v1/print", "v1%2fprint", "..", "https://evil"} {
		options, _ := json.Marshal(map[string]string{"endpoint": "print/analyze", "api_version": version})
		if _, _, err := wireRoute(generation.Offering{ProviderOptions: options}); err == nil {
			t.Fatalf("api version %q accepted", version)
		}
	}
	var response taskResponse
	raw := `{"printability":` + analysisWire + `,"ignored":"` + strings.Repeat("x", 65536) + `"}`
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.RawJSON) != 0 {
		t.Fatal("large legacy response unnecessarily retained raw copy")
	}
	if claims, err := response.claims("attempt", reportOffering("analysis")); err == nil || len(claims) != 0 {
		t.Fatal("oversized report response accepted")
	}
	offering := reportOffering("analysis")
	offering.Outputs[0].MaxBytes = 8
	if claims, err := func() ([]generation.ArtifactClaim, error) {
		var r taskResponse
		_ = json.Unmarshal([]byte(`{"printability":`+analysisWire+`}`), &r)
		return r.claims("attempt", offering)
	}(); err == nil || len(claims) != 0 {
		t.Fatal("Schema report byte limit ignored")
	}
	repair := reportOffering("repair")
	repair.Outputs = repair.Outputs[:1]
	if ValidateOffering(repair) == nil {
		t.Fatal("repair without required model accepted")
	}
}
