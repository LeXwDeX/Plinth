package catalog

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// #376: the Agent marks which side each reference picture shows through the
// image-to-3D tool's view_labels argument. The tool declares the argument and
// the mark that fills a picture left out; which models take marks, and which
// marks, is the Offering's own declaration, and the Catalog checks the two
// against each other.

// imageTo3DTool finds the shipped tool and its index.
func imageTo3DTool(t *testing.T, product *Catalog) (int, ChatToolSchema) {
	t.Helper()
	for index, tool := range product.application.Chat.Tools {
		if tool.Name == "create_model_3d_from_image" {
			return index, tool
		}
	}
	t.Fatal("create_model_3d_from_image is not declared")
	return 0, ChatToolSchema{}
}

func TestImageTo3DToolDeclaresReferenceMarks(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	_, tool := imageTo3DTool(t, product)
	if tool.ReferenceImageLabelsArgument != "view_labels" || tool.ReferenceImageLabelUnknown != "?" {
		t.Fatalf("tool declares %q / %q", tool.ReferenceImageLabelsArgument, tool.ReferenceImageLabelUnknown)
	}
	// The argument is a map from a handle to a mark, optional, and no Offering
	// parameter: nothing maps it, so it can only reach the input through the
	// executor's binding to the final picture order.
	var parameters struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(parameters.Required, "view_labels") {
		t.Error("view_labels is required")
	}
	property, ok := referenceImageLabelsArgument(tool)
	if !ok || property.Type != "object" || property.AdditionalProperties.Type != "string" {
		t.Fatalf("view_labels = %+v", property)
	}
	if _, mapped := tool.ArgumentMappings["view_labels"]; mapped {
		t.Error("view_labels is mapped to an Offering parameter")
	}
	// The enum is what the Agent is told; the Rodin options are what the
	// executor checks. They are the same list in the same order.
	offering, ok := product.Get("hyper3d/rodin-gen-2.5/image-to-3d")
	if !ok {
		t.Fatal("missing the Rodin image Offering")
	}
	var options []string
	for _, parameter := range offering.Parameters {
		if parameter.Semantic == schemacontract.SemanticReferenceImageLabels {
			for _, option := range parameter.Options {
				options = append(options, option.Value)
			}
		}
	}
	if !slices.Equal(property.AdditionalProperties.Enum, options) || !slices.Contains(options, "?") {
		t.Errorf("view_labels enum %v differs from the Offering's options %v", property.AdditionalProperties.Enum, options)
	}
	var described struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(parameters.Properties["view_labels"], &described); err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{"handle", "?", "ignores it for any other", "never from its file name"} {
		if !strings.Contains(described.Description, word) {
			t.Errorf("view_labels description does not say %q", word)
		}
	}
	if !strings.Contains(tool.Description, "view_labels") {
		t.Error("the tool description does not tell the Agent about view_labels")
	}
	// Nothing else takes marks: the multi-view tool stays labelled by view keys,
	// and Rodin is still no multi-view candidate (the semantic is not reference-views).
	for _, candidate := range product.application.Chat.Tools {
		if candidate.Name != tool.Name && (candidate.ReferenceImageLabelsArgument != "" || candidate.ReferenceImageLabelUnknown != "") {
			t.Errorf("%s declares reference marks", candidate.Name)
		}
		if candidate.Name == "create_model_3d_from_views" && slices.Contains(DerivedModelChoices(candidate, product.List(""), nil), "rodin-gen-2.5") {
			t.Error("the multi-view tool selects Rodin")
		}
	}
	if !slices.Contains(DerivedModelChoices(tool, product.List(""), nil), "rodin-gen-2.5") {
		t.Error("the single-picture 3D tool lost Rodin")
	}
	context := product.application.Chat.Context
	if !strings.Contains(context.ReferenceLabelPictureTemplate, "{handle}") ||
		!strings.Contains(context.ReferenceLabelValueTemplate, "{handle}") || !strings.Contains(context.ReferenceLabelValueTemplate, "{marks}") {
		t.Errorf("refusal templates = %q / %q", context.ReferenceLabelPictureTemplate, context.ReferenceLabelValueTemplate)
	}
}

