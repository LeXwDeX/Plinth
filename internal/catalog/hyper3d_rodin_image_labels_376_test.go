package catalog

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// #376: Rodin Gen-2.5 image-to-3D takes image_label next to its pictures, one
// mark per picture by position. The Offering declares it as a hidden
// string-array with the reference-image-labels semantic. It is the platform's
// wire parameter, not a setting: nobody picks it on the panel, and it makes the
// Offering no multi-view service.

const rodinImageOffering = schemacontract.OfferingID("hyper3d/rodin-gen-2.5/image-to-3d")

func rodinImageParameters(t *testing.T) (offering schemacontract.Offering, labels, pictures schemacontract.Parameter, labelsIndex, picturesIndex int) {
	t.Helper()
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := product.Get(rodinImageOffering)
	if !ok {
		t.Fatalf("missing Offering %s", rodinImageOffering)
	}
	labelsIndex, picturesIndex = -1, -1
	for index, parameter := range offering.Parameters {
		switch parameter.Name {
		case "reference_image_labels":
			labels, labelsIndex = parameter, index
		case "reference_assets":
			pictures, picturesIndex = parameter, index
		}
	}
	if labelsIndex < 0 || picturesIndex < 0 {
		t.Fatalf("%s lacks reference_image_labels (%d) or reference_assets (%d)", rodinImageOffering, labelsIndex, picturesIndex)
	}
	return offering, labels, pictures, labelsIndex, picturesIndex
}

func TestRodinImageLabelsAreADeclaredHiddenPositionalList(t *testing.T) {
	offering, labels, pictures, labelsIndex, picturesIndex := rodinImageParameters(t)
	if offering.Revision != "2026-11-20.376.1" {
		t.Errorf("revision = %q: the new parameter is a new Offering revision", offering.Revision)
	}
	// Declaration order is wire order (imagewire): the marks come before the pictures.
	if labelsIndex >= picturesIndex {
		t.Errorf("labels are parameter %d, pictures %d: the marks must be declared first", labelsIndex, picturesIndex)
	}
	if labels.Type != "string-array" || labels.WireName != "image_label" || labels.Semantic != "reference-image-labels" {
		t.Errorf("labels = %+v", labels)
	}
	// One mark per picture, so the list is as long as the pictures can be; it
	// repeats values (two pictures can both be "?") and is never defaulted, so a
	// request without marks sends none.
	if labels.MaxItems != pictures.MaxItems || labels.MaxItems != 5 || !labels.AllowDuplicateValues ||
		labels.Required || labels.Fixed || labels.MinItems != 0 || len(labels.Default) != 0 {
		t.Errorf("labels shape = max %d (pictures %d), duplicates %v, required %v, fixed %v, min %d, default %s",
			labels.MaxItems, pictures.MaxItems, labels.AllowDuplicateValues, labels.Required, labels.Fixed, labels.MinItems, labels.Default)
	}
	if len(labels.WireValues) != 0 || len(labels.WireTemplate) != 0 || len(labels.OmitWhen) != 0 || len(labels.ForcedWhen) != 0 {
		t.Errorf("labels must be sent as they are: %+v", labels)
	}
	var values []string
	for _, option := range labels.Options {
		values = append(values, option.Value)
		if strings.TrimSpace(option.Label) == "" {
			t.Errorf("option %q has no label", option.Value)
		}
	}
	if want := []string{"F", "FL", "FR", "B", "BL", "BR", "L", "R", "U", "D", "?"}; !slices.Equal(values, want) {
		t.Errorf("options = %v, want the Gen-2.5 page's list %v", values, want)
	}
	// Hidden: no panel renders it. The public copy names the parameter by its
	// label, never by the wire field.
	if labels.Presentation == nil || labels.Presentation.Surface != schemacontract.SurfaceHidden {
		t.Errorf("presentation = %+v, want surface hidden", labels.Presentation)
	}
	for _, text := range []string{labels.Label, labels.Description} {
		if strings.TrimSpace(text) == "" || strings.Contains(text, "image_label") || strings.Contains(text, "reference_image_labels") {
			t.Errorf("copy %q is empty or names a wire field", text)
		}
	}
	// The sibling text-to-3D Offering has no pictures to mark.
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	text, ok := product.Get("hyper3d/rodin-gen-2.5/text-to-3d")
	if !ok {
		t.Fatal("missing the text-to-3D Offering")
	}
	for _, parameter := range text.Parameters {
		if parameter.Semantic == schemacontract.SemanticReferenceImageLabels || parameter.WireName == "image_label" {
			t.Errorf("text-to-3D declares %+v", parameter)
		}
	}
}

