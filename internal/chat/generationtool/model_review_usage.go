package generationtool

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modelinspect"
)

// The purpose check of inspect_model (#103): the production brief selects a
// purpose profile, the profile's budget and scale range say what the model is
// for, and the inspect report says whether the saved file meets it. Every
// check is pass, fail or unknown; unknown means the purpose does not ask for
// the property or the file does not contain enough data to judge it.
const (
	modelReviewUsageStatusPass    = "pass"
	modelReviewUsageStatusFail    = "fail"
	modelReviewUsageStatusUnknown = "unknown"

	modelReviewUsageSourceBrief   = "production_brief"
	modelReviewUsageSourceDefault = "default_profile"
)

// modelReviewUsageCheck is the usage_check object of an inspect_model result.
type modelReviewUsageCheck struct {
	Purpose       string                      `json:"purpose"`
	PurposeLabel  string                      `json:"purpose_label"`
	PurposeSource string                      `json:"purpose_source"`
	Note          string                      `json:"note,omitempty"`
	Checks        []modelReviewUsageCheckItem `json:"checks"`
}

// modelReviewUsageCheckItem is one judged property. Actual and Budget hold the
// measured value and the purpose's bound; FixHint is present only on a fail.
type modelReviewUsageCheckItem struct {
	Check   string         `json:"check"`
	Status  string         `json:"status"`
	Actual  any            `json:"actual"`
	Budget  map[string]any `json:"budget"`
	FixHint string         `json:"fix_hint,omitempty"`
}

// modelReviewUsageText localizes a Schema template and fills its placeholders.
type modelReviewUsageText func(template string, replacements ...string) string

// modelReviewFixAvailability reports whether an enabled, runtime-available
// Offering can perform one declared fix capability.
type modelReviewFixAvailability func(catalog.ChatModelInspectFixCapabilitySchema) bool

// newModelReviewUsageCheck judges a report against the purpose selected by the
// frozen production brief. A brief without a usable purpose falls back to the
// Schema's default profile; a brief triangle_budget overrides the profile's
// maximum. It returns nil when the Schema declares no usage profiles, which is
// how historical Catalogs stay valid.
func newModelReviewUsageCheck(
	report modelinspect.Report,
	usage catalog.ChatModel3DUsageSchema,
	production catalog.ChatModelInspectProductionSchema,
	brief chat.ProductionBrief,
	text modelReviewUsageText,
	available modelReviewFixAvailability,
) *modelReviewUsageCheck {
	profile, source, found := modelReviewUsageProfile(usage, brief)
	if !found {
		return nil
	}
	budget, override := modelReviewUsageBudget(usage, profile, brief)
	note := modelReviewUsageNote(profile, source, override, production, text)
	checks := []modelReviewUsageCheckItem{
		modelReviewUsageTrianglesCheck(report, budget),
		modelReviewUsageUVCheck(report, budget),
		modelReviewUsageWatertightCheck(report, budget),
		modelReviewUsageSkinCheck(report, budget),
		modelReviewUsageTextureSizeCheck(report, budget),
		modelReviewUsagePivotCheck(report, production),
		modelReviewUsageYUpCheck(report, production),
		modelReviewUsageScaleCheck(report, profile),
		modelReviewUsageDebrisCheck(report, production),
		modelReviewUsageDegenerateCheck(report, production),
	}
	fixes := make(map[string]catalog.ChatModelInspectFixSchema, len(production.Fixes))
	for _, fix := range production.Fixes {
		fixes[fix.Check] = fix
	}
	for index := range checks {
		if checks[index].Status != modelReviewUsageStatusFail {
			continue
		}
		fix, exists := fixes[checks[index].Check]
		if !exists {
			continue
		}
		checks[index].FixHint = modelReviewUsageFixHint(fix, profile, production, text, available)
	}
	return &modelReviewUsageCheck{
		Purpose:       profile.ID,
		PurposeLabel:  profile.Label,
		PurposeSource: source,
		Note:          note,
		Checks:        checks,
	}
}

