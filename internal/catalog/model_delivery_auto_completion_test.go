package catalog

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

func loadDefaultDocument(tb testing.TB) map[string]any {
	tb.Helper()
	raw, err := os.ReadFile("default_catalog.json")
	if err != nil {
		tb.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		tb.Fatal(err)
	}
	return document
}

func encodeDocument(tb testing.TB, document map[string]any) []byte {
	tb.Helper()
	encoded, err := json.Marshal(document)
	if err != nil {
		tb.Fatal(err)
	}
	return encoded
}

// #461: the automatic delivery completion is Schema-driven. The loader refuses
// a route that would spend without a bound, a free route that is not free, a
// route a person must approve by hand, and a route that cannot take the files
// the platform already saved.

func autoCompletionFixture(tb testing.TB) (ApplicationSchema, map[schemacontract.OfferingID]schemacontract.Offering) {
	tb.Helper()
	product, err := LoadDefault()
	if err != nil {
		tb.Fatal(err)
	}
	return product.Application(), product.byID
}

func conversionByOffering(schema *ModelDeliverySchema, id string) *ModelConversionSchema {
	for index := range schema.Conversions {
		if schema.Conversions[index].OfferingID == id {
			return &schema.Conversions[index]
		}
	}
	return nil
}

// validateAutoCompletion runs both halves of the loader's judgement: the shape
// of the block (ValidateApplicationSchema) and the routes against the loaded
// Offerings and the approval policy.
func validateAutoCompletion(application ApplicationSchema, offerings map[schemacontract.OfferingID]schemacontract.Offering) error {
	if err := ValidateApplicationSchema(application); err != nil {
		return err
	}
	return validateModelDeliveryAutoCompletion(application, offerings)
}

func TestShippedAutoCompletionRoutesAreAccepted(t *testing.T) {
	application, offerings := autoCompletionFixture(t)
	if err := validateAutoCompletion(application, offerings); err != nil {
		t.Fatalf("shipped routes rejected: %v", err)
	}
}

func TestAutoCompletionRoutesMustBeBounded(t *testing.T) {
	cases := map[string]struct {
		mutate func(*ApplicationSchema)
		want   string
	}{
		"a free route that costs credits": {
			func(s *ApplicationSchema) {
				conversion := conversionByOffering(&s.ModelDelivery, "meshy/processing/convert")
				conversion.Auto, conversion.MaxCredits = ConversionAutoFree, 0
			},
			"估价不是 0",
		},
		"a paid route without a bound": {
			func(s *ApplicationSchema) {
				conversionByOffering(&s.ModelDelivery, "local/generated-glb/to-gltf").Auto = ConversionAutoPaid
			},
			"必须声明 max_credits",
		},
		"a route that cannot take a saved file": {
			func(s *ApplicationSchema) {
				conversion := conversionByOffering(&s.ModelDelivery, "local/generated-glb/to-gltf")
				conversion.Auto, conversion.MaxCredits = ConversionAutoPaid, 5
				conversion.OfferingID = "tripo/tripo-v3.1/format-conversion"
				s.ModelDelivery.AutoCompletion.RouteOrder[0] = "tripo/tripo-v3.1/format-conversion"
			},
			"不接受已保存的文件作为来源",
		},
		"a route order that omits an automatic conversion": {
			func(s *ApplicationSchema) {
				s.ModelDelivery.AutoCompletion.RouteOrder = s.ModelDelivery.AutoCompletion.RouteOrder[:1]
			},
			"route_order",
		},
		"a route order that names a route without auto": {
			func(s *ApplicationSchema) {
				s.ModelDelivery.AutoCompletion.RouteOrder = append(s.ModelDelivery.AutoCompletion.RouteOrder, "local/static-glb/to-gltf")
			},
			"没有声明 auto",
		},
		"a capability the coverage does not deliver": {
			func(s *ApplicationSchema) {
				s.ModelDelivery.AutoCompletion.SourceCapabilities = []schemacontract.Capability{"text-to-image"}
			},
			"source_capabilities",
		},
		"a scan that never runs": {
			func(s *ApplicationSchema) {
				s.ModelDelivery.AutoCompletion.ScanIntervalMilliseconds = 0
			},
			"必须为正数",
		},
		// #510: a record waiting for a platform slot is looked at again after
		// the declared slot wait, which must exist and not exceed the scan.
		"no slot wait": {
			func(s *ApplicationSchema) {
				s.ModelDelivery.AutoCompletion.SlotWaitIntervalMilliseconds = 0
			},
			"slot_wait_interval_milliseconds",
		},
		"a slot wait longer than the scan": {
			func(s *ApplicationSchema) {
				auto := &s.ModelDelivery.AutoCompletion
				auto.SlotWaitIntervalMilliseconds = auto.ScanIntervalMilliseconds + 1
			},
			"slot_wait_interval_milliseconds",
		},
		"a ceiling of zero": {
			func(s *ApplicationSchema) {
				s.ModelDelivery.AutoCompletion.MaxCreditsPerTask = 0
			},
			"必须为正数",
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			application, offerings := autoCompletionFixture(t)
			testCase.mutate(&application)
			err := validateAutoCompletion(application, offerings)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v; want %q", err, testCase.want)
			}
		})
	}
}