// A hidden platform parameter is nobody's setting: not the person's panel, not
// the Agent's set_params or list_offerings, and not a step input either.
func TestRodinImageLabelsAreNobodysSetting(t *testing.T) {
	offering, labels, _, _, _ := rodinImageParameters(t)
	if offering.UserParameterAllowed(labels) || offering.AgentParameterAllowed(labels) || offering.OverrideParameterAllowed(labels) {
		t.Error("the marks are a setting the person, the Agent or a purpose can change")
	}
	if schemacontract.StepInputParameter(labels) {
		t.Error("the marks are an ordinary step input")
	}
	if labels.AdjustmentPolicy != "" {
		t.Errorf("adjustment policy = %q", labels.AdjustmentPolicy)
	}
}

// The new semantic is not reference-views: Rodin keeps taking its pictures as
// one plain reference set, so it stays out of every multi-view path (the tool's
// model choices, the card, the upload panel).
func TestRodinImageLabelsDoNotMakeRodinAMultiviewService(t *testing.T) {
	offering, _, _, _, _ := rodinImageParameters(t)
	if offeringTakesSeveralViews(offering) {
		t.Fatal("Rodin takes several views: the marks made it a multi-view Offering")
	}
	for _, parameter := range offering.Parameters {
		if parameter.Semantic == schemacontract.SemanticReferenceViews || parameter.Name == "reference_views" {
			t.Errorf("Rodin declares the multi-view parameter %+v", parameter)
		}
	}
	if !schemacontract.SemanticReferenceImageLabels.Valid() || schemacontract.SemanticReferenceImageLabels == schemacontract.SemanticReferenceViews {
		t.Error("reference-image-labels is not its own valid semantic")
	}
	if schemacontract.SemanticReferenceImageLabels.SkillSettable() {
		t.Error("a Skill may override the marks, which only the platform sets")
	}
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var fromViews, fromImage ChatToolSchema
	for _, candidate := range product.Application().Chat.Tools {
		switch candidate.Name {
		case "create_model_3d_from_views":
			fromViews = candidate
		case "create_model_3d_from_image":
			fromImage = candidate
		}
	}
	if fromViews.Name == "" || fromImage.Name == "" {
		t.Fatal("3D tools are not declared")
	}
	if slices.Contains(DerivedModelChoices(fromViews, product.List(""), nil), "rodin-gen-2.5") {
		t.Error("the multi-view tool selects Rodin")
	}
	if !slices.Contains(DerivedModelChoices(fromImage, product.List(""), nil), "rodin-gen-2.5") {
		t.Error("the single-picture 3D tool lost Rodin")
	}
}

// rodinNormalized normalizes input for the Rodin image Offering as a request
// would, and reads the frozen input back.
func rodinNormalized(t *testing.T, input string) (map[string]any, error) {
	t.Helper()
	offering, _, _, _, _ := rodinImageParameters(t)
	normalized, err := normalizeInput(offering, json.RawMessage(input))
	if err != nil {
		return nil, err
	}
	var values map[string]any
	if err := json.Unmarshal(normalized, &values); err != nil {
		t.Fatal(err)
	}
	return values, nil
}