// modelReviewUsageProfile resolves the purpose and where it came from. A brief
// purpose the Schema no longer declares is ignored, like a session without a
// brief, so a stale session cannot block a read-only inspection.
func modelReviewUsageProfile(
	usage catalog.ChatModel3DUsageSchema,
	brief chat.ProductionBrief,
) (catalog.ChatModel3DUsageProfile, string, bool) {
	if len(usage.Profiles) == 0 {
		return catalog.ChatModel3DUsageProfile{}, "", false
	}
	if !brief.Empty() {
		field := ""
		for _, declared := range usage.Brief.Fields {
			if declared.Type == "profile" {
				field = declared.Name
				break
			}
		}
		if field != "" {
			id := brief.Text(field)
			for _, profile := range usage.Profiles {
				if profile.ID == id {
					return profile, modelReviewUsageSourceBrief, true
				}
			}
		}
	}
	for _, profile := range usage.Profiles {
		if profile.ID == usage.DefaultProfile {
			return profile, modelReviewUsageSourceDefault, true
		}
	}
	return catalog.ChatModel3DUsageProfile{}, "", false
}

// modelReviewUsageBudget applies the brief's triangle override to the profile's
// maximum. The field is located by its declared type, so a renamed Schema
// field keeps working (#103 review C3). The value is reported verbatim even
// when it is below the profile's minimum; the check then fails for the range
// it actually declares.
func modelReviewUsageBudget(
	usage catalog.ChatModel3DUsageSchema,
	profile catalog.ChatModel3DUsageProfile,
	brief chat.ProductionBrief,
) (catalog.ChatModel3DUsageBudget, *int) {
	budget := profile.Budget
	if value, exists := brief.Integer(usage.Brief.FieldNameOfType("integer")); exists && value > 0 {
		budget.MaxTriangles = value
		return budget, &value
	}
	return budget, nil
}

func modelReviewUsageNote(
	profile catalog.ChatModel3DUsageProfile,
	source string,
	override *int,
	production catalog.ChatModelInspectProductionSchema,
	text modelReviewUsageText,
) string {
	template := production.DefaultPurposeText
	if source == modelReviewUsageSourceBrief {
		template = production.BriefPurposeText
	}
	note := text(template, "{purpose}", profile.Label)
	if override != nil {
		note += text(production.TriangleOverrideText, "{value}", strconv.Itoa(*override))
	}
	return note
}

func modelReviewUsageItem(check, status string, actual any, budget map[string]any) modelReviewUsageCheckItem {
	return modelReviewUsageCheckItem{Check: check, Status: status, Actual: actual, Budget: budget}
}

func modelReviewUsageReadError(report modelinspect.Report) bool {
	return modelReviewUsageWarned(report, "read_error")
}

func modelReviewUsageWarned(report modelinspect.Report, names ...string) bool {
	for _, warning := range report.Warnings {
		for _, name := range names {
			if warning == name {
				return true
			}
		}
	}
	return false
}

func modelReviewUsageTrianglesCheck(
	report modelinspect.Report,
	budget catalog.ChatModel3DUsageBudget,
) modelReviewUsageCheckItem {
	values := map[string]any{"maximum": budget.MaxTriangles}
	minTriangles := 0
	if budget.MinTriangles != nil {
		minTriangles = *budget.MinTriangles
		values["minimum"] = minTriangles
	}
	status := modelReviewUsageStatusPass
	switch {
	case modelReviewUsageReadError(report):
		status = modelReviewUsageStatusUnknown
	case report.Triangles < int64(minTriangles) || report.Triangles > int64(budget.MaxTriangles):
		status = modelReviewUsageStatusFail
	}
	return modelReviewUsageItem(catalog.ModelReviewUsageCheckTriangles, status, report.Triangles, values)
}

