package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefaultModelDeliveryDeclaresEveryTargetFormatAndItsConversions(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	delivery := product.Application().ModelDelivery
	var ids []string
	for _, format := range delivery.Formats {
		ids = append(ids, format.ID)
	}
	if got := strings.Join(ids, ","); got != "glb,gltf,fbx,obj,stl,usdz,3mf" {
		t.Fatalf("target formats = %s", got)
	}
	// Meshy Convert converts every missing format in one run (#330; Remesh
	// with convert_format_only is retired and only recognizes old plans);
	// Tripo converts one format per run.
	var convert, remesh, tripo *ModelConversionSchema
	for index := range delivery.Conversions {
		switch delivery.Conversions[index].OfferingID {
		case "meshy/processing/convert":
			convert = &delivery.Conversions[index]
		case "meshy/processing/remesh":
			remesh = &delivery.Conversions[index]
		case "tripo/tripo-v3.1/format-conversion":
			tripo = &delivery.Conversions[index]
		}
	}
	if convert == nil || remesh == nil || tripo == nil || convert.Retired || !remesh.Retired || len(convert.Input) != 0 {
		t.Fatalf("conversions = %#v", delivery.Conversions)
	}
	var input map[string]any
	if err := json.Unmarshal(remesh.Input, &input); err != nil || input["convert_format_only"] != true {
		t.Fatalf("retired remesh input = %s", remesh.Input)
	}
	if tripo.FormatParameter != "format" || tripo.FormatValues["gltf"] != "GLTF" || tripo.FormatValues["3mf"] != "3MF" {
		t.Fatalf("tripo conversion = %#v", tripo)
	}
	if len(delivery.BlockedCapabilities) == 0 {
		t.Fatal("rigged and animated results must not be converted")
	}
	if delivery.Archive.Audit.Action != "download_model_archive" {
		t.Fatalf("archive audit = %#v", delivery.Archive.Audit)
	}
	routes := product.Application().Runtime.HTTP.Routes
	if routes.ModelFormats == "" || routes.ModelFormatsComplete == "" || routes.ModelArchive == "" {
		t.Fatalf("routes = %#v", routes)
	}
	public, err := json.Marshal(product.Application().Public())
	if err != nil {
		t.Fatal(err)
	}
	published := product.Application().Public().DeliveryFormats
	if len(published) != len(delivery.Formats) || published[0].ID != delivery.Formats[0].ID || published[0].Label != delivery.Formats[0].Label {
		t.Fatalf("delivery formats = %#v, want the declared formats in order", published)
	}
	if strings.Contains(string(public), "model_delivery") || strings.Contains(string(public), "format_values") || strings.Contains(string(public), "source_formats") {
		t.Fatal("the conversion declarations are server-side only")
	}
}

func TestModelDeliveryRejectsInconsistentDeclarations(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		mutate func(*ModelDeliverySchema)
		want   string
	}{
		"unknown conversion format": {func(d *ModelDeliverySchema) { d.Conversions[0].SourceFormats = []string{"nope"} }, "未知 format"},
		"format values without parameter": {func(d *ModelDeliverySchema) {
			d.Conversions[0].FormatParameter = ""
		}, "format_parameter"},
		"conversion without formats": {func(d *ModelDeliverySchema) {
			d.Conversions[1].Provides = nil
			d.Conversions[1].FormatValues = nil
		}, "没有声明可提供的格式"},
		"duplicate format":            {func(d *ModelDeliverySchema) { d.Formats[1].ID = d.Formats[0].ID }, "重复"},
		"duplicate directory":         {func(d *ModelDeliverySchema) { d.Formats[1].Directory = d.Formats[0].Directory }, "重复"},
		"extension without dot":       {func(d *ModelDeliverySchema) { d.Formats[0].Extension = "glb" }, "不完整"},
		"unknown capability":          {func(d *ModelDeliverySchema) { d.Capabilities = append(d.Capabilities, "no-such") }, "未知 capability"},
		"empty reason":                {func(d *ModelDeliverySchema) { d.Reasons.NoRoute = " " }, "reasons.no_route"},
		"no size bound":               {func(d *ModelDeliverySchema) { d.Archive.MaxBytes = 0 }, "archive"},
		"other directory clash":       {func(d *ModelDeliverySchema) { d.Archive.OtherDirectory = d.Formats[0].Directory }, "冲突"},
		"readme without placeholders": {func(d *ModelDeliverySchema) { d.Archive.Readme.MissingLine = "missing" }, "readme"},
		"unknown audit action":        {func(d *ModelDeliverySchema) { d.Archive.Audit.Action = "no_such_action" }, "audit action"},
		"obj package format":          {func(d *ModelDeliverySchema) { d.Archive.ObjPackage.FormatID = "nope" }, "obj_package"},
		"bad fixed input":             {func(d *ModelDeliverySchema) { d.Conversions[1].Input = json.RawMessage(`[1]`) }, "JSON 对象"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			application := product.Application()
			testCase.mutate(&application.ModelDelivery)
			err := ValidateApplicationSchema(application)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v; want %q", err, testCase.want)
			}
		})
	}
}