// The marks keep their order and repeat; nothing is added when they are not
// given; an unlisted mark and too many marks are refused where the input is
// normalized, before anything is paid.
func TestRodinImageLabelsNormalization(t *testing.T) {
	const base = `"prompt":"a robot","authorize_extra_credits":true,"reference_assets":["a","b","c"]`
	values, err := rodinNormalized(t, `{`+base+`,"reference_image_labels":["?","F","?"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := json.Marshal(values["reference_image_labels"]); string(got) != `["?","F","?"]` {
		t.Errorf("marks = %s, want the given order with the repeat", got)
	}
	values, err = rodinNormalized(t, `{`+base+`}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := values["reference_image_labels"]; present {
		t.Errorf("marks appeared without being given: %v", values["reference_image_labels"])
	}
	if _, err := rodinNormalized(t, `{`+base+`,"reference_image_labels":[]}`); err != nil {
		t.Errorf("an empty list is refused: %v", err)
	}
	for name, input := range map[string]string{
		"an unlisted mark":       `{` + base + `,"reference_image_labels":["F","X"]}`,
		"a lowercase mark":       `{` + base + `,"reference_image_labels":["f"]}`,
		"six marks":              `{` + base + `,"reference_image_labels":["F","F","F","F","F","F"]}`,
		"an empty mark":          `{` + base + `,"reference_image_labels":[""]}`,
		"a mark that is no text": `{` + base + `,"reference_image_labels":[1]}`,
		"a text, not a list":     `{` + base + `,"reference_image_labels":"F"}`,
	} {
		if _, err := rodinNormalized(t, input); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// allow_duplicate_values is the Schema's opt-in for a positional list. A
// string-array is a set unless it says otherwise, so every other multi-select
// still refuses a repeated value, and only a string-array may declare it.
func TestAllowDuplicateValuesIsAnOptInForStringArrays(t *testing.T) {
	set := schemacontract.Parameter{
		Name: "formats", Type: "string-array", MaxItems: 3,
		Options: []schemacontract.ParameterOption{{Value: "glb", Label: "GLB"}, {Value: "fbx", Label: "FBX"}},
	}
	if _, err := validateValue(set, []any{"glb", "glb"}); err == nil || !strings.Contains(err.Error(), "不能重复") {
		t.Fatalf("a repeated value in a plain string-array = %v, want a refusal", err)
	}
	if _, err := validateValue(set, []any{"glb", "fbx"}); err != nil {
		t.Fatalf("distinct values: %v", err)
	}
	positional := set
	positional.AllowDuplicateValues = true
	got, err := validateValue(positional, []any{"glb", "glb", "fbx"})
	if err != nil || !slices.Equal(got.([]string), []string{"glb", "glb", "fbx"}) {
		t.Fatalf("a positional list = %v, %v", got, err)
	}
	// The other limits still apply to it.
	if _, err := validateValue(positional, []any{"glb", "glb", "glb", "glb"}); err == nil {
		t.Error("max_items no longer limits a positional list")
	}
	if _, err := validateValue(positional, []any{"glb", "obj"}); err == nil {
		t.Error("an undeclared option passed a positional list")
	}
	// required_items still sees a value that repeats.
	required := positional
	required.RequiredItems = []string{"fbx"}
	if _, err := validateValue(required, []any{"glb", "glb"}); err == nil {
		t.Error("required_items lost its check")
	}
	if _, err := validateValue(required, []any{"fbx", "fbx"}); err != nil {
		t.Errorf("required_items with a repeat: %v", err)
	}
	for _, kind := range []string{"text", "select", "number", "boolean", "asset", "asset-array"} {
		parameter := schemacontract.Parameter{Name: "wrong", Type: kind, AllowDuplicateValues: true}
		if _, err := validateValueDefinition(parameter); err == nil || !strings.Contains(err.Error(), "allow_duplicate_values") {
			t.Errorf("%s declaring allow_duplicate_values = %v, want a refusal naming it", kind, err)
		}
	}
	if _, err := validateValueDefinition(positional); err != nil {
		t.Errorf("a string-array declaring it was refused: %v", err)
	}
}