func modelReviewUsageUVCheck(
	report modelinspect.Report,
	budget catalog.ChatModel3DUsageBudget,
) modelReviewUsageCheckItem {
	status := modelReviewUsageStatusUnknown
	if budget.RequireUV {
		status = modelReviewUsageStatusPass
		if !report.UV.AllHaveUV {
			status = modelReviewUsageStatusFail
		}
	}
	return modelReviewUsageItem(
		catalog.ModelReviewUsageCheckUV, status, report.UV.AllHaveUV,
		map[string]any{"required": budget.RequireUV},
	)
}

func modelReviewUsageWatertightCheck(
	report modelinspect.Report,
	budget catalog.ChatModel3DUsageBudget,
) modelReviewUsageCheckItem {
	status := modelReviewUsageStatusUnknown
	if budget.RequireWatertight {
		switch {
		case modelReviewUsageReadError(report) || report.Triangles == 0,
			// Topology skipped the parts with bad data, so it cannot judge them.
			modelReviewUsageWarned(report, "invalid_indices", "invalid_positions"):
		case report.Topology.Watertight:
			status = modelReviewUsageStatusPass
		default:
			status = modelReviewUsageStatusFail
		}
	}
	return modelReviewUsageItem(
		catalog.ModelReviewUsageCheckWatertight, status, report.Topology.Watertight,
		map[string]any{"required": budget.RequireWatertight},
	)
}

func modelReviewUsageSkinCheck(
	report modelinspect.Report,
	budget catalog.ChatModel3DUsageBudget,
) modelReviewUsageCheckItem {
	status := modelReviewUsageStatusUnknown
	if budget.RequireSkin {
		status = modelReviewUsageStatusPass
		if len(report.Skins) == 0 {
			status = modelReviewUsageStatusFail
		}
	}
	return modelReviewUsageItem(
		catalog.ModelReviewUsageCheckSkin, status, len(report.Skins),
		map[string]any{"required": budget.RequireSkin},
	)
}

func modelReviewUsageTextureSizeCheck(
	report modelinspect.Report,
	budget catalog.ChatModel3DUsageBudget,
) modelReviewUsageCheckItem {
	actual := 0
	for _, material := range report.Materials {
		for _, slot := range material.Slots {
			if !slot.Present {
				continue
			}
			if longest := max(slot.Width, slot.Height); longest > actual {
				actual = longest
			}
		}
	}
	values := map[string]any{}
	status := modelReviewUsageStatusUnknown
	if budget.MaxTextureSize != nil {
		values["maximum"] = *budget.MaxTextureSize
		status = modelReviewUsageStatusPass
		if actual > *budget.MaxTextureSize {
			status = modelReviewUsageStatusFail
		}
	}
	return modelReviewUsageItem(catalog.ModelReviewUsageCheckTextureSize, status, actual, values)
}

func modelReviewUsageBBoxDiagonal(report modelinspect.Report) float64 {
	size := report.BBox.Size
	return math.Sqrt(size[0]*size[0] + size[1]*size[1] + size[2]*size[2])
}

func modelReviewUsageLongestEdge(report modelinspect.Report) float64 {
	size := report.BBox.Size
	return max(size[0], size[1], size[2])
}

func modelReviewUsageShortestEdge(report modelinspect.Report) float64 {
	size := report.BBox.Size
	return min(size[0], size[1], size[2])
}

func modelReviewUsagePivotCheck(
	report modelinspect.Report,
	production catalog.ChatModelInspectProductionSchema,
) modelReviewUsageCheckItem {
	values := map[string]any{"maximum_ratio": production.PivotBottomCenterRatio}
	actual := 0.0
	status := modelReviewUsageStatusUnknown
	diagonal := modelReviewUsageBBoxDiagonal(report)
	if !modelReviewUsageReadError(report) && diagonal > 0 {
		offset := report.BBox.BottomCenterOffset
		distance := math.Sqrt(offset[0]*offset[0] + offset[1]*offset[1] + offset[2]*offset[2])
		actual = distance / diagonal
		status = modelReviewUsageStatusPass
		if actual > production.PivotBottomCenterRatio {
			status = modelReviewUsageStatusFail
		}
	}
	return modelReviewUsageItem(catalog.ModelReviewUsageCheckPivotBottomCenter, status, actual, values)
}

