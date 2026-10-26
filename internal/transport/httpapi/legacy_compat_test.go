package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestLegacyHealthContractRemainsAvailableDuringCutover(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	request := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Status    string `json:"status"`
		Service   string `json:"service"`
		Version   string `json:"version"`
		Timestamp string `json:"timestamp"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "ok" {
		t.Fatalf("status field = %q", body.Status)
	}
	if body.Service != "Plinth Platform" {
		t.Fatalf("service = %q", body.Service)
	}
	if body.Version == "" {
		t.Fatal("version is empty")
	}
	if _, err := time.Parse(time.RFC3339Nano, body.Timestamp); err != nil {
		t.Fatalf("timestamp = %q: %v", body.Timestamp, err)
	}
}

func TestLegacyVersionContractUsesProductSchemaServiceVersion(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	response := serveLegacyRequest(
		handler,
		http.MethodGet,
		"/api/version",
		nil,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("X-Art-AI-Runtime"); got != "go" {
		t.Fatalf("X-Art-AI-Runtime = %q", got)
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Version != "0.1.0" {
		t.Fatalf("version = %q", body.Version)
	}
}

func TestUnknownAPIRouteDoesNotMasqueradeAsSuccessfulHTML(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	request := httptest.NewRequest(http.MethodGet, "/api/does-not-exist", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestLegacyProviderDiscoveryIsProjectedFromUnifiedSchema(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	response := serveLegacyRequest(
		handler,
		http.MethodGet,
		"/api/providers?capability=image-to-image&projection=extended",
		nil,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var discovered struct {
		Providers []struct {
			ID               string   `json:"id"`
			Name             string   `json:"name"`
			Features         []string `json:"features"`
			Recommended      bool     `json:"recommended"`
			EstimatedCredits int      `json:"estimatedCredits"`
			ParamSchemas     map[generation.Capability][]struct {
				Name         string                          `json:"name"`
				Label        string                          `json:"label"`
				Type         generation.PresentationControl  `json:"type"`
				Default      any                             `json:"default"`
				Options      []generation.ParameterOption    `json:"options"`
				Description  string                          `json:"description"`
				DisabledWhen []generation.ParameterCondition `json:"disabled_when"`
				Notices      []generation.ParameterNotice    `json:"notices"`
			} `json:"paramSchemas"`
			Constraints map[generation.Capability]struct {
				PromptMaxLength       int      `json:"promptMaxLength"`
				ReferenceImageMin     int      `json:"referenceImageMin"`
				ReferenceImageMax     int      `json:"referenceImageMax"`
				ReferenceImageFormats []string `json:"referenceImageFormats"`
				ReferenceImageMaxSize int64    `json:"referenceImageMaxSize"`
			} `json:"constraints"`
		} `json:"providers"`
		DefaultModel string `json:"defaultModel"`
	}
	if err := json.NewDecoder(response.Body).Decode(&discovered); err != nil {
		t.Fatal(err)
	}
	if discovered.DefaultModel != "nano-banana-pro" ||
		len(discovered.Providers) != 5 {
		t.Fatalf("discovered = %#v", discovered)
	}
	first := discovered.Providers[0]
	if first.ID != "nano-banana-pro" ||
		first.Name != "Nano Banana Pro (高质量)" ||
		first.EstimatedCredits != 9 ||
		first.Recommended {
		t.Fatalf("first provider = %#v", first)
	}
	schema := first.ParamSchemas[generation.ImageToImage]
	if len(schema) != 3 ||
		schema[0].Name != "aspect_ratio" ||
		schema[0].Label != "宽高比" ||
		schema[0].Type != generation.ControlSelect ||
		len(schema[0].DisabledWhen) != 1 ||
		schema[0].DisabledWhen[0].Parameter != "generate_multi_view" ||
		len(schema[0].Notices) != 1 ||
		schema[0].Notices[0].MessageKey !=
			"ui.image_to_image.aspect_ratio_warning" ||
		schema[1].Name != "generate_multi_view" ||
		schema[1].Description != "生成多个视角的图片" {
		t.Fatalf("param schema = %#v", schema)
	}
	constraints := first.Constraints[generation.ImageToImage]
	if constraints.PromptMaxLength != 10000 ||
		constraints.ReferenceImageMin != 1 ||
		constraints.ReferenceImageMax != 5 ||
		constraints.ReferenceImageMaxSize != 20_000_000 || // Meshy official 20 MB (#276)
		!slices.Equal(
			constraints.ReferenceImageFormats,
			[]string{"png", "jpg", "jpeg"},
		) {
		t.Fatalf("constraints = %#v", constraints)
	}
	var skillProviderFound bool
	for _, provider := range discovered.Providers {
		if provider.ID == "gpt-image-2.5-sunburst" {
			skillProviderFound = slices.Contains(provider.Features, "skills")
		}
	}
	if !skillProviderFound {
		t.Fatalf("gpt-image-2.5-sunburst skills feature missing: %#v", discovered.Providers)
	}
}

func TestLegacyProviderDiscoveryPreservesDefaultProjectionAndOptInExtensions(
	t *testing.T,
) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	response := serveLegacyRequest(
		handler,
		http.MethodGet,
		"/api/providers",
		nil,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var providers []map[string]any
	if err := json.NewDecoder(response.Body).Decode(&providers); err != nil {
		t.Fatal(err)
	}
	expected := []struct {
		id          string
		providerKey string
	}{
		{"nano-banana-pro", "meshy"},
		{"nano-banana-2", "meshy"},
		{"gpt-image-2.5-sunburst", "openai"},
		{"doubao-seedream-4-5-251128", "jimeng"},
		{"gemini-3-pro-image", "gemini"},
		{"meshy-7.1", "meshy-3d"},
		{"meshy-t2.5", "meshy-3d"},
		{"tripo-v3.1-full", "tripo-3d"},
		{"meshy-processing", "meshy-processing"},
		{"matting-default", "matting"},
		{"local-glb-to-gltf", "local-model"},
		{"local-gltf-to-glb", "local-model"},
		{"local-generated-glb-to-gltf", "local-model"},
		{"tripo-uploaded-glb-to-stl", "tripo-processing"},
		{"meshy-uv-unwrap", "meshy-uv-unwrap"},
		{"meshy-resize", "meshy-resize"},
		{"meshy-convert", "meshy-convert"},
		{"meshy-analyze-printability", "meshy-analyze-printability"},
		{"meshy-multi-color-print", "meshy-multi-color-print"},
		{"meshy-text-to-motion", "meshy-text-to-motion"},
		{"tripo-rig-check", "tripo-rig-check"},
		{"tripo-rigging", "tripo"},
		{"tripo-rigging-v3", "tripo"},
		{"tripo-animation", "tripo"},
		{"tripo-texture", "tripo"},
		// #185 Tripo geometry that passed qualification, in legacy_models order.
		{"tripo-decimate-triangle", "tripo"},
		{"tripo-segment", "tripo"},
		{"tripo-complete", "tripo"},
		{"tripo-quick-cap", "tripo"},
		{"tripo-smartsegment-image", "tripo"},
		{"tripo-smartsegment-model", "tripo"},
		{"local-asset-recovery", "local"},
		{"tripo-image-to-splat", "tripo"},
		{"creative-lab-fridge-magnet-prototype", "meshy-creative-lab"},
		{"creative-lab-fridge-magnet-build", "meshy-creative-lab"},
		{"creative-lab-figure-prototype", "meshy-creative-lab"},
		{"creative-lab-figure-build", "meshy-creative-lab"},
		{"creative-lab-vinyl-figure-prototype", "meshy-creative-lab"},
		{"creative-lab-vinyl-figure-build", "meshy-creative-lab"},
		{"creative-lab-brick-figure-prototype", "meshy-creative-lab"},
		{"creative-lab-brick-figure-build", "meshy-creative-lab"},
		{"creative-lab-fidget-collapsible-generate", "meshy-creative-lab"},
		{"creative-lab-keychain-prototype", "meshy-creative-lab"},
		{"creative-lab-keychain-build", "meshy-creative-lab"},
		{"creative-lab-lamp-prototype", "meshy-creative-lab"},
		{"creative-lab-lamp-build", "meshy-creative-lab"},
		{"creative-lab-fidget-pixel-prototype", "meshy-creative-lab"},
		{"creative-lab-fidget-pixel-build", "meshy-creative-lab"},
		{"creative-lab-keycap-prototype", "meshy-creative-lab"},
		{"creative-lab-keycap-build", "meshy-creative-lab"},
	}
	if len(providers) != len(expected) {
		t.Fatalf("provider count = %d, want %d", len(providers), len(expected))
	}
	for index, want := range expected {
		if providers[index]["id"] != want.id ||
			providers[index]["providerKey"] != want.providerKey {
			t.Fatalf("provider[%d] = %#v, want %#v", index, providers[index], want)
		}
		for _, extension := range []string{
			"estimatedCredits",
			"features",
			"inputSchemas",
		} {
			if _, exists := providers[index][extension]; exists {
				t.Fatalf(
					"default provider[%d] leaked extension %q: %#v",
					index,
					extension,
					providers[index],
				)
			}
		}
	}

	response = serveLegacyRequest(
		handler,
		http.MethodGet,
		"/api/providers?capability=image-to-image",
		nil,
	)
	var legacy struct {
		Providers []map[string]any `json:"providers"`
	}
	if err := json.NewDecoder(response.Body).Decode(&legacy); err != nil {
		t.Fatal(err)
	}
	if len(legacy.Providers) == 0 {
		t.Fatalf("legacy providers = %#v", legacy)
	}
	for _, field := range []string{
		"providerKey",
		"estimatedCredits",
		"inputSchemas",
	} {
		if _, exists := legacy.Providers[0][field]; exists {
			t.Fatalf("legacy capability projection leaked %q: %#v", field, legacy)
		}
	}
	parameterSchemas, ok := legacy.Providers[0]["paramSchemas"].(map[string]any)
	if !ok {
		t.Fatalf("legacy parameter schemas = %#v", legacy.Providers[0])
	}
	imageParameters, ok := parameterSchemas["image-to-image"].([]any)
	if !ok || len(imageParameters) == 0 {
		t.Fatalf("legacy image parameters = %#v", parameterSchemas)
	}
	firstParameter, ok := imageParameters[0].(map[string]any)
	if !ok {
		t.Fatalf("legacy first parameter = %#v", imageParameters[0])
	}
	for _, extension := range []string{"disabled_when", "notices"} {
		if _, exists := firstParameter[extension]; exists {
			t.Fatalf("legacy parameter leaked %q: %#v", extension, firstParameter)
		}
	}
	constraints, ok := legacy.Providers[0]["constraints"].(map[string]any)
	if !ok {
		t.Fatalf("legacy constraints = %#v", legacy.Providers[0])
	}
	imageConstraints, ok := constraints["image-to-image"].(map[string]any)
	if !ok {
		t.Fatalf("legacy image constraints = %#v", constraints)
	}
	outputSizes, ok := imageConstraints["outputSizes"].([]any)
	if !ok || len(outputSizes) == 0 {
		t.Fatalf("legacy output sizes = %#v", imageConstraints)
	}
	firstOutputSize, ok := outputSizes[0].(map[string]any)
	if !ok || firstOutputSize["label"] != "1:1 方形" {
		t.Fatalf("legacy first output size = %#v", outputSizes[0])
	}

	response = serveLegacyRequest(
		handler,
		http.MethodGet,
		"/api/providers?capability=image-to-image&projection=extended",
		nil,
	)
	var extended struct {
		Providers []map[string]any `json:"providers"`
	}
	if err := json.NewDecoder(response.Body).Decode(&extended); err != nil {
		t.Fatal(err)
	}
	if len(extended.Providers) == 0 ||
		extended.Providers[0]["estimatedCredits"] == nil ||
		extended.Providers[0]["inputSchemas"] == nil {
		t.Fatalf("extended providers = %#v", extended)
	}
	parameterSchemas, ok = extended.Providers[0]["paramSchemas"].(map[string]any)
	if !ok {
		t.Fatalf("extended parameter schemas = %#v", extended.Providers[0])
	}
	imageParameters, ok = parameterSchemas["image-to-image"].([]any)
	if !ok || len(imageParameters) == 0 {
		t.Fatalf("extended image parameters = %#v", parameterSchemas)
	}
	firstParameter, ok = imageParameters[0].(map[string]any)
	if !ok ||
		firstParameter["disabled_when"] == nil ||
		firstParameter["notices"] == nil {
		t.Fatalf("extended first parameter = %#v", imageParameters[0])
	}
}

func TestLegacyProviderDiscoveryIncludesProcessingSchemaWithoutHandlerFieldLists(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	response := serveLegacyRequest(
		handler,
		http.MethodGet,
		"/api/providers?capability=background-removal",
		nil,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var discovered struct {
		Providers []struct {
			ParamSchemas map[generation.Capability][]struct {
				Name string `json:"name"`
			} `json:"paramSchemas"`
		} `json:"providers"`
		DefaultModel string `json:"defaultModel"`
	}
	if err := json.NewDecoder(response.Body).Decode(&discovered); err != nil {
		t.Fatal(err)
	}
	if discovered.DefaultModel != "matting-default" ||
		len(discovered.Providers) != 1 {
		t.Fatalf("discovered = %#v", discovered)
	}
	var names []string
	for _, parameter := range discovered.Providers[0].ParamSchemas[generation.BackgroundRemove] {
		names = append(names, parameter.Name)
	}
	if !slices.Equal(names, []string{
		"transparent_object",
	}) {
		t.Fatalf("schema names = %#v", names)
	}
}

func TestExtendedProviderProjectionOwnsAnimationLibraryFromOfferingSchema(
	t *testing.T,
) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	response := serveLegacyRequest(
		handler,
		http.MethodGet,
		"/api/providers?capability=animation&projection=extended",
		nil,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var discovered struct {
		Providers []struct {
			ID           string `json:"id"`
			ParamSchemas map[generation.Capability][]struct {
				Name    string `json:"name"`
				Options []struct {
					Value string `json:"value"`
					Label string `json:"label"`
					Group string `json:"group"`
				} `json:"options"`
			} `json:"paramSchemas"`
		} `json:"providers"`
	}
	if err := json.NewDecoder(response.Body).Decode(&discovered); err != nil {
		t.Fatal(err)
	}
	// #186 route B adds Tripo Retarget beside Meshy; it exposes only walk.
	if len(discovered.Providers) != 2 || discovered.Providers[0].ID != "meshy-processing" || discovered.Providers[1].ID != "tripo-animation" {
		t.Fatalf("providers = %#v", discovered.Providers)
	}
	tripoActions := discovered.Providers[1].ParamSchemas[generation.Animation]
	if len(tripoActions) != 1 || tripoActions[0].Name != "animation_action" || len(tripoActions[0].Options) != 1 || tripoActions[0].Options[0].Value != "preset:walk" {
		t.Fatalf("Tripo Retarget actions = %#v", tripoActions)
	}
	parameters := discovered.Providers[0].ParamSchemas[generation.Animation]
	if len(parameters) != 1 || parameters[0].Name != "action_id" {
		t.Fatalf("parameters = %#v", parameters)
	}
	options := parameters[0].Options
	if len(options) != 656 {
		t.Fatalf("animation option count = %d", len(options))
	}
	if options[0].Value != "0" ||
		options[0].Label != "待机 (Idle)" ||
		options[0].Group != "日常动作 (DailyActions)" ||
		options[4].Value != "4" || options[4].Label != "攻击 (Attack)" {
		t.Fatalf("first animation option = %#v", options[0])
	}
}

func TestLegacyTextToImageWriteRequiresApprovedExecution(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	response := serveLegacyRequest(
		handler,
		http.MethodPost,
		"/api/meshy/text-to-image",
		strings.NewReader(`{
			"prompt":"a brass hummingbird",
			"ai_model":"gpt-image-2.5-sunburst",
			"size":"1024x1024",
			"quality":"high"
		}`),
	)
	if response.Code != http.StatusForbidden {
		t.Fatalf("legacy write status = %d, want 403; body = %s", response.Code, response.Body.String())
	}
}

func TestLegacyDefaultModelWriteRequiresApprovedExecution(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	response := serveLegacyRequest(
		handler,
		http.MethodPost,
		"/api/meshy/text-to-image",
		strings.NewReader(`{"prompt":"an owl"}`),
	)
	if response.Code != http.StatusForbidden {
		t.Fatalf("legacy write status = %d, want 403; body = %s", response.Code, response.Body.String())
	}
}

func TestLegacyTextTo3DWriteRequiresApprovedExecution(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	response := serveLegacyRequest(
		handler,
		http.MethodPost,
		"/api/meshy/text-to-3d",
		strings.NewReader(`{
			"mode":"preview",
			"prompt":"a brass hummingbird",
			"ai_model":"meshy-7"
		}`),
	)
	if response.Code != http.StatusForbidden {
		t.Fatalf("legacy write status = %d, want 403; body = %s", response.Code, response.Body.String())
	}
}

func TestLegacyTextTo3DMissingModeWriteRequiresApprovedExecution(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	response := serveLegacyRequest(
		handler,
		http.MethodPost,
		"/api/meshy/text-to-3d",
		strings.NewReader(`{
			"prompt":"a brass hummingbird",
			"ai_model":"meshy-7"
		}`),
	)

	if response.Code != http.StatusForbidden {
		t.Fatalf("legacy write status = %d, want 403; body = %s", response.Code, response.Body.String())
	}
}

func TestLegacyTripoWriteRequiresApprovedExecution(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	response := serveLegacyRequest(
		handler,
		http.MethodPost,
		"/api/meshy/text-to-3d",
		strings.NewReader(`{
			"prompt":"手指",
			"ai_model":"tripo-v3.1-full",
			"export_uv":true,
			"pbr":true,
			"quad":false,
			"smart_low_poly":true,
			"auto_size":true,
			"texture":true,
			"texture_quality":"standard",
			"geometry_quality":"standard"
		}`),
	)
	if response.Code != http.StatusForbidden {
		t.Fatalf("legacy write status = %d, want 403; body = %s", response.Code, response.Body.String())
	}
}

func TestLegacyTextTo3DControlWriteRequiresApprovedExecution(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	response := serveLegacyRequest(
		handler,
		http.MethodPost,
		"/api/meshy/text-to-3d",
		strings.NewReader(`{
			"mode":"preview",
			"prompt":"a schema-driven fox",
			"ai_model":"tripo-v3.1-full"
		}`),
	)
	if response.Code != http.StatusForbidden {
		t.Fatalf("legacy write status = %d, want 403; body = %s", response.Code, response.Body.String())
	}
}

func TestLegacyImageToImageWriteRequiresApprovedExecution(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	response := serveLegacyRequest(
		handler,
		http.MethodPost,
		"/api/meshy/image-to-image",
		strings.NewReader(`{
			"prompt":"turn it into a brass figurine",
			"reference_image_urls":[
				"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
			],
			"ai_model":"gpt-image-2.5-sunburst",
			"size":"1024x1024",
			"quality":"high"
		}`),
	)
	if response.Code != http.StatusForbidden {
		t.Fatalf("legacy write status = %d, want 403; body = %s", response.Code, response.Body.String())
	}
}

func TestLegacyImageTo3DWriteRequiresApprovedExecution(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	const inlinePNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	response := serveLegacyRequest(
		handler,
		http.MethodPost,
		"/api/meshy/image-to-3d",
		strings.NewReader(`{
			"image_url":"`+inlinePNG+`",
			"image_urls":["`+inlinePNG+`"],
			"ai_model":"meshy-7",
			"target_formats":["glb","fbx"],
			"should_texture":true,
			"enable_pbr":true
		}`),
	)
	if response.Code != http.StatusForbidden {
		t.Fatalf("legacy write status = %d, want 403; body = %s", response.Code, response.Body.String())
	}
}

func TestLegacyProviderDiscoveryAppliesConfiguredWireCompatibility(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)

	allProviders := legacyProviderList(
		t,
		serveLegacyRequest(handler, http.MethodGet, "/api/providers", nil),
	)
	gptImage := legacyProviderByID(t, allProviders, "gpt-image-2.5-sunburst")
	if gptImage["recommended"] != true {
		t.Fatalf("gpt-image-2.5-sunburst recommended = %#v", gptImage["recommended"])
	}
	meshyAll := legacyProviderByID(t, allProviders, "meshy-7.1")
	if _, exists := meshyAll["constraints"]; exists {
		t.Fatalf("unfiltered meshy constraints = %#v", meshyAll["constraints"])
	}

	textToImage := legacyFilteredProviderList(
		t,
		serveLegacyRequest(
			handler,
			http.MethodGet,
			"/api/providers?capability=text-to-image",
			nil,
		),
	)
	geminiPro := legacyProviderByID(t, textToImage, "gemini-3-pro-image")
	textConstraints := legacyProviderConstraints(
		t,
		geminiPro,
		"text-to-image",
	)
	// Current Gemini protocol exposes configured aspect ratios.
	if sizes, ok := textConstraints["outputSizes"].([]any); !ok || len(sizes) != 10 {
		t.Fatalf("gemini output sizes = %#v", textConstraints["outputSizes"])
	}

	textTo3D := legacyFilteredProviderList(
		t,
		serveLegacyRequest(
			handler,
			http.MethodGet,
			"/api/providers?capability=text-to-3d",
			nil,
		),
	)
	meshy6 := legacyProviderByID(t, textTo3D, "meshy-7.1")
	meshyTextConstraints, ok := meshy6["constraints"].(map[string]any)
	if !ok {
		t.Fatalf("meshy text constraints = %#v", meshy6["constraints"])
	}
	if _, exists := meshyTextConstraints["image-to-3d"]; exists {
		t.Fatalf("meshy text constraints = %#v", meshyTextConstraints)
	}
	meshyParameters := legacyProviderParameters(t, meshy6, "text-to-3d")
	meshyImageParameters := legacyProviderParameters(t, meshy6, "image-to-3d")
	// 生成面数、重拓扑与几何分辨率由后端固定（hidden），旧投影不得出现。
	for _, item := range meshyImageParameters {
		if parameter, ok := item.(map[string]any); ok && parameter["name"] == "target_polycount" {
			t.Fatalf("hidden meshy target_polycount leaked: %#v", parameter)
		}
	}
	// 导出格式也由后端固定为全部格式（hidden），旧投影不得出现。
	for _, item := range meshyParameters {
		if parameter, ok := item.(map[string]any); ok && parameter["name"] == "target_formats" {
			t.Fatalf("hidden meshy target_formats leaked: %#v", parameter)
		}
	}
	texturePrompt := legacyMapByStringField(
		t,
		meshyParameters,
		"name",
		"texture_prompt",
	)
	if value, exists := texturePrompt["required"]; !exists || value != false {
		t.Fatalf("meshy texture_prompt required = %#v", texturePrompt)
	}

	tripo := legacyProviderByID(t, textTo3D, "tripo-v3.1-full")
	tripoParameters := legacyProviderParameters(t, tripo, "text-to-3d")
	// face_limit 声明 surface=hidden：默认投影不得出现（默认 200 万后端注入）
	for _, raw := range tripoParameters {
		parameter, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := parameter["name"].(string); name == "face_limit" {
			t.Fatalf("hidden face_limit leaked: %#v", parameter)
		}
	}

	remeshProviders := legacyFilteredProviderList(
		t,
		serveLegacyRequest(
			handler,
			http.MethodGet,
			"/api/providers?capability=remesh",
			nil,
		),
	)
	processing := legacyProviderByID(t, remeshProviders, "meshy-processing")
	if processing["name"] != "Meshy Remesh" {
		t.Fatalf("processing name = %#v", processing["name"])
	}
	animationParameters := legacyProviderParameters(t, processing, "animation")
	if len(animationParameters) != 0 {
		t.Fatalf("legacy animation parameters = %#v", animationParameters)
	}
	remeshParameters := legacyProviderParameters(t, processing, "remesh")
	for _, omitted := range []string{
		"resize_height",
		"auto_size",
		"convert_format_only",
	} {
		for _, item := range remeshParameters {
			parameter, itemOK := item.(map[string]any)
			if itemOK && parameter["name"] == omitted {
				t.Fatalf("legacy remesh leaked %q: %#v", omitted, remeshParameters)
			}
		}
	}
	retextureParameters := legacyProviderParameters(t, processing, "retexture")
	prompt := legacyMapByStringField(t, retextureParameters, "name", "prompt")
	if prompt["required"] != true {
		t.Fatalf("retexture prompt required = %#v", prompt)
	}
	// Meshy Retexture documents no negative_prompt (#329).
	for _, item := range retextureParameters {
		if parameter, itemOK := item.(map[string]any); itemOK && parameter["name"] == "negative_prompt" {
			t.Fatalf("legacy retexture lists negative_prompt: %#v", retextureParameters)
		}
	}

	imageTo3D := legacyFilteredProviderList(
		t,
		serveLegacyRequest(
			handler,
			http.MethodGet,
			"/api/providers?capability=image-to-3d",
			nil,
		),
	)
	meshyImageTo3D := legacyProviderByID(t, imageTo3D, "meshy-7.1")
	imageConstraints := legacyProviderConstraints(
		t,
		meshyImageTo3D,
		"image-to-3d",
	)
	if imageConstraints["promptMaxLength"] != float64(600) {
		t.Fatalf("meshy image-to-3d constraints = %#v", imageConstraints)
	}

	rigging := legacyFilteredProviderList(
		t,
		serveLegacyRequest(
			handler,
			http.MethodGet,
			"/api/providers?capability=rigging",
			nil,
		),
	)
	riggingProcessing := legacyProviderByID(t, rigging, "meshy-processing")
	riggingConstraints := legacyProviderConstraints(
		t,
		riggingProcessing,
		"rigging",
	)
	if riggingConstraints["polycountMin"] != float64(10000) ||
		riggingConstraints["polycountMax"] != float64(300000) {
		t.Fatalf("rigging constraints = %#v", riggingConstraints)
	}

	background := legacyFilteredProviderList(
		t,
		serveLegacyRequest(
			handler,
			http.MethodGet,
			"/api/providers?capability=background-removal",
			nil,
		),
	)
	matting := legacyProviderByID(t, background, "matting-default")
	backgroundConstraints := legacyProviderConstraints(
		t,
		matting,
		"background-removal",
	)
	if _, exists := backgroundConstraints["referenceImageMaxSize"]; exists {
		t.Fatalf("background constraints = %#v", backgroundConstraints)
	}
}

func reconcileLegacyTask(
	t *testing.T,
	module *generation.Module,
	taskID generation.TaskID,
	workerID string,
) {
	t.Helper()
	for range 8 {
		if _, err := module.ReconcileOnce(context.Background(), generation.CoordinatorConfig{
			WorkerID:  workerID,
			LeaseFor:  time.Minute,
			BatchSize: 10,
		}); err != nil {
			t.Fatal(err)
		}
		task, err := module.Get(context.Background(), "owner-a", taskID)
		if err != nil {
			t.Fatal(err)
		}
		if task.State.Terminal() {
			if task.State != generation.TaskSucceeded {
				t.Fatalf("task = %#v", task)
			}
			return
		}
		time.Sleep(12 * time.Millisecond)
	}
	task, _ := module.Get(context.Background(), "owner-a", taskID)
	t.Fatalf("task did not reach a terminal state: state=%s failure=%#v materialize=%d", task.State, task.Failure, task.MaterializeFailures)
}

func legacyProviderList(
	t *testing.T,
	response *httptest.ResponseRecorder,
) []map[string]any {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var providers []map[string]any
	if err := json.NewDecoder(response.Body).Decode(&providers); err != nil {
		t.Fatal(err)
	}
	return providers
}

func legacyFilteredProviderList(
	t *testing.T,
	response *httptest.ResponseRecorder,
) []map[string]any {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Providers []map[string]any `json:"providers"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.Providers
}

