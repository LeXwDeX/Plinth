package catalog

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// #129: model3d_usage.budget_limits says which parameter semantics a purpose's
// budget bounds; the purpose then gives a bounded parameter nobody chose a
// value within its budget.

func budgetTriangleLimit() ChatModel3DUsageBudgetLimit {
	return ChatModel3DUsageBudgetLimit{Semantic: schemacontract.SemanticPolycount, Bound: BudgetBoundMaxTriangles,
		ItemTemplate: "{model} {parameter} {value} {maximum}"}
}

func budgetTextureLimit() ChatModel3DUsageBudgetLimit {
	return ChatModel3DUsageBudgetLimit{Semantic: schemacontract.SemanticTextureSize, Bound: BudgetBoundMaxTextureSize,
		OptionValues: map[string]float64{"2k": 2048, "4k": 4096, "8k": 8192},
		ItemTemplate: "{model} {parameter} {value} {maximum}"}
}

func budgetTextureParameter(defaultValue string) schemacontract.Parameter {
	parameter := schemacontract.Parameter{Name: "texture_resolution", Type: "select", Semantic: schemacontract.SemanticTextureSize,
		Options: []schemacontract.ParameterOption{{Value: "2k"}, {Value: "4k"}, {Value: "8k"}}}
	if defaultValue != "" {
		parameter.Default = json.RawMessage(`"` + defaultValue + `"`)
	}
	return parameter
}

func TestBudgetLimitFitsAParameterNobodyChose(t *testing.T) {
	faceLimit := func(defaultValue string, minimum, maximum *float64) schemacontract.Parameter {
		parameter := schemacontract.Parameter{Name: "face_limit", Type: "number", Integer: true,
			Semantic: schemacontract.SemanticPolycount, Minimum: minimum, Maximum: maximum}
		if defaultValue != "" {
			parameter.Default = json.RawMessage(defaultValue)
		}
		return parameter
	}
	remesh := usageOffering()
	remesh.OutputTriangles = &schemacontract.OutputTriangleEstimate{CountParameter: "target_polycount",
		TopologyParameter: "topology", TrianglesPerFace: map[string]float64{"quad": 2, "triangle": 1}}
	remesh.Parameters = []schemacontract.Parameter{
		{Name: "topology", Type: "select", Default: json.RawMessage(`"quad"`)},
		{Name: "target_polycount", Type: "number", Semantic: schemacontract.SemanticPolycount,
			Default: json.RawMessage(`300000`), Minimum: usageNumber(100), Maximum: usageNumber(300000)},
	}
	tests := []struct {
		name      string
		limit     ChatModel3DUsageBudgetLimit
		offering  schemacontract.Offering
		parameter schemacontract.Parameter
		baseline  string
		values    map[string]json.RawMessage
		maximum   float64
		want      string
	}{
		{name: "a default over the budget is lowered to it", limit: budgetTriangleLimit(), offering: usageOffering(),
			parameter: faceLimit("2000000", usageNumber(1000), usageNumber(2000000)), baseline: "2000000", maximum: 20000, want: "20000"},
		{name: "a default within the budget stays", limit: budgetTriangleLimit(), offering: usageOffering(),
			parameter: faceLimit("15000", nil, usageNumber(30000)), baseline: "15000", maximum: 20000},
		// Without a default the upstream decides adaptively (Tripo P2's
		// low-poly face count): the budget leaves the parameter unset even when
		// its declared range goes past the budget.
		{name: "a parameter without a default stays adaptive", limit: budgetTriangleLimit(), offering: usageOffering(),
			parameter: faceLimit("", usageNumber(48), usageNumber(50000)), maximum: 20000},
		{name: "an unbounded parameter without a default stays adaptive", limit: budgetTriangleLimit(), offering: usageOffering(),
			parameter: faceLimit("", nil, nil), maximum: 5000},
		{name: "a budget below the minimum takes the minimum", limit: budgetTriangleLimit(), offering: usageOffering(),
			parameter: faceLimit("2000000", usageNumber(1000), usageNumber(2000000)), baseline: "2000000", maximum: 500, want: "1000"},
		{name: "a quad counts as two triangles", limit: budgetTriangleLimit(), offering: remesh,
			parameter: remesh.Parameters[1], baseline: "300000", maximum: 20000, want: "10000"},
		{name: "a triangle topology counts once", limit: budgetTriangleLimit(), offering: remesh,
			parameter: remesh.Parameters[1], baseline: "300000", values: map[string]json.RawMessage{"topology": json.RawMessage(`"triangle"`)},
			maximum: 20000, want: "20000"},
		{name: "a select takes its largest option within the budget", limit: budgetTextureLimit(), offering: usageOffering(),
			parameter: budgetTextureParameter("8k"), baseline: `"8k"`, maximum: 4096, want: `"4k"`},
		{name: "a select within the budget stays", limit: budgetTextureLimit(), offering: usageOffering(),
			parameter: budgetTextureParameter("2k"), baseline: `"2k"`, maximum: 2048},
		{name: "a select with no option within the budget takes the smallest", limit: budgetTextureLimit(), offering: usageOffering(),
			parameter: budgetTextureParameter("8k"), baseline: `"8k"`, maximum: 1024, want: `"2k"`},
		{name: "a select without a default stays open", limit: budgetTextureLimit(), offering: usageOffering(),
			parameter: budgetTextureParameter(""), maximum: 2048},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var baseline json.RawMessage
			if test.baseline != "" {
				baseline = json.RawMessage(test.baseline)
			}
			value, fitted := test.limit.Fit(test.offering, test.parameter, baseline, test.values, test.maximum)
			if test.want == "" {
				if fitted {
					t.Fatalf("Fit changed a value that fits: %s", value)
				}
				return
			}
			if !fitted || string(value) != test.want {
				t.Fatalf("Fit = %s (%v), want %s", value, fitted, test.want)
			}
		})
	}
}