func modelReviewUsageYUpCheck(
	report modelinspect.Report,
	production catalog.ChatModelInspectProductionSchema,
) modelReviewUsageCheckItem {
	values := map[string]any{
		"axis":       "y",
		"flat_ratio": production.YUpFlatRatio,
	}
	actual := ""
	status := modelReviewUsageStatusUnknown
	longest := modelReviewUsageLongestEdge(report)
	if !modelReviewUsageReadError(report) && longest > 0 {
		if modelReviewUsageShortestEdge(report) <= production.YUpFlatRatio*longest {
			actual = "flat"
		} else {
			size := report.BBox.Size
			switch {
			case size[1] >= size[0] && size[1] >= size[2]:
				actual = "y"
				status = modelReviewUsageStatusPass
			case size[0] >= size[2]:
				actual = "x"
				status = modelReviewUsageStatusFail
			default:
				actual = "z"
				status = modelReviewUsageStatusFail
			}
		}
	}
	return modelReviewUsageItem(catalog.ModelReviewUsageCheckYUp, status, actual, values)
}

func modelReviewUsageScaleCheck(
	report modelinspect.Report,
	profile catalog.ChatModel3DUsageProfile,
) modelReviewUsageCheckItem {
	actual := 0.0
	if !modelReviewUsageReadError(report) {
		actual = modelReviewUsageLongestEdge(report)
	}
	values := map[string]any{}
	status := modelReviewUsageStatusUnknown
	if rangeM := profile.ScaleRangeM; rangeM != nil {
		values["minimum"] = rangeM.Min
		values["maximum"] = rangeM.Max
		values["unit"] = "m"
		if !modelReviewUsageReadError(report) && actual > 0 {
			status = modelReviewUsageStatusPass
			if actual < rangeM.Min || actual > rangeM.Max {
				status = modelReviewUsageStatusFail
			}
		}
	}
	return modelReviewUsageItem(catalog.ModelReviewUsageCheckPlausibleScale, status, actual, values)
}

func modelReviewUsageRatioCheck(
	check string,
	triangles, affected int64,
	report modelinspect.Report,
	maximum float64,
) modelReviewUsageCheckItem {
	values := map[string]any{"maximum_ratio": maximum}
	actual := 0.0
	status := modelReviewUsageStatusUnknown
	if !modelReviewUsageReadError(report) && report.Triangles > 0 {
		actual = float64(affected) / float64(report.Triangles)
		status = modelReviewUsageStatusPass
		if actual > maximum {
			status = modelReviewUsageStatusFail
		}
	}
	return modelReviewUsageItem(check, status, actual, values)
}

func modelReviewUsageDebrisCheck(
	report modelinspect.Report,
	production catalog.ChatModelInspectProductionSchema,
) modelReviewUsageCheckItem {
	return modelReviewUsageRatioCheck(
		catalog.ModelReviewUsageCheckDebris,
		report.Triangles, report.Topology.DebrisTriangles,
		report, production.DebrisRatio,
	)
}

func modelReviewUsageDegenerateCheck(
	report modelinspect.Report,
	production catalog.ChatModelInspectProductionSchema,
) modelReviewUsageCheckItem {
	return modelReviewUsageRatioCheck(
		catalog.ModelReviewUsageCheckDegenerate,
		report.Triangles, report.Topology.DegenerateTriangles,
		report, production.DegenerateRatio,
	)
}