func legacyProviderByID(
	t *testing.T,
	providers []map[string]any,
	modelID string,
) map[string]any {
	t.Helper()
	for _, provider := range providers {
		if provider["id"] == modelID {
			return provider
		}
	}
	t.Fatalf("provider %q not found: %#v", modelID, providers)
	return nil
}

func legacyProviderParameters(
	t *testing.T,
	provider map[string]any,
	capability string,
) []any {
	t.Helper()
	schemas, ok := provider["paramSchemas"].(map[string]any)
	if !ok {
		t.Fatalf("parameter schemas = %#v", provider["paramSchemas"])
	}
	parameters, ok := schemas[capability].([]any)
	if !ok {
		t.Fatalf("parameters %q = %#v", capability, schemas[capability])
	}
	return parameters
}

func legacyProviderConstraints(
	t *testing.T,
	provider map[string]any,
	capability string,
) map[string]any {
	t.Helper()
	constraints, ok := provider["constraints"].(map[string]any)
	if !ok {
		t.Fatalf("constraints = %#v", provider["constraints"])
	}
	selected, ok := constraints[capability].(map[string]any)
	if !ok {
		t.Fatalf("constraints %q = %#v", capability, constraints[capability])
	}
	return selected
}

func legacyMapByStringField(
	t *testing.T,
	items []any,
	field string,
	value string,
) map[string]any {
	t.Helper()
	index := legacyMapIndexByStringField(t, items, field, value)
	item, ok := items[index].(map[string]any)
	if !ok {
		t.Fatalf("item %d = %#v", index, items[index])
	}
	return item
}