// rodinLikeGenerationOffering reads like the shipped Rodin Gen-2.5 (#304): a
// 3D generation Offering whose face count is in quads or triangles by its
// topology parameter, with a factor that is not a whole number.
func rodinLikeGenerationOffering() schemacontract.Offering {
	offering := usageOffering(
		schemacontract.Parameter{Name: "mesh_mode", Type: "select", Default: json.RawMessage(`"Raw"`),
			Options: []schemacontract.ParameterOption{{Value: "Raw"}, {Value: "Quad"}}},
		schemacontract.Parameter{Name: "quality_override", Type: "number", Integer: true, Semantic: schemacontract.SemanticPolycount,
			Default: json.RawMessage(`2000000`), Minimum: usageNumber(500), Maximum: usageNumber(2000000)},
	)
	offering.OutputTriangles = &schemacontract.OutputTriangleEstimate{CountParameter: "quality_override", TopologyParameter: "mesh_mode",
		TrianglesPerFace: map[string]float64{"Raw": 1, "Quad": 2.2}}
	return offering
}

// A face count of a generation Offering that declares output_triangles is
// measured and fitted in triangles with its fractional factor (#304): the
// largest count whose converted amount stays inside the budget, rounded down,
// and an amount that is a whole triangle count however the binary product of a
// decimal factor falls.
func TestBudgetLimitConvertsAFractionalFactor(t *testing.T) {
	offering := rodinLikeGenerationOffering()
	count := offering.Parameters[1]
	limit := budgetTriangleLimit()
	mode := func(topology string) map[string]json.RawMessage {
		return map[string]json.RawMessage{"mesh_mode": json.RawMessage(`"` + topology + `"`)}
	}

	for name, test := range map[string]struct {
		value  string
		values map[string]json.RawMessage
		want   float64
	}{
		"the real sample":                                   {"10000", mode("Quad"), 22000},
		"a product with binary noise":                       {"3200", mode("Quad"), 7040}, // 3200 * 2.2 = 7040.000000000001
		"a whole product":                                   {"4000", mode("Quad"), 8800},
		"a fraction of a triangle rounds up":                {"9091", mode("Quad"), 20001}, // 20000.2
		"triangles count once":                              {"10000", mode("Raw"), 10000},
		"the default topology is triangles":                 {"10000", nil, 10000},
		"an unknown topology is read as the largest factor": {"10000", mode("Hexagon"), 22000},
	} {
		t.Run("measure "+name, func(t *testing.T) {
			got, measured := limit.Measure(offering, count, json.RawMessage(test.value), test.values)
			if !measured || got != test.want {
				t.Fatalf("Measure = %v (%v), want %v", got, measured, test.want)
			}
		})
	}
	// Only a triangle bound converts: a texture bound reads the number as it is.
	texture := ChatModel3DUsageBudgetLimit{Semantic: schemacontract.SemanticPolycount, Bound: BudgetBoundMaxTextureSize}
	if got, measured := texture.Measure(offering, count, json.RawMessage(`10000`), mode("Quad")); !measured || got != 10000 {
		t.Fatalf("a texture bound converted a face count: %v (%v)", got, measured)
	}
	// So does an Offering that declares no estimate.
	plain := usageOffering(count)
	if got, measured := limit.Measure(plain, count, json.RawMessage(`10000`), mode("Quad")); !measured || got != 10000 {
		t.Fatalf("an Offering without an estimate converted a face count: %v (%v)", got, measured)
	}

	for name, test := range map[string]struct {
		baseline string
		values   map[string]json.RawMessage
		maximum  float64
		want     string // empty: the baseline fits and stays
	}{
		"a phone character":                    {"200000", mode("Quad"), 20000, "9090"},   // 9090 * 2.2 = 19998
		"a phone prop":                         {"200000", mode("Quad"), 8000, "3636"},    // 7999.2
		"a PC character":                       {"200000", mode("Quad"), 100000, "45454"}, // 99998.8
		"a PC prop":                            {"200000", mode("Quad"), 40000, "18181"},  // 39998.2
		"a whole boundary with binary noise":   {"200000", mode("Quad"), 22000, "10000"},
		"another whole boundary":               {"200000", mode("Quad"), 6600, "3000"},
		"triangles keep the budget one to one": {"2000000", mode("Raw"), 20000, "20000"},
		"the default topology is triangles":    {"2000000", nil, 20000, "20000"},
		"a count one face over the boundary":   {"9091", mode("Quad"), 20000, "9090"}, // 20000.2 rounds up to 20001
		"a count on the boundary stays":        {"10000", mode("Quad"), 22000, ""},
		"a count within the budget stays":      {"8000", mode("Quad"), 20000, ""}, // 17600
		"a budget under the minimum":           {"200000", mode("Quad"), 600, "500"},
	} {
		t.Run("fit "+name, func(t *testing.T) {
			value, fitted := limit.Fit(offering, count, json.RawMessage(test.baseline), test.values, test.maximum)
			if test.want == "" {
				if fitted {
					t.Fatalf("Fit changed a value within the budget: %s", value)
				}
				return
			}
			if !fitted || string(value) != test.want {
				t.Fatalf("Fit = %s (%v), want %s", value, fitted, test.want)
			}
			// A fitted count is inside the budget and one face more is not (the
			// minimum aside).
			if amount, _ := limit.Measure(offering, count, value, test.values); amount > test.maximum && test.want != "500" {
				t.Fatalf("fitted count %s is %v triangles, over the %v budget", value, amount, test.maximum)
			}
			var faces float64
			if err := json.Unmarshal(value, &faces); err != nil {
				t.Fatal(err)
			}
			if amount, _ := limit.Measure(offering, count, json.RawMessage(strconv.FormatFloat(faces+1, 'f', -1, 64)), test.values); amount <= test.maximum && test.want != "500" {
				t.Fatalf("fitted count %s is not the largest: %v faces still fit %v", value, faces+1, test.maximum)
			}
		})
	}
}