func TestModelDeliveryConversionsMustMatchTheirOfferings(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offerings := product.byID
	cases := map[string]struct {
		mutate func(*ModelDeliverySchema)
		want   string
	}{
		"unknown offering":           {func(d *ModelDeliverySchema) { d.Conversions[0].OfferingID = "nobody/processing/convert" }, "未知 Offering"},
		"not a processing offering":  {func(d *ModelDeliverySchema) { d.Conversions[0].OfferingID = "meshy/meshy-7.1/text-to-3d" }, "处理类"},
		"undeclared fixed parameter": {func(d *ModelDeliverySchema) { d.Conversions[1].Input = json.RawMessage(`{"no_such_parameter":true}`) }, "未在 Offering"},
		"format parameter missing":   {func(d *ModelDeliverySchema) { d.Conversions[0].FormatParameter = "no_such" }, "format_parameter"},
		"format value not an option": {func(d *ModelDeliverySchema) { d.Conversions[0].FormatValues["gltf"] = "COLLADA" }, "不是 Offering"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			delivery := cloneModelDelivery(product.Application().ModelDelivery)
			testCase.mutate(&delivery)
			err := validateModelDeliveryOfferings(delivery, offerings)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v; want %q", err, testCase.want)
			}
		})
	}
	if err := validateModelDeliveryOfferings(product.Application().ModelDelivery, offerings); err != nil {
		t.Fatalf("shipped declarations rejected: %v", err)
	}
}

func TestModelDeliveryIsOptionalForMinimalSchemas(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	application.ModelDelivery = ModelDeliverySchema{}
	// Dropping the declaration drops its translations with it.
	for path := range application.Localization.Bundles["en"].Fields {
		if strings.HasPrefix(path, "model_delivery.") {
			delete(application.Localization.Bundles["en"].Fields, path)
		}
	}
	if !application.ModelDelivery.Disabled() {
		t.Fatal("empty declaration must be disabled")
	}
	if err := ValidateApplicationSchema(application); err != nil {
		t.Fatalf("schema without model_delivery rejected: %v", err)
	}
}

// TestEnabledModelOfferingsAreCoveredByModelDelivery keeps the format list
// honest: a finished task of an enabled Offering whose required output is a
// model must be either covered by the format projection or explicitly
// blocked from conversion. Otherwise the workspace lists every format,
// including the delivered GLB, as unavailable (#187 Bang, 2026-10-21).
func TestEnabledModelOfferingsAreCoveredByModelDelivery(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	delivery := product.Application().ModelDelivery
	covered := map[string]bool{}
	for _, capability := range delivery.Capabilities {
		covered[string(capability)] = true
	}
	for _, capability := range delivery.BlockedCapabilities {
		covered[string(capability)] = true
	}
	for _, offering := range product.List("") {
		for _, output := range offering.Outputs {
			if output.Role == "model" && output.Delivery == "required" && !covered[string(offering.Capability)] {
				t.Errorf("enabled %s (%s) delivers a required model but its capability has no format coverage", offering.ID, offering.Capability)
			}
		}
	}
}

