package catalog

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// #330: format completion and format conversion use Meshy's Convert API
// (https://docs.meshy.ai/en/api/convert, checked 2026-10-22): POST
// /openapi/v1/convert takes input_task_id or model_url (data URI up to
// 100 MB), target_formats is required (glb, fbx, obj, usdz, blend, stl, 3mf),
// the task object carries only model_urls, and a run costs 1 credit
// (https://docs.meshy.ai/en/api/pricing). Remesh's deprecated
// convert_format_only is no longer sent.

const meshyConvertID = schemacontract.OfferingID("meshy/processing/convert")

func meshy330Offering(t *testing.T, product *Catalog, id schemacontract.OfferingID) schemacontract.Offering {
	t.Helper()
	offering, ok := product.Get(id)
	if !ok {
		t.Fatalf("offering %s is missing", id)
	}
	return offering
}

func meshy330Parameter(offering schemacontract.Offering, name string) (schemacontract.Parameter, bool) {
	for _, parameter := range offering.Parameters {
		if parameter.Name == name {
			return parameter, true
		}
	}
	return schemacontract.Parameter{}, false
}

func TestMeshy330ConvertOfferingFollowsTheDocumentedContract(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	convert := meshy330Offering(t, product, meshyConvertID)
	if convert.Capability != schemacontract.FormatConversion || convert.Domain != schemacontract.DomainProcessing ||
		convert.ProviderID != "meshy" || convert.AdapterKey != "meshy.v2" || !convert.Enabled || convert.Default || convert.Recommended {
		t.Fatalf("convert offering = %#v", convert)
	}
	// One Convert run costs 1 credit, whatever formats it converts to.
	if convert.EstimatedCredits != 1 || !convert.CreditsMetered || convert.NativePricing != nil {
		t.Fatalf("convert price = %d metered=%v native=%v", convert.EstimatedCredits, convert.CreditsMetered, convert.NativePricing)
	}
	if got := convert.EstimateTotalCredits(json.RawMessage(`{"source_task":"task-1"}`)); got != 1 {
		t.Fatalf("convert total estimate = %d", got)
	}
	remesh := meshy330Offering(t, product, "meshy/processing/remesh")
	sourceTask, ok := meshy330Parameter(convert, "source_task")
	remeshTask, _ := meshy330Parameter(remesh, "source_task")
	// The documentation names no task types; the documented Remesh/Retexture
	// list (without Multi-Image) is used, as for Rigging (#329).
	if !ok || sourceTask.Type != "task" || sourceTask.WireName != "input_task_id" || !sourceTask.SameProvider ||
		sourceTask.UpstreamJobMaxAgeSeconds != 172800 || !slices.Equal(sourceTask.TaskOfferings, remeshTask.TaskOfferings) ||
		slices.Contains(sourceTask.TaskOfferings, "meshy/meshy-7.1-multiview/image-to-3d") {
		t.Fatalf("source_task = %#v", sourceTask)
	}
	sourceAsset, ok := meshy330Parameter(convert, "source_asset")
	if !ok || sourceAsset.Type != "asset" || sourceAsset.WireName != "model_url" || sourceAsset.MaxBytes != 70<<20 ||
		!slices.Equal(sourceAsset.Extensions, []string{".glb", ".gltf", ".obj", ".fbx", ".stl"}) {
		t.Fatalf("source_asset = %#v", sourceAsset)
	}
	formats, ok := meshy330Parameter(convert, "target_formats")
	var defaults []string
	if ok {
		_ = json.Unmarshal(formats.Default, &defaults)
	}
	six := []string{"glb", "fbx", "obj", "usdz", "stl", "3mf"}
	if !ok || !formats.Required || !formats.Fixed || formats.Type != "string-array" || !slices.Equal(defaults, six) ||
		formats.Presentation == nil || formats.Presentation.Surface != "readonly" {
		t.Fatalf("target_formats = %#v", formats)
	}
	// The description says what the person gets: model files only, and an
	// OBJ without a material library or textures.
	if !strings.Contains(formats.Description, "OBJ") || !strings.Contains(formats.Description, "贴图") {
		t.Fatalf("target_formats description = %q", formats.Description)
	}
	if len(convert.Parameters) != 3 {
		t.Fatalf("convert declares %d parameters, want source_task, source_asset and target_formats", len(convert.Parameters))
	}
	var outputs []string
	for _, output := range convert.Outputs {
		if output.Role != "model" || output.Minimum == nil || *output.Minimum != 1 {
			t.Fatalf("convert output %#v: only the six model files are delivered", output)
		}
		outputs = append(outputs, output.Variant)
	}
	if !slices.Equal(outputs, six) {
		t.Fatalf("convert outputs = %v", outputs)
	}
	var options map[string]any
	if err := json.Unmarshal(convert.ProviderOptions, &options); err != nil {
		t.Fatal(err)
	}
	inline, _ := options["inline_model_input"].(map[string]any)
	results, _ := options["result_assets"].([]any)
	if options["endpoint"] != "convert" || options["api_version"] != "v1" || inline["max_bytes"] != float64(70<<20) || len(results) != 1 {
		t.Fatalf("convert provider_options = %v", options)
	}
	if result := results[0].(map[string]any); result["path"] != "model_urls" || result["role"] != "model" || result["mode"] != "map" {
		t.Fatalf("convert result_assets = %v", results)
	}
}