// The declaration is checked as the loader checks it, on values the JSON
// loader cannot produce too (#304): a factor is positive and finite, covers
// every option of the topology parameter and nothing else, and a 3D
// generation Offering may declare it as well as a processing one.
func TestOutputTriangleDeclarationValidation(t *testing.T) {
	declared := func(domain schemacontract.CapabilityDomain, change func(*schemacontract.Offering)) schemacontract.Offering {
		offering := rodinLikeGenerationOffering()
		offering.Domain = domain
		offering.ExecutionMessages.PlanSourceFaceLimit = "{source_service} {service} {estimate} {limit} {choices}"
		if change != nil {
			change(&offering)
		}
		return offering
	}
	factors := func(offering *schemacontract.Offering) map[string]float64 {
		return offering.OutputTriangles.TrianglesPerFace
	}
	for name, offering := range map[string]schemacontract.Offering{
		"a decimal factor on a generation Offering":  declared(schemacontract.DomainModel3D, nil),
		"a decimal factor on a processing Offering":  declared(schemacontract.DomainProcessing, nil),
		"a whole factor on a generation Offering":    declared(schemacontract.DomainModel3D, func(o *schemacontract.Offering) { factors(o)["Quad"] = 2 }),
		"a factor below one is still a positive one": declared(schemacontract.DomainModel3D, func(o *schemacontract.Offering) { factors(o)["Quad"] = 0.5 }),
	} {
		if err := validateOfferingModelLimits(offering); err != nil {
			t.Errorf("%s was refused: %v", name, err)
		}
	}
	for name, test := range map[string]struct {
		offering schemacontract.Offering
		want     string
	}{
		"zero":         {declared(schemacontract.DomainModel3D, func(o *schemacontract.Offering) { factors(o)["Quad"] = 0 }), "正的有限数"},
		"negative":     {declared(schemacontract.DomainModel3D, func(o *schemacontract.Offering) { factors(o)["Quad"] = -2.2 }), "正的有限数"},
		"not a number": {declared(schemacontract.DomainModel3D, func(o *schemacontract.Offering) { factors(o)["Quad"] = math.NaN() }), "正的有限数"},
		"infinite":     {declared(schemacontract.DomainModel3D, func(o *schemacontract.Offering) { factors(o)["Quad"] = math.Inf(1) }), "正的有限数"},
		"minus infinite": {declared(schemacontract.DomainModel3D, func(o *schemacontract.Offering) {
			factors(o)["Raw"] = math.Inf(-1)
		}), "正的有限数"},
		"a topology option left out": {declared(schemacontract.DomainModel3D, func(o *schemacontract.Offering) { delete(factors(o), "Raw") }), "覆盖拓扑参数的每个选项"},
		"a factor for no option":     {declared(schemacontract.DomainModel3D, func(o *schemacontract.Offering) { factors(o)["Hexagon"] = 4 }), "覆盖拓扑参数的每个选项"},
		"an image Offering":          {declared(schemacontract.DomainImage, nil), "3D 生成与处理能力"},
		"no refusal text": {declared(schemacontract.DomainModel3D, func(o *schemacontract.Offering) {
			o.ExecutionMessages.PlanSourceFaceLimit = ""
		}), "plan_source_face_limit"},
	} {
		err := validateOfferingModelLimits(test.offering)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: error = %v, want mention of %q", name, err, test.want)
		}
	}
}