func TestReferenceImageLabelsAreValidatedSchemaDeclarations(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateApplicationSchema(product.Application()); err != nil {
		t.Fatalf("shipped schema: %v", err)
	}
	for name, test := range map[string]struct {
		break_ func(app *ApplicationSchema, tool *ChatToolSchema)
		want   string
	}{
		"argument is no tool parameter": {func(_ *ApplicationSchema, tool *ChatToolSchema) { tool.ReferenceImageLabelsArgument = "roles" }, "不是工具参数"},
		"argument is no object":         {func(_ *ApplicationSchema, tool *ChatToolSchema) { tool.ReferenceImageLabelsArgument = "anchors" }, "值为字符串的对象"},
		"argument carries numbers": {func(_ *ApplicationSchema, tool *ChatToolSchema) {
			var parameters map[string]any
			if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
				t.Fatal(err)
			}
			property := parameters["properties"].(map[string]any)["view_labels"].(map[string]any)
			property["additionalProperties"] = map[string]any{"type": "number"}
			tool.Parameters, _ = json.Marshal(parameters)
		}, "值为字符串的对象"},
		"argument is an Offering parameter": {func(_ *ApplicationSchema, tool *ChatToolSchema) {
			tool.ArgumentMappings = maps.Clone(tool.ArgumentMappings)
			tool.ArgumentMappings["view_labels"] = "reference-image-labels"
		}, "不能映射"},
		"no mark for a picture left out":   {func(_ *ApplicationSchema, tool *ChatToolSchema) { tool.ReferenceImageLabelUnknown = " " }, "reference_image_label_unknown"},
		"unknown mark without an argument": {func(_ *ApplicationSchema, tool *ChatToolSchema) { tool.ReferenceImageLabelsArgument = "" }, "reference_image_label_unknown"},
		"picture refusal without handle": {func(app *ApplicationSchema, _ *ChatToolSchema) {
			app.Chat.Context.ReferenceLabelPictureTemplate = "no"
		}, "占位符"},
		"value refusal without marks": {func(app *ApplicationSchema, _ *ChatToolSchema) {
			app.Chat.Context.ReferenceLabelValueTemplate = "{handle}"
		}, "占位符"},
	} {
		t.Run(name, func(t *testing.T) {
			broken := product.Application()
			broken.Chat.Tools = slices.Clone(broken.Chat.Tools)
			index, _ := imageTo3DTool(t, product)
			test.break_(&broken, &broken.Chat.Tools[index])
			err := ValidateApplicationSchema(broken)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want one naming %q", err, test.want)
			}
		})
	}
}

// Marks are the executor's binding of pictures: a tool that reads no pictures,
// or that processes an existing model, has nothing to mark.
func TestReferenceImageLabelsNeedAToolThatReadsPictures(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	_, tool := imageTo3DTool(t, product)
	context := product.application.Chat.Context
	if err := validateReferenceImageLabels(tool, true, context); err != nil {
		t.Fatalf("the shipped tool: %v", err)
	}
	if err := validateReferenceImageLabels(tool, false, context); err == nil || !strings.Contains(err.Error(), "读取参考图") {
		t.Errorf("a tool that reads no pictures: %v", err)
	}
	processing := tool
	processing.Processing = &ChatToolProcessingSchema{}
	if err := validateReferenceImageLabels(processing, true, context); err == nil || !strings.Contains(err.Error(), "读取参考图") {
		t.Errorf("a processing tool: %v", err)
	}
}

// The tool and the Offerings that take marks are checked together when the
// Catalog loads: an Offering must offer the mark that fills a picture left out
// and no mark the Agent is not told about, and a tool needs an Offering to use
// the argument with.
func TestReferenceImageLabelsAgreeWithTheOfferingsThatTakeThem(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	index, _ := imageTo3DTool(t, product)
	check := func(mutate func(tools []ChatToolSchema, offerings generationMap), want string) {
		t.Helper()
		tools := slices.Clone(product.application.Chat.Tools)
		offerings := maps.Clone(product.byID)
		mutate(tools, offerings)
		err := validateChatToolReferenceImageLabels(tools, offerings)
		switch {
		case want == "" && err != nil:
			t.Fatalf("a consistent declaration was refused: %v", err)
		case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
			t.Fatalf("error = %v, want one naming %q", err, want)
		}
	}
	// editMarks rewrites the Rodin Offering's mark parameter without touching
	// the shared Catalog: the Parameters slice is copied first.
	editMarks := func(offerings generationMap, edit func(parameter *schemacontract.Parameter)) {
		offering := offerings["hyper3d/rodin-gen-2.5/image-to-3d"]
		offering.Parameters = slices.Clone(offering.Parameters)
		for position := range offering.Parameters {
			if offering.Parameters[position].Semantic == schemacontract.SemanticReferenceImageLabels {
				offering.Parameters[position].Options = slices.Clone(offering.Parameters[position].Options)
				edit(&offering.Parameters[position])
			}
		}
		offerings[offering.ID] = offering
	}
	check(func([]ChatToolSchema, generationMap) {}, "")
	check(func(_ []ChatToolSchema, offerings generationMap) {
		editMarks(offerings, func(parameter *schemacontract.Parameter) {
			parameter.Options = slices.DeleteFunc(parameter.Options, func(option schemacontract.ParameterOption) bool { return option.Value == "?" })
		})
	}, `没有 "?"`)
	check(func(tools []ChatToolSchema, _ generationMap) {
		tools[index].Parameters = json.RawMessage(strings.Replace(string(tools[index].Parameters), `"U", "D", "?"`, `"D", "?"`, 1))
	}, `"U" 不在参数`)
	check(func(_ []ChatToolSchema, offerings generationMap) {
		editMarks(offerings, func(parameter *schemacontract.Parameter) { parameter.Semantic = "" })
	}, "没有 Offering 声明 reference-image-labels")
	// A tool without the argument is not checked, whatever the Offerings declare.
	check(func(tools []ChatToolSchema, offerings generationMap) {
		tools[index].ReferenceImageLabelsArgument, tools[index].ReferenceImageLabelUnknown = "", ""
		editMarks(offerings, func(parameter *schemacontract.Parameter) { parameter.Options = nil })
	}, "")
}