func legacyMapIndexByStringField(
	t *testing.T,
	items []any,
	field string,
	value string,
) int {
	t.Helper()
	for index, raw := range items {
		item, ok := raw.(map[string]any)
		if ok && item[field] == value {
			return index
		}
	}
	t.Fatalf("%s=%q not found: %#v", field, value, items)
	return -1
}

func serveLegacyRequest(
	handler http.Handler,
	method string,
	target string,
	body io.Reader,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, body)
	request.Header.Set("X-User-ID", "owner-a")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestImageExtendedDiscoveryPreservesNewParameterControls(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	response := serveLegacyRequest(handler, http.MethodGet, "/api/providers?capability=image-to-image&projection=extended", nil)
	var discovered struct {
		Providers []struct {
			ID           string `json:"id"`
			ParamSchemas map[string][]struct {
				Name       string               `json:"name"`
				Type       string               `json:"type"`
				Validation generation.Parameter `json:"validation"`
			} `json:"paramSchemas"`
		}
	}
	if err := json.Unmarshal(response.Body.Bytes(), &discovered); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, provider := range discovered.Providers {
		for _, p := range provider.ParamSchemas["image-to-image"] {
			switch provider.ID + ":" + p.Name {
			case "gpt-image-2.5-sunburst:size":
				if p.Type != "text" || p.Validation.ImageSize == nil {
					t.Fatalf("size projection=%#v", p)
				}
				found["size"] = true
			case "gpt-image-2.5-sunburst:n":
				if p.Type != "number" || !p.Validation.Integer {
					t.Fatalf("n projection=%#v", p)
				}
				found["n"] = true
			case "gpt-image-2.5-sunburst:mask":
				if p.Type != "asset" || p.Validation.MaxBytes <= 0 {
					t.Fatalf("mask projection=%#v", p)
				}
				found["mask"] = true
			case "gemini-3-pro-image:aspect_ratio":
				if p.Type != "select" {
					t.Fatalf("ratio projection=%#v", p)
				}
				found["ratio"] = true
			case "gemini-3-pro-image:output_format", "gemini-3-pro-image:search_grounding":
				t.Fatalf("unsupported Interactions control projected: %s", p.Name)
			}
		}
	}
	if len(found) != 4 {
		t.Fatalf("missing projected controls: %#v", found)
	}
}