func TestBudgetLimitsRejectInvalidDeclarations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ChatModel3DUsageSchema)
		want   string
	}{
		{name: "an unknown semantic", mutate: func(u *ChatModel3DUsageSchema) {
			u.BudgetLimits[0].Semantic = "texture-resolution"
		}, want: "semantic"},
		{name: "a duplicate semantic", mutate: func(u *ChatModel3DUsageSchema) {
			u.BudgetLimits[1].Semantic = u.BudgetLimits[0].Semantic
		}, want: "重复"},
		{name: "an unknown bound", mutate: func(u *ChatModel3DUsageSchema) {
			u.BudgetLimits[0].Bound = "max_vertices"
		}, want: "bound"},
		{name: "an option without a positive amount", mutate: func(u *ChatModel3DUsageSchema) {
			u.BudgetLimits[1].OptionValues = map[string]float64{"2k": 0}
		}, want: "option_values"},
		{name: "an item without the maximum", mutate: func(u *ChatModel3DUsageSchema) {
			u.BudgetLimits[0].ItemTemplate = "{model} {parameter} {value}"
		}, want: "item_template"},
		{name: "a note without the items", mutate: func(u *ChatModel3DUsageSchema) {
			u.BudgetNote.Template = "over budget"
		}, want: "budget_note.template"},
		{name: "a note without a separator", mutate: func(u *ChatModel3DUsageSchema) {
			u.BudgetNote.Separator = " "
		}, want: "separator"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			product, err := LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			application := product.Application()
			usage := &application.Chat.Model3DUsage
			usage.BudgetLimits = append([]ChatModel3DUsageBudgetLimit(nil), usage.BudgetLimits...)
			test.mutate(usage)
			err = ValidateApplicationSchema(application)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want mention of %q", err, test.want)
			}
		})
	}
}