// #185: a result whose Offering delivers required export formats must get the
// format list in its 格式 tab, so its capability is covered (not merely
// blocked from conversion).
func TestModelDeliveryCoversEveryEnabledOfferingWithRequiredExports(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	covered := map[string]bool{}
	for _, capability := range product.Application().ModelDelivery.Capabilities {
		covered[string(capability)] = true
	}
	for _, offering := range product.List("") {
		for _, output := range offering.Outputs {
			if output.Role == "converted_model" && output.Delivery == "required" && !covered[string(offering.Capability)] {
				t.Errorf("%s delivers required %s but model_delivery does not cover %s", offering.ID, output.Variant, offering.Capability)
				break
			}
		}
	}
}

// #185: a delivered export must be listed as that format, otherwise the 格式
// tab offers a paid conversion for a file the user already has. Tripo's native
// OBJ export is a ZIP (OBJ, material library and textures), so it is declared
// as application/zip and claimed by the OBJ format.
func TestEveryRequiredExportOfAnEnabledOfferingIsClaimedByItsFormat(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	delivery := product.Application().ModelDelivery
	covered := map[string]bool{}
	for _, capability := range delivery.Capabilities {
		covered[string(capability)] = true
	}
	formats := map[string]ModelFormatSchema{}
	for _, format := range delivery.Formats {
		formats[format.ID] = format
	}
	claims := func(format ModelFormatSchema, role, variant, mediaType string) bool {
		for _, match := range format.Matches {
			roleOK, variantOK, mediaOK := false, len(match.Variants) == 0 && len(match.VariantPrefixes) == 0, len(match.MediaTypes) == 0
			for _, r := range match.Roles {
				roleOK = roleOK || r == role
			}
			for _, v := range match.Variants {
				variantOK = variantOK || strings.EqualFold(v, variant)
			}
			for _, prefix := range match.VariantPrefixes {
				variantOK = variantOK || prefix != "" && strings.HasPrefix(strings.ToLower(variant), strings.ToLower(prefix))
			}
			for _, m := range match.MediaTypes {
				mediaOK = mediaOK || strings.EqualFold(m, mediaType)
			}
			if roleOK && variantOK && mediaOK {
				return true
			}
		}
		return false
	}
	checked := 0
	for _, offering := range product.List("") {
		if !covered[string(offering.Capability)] {
			continue
		}
		for _, output := range offering.Outputs {
			format, isFormat := formats[output.Variant]
			if output.Role != "converted_model" || output.Delivery != "required" || !isFormat {
				continue
			}
			for _, mediaType := range output.MediaTypes {
				checked++
				if !claims(format, output.Role, output.Variant, mediaType) {
					t.Errorf("%s delivers %s as %s, but the %s format does not claim it", offering.ID, output.Variant, mediaType, format.ID)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no required export checked")
	}
}

// Tripo's native export ledger declares the OBJ child by its real bytes in
// every Offering, enabled or not, so re-enabling one cannot bring back the
// mislabelled slot.
func TestTripoNativeOBJExportIsDeclaredAsZIP(t *testing.T) {
	var raw struct {
		Offerings []struct {
			ID      string `json:"id"`
			Outputs []struct {
				Role       string   `json:"role"`
				Variant    string   `json:"variant"`
				MediaTypes []string `json:"media_types"`
			} `json:"outputs"`
			ProviderOptions struct {
				NativeConversion *struct {
					Formats []struct {
						Format    string `json:"format"`
						Variant   string `json:"variant"`
						MediaType string `json:"media_type"`
					} `json:"formats"`
				} `json:"native_conversion"`
			} `json:"provider_options"`
		} `json:"offerings"`
	}
	if err := json.Unmarshal(defaultCatalog, &raw); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, offering := range raw.Offerings {
		if offering.ProviderOptions.NativeConversion == nil {
			continue
		}
		for _, format := range offering.ProviderOptions.NativeConversion.Formats {
			if format.Format != "OBJ" {
				continue
			}
			seen++
			if format.MediaType != "application/zip" {
				t.Errorf("%s native OBJ child declared as %q", offering.ID, format.MediaType)
			}
			for _, output := range offering.Outputs {
				if output.Role == "converted_model" && output.Variant == format.Variant && (len(output.MediaTypes) != 1 || output.MediaTypes[0] != "application/zip") {
					t.Errorf("%s OBJ slot declared as %v", offering.ID, output.MediaTypes)
				}
			}
		}
	}
	if seen < 8 {
		t.Fatalf("Tripo native OBJ exports found: %d", seen)
	}
}