// Existing defaults stay: with no choice the first compatible format
// conversion in server order (priority) is still the one used before.
func TestMeshy330ConvertDoesNotBecomeTheDefaultConversion(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offerings := product.List(schemacontract.FormatConversion)
	if len(offerings) == 0 || offerings[len(offerings)-1].ID != meshyConvertID {
		ids := []schemacontract.OfferingID{}
		for _, offering := range offerings {
			ids = append(ids, offering.ID)
		}
		t.Fatalf("format conversion order = %v, want Meshy Convert last", ids)
	}
	delivery := product.Application().ModelDelivery
	var last ModelConversionSchema
	for _, conversion := range delivery.Conversions {
		if !conversion.Retired {
			last = conversion
		}
	}
	if last.OfferingID != string(meshyConvertID) || !slices.Equal(last.Provides, []string{"glb", "fbx", "obj", "usdz", "stl", "3mf"}) ||
		len(last.Input) != 0 || last.Retired {
		t.Fatalf("last conversion = %#v, want Meshy Convert after the local routes", last)
	}
	for _, conversion := range delivery.Conversions {
		if conversion.OfferingID == "meshy/processing/remesh" && !conversion.Retired {
			t.Fatalf("Remesh is still a format-completion route: %#v", conversion)
		}
	}
	// The Meshy connection runs it like the other Meshy model-input steps.
	for _, connection := range product.Application().Runtime.ProviderConnections {
		if connection.ID != "meshy" {
			continue
		}
		found := false
		for _, group := range connection.ConditionalCapabilities {
			found = found || (slices.Contains(group.Capabilities, schemacontract.FormatConversion) && slices.Contains(group.Capabilities, schemacontract.Remesh))
		}
		if !found {
			t.Fatalf("meshy connection does not run format conversion: %#v", connection.ConditionalCapabilities)
		}
	}
}