func TestBudgetLimitsMustMeasureEveryBoundedParameterAndFitThePurpose(t *testing.T) {
	profile := ChatModel3DUsageProfile{
		ID: "p", Label: "P", Description: "d", Reason: "r",
		RecommendedOfferings: map[string][]string{"text-to-3d": {"test/model/text-to-3d"}},
		Budget:               ChatModel3DUsageBudget{MaxTriangles: 20000, MaxTextureSize: func() *int { size := 2048; return &size }()},
	}
	faceLimit := schemacontract.Parameter{Name: "face_limit", Type: "number", Integer: true,
		Semantic: schemacontract.SemanticPolycount, Minimum: usageNumber(100), Maximum: usageNumber(50000)}
	tests := []struct {
		name      string
		offering  schemacontract.Offering
		overrides map[string]json.RawMessage
		want      string
	}{
		{name: "a select option without an amount",
			offering: usageOffering(schemacontract.Parameter{Name: "texture_resolution", Type: "select",
				Semantic: schemacontract.SemanticTextureSize, Options: []schemacontract.ParameterOption{{Value: "2k"}, {Value: "16k"}}}),
			want: "option_values"},
		{name: "a parameter the limit cannot measure",
			offering: usageOffering(schemacontract.Parameter{Name: "face_limit", Type: "boolean", Semantic: schemacontract.SemanticPolycount}),
			want:     "不能约束"},
		{name: "an override over the purpose's own budget",
			offering:  usageOffering(faceLimit),
			overrides: map[string]json.RawMessage{"face_limit": json.RawMessage(`30000`)},
			want:      "超出用途预算"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := profile
			if test.overrides != nil {
				current.ParameterOverrides = map[string]map[string]json.RawMessage{string(test.offering.ID): test.overrides}
			}
			usage := ChatModel3DUsageSchema{DefaultProfile: "p", Profiles: []ChatModel3DUsageProfile{current},
				BudgetLimits: []ChatModel3DUsageBudgetLimit{budgetTriangleLimit(), budgetTextureLimit()}}
			err := validateModel3DUsageOfferings(usage, generationMap{test.offering.ID: test.offering}, "views")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want mention of %q", err, test.want)
			}
		})
	}
	within := profile
	within.ParameterOverrides = map[string]map[string]json.RawMessage{"test/model/text-to-3d": {"face_limit": json.RawMessage(`20000`)}}
	usage := ChatModel3DUsageSchema{DefaultProfile: "p", Profiles: []ChatModel3DUsageProfile{within},
		BudgetLimits: []ChatModel3DUsageBudgetLimit{budgetTriangleLimit(), budgetTextureLimit()}}
	if err := validateModel3DUsageOfferings(usage, generationMap{"test/model/text-to-3d": usageOffering(faceLimit)}, "views"); err != nil {
		t.Fatalf("an override within the budget was refused: %v", err)
	}
}