// A route whose capability is in the session's manual approval list would need
// a person in manual mode: the automatic completion must not declare one.
func TestAutoCompletionRouteMayNotBeAManualApprovalCapability(t *testing.T) {
	application, offerings := autoCompletionFixture(t)
	application.Chat.ApprovalPolicy.ManualApprovalCapabilities = append(
		application.Chat.ApprovalPolicy.ManualApprovalCapabilities, schemacontract.FormatConversion)
	if err := validateModelDeliveryAutoCompletion(application, offerings); err == nil ||
		!strings.Contains(err.Error(), "人工批准清单") {
		t.Fatalf("error = %v, want the manual-approval refusal", err)
	}
}

// The shipped paid route is Meshy Convert at one credit per run; the declared
// ceiling of 30 covers it. A bound under the Offering's own highest estimate is
// refused, because the route could never run under it.
func TestAutoCompletionPaidBoundCoversTheOffering(t *testing.T) {
	application, offerings := autoCompletionFixture(t)
	conversion := conversionByOffering(&application.ModelDelivery, "meshy/processing/convert")
	conversion.MaxCredits = 1
	if err := validateModelDeliveryAutoCompletion(application, offerings); err != nil {
		t.Fatalf("error = %v, want a bound equal to the estimate to be accepted", err)
	}
	// 0 is not a bound: the shape check refuses it as a missing bound.
	conversion.MaxCredits = 0
	if err := validateAutoCompletion(application, offerings); err == nil {
		t.Fatal("a paid route without a bound was accepted")
	}
}

// A free route whose Offering is priced in native units still charges: the
// legacy estimate alone reports 0 and the automatic completion would treat it
// as free, never counting it against the per-Task paid ceiling (#478).
func TestAutoCompletionFreeRouteMayNotBeNativePriced(t *testing.T) {
	application, offerings := autoCompletionFixture(t)
	const id = "local/generated-glb/to-gltf"
	local, ok := offerings[schemacontract.OfferingID(id)]
	if !ok {
		t.Fatal("免费的本地转换 offering 缺失")
	}
	local.NativePricing = &schemacontract.NativePricing{
		Version: 1, Unit: "local.credit", UnitLabel: "本地积分", BaseAmount: "1",
	}
	offerings[schemacontract.OfferingID(id)] = local
	err := validateAutoCompletion(application, offerings)
	if err == nil || !strings.Contains(err.Error(), "按原生计价") {
		t.Fatalf("error = %v; want the native-pricing refusal", err)
	}
}

// Retired routes are recognized by their fixed input so old plans still count;
// they never run automatically.
func TestRetiredRouteMayNotCompleteAutomatically(t *testing.T) {
	application, offerings := autoCompletionFixture(t)
	conversionByOffering(&application.ModelDelivery, "meshy/processing/remesh").Auto = ConversionAutoFree
	err := validateAutoCompletion(application, offerings)
	if err == nil || !strings.Contains(err.Error(), "已退役") {
		t.Fatalf("error = %v, want the retired-route refusal", err)
	}
}
// TestLoaderRefusesAnAutomaticRouteOnTheManualApprovalList proves the wiring:
// the check is not a helper a caller may forget, it runs while the Catalog
// loads, so a document that declares one is refused as a whole.
func TestLoaderRefusesAnAutomaticRouteOnTheManualApprovalList(t *testing.T) {
	document := loadDefaultDocument(t)
	application, ok := document["application"].(map[string]any)
	if !ok {
		t.Fatalf("document has no application block: %T", document["application"])
	}
	chat, ok := application["chat"].(map[string]any)
	if !ok {
		t.Fatalf("document has no chat block: %T", document["chat"])
	}
	policy, ok := chat["approval_policy"].(map[string]any)
	if !ok {
		t.Fatalf("document has no approval_policy block: %T", chat["approval_policy"])
	}
	policy["manual_approval_capabilities"] = append(
		toStringSlice(policy["manual_approval_capabilities"]), string(schemacontract.FormatConversion))
	if _, err := Load(bytes.NewReader(encodeDocument(t, document))); err == nil ||
		!strings.Contains(err.Error(), "人工批准清单") {
		t.Fatalf("error = %v, want the load to refuse the route", err)
	}
}

func toStringSlice(raw any) []string {
	items, _ := raw.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}
