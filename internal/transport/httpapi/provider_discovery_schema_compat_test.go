package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestLegacyProviderDiscoveryWireContractComesFromProductSchema(
	t *testing.T,
) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithApplication(
		t,
		[]string{"admin"},
		func(application *catalog.ApplicationSchema) {
			application.Runtime.HTTP.LegacyRoutes.Providers =
				"/configured/provider-catalog"
			api := &application.ProviderDiscovery.API
			api.Query.Capability = "goal"
			api.Query.Domain = "family"
			api.Query.Projection = "view"
			api.ProjectionValues.Extended = "complete"
			api.EnvelopeFields.Providers = "entries"
			api.EnvelopeFields.DefaultModel = "selected"
			api.ProviderFields.ID = "model_id"
			api.ProviderFields.Name = "display_name"
			api.ProviderFields.Capabilities = "goals"
			api.ProviderFields.Domain = "family"
			api.ProviderFields.Features = "traits"
			api.ProviderFields.ParameterSchemas = "forms"
			api.ProviderFields.InputSchemas = "inputs"
			api.ProviderFields.Constraints = "limits"
			api.ProviderFields.Recommended = "preferred"
			api.ProviderFields.EstimatedCredits = "credits"
			api.ProviderFields.ProviderKey = "source"
			api.ParameterFields.Name = "parameter_id"
			api.ParameterFields.Label = "display_label"
			api.ParameterFields.Type = "control"
			api.ParameterFields.Options = "choices"
			api.ParameterFields.Default = "initial"
			api.ParameterFields.Required = "mandatory"
			api.ParameterFields.Minimum = "minimum"
			api.ParameterFields.Maximum = "maximum"
			api.ParameterFields.Step = "increment"
			api.ParameterFields.Description = "help"
			api.ParameterFields.DisabledWhen = "disabled_if"
			api.ParameterFields.Notices = "hints"
			api.OptionFields.Value = "choice_id"
			api.OptionFields.Label = "choice_label"
			api.OptionFields.Group = "choice_group"
			api.ConditionFields.Parameter = "depends_on"
			api.ConditionFields.Operator = "comparison"
			api.ConditionFields.Value = "expected"
			api.NoticeFields.When = "condition"
			api.NoticeFields.MessageKey = "copy_key"
			api.NoticeFields.Tone = "severity"
			api.InputFields.Name = "input_id"
			api.InputFields.Label = "input_label"
			api.InputFields.Type = "input_type"
			api.InputFields.MediaTypes = "media"
			api.InputFields.Extensions = "suffixes"
			api.InputFields.MaxBytes = "byte_limit"
			api.InputFields.MinItems = "minimum_items"
			api.InputFields.MaxItems = "maximum_items"
			api.ConstraintFields.PromptMaxLength = "prompt_limit"
			api.ConstraintFields.NegativePromptMaxLength =
				"negative_prompt_limit"
			api.ConstraintFields.ReferenceImageMin = "reference_minimum"
			api.ConstraintFields.ReferenceImageMax = "reference_maximum"
			api.ConstraintFields.ReferenceImageFormats =
				"reference_suffixes"
			api.ConstraintFields.ReferenceImageMaxSize =
				"reference_byte_limit"
			api.ConstraintFields.PolycountMin = "polycount_minimum"
			api.ConstraintFields.PolycountMax = "polycount_maximum"
			api.ConstraintFields.OutputSizes = "output_sizes"
		},
	)

	request := httptest.NewRequest(
		http.MethodGet,
		"/configured/provider-catalog?goal=text-to-image&view=complete",
		nil,
	)
	request.Header.Set("X-User-ID", "admin-user")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	entries, ok := body["entries"].([]any)
	if !ok || len(entries) == 0 {
		t.Fatalf("configured envelope = %#v", body)
	}
	if _, ok := body["selected"].(string); !ok {
		t.Fatalf("configured selected model missing: %#v", body)
	}
	entry, ok := entries[0].(map[string]any)
	if !ok ||
		entry["model_id"] == "" ||
		entry["display_name"] == "" ||
		entry["family"] != "image" ||
		entry["credits"] == nil {
		t.Fatalf("configured provider = %#v", entries[0])
	}
	forms, ok := entry["forms"].(map[string]any)
	if !ok {
		t.Fatalf("configured forms = %#v", entry)
	}
	parameters, ok := forms["text-to-image"].([]any)
	if !ok || len(parameters) == 0 {
		t.Fatalf("configured parameters = %#v", forms)
	}
	parameter, ok := parameters[0].(map[string]any)
	if !ok ||
		parameter["parameter_id"] == "" ||
		parameter["display_label"] == "" ||
		parameter["control"] == "" {
		t.Fatalf("configured parameter = %#v", parameters[0])
	}
	choices, ok := parameter["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Fatalf("configured choices = %#v", parameter)
	}
	choice, ok := choices[0].(map[string]any)
	if !ok ||
		choice["choice_id"] == nil ||
		choice["choice_label"] == nil {
		t.Fatalf("configured choice = %#v", choices[0])
	}
	disabledIf, ok := parameter["disabled_if"].([]any)
	if !ok || len(disabledIf) == 0 {
		t.Fatalf("configured disabled condition = %#v", parameter)
	}
	condition, ok := disabledIf[0].(map[string]any)
	if !ok ||
		condition["depends_on"] == nil ||
		condition["comparison"] == nil ||
		condition["expected"] == nil {
		t.Fatalf("configured condition = %#v", disabledIf[0])
	}
	limits, ok := entry["limits"].(map[string]any)
	if !ok {
		t.Fatalf("configured limits = %#v", entry)
	}
	textToImageLimits, ok := limits["text-to-image"].(map[string]any)
	if !ok || textToImageLimits["prompt_limit"] == nil {
		t.Fatalf("configured prompt limit = %#v", limits)
	}
	for _, leaked := range []string{
		"providers",
		"defaultModel",
		"id",
		"name",
		"capabilities",
		"domain",
		"estimatedCredits",
		"providerKey",
		"paramSchemas",
	} {
		if _, exists := body[leaked]; exists {
			t.Fatalf("default envelope field %q leaked: %#v", leaked, body)
		}
		if _, exists := entry[leaked]; exists {
			t.Fatalf("default provider field %q leaked: %#v", leaked, entry)
		}
	}

	request = httptest.NewRequest(
		http.MethodGet,
		"/configured/provider-catalog?goal=image-to-3d&view=complete",
		nil,
	)
	request.Header.Set("X-User-ID", "admin-user")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf(
			"input status = %d, body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	body = map[string]any{}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	entries, ok = body["entries"].([]any)
	if !ok || len(entries) == 0 {
		t.Fatalf("configured input envelope = %#v", body)
	}
	entry, ok = entries[0].(map[string]any)
	if !ok {
		t.Fatalf("configured input provider = %#v", entries[0])
	}
	inputs, ok := entry["inputs"].(map[string]any)
	if !ok {
		t.Fatalf("configured inputs = %#v", entry)
	}
	imageInputs, ok := inputs["image-to-3d"].([]any)
	if !ok || len(imageInputs) == 0 {
		t.Fatalf("configured image inputs = %#v", inputs)
	}
	input, ok := imageInputs[0].(map[string]any)
	if !ok ||
		input["input_id"] == nil ||
		input["input_label"] == nil ||
		input["input_type"] == nil ||
		input["media"] == nil ||
		input["suffixes"] == nil ||
		input["byte_limit"] == nil {
		t.Fatalf("configured input = %#v", imageInputs[0])
	}
}