// The shipped Schema bounds the triangle count and the texture size, and every
// 3D texture resolution and remesh face count declares the semantic that lets
// the budget find it.
func TestShippedBudgetLimitsBoundTrianglesAndTextures(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	usage := product.Application().Chat.Model3DUsage
	for semantic, bound := range map[schemacontract.ParameterSemantic]string{
		schemacontract.SemanticPolycount:   BudgetBoundMaxTriangles,
		schemacontract.SemanticTextureSize: BudgetBoundMaxTextureSize,
	} {
		limit, declared := usage.BudgetLimit(semantic)
		if !declared || limit.Bound != bound {
			t.Fatalf("budget limit of %s = %+v (%v), want bound %s", semantic, limit, declared, bound)
		}
	}
	for _, id := range []schemacontract.OfferingID{
		"meshy/meshy-7.1/text-to-3d", "meshy/meshy-7.1/image-to-3d", "meshy/meshy-t2.5/image-to-3d",
		"meshy/meshy-7.1-multiview/image-to-3d", "meshy/processing/retexture",
	} {
		offering, found := product.Get(id)
		if !found {
			t.Fatalf("%s missing", id)
		}
		parameter, found := offeringParameter(offering.Parameters, "texture_resolution")
		if !found || parameter.Semantic != schemacontract.SemanticTextureSize || !offering.AgentParameterAllowed(parameter) {
			t.Fatalf("%s texture_resolution = %+v, want a public texture-size parameter", id, parameter)
		}
	}
	for _, id := range []schemacontract.OfferingID{
		"tripo/tripo-v3.1-full/text-to-3d", "tripo/tripo-p2/text-to-3d", "meshy/processing/remesh",
		"tripo/geometry/decimate-triangle", "tripo/geometry/decimate-quad",
	} {
		offering, found := product.Get(id)
		if !found {
			t.Fatalf("%s missing", id)
		}
		bounded := false
		for _, parameter := range offering.Parameters {
			bounded = bounded || (parameter.Semantic == schemacontract.SemanticPolycount && offering.AgentParameterAllowed(parameter))
		}
		if !bounded {
			t.Fatalf("%s declares no public polycount parameter", id)
		}
	}
}

// Meshy 7.1 ignores its face target unless it remeshes, so a target below the
// default forces the remesh that makes it effective.
func TestMeshyFaceTargetForcesTheRemesh(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		capability schemacontract.Capability
		id         schemacontract.OfferingID
		input      string
	}{
		{schemacontract.TextTo3D, "meshy/meshy-7.1/text-to-3d", `{"prompt":"crate"}`},
		{schemacontract.ImageTo3D, "meshy/meshy-7.1/image-to-3d", `{"reference_assets":["asset-1"]}`},
	} {
		for target, remesh := range map[string]bool{"": false, "300000": false, "20000": true} {
			input := test.input
			if target != "" {
				input = strings.TrimSuffix(input, "}") + `,"target_polycount":` + target + `}`
			}
			_, normalized, err := product.Normalize(test.capability, test.id, json.RawMessage(input))
			if err != nil {
				t.Fatalf("%s %s: %v", test.id, input, err)
			}
			var values map[string]any
			if err := json.Unmarshal(normalized, &values); err != nil || values["should_remesh"] != remesh {
				t.Fatalf("%s %s normalized = %s, want should_remesh %v", test.id, input, normalized, remesh)
			}
		}
	}
}