func TestMeshy330RemeshNoLongerSendsConvertFormatOnly(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	remesh := meshy330Offering(t, product, "meshy/processing/remesh")
	if _, exists := meshy330Parameter(remesh, "convert_format_only"); exists {
		t.Fatal("remesh still declares convert_format_only")
	}
	listed := false
	for _, entry := range product.Application().ProviderDeprecations {
		if len(entry.Exemptions) != 0 {
			t.Fatalf("exemptions = %#v, want none", entry.Exemptions)
		}
		listed = listed || slices.ContainsFunc(entry.Parameters, func(field DeprecatedWireField) bool { return field.Name == "convert_format_only" })
		if entry.ProviderID == "meshy" && !slices.Contains(entry.Sources, "https://docs.meshy.ai/en/api/convert") {
			t.Fatalf("meshy deprecation sources = %v", entry.Sources)
		}
	}
	if !listed {
		t.Fatal("convert_format_only must stay listed as deprecated so the Catalog keeps rejecting it")
	}
	for _, tool := range product.Application().Chat.Tools {
		if tool.Processing == nil {
			continue
		}
		if _, mapped := tool.Processing.ParameterArguments["convert_format_only"]; mapped || strings.Contains(string(tool.Parameters), "convert_format_only") {
			t.Fatalf("tool %s still offers convert_format_only", tool.Name)
		}
	}
	// The validation still blocks it.
	err = mutateDefaultCatalog(t, func(document map[string]any) {
		offering := offeringByID(t, document, "meshy/processing/remesh")
		offering["parameters"] = append(offeringParameters(offering), map[string]any{"name": "convert_format_only", "label": "仅转换格式", "type": "boolean"})
	})
	if err == nil || !strings.Contains(err.Error(), "provider_deprecations") {
		t.Fatalf("convert_format_only on remesh accepted or wrong error: %v", err)
	}
}

// Completion plans made before #330 ran Remesh with convert_format_only. The
// retired conversion still recognizes them (their files and running state),
// but never plans a new run, and is exempt from the input declaration check.
func TestMeshy330RetiredRemeshConversionOnlyRecognizesOldPlans(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var retired *ModelConversionSchema
	for index, conversion := range product.Application().ModelDelivery.Conversions {
		if conversion.Retired {
			if retired != nil {
				t.Fatal("more than one retired conversion")
			}
			retired = &product.Application().ModelDelivery.Conversions[index]
		}
	}
	var input map[string]any
	if retired == nil || retired.OfferingID != "meshy/processing/remesh" || json.Unmarshal(retired.Input, &input) != nil ||
		input["convert_format_only"] != true || len(retired.Provides) != 6 {
		t.Fatalf("retired conversion = %#v", retired)
	}
	for name, change := range map[string]func(map[string]any){
		"retired entry with format values": func(conversion map[string]any) {
			conversion["format_parameter"] = "topology"
			conversion["format_values"] = map[string]any{"glb": "quad"}
		},
		"retired entry without provides": func(conversion map[string]any) { delete(conversion, "provides") },
	} {
		t.Run(name, func(t *testing.T) {
			err := mutateDefaultCatalog(t, func(document map[string]any) {
				delivery := document["application"].(map[string]any)["model_delivery"].(map[string]any)
				for _, raw := range delivery["conversions"].([]any) {
					if conversion := raw.(map[string]any); conversion["retired"] == true {
						change(conversion)
					}
				}
			})
			if err == nil || !strings.Contains(err.Error(), "conversions") {
				t.Fatalf("invalid retired conversion accepted or wrong error: %v", err)
			}
		})
	}
}

// A Meshy Convert OBJ is a conversion result: the ZIP README says it has no
// material library or textures when none came with it.
func TestMeshy330ConvertedObjNoteCoversMeshyConvert(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	object := product.Application().ModelDelivery.Archive.ObjPackage
	if !slices.Contains(object.ConvertedOfferings, string(meshyConvertID)) {
		t.Fatalf("converted offerings = %v", object.ConvertedOfferings)
	}
	note := product.Application().ModelDelivery.Archive.Readme.ConvertedObjNote
	if !strings.Contains(note, "Meshy Convert") || !strings.Contains(note, "{path}") {
		t.Fatalf("converted OBJ note does not cover Meshy Convert: %q", note)
	}
	if err := mutateDefaultCatalog(t, func(document map[string]any) {
		archive := document["application"].(map[string]any)["model_delivery"].(map[string]any)["archive"].(map[string]any)
		archive["obj_package"].(map[string]any)["converted_offerings"] = []any{"meshy/processing/missing"}
	}); err == nil || !strings.Contains(err.Error(), "converted_offerings") {
		t.Fatalf("unknown converted offering accepted or wrong error: %v", err)
	}
}