// modelReviewUsageFixHint renders the fix of a failed check. With no options
// the Schema template is a static sentence. Otherwise only options whose every
// capability is available are named; when none is, the Schema's
// no-capability sentence is the hint instead of a capability that would not
// work.
func modelReviewUsageFixHint(
	fix catalog.ChatModelInspectFixSchema,
	profile catalog.ChatModel3DUsageProfile,
	production catalog.ChatModelInspectProductionSchema,
	text modelReviewUsageText,
	available modelReviewFixAvailability,
) string {
	replacements := make([]string, 0, 6)
	if strings.Contains(fix.Template, "{capabilities}") {
		options := make([]string, 0, len(fix.Options))
		for _, option := range fix.Options {
			capabilities := make([]string, 0, len(option.Capabilities))
			supported := true
			for _, candidate := range option.Capabilities {
				if !available(candidate) {
					supported = false
					break
				}
				capabilities = append(capabilities, modelReviewFixCapabilityText(candidate))
			}
			if supported && len(capabilities) > 0 {
				options = append(options, strings.Join(capabilities, production.CapabilityJoiner))
			}
		}
		if len(options) == 0 {
			return text(production.NoCapabilityText)
		}
		replacements = append(replacements, "{capabilities}", strings.Join(options, production.OptionSeparator))
	}
	if strings.Contains(fix.Template, "{minimum}") || strings.Contains(fix.Template, "{maximum}") {
		if profile.ScaleRangeM == nil {
			return ""
		}
		replacements = append(
			replacements,
			"{minimum}", modelReviewUsageNumber(profile.ScaleRangeM.Min),
			"{maximum}", modelReviewUsageNumber(profile.ScaleRangeM.Max),
		)
	}
	return text(fix.Template, replacements...)
}

func modelReviewFixCapabilityText(candidate catalog.ChatModelInspectFixCapabilitySchema) string {
	if candidate.Parameter == "" {
		return candidate.Capability
	}
	return candidate.Capability + "(" + candidate.Parameter + "=" + candidate.Value + ")"
}

func modelReviewUsageNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// modelReviewFixAvailability returns a cached availability check over the
// enabled Offerings the runtime gateway currently offers. The cache is scoped
// to one inspect call so a disabled or unavailable Offering is never named.
func (executor *Executor) modelReviewFixAvailability() modelReviewFixAvailability {
	cache := map[generation.Capability][]generation.Offering{}
	return func(candidate catalog.ChatModelInspectFixCapabilitySchema) bool {
		capability := generation.Capability(candidate.Capability)
		offerings, cached := cache[capability]
		if !cached {
			offerings = executor.availableOfferings(capability)
			cache[capability] = offerings
		}
		for _, offering := range offerings {
			if modelReviewOfferingSupportsFix(offering, candidate) {
				return true
			}
		}
		return false
	}
}

// modelReviewOfferingSupportsFix reports whether an available Offering has the
// public parameter value a fix names. A fixed parameter counts only when its
// default already is the requested value.
func modelReviewOfferingSupportsFix(
	offering generation.Offering,
	candidate catalog.ChatModelInspectFixCapabilitySchema,
) bool {
	if candidate.Parameter == "" {
		return true
	}
	for _, parameter := range offering.Parameters {
		if parameter.Name != candidate.Parameter {
			continue
		}
		if parameter.Fixed {
			return modelReviewParameterDefaultMatches(parameter, candidate.Value)
		}
		if !offering.AgentParameterAllowed(parameter) {
			return false
		}
		return modelReviewParameterAccepts(parameter, candidate.Value)
	}
	return false
}

func modelReviewParameterAccepts(parameter generation.Parameter, value string) bool {
	switch parameter.Type {
	case "select":
		for _, option := range parameter.Options {
			if option.Value == value {
				return true
			}
		}
		return false
	case "boolean":
		return value == "true" || value == "false"
	default:
		return true
	}
}

func modelReviewParameterDefaultMatches(parameter generation.Parameter, value string) bool {
	switch parameter.Type {
	case "select", "text":
		var text string
		return json.Unmarshal(parameter.Default, &text) == nil && text == value
	case "boolean":
		var flag bool
		return json.Unmarshal(parameter.Default, &flag) == nil && strconv.FormatBool(flag) == value
	default:
		return false
	}
}
