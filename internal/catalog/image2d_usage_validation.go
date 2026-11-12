package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// image2DUsageShapes are the image input shapes a 2D purpose may recommend
// offerings for. They are also the Capability ids: text-to-image and
// image-to-image.
var image2DUsageShapes = []string{"text-to-image", "image-to-image"}

var (
	image2DAspectRatios = []string{"1:1", "2:3", "3:2", "3:4", "4:3", "16:9", "9:16"}
	image2DBackgrounds  = []string{"any", "solid", "transparent", "solid_or_transparent"}
	image2DSubjects     = []string{"single", "multiple"}
	image2DPoses        = []string{"", "a-pose", "t-pose"}
)

// validateImage2DUsageShape checks the parts of application.chat.image2d_usage
// that need no Offering: profiles, requirements and the requirement note. A
// Schema without 2D purposes stays valid (historical Catalogs).
func validateImage2DUsageShape(schema ChatSchema) error {
	usage := schema.Image2DUsage
	if len(usage.Profiles) == 0 {
		if strings.TrimSpace(usage.DefaultProfile) != "" || strings.TrimSpace(usage.RequirementsNote.Template) != "" {
			return fmt.Errorf("image2d_usage.profiles 不能为空")
		}
		return nil
	}
	profiles := make(map[string]bool, len(usage.Profiles))
	for index, profile := range usage.Profiles {
		if strings.TrimSpace(profile.ID) == "" {
			return fmt.Errorf("image2d_usage.profiles[%d].id 不能为空", index)
		}
		if profiles[profile.ID] {
			return fmt.Errorf("image2d_usage.profiles 中 %q 重复", profile.ID)
		}
		profiles[profile.ID] = true
		for name, value := range map[string]string{
			"label":       profile.Label,
			"description": profile.Description,
			"reason":      profile.Reason,
		} {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("image2d_usage.profiles[%d].%s 不能为空", index, name)
			}
		}
		if len([]rune(profile.Reason)) > 4096 {
			return fmt.Errorf("image2d_usage.profiles[%d].reason 不能超过 4096 个字符", index)
		}
		if len([]rune(profile.PromptGuidance)) > 4096 {
			return fmt.Errorf("image2d_usage.profiles[%d].prompt_guidance 不能超过 4096 个字符", index)
		}
		acknowledged := map[string]bool{}
		for _, id := range profile.ResolutionUnverifiedOfferings {
			if strings.TrimSpace(id) == "" || acknowledged[id] {
				return fmt.Errorf("image2d_usage.profiles[%d].resolution_unverified_offerings 含空或重复的 Offering ID", index)
			}
			acknowledged[id] = true
		}
		if len(profile.RecommendedOfferings) == 0 {
			return fmt.Errorf("image2d_usage.profiles[%d].recommended_offerings 不能为空", index)
		}
		for shape, offerings := range profile.RecommendedOfferings {
			if !containsString(image2DUsageShapes, shape) {
				return fmt.Errorf("image2d_usage.profiles[%d].recommended_offerings 含未知输入形态 %q", index, shape)
			}
			if len(offerings) == 0 {
				return fmt.Errorf("image2d_usage.profiles[%d].recommended_offerings[%q] 不能为空", index, shape)
			}
			seen := map[string]bool{}
			for _, id := range offerings {
				if strings.TrimSpace(id) == "" {
					return fmt.Errorf("image2d_usage.profiles[%d].recommended_offerings[%q] 含空 Offering ID", index, shape)
				}
				if seen[id] {
					return fmt.Errorf("image2d_usage.profiles[%d].recommended_offerings[%q] 中 %q 重复", index, shape, id)
				}
				seen[id] = true
			}
		}
		if err := validateImage2DRequirements(profile.Requirements); err != nil {
			return fmt.Errorf("image2d_usage.profiles[%d].requirements: %w", index, err)
		}
	}
	if !profiles[usage.DefaultProfile] {
		return fmt.Errorf("image2d_usage.default_profile %q 不存在", usage.DefaultProfile)
	}
	if err := validateImage2DOutputResolutionsShape(usage.OutputResolutions); err != nil {
		return err
	}
	return validateImage2DRequirementsNote(usage)
}

// validateImage2DOutputResolutionsShape checks the Offering-independent part of
// the resolution tier table: a named parameter and a positive pixel count for
// every tier.
func validateImage2DOutputResolutionsShape(resolutions map[string]ChatImage2DOutputResolution) error {
	ids := make([]string, 0, len(resolutions))
	for id := range resolutions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		resolution := resolutions[id]
		if strings.TrimSpace(id) == "" || strings.TrimSpace(resolution.Parameter) == "" || len(resolution.Pixels) == 0 {
			return fmt.Errorf("image2d_usage.output_resolutions[%q] 必须声明 Offering ID、parameter 与 pixels", id)
		}
		for tier, pixels := range resolution.Pixels {
			if strings.TrimSpace(tier) == "" || pixels <= 0 {
				return fmt.Errorf("image2d_usage.output_resolutions[%q].pixels[%q] 必须是正整数", id, tier)
			}
		}
	}
	return nil
}

// validateImage2DRequirements checks one purpose's delivery requirements.
func validateImage2DRequirements(requirements ChatImage2DRequirements) error {
	if !containsString(image2DAspectRatios, requirements.AspectRatio) {
		return fmt.Errorf("aspect_ratio %q 未声明", requirements.AspectRatio)
	}
	if requirements.MinimumShortEdge <= 0 {
		return fmt.Errorf("minimum_short_edge 必须大于 0")
	}
	if !containsString(image2DBackgrounds, requirements.Background) {
		return fmt.Errorf("background %q 未声明", requirements.Background)
	}
	if !containsString(image2DSubjects, requirements.Subjects) {
		return fmt.Errorf("subjects %q 未声明", requirements.Subjects)
	}
	if !containsString(image2DPoses, requirements.Pose) {
		return fmt.Errorf("pose %q 未声明", requirements.Pose)
	}
	return nil
}

// validateImage2DRequirementsNote checks the words that render requirements
// into the brief. Every select value has declared text, so no enum literal
// can leak into the Agent context.
func validateImage2DRequirementsNote(usage ChatImage2DUsageSchema) error {
	note := usage.RequirementsNote
	templates := map[string]string{
		"template":                    note.Template,
		"separator":                   note.Separator,
		"aspect_ratio_template":       note.AspectRatioTemplate,
		"minimum_short_edge_template": note.MinimumShortEdgeTemplate,
		"background_template":         note.BackgroundTemplate,
		"subjects_template":           note.SubjectsTemplate,
		"pose_template":               note.PoseTemplate,
	}
	for name, value := range templates {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("image2d_usage.requirements_note.%s 不能为空", name)
		}
	}
	placeholders := map[string][]string{
		"template":                    {"{items}"},
		"aspect_ratio_template":       {"{value}"},
		"minimum_short_edge_template": {"{value}"},
		"background_template":         {"{value}"},
		"subjects_template":           {"{value}"},
		"pose_template":               {"{value}"},
	}
	for name, required := range placeholders {
		for _, placeholder := range required {
			if !strings.Contains(templates[name], placeholder) {
				return fmt.Errorf("image2d_usage.requirements_note.%s 必须包含 %s", name, placeholder)
			}
		}
	}
	// The words of the two optional requirement items are required exactly when
	// some purpose uses them, so a purpose can never render an empty item.
	for _, profile := range usage.Profiles {
		if profile.Requirements.Tileable && strings.TrimSpace(note.TileableText) == "" {
			return fmt.Errorf("image2d_usage.requirements_note.tileable_text 不能为空：用途 %q 要求可平铺", profile.ID)
		}
		if strings.TrimSpace(profile.PromptGuidance) != "" && !strings.Contains(note.GuidanceTemplate, "{value}") {
			return fmt.Errorf("image2d_usage.requirements_note.guidance_template 必须包含 {value}：用途 %q 声明了 prompt_guidance", profile.ID)
		}
	}
	for name, texts := range map[string]map[string]string{
		"background_texts": note.BackgroundTexts,
		"subjects_texts":   note.SubjectsTexts,
		"pose_texts":       note.PoseTexts,
	} {
		values := map[string][]string{
			"background_texts": image2DBackgrounds,
			"subjects_texts":   image2DSubjects,
			"pose_texts":       image2DPoses[1:],
		}[name]
		for _, value := range values {
			if strings.TrimSpace(texts[value]) == "" {
				return fmt.Errorf("image2d_usage.requirements_note.%s 缺少 %q 的文案", name, value)
			}
		}
	}
	return nil
}

// validateImage2DUsageOfferings checks the Offering IDs and parameter
// overrides once the catalog's Offerings are built. Every override is frozen
// the way a real submission would freeze it, and every purpose requirement
// must be expressible by each Offering it recommends (#239).
func validateImage2DUsageOfferings(usage ChatImage2DUsageSchema, byID generationMap) error {
	if len(usage.Profiles) == 0 {
		return nil
	}
	if err := validateImage2DOutputResolutions(usage.OutputResolutions, byID); err != nil {
		return err
	}
	for index, profile := range usage.Profiles {
		shapes := make([]string, 0, len(profile.RecommendedOfferings))
		for shape := range profile.RecommendedOfferings {
			shapes = append(shapes, shape)
		}
		sort.Strings(shapes)
		for _, shape := range shapes {
			for _, id := range profile.RecommendedOfferings[shape] {
				offering, exists := byID[schemacontract.OfferingID(id)]
				if !exists {
					return fmt.Errorf("image2d_usage.profiles[%d].recommended_offerings[%q] 的 Offering %q 不存在", index, shape, id)
				}
				if err := checkImage2DRecommendedShape(offering, shape); err != nil {
					return fmt.Errorf("image2d_usage.profiles[%d].recommended_offerings[%q]: %w", index, shape, err)
				}
				if err := checkImage2DRequirementsExpressible(offering, profile.Requirements, profile.ParameterOverrides[id]); err != nil {
					return fmt.Errorf("image2d_usage.profiles[%d].recommended_offerings[%q] 的 Offering %q: %w", index, shape, id, err)
				}
			}
			if err := checkImage2DFirstRecommendationReach(usage, profile, shape, byID); err != nil {
				return fmt.Errorf("image2d_usage.profiles[%d].recommended_offerings[%q]: %w", index, shape, err)
			}
		}
		if err := checkImage2DUnverifiedResolutions(profile, byID); err != nil {
			return fmt.Errorf("image2d_usage.profiles[%d].resolution_unverified_offerings: %w", index, err)
		}
		overrideOfferings := make([]string, 0, len(profile.ParameterOverrides))
		for id := range profile.ParameterOverrides {
			overrideOfferings = append(overrideOfferings, id)
		}
		sort.Strings(overrideOfferings)
		for _, id := range overrideOfferings {
			offering, exists := byID[schemacontract.OfferingID(id)]
			if !exists {
				return fmt.Errorf("image2d_usage.profiles[%d].parameter_overrides 的 Offering %q 不存在", index, id)
			}
			if offering.Domain != schemacontract.DomainImage {
				return fmt.Errorf("image2d_usage.profiles[%d].parameter_overrides 的 Offering %q 不属于图像域", index, id)
			}
			if err := simulateProfileParameters(offering, profile.ParameterOverrides[id]); err != nil {
				return fmt.Errorf("image2d_usage.profiles[%d].parameter_overrides[%q]: %w", index, id, err)
			}
		}
	}
	return nil
}

// checkImage2DRecommendedShape refuses an Offering whose capability is not the
// recommended input shape.
func checkImage2DRecommendedShape(offering schemacontract.Offering, shape string) error {
	if offering.Domain != schemacontract.DomainImage {
		return fmt.Errorf("Offering %q 不属于图像域", offering.ID)
	}
	if string(offering.Capability) != shape {
		return fmt.Errorf("Offering %q 的能力是 %q，不是 %q", offering.ID, offering.Capability, shape)
	}
	return nil
}

// checkImage2DRequirementsExpressible checks that an Offering can deliver the
// purpose's requirements: its output-size parameter freezes the required aspect
// ratio, it either declares a transparent background option / a
// remove_background switch, and it has a pose_mode with the required pose. A
// purpose that required something no recommended Offering can express would be
// a promise the Catalog cannot keep.
func checkImage2DRequirementsExpressible(
	offering schemacontract.Offering,
	requirements ChatImage2DRequirements,
	overrides map[string]json.RawMessage,
) error {
	if err := checkImage2DAspectRatio(offering, requirements.AspectRatio, overrides); err != nil {
		return err
	}
	if requirements.Background != "any" {
		background, hasBackground := offeringParameter(offering.Parameters, "background")
		transparent := false
		if hasBackground && background.Type == "select" {
			for _, option := range background.Options {
				if option.Value == "transparent" {
					transparent = true
					break
				}
			}
		}
		remove, hasRemove := offeringParameter(offering.Parameters, "remove_background")
		if !transparent && !(hasRemove && remove.Type == "boolean") {
			return fmt.Errorf("要求背景 %q，但 Offering 没有 transparent 背景选项或 remove_background 开关", requirements.Background)
		}
	}
	if requirements.Pose == "" {
		return nil
	}
	pose, declared := offeringParameter(offering.Parameters, "pose_mode")
	if !declared || pose.Type != "select" {
		return fmt.Errorf("要求姿势 %q，但 Offering 没有 pose_mode 参数", requirements.Pose)
	}
	for _, option := range pose.Options {
		if option.Value == requirements.Pose {
			return nil
		}
	}
	return fmt.Errorf("要求姿势 %q，但 Offering 的 pose_mode 没有该选项", requirements.Pose)
}

// checkImage2DAspectRatio checks that the Offering's frame actually matches the
// purpose's required aspect ratio when the purpose applies its overrides: the
// brief renders the requirement, so a recommendation freezing another ratio
// would generate something the brief does not describe (#239 review).
func checkImage2DAspectRatio(
	offering schemacontract.Offering,
	required string,
	overrides map[string]json.RawMessage,
) error {
	if required == "" {
		return nil
	}
	name, value, found := image2DOutputSize(offering, overrides)
	if !found {
		return fmt.Errorf("要求画幅 %q，但 Offering 没有可冻结的 output-size 参数", required)
	}
	ratio, measured := image2DRatio(value)
	if !measured || ratio != required {
		return fmt.Errorf("要求画幅 %q，但 %s 会冻结为 %q（比例 %q）", required, name, value, ratio)
	}
	return nil
}

// image2DOutputSize returns the effective value of the parameter that decides
// the output frame: the purpose's override when it sets one, else the
// declaration's default. Values that do not read as a frame are skipped so a
// second output-size parameter can still express the ratio.
func image2DOutputSize(
	offering schemacontract.Offering,
	overrides map[string]json.RawMessage,
) (string, string, bool) {
	for _, parameter := range offering.Parameters {
		if parameter.Semantic != schemacontract.SemanticOutputSize {
			continue
		}
		raw, overridden := overrides[parameter.Name]
		if !overridden {
			raw = parameter.Default
		}
		var value string
		if len(bytes.TrimSpace(raw)) == 0 || json.Unmarshal(raw, &value) != nil {
			continue
		}
		if _, measured := image2DRatio(value); measured {
			return parameter.Name, value, true
		}
	}
	return "", "", false
}

// image2DRatio renders an output-size value as a reduced aspect ratio: "3:4"
// and "1152x1536" both read as "3:4". A value that names neither shape is not
// measured, so it cannot accidentally satisfy a requirement.
func image2DRatio(value string) (string, bool) {
	value = strings.TrimSpace(value)
	width, height, separated := strings.Cut(value, "x")
	if !separated {
		width, height, separated = strings.Cut(value, ":")
	}
	if !separated {
		return "", false
	}
	parsedWidth, widthErr := strconv.Atoi(strings.TrimSpace(width))
	parsedHeight, heightErr := strconv.Atoi(strings.TrimSpace(height))
	if widthErr != nil || heightErr != nil || parsedWidth <= 0 || parsedHeight <= 0 {
		return "", false
	}
	divisor := image2DGCD(parsedWidth, parsedHeight)
	return fmt.Sprintf("%d:%d", parsedWidth/divisor, parsedHeight/divisor), true
}

func image2DGCD(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// validateImage2DOutputResolutions checks the resolution tier table against the
// Offerings it describes: each names an image Offering with a select parameter
// of that name, and every option of the parameter has a pixel count (and no
// other tier is listed), so a tier can never be read as an unknown size.
func validateImage2DOutputResolutions(resolutions map[string]ChatImage2DOutputResolution, byID generationMap) error {
	ids := make([]string, 0, len(resolutions))
	for id := range resolutions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		resolution := resolutions[id]
		offering, exists := byID[schemacontract.OfferingID(id)]
		if !exists {
			return fmt.Errorf("image2d_usage.output_resolutions 的 Offering %q 不存在", id)
		}
		if offering.Domain != schemacontract.DomainImage {
			return fmt.Errorf("image2d_usage.output_resolutions 的 Offering %q 不属于图像域", id)
		}
		parameter, declared := offeringParameter(offering.Parameters, resolution.Parameter)
		if !declared || parameter.Type != "select" {
			return fmt.Errorf("image2d_usage.output_resolutions[%q] 的参数 %q 不是该 Offering 的 select 参数", id, resolution.Parameter)
		}
		options := map[string]bool{}
		for _, option := range parameter.Options {
			options[option.Value] = true
			if resolution.Pixels[option.Value] <= 0 {
				return fmt.Errorf("image2d_usage.output_resolutions[%q].pixels 缺少选项 %q 的像素数", id, option.Value)
			}
		}
		for tier := range resolution.Pixels {
			if !options[tier] {
				return fmt.Errorf("image2d_usage.output_resolutions[%q].pixels 的 %q 不是参数 %q 的选项", id, tier, resolution.Parameter)
			}
		}
	}
	return nil
}

// checkImage2DFirstRecommendationReach is the rule of #282: the first
// recommendation of every input shape is what the Agent runs unless the person
// or the Agent chose otherwise, so it must be able to reach the purpose's
// minimum short edge at the purpose's frame. That is shown from the Schema
// alone: an explicit WxH frame value, or an aspect ratio with a declared
// resolution tier. An Offering with neither is only acceptable when the purpose
// lists it in resolution_unverified_offerings, an explicit and reviewable
// acknowledgement; the delivered picture is checked either way.
func checkImage2DFirstRecommendationReach(
	usage ChatImage2DUsageSchema,
	profile ChatImage2DUsageProfile,
	shape string,
	byID generationMap,
) error {
	recommended := profile.RecommendedOfferings[shape]
	if len(recommended) == 0 {
		return nil
	}
	first := recommended[0]
	offering, exists := byID[schemacontract.OfferingID(first)]
	if !exists {
		return nil
	}
	minimum := profile.Requirements.MinimumShortEdge
	short, proven := Image2DShortEdge(offering, profile.ParameterOverrides[first], usage.OutputResolutions)
	acknowledged := containsString(profile.ResolutionUnverifiedOfferings, first)
	switch {
	case proven && short < minimum:
		return fmt.Errorf("首推 Offering %q 在用途的画幅下最多达到短边 %d 像素，低于用途要求的 %d；"+
			"把能达到的 Offering 排到首位，或提高覆盖值", first, short, minimum)
	case proven && acknowledged:
		return fmt.Errorf("首推 Offering %q 的分辨率可由 Schema 证明（短边 %d 像素），不应列入 resolution_unverified_offerings", first, short)
	case proven || acknowledged:
		return nil
	}
	return fmt.Errorf("首推 Offering %q 在 Schema 里没有可证明的分辨率（画幅参数不是 WxH 且没有 output_resolutions 档位声明），"+
		"无法保证达到用途要求的短边 %d 像素；把能证明的 Offering 排到首位，或把它列入该用途的 resolution_unverified_offerings",
		first, minimum)
}

// checkImage2DUnverifiedResolutions refuses an acknowledgement that names
// something other than a first recommendation, so the list cannot go stale.
func checkImage2DUnverifiedResolutions(profile ChatImage2DUsageProfile, byID generationMap) error {
	firsts := map[string]bool{}
	for _, ids := range profile.RecommendedOfferings {
		if len(ids) > 0 {
			firsts[ids[0]] = true
		}
	}
	for _, id := range profile.ResolutionUnverifiedOfferings {
		if !firsts[id] {
			return fmt.Errorf("%q 不是该用途任何输入形态的首推 Offering", id)
		}
		if _, exists := byID[schemacontract.OfferingID(id)]; !exists {
			return fmt.Errorf("Offering %q 不存在", id)
		}
	}
	return nil
}

// Image2DShortEdge reports the short edge, in pixels, that an Offering
// delivers at the frame a purpose freezes for it, when the Schema shows it:
// the frame value is an explicit WxH (the smaller side), or an aspect ratio
// whose resolution tier the output_resolutions table declares (then the
// tier's pixel count divided into that ratio, rounded down). Overrides are the
// purpose's parameter overrides for the Offering; the Offering's own defaults
// fill the rest. ok is false when the Schema does not show the size.
func Image2DShortEdge(
	offering schemacontract.Offering,
	overrides map[string]json.RawMessage,
	resolutions map[string]ChatImage2DOutputResolution,
) (int, bool) {
	_, frame, found := image2DOutputSize(offering, overrides)
	if !found {
		return 0, false
	}
	if width, height, sized := image2DPixelSize(frame); sized {
		return min(width, height), true
	}
	declared, tiered := resolutions[string(offering.ID)]
	if !tiered {
		return 0, false
	}
	tier, selected := image2DEffectiveText(offering, declared.Parameter, overrides)
	pixels := declared.Pixels[tier]
	if !selected || pixels <= 0 {
		return 0, false
	}
	ratio, measured := image2DRatio(frame)
	if !measured {
		return 0, false
	}
	first, second, _ := strings.Cut(ratio, ":")
	left, _ := strconv.Atoi(first)
	right, _ := strconv.Atoi(second)
	short, long := min(left, right), max(left, right)
	return int(math.Floor(math.Sqrt(float64(pixels) * float64(short) / float64(long)))), true
}

// ReachingOfferings returns, in recommendation order and without repeats, the
// Offerings the purpose recommends (for every input shape) that the Schema
// shows reaching its minimum short edge. inspect_image names them when a
// picture falls short (#282).
func (usage ChatImage2DUsageSchema) ReachingOfferings(
	profile ChatImage2DUsageProfile,
	lookup func(id string) (schemacontract.Offering, bool),
) []schemacontract.Offering {
	var reaching []schemacontract.Offering
	seen := map[string]bool{}
	for _, shape := range image2DUsageShapes {
		for _, id := range profile.RecommendedOfferings[shape] {
			if seen[id] {
				continue
			}
			seen[id] = true
			offering, found := lookup(id)
			if !found {
				continue
			}
			short, proven := Image2DShortEdge(offering, profile.ParameterOverrides[id], usage.OutputResolutions)
			if proven && short >= profile.Requirements.MinimumShortEdge {
				reaching = append(reaching, offering)
			}
		}
	}
	return reaching
}

// image2DPixelSize reads an explicit "WxH" frame; an aspect ratio or any other
// text is not one.
func image2DPixelSize(value string) (int, int, bool) {
	width, height, separated := strings.Cut(strings.TrimSpace(value), "x")
	if !separated {
		return 0, 0, false
	}
	parsedWidth, widthErr := strconv.Atoi(strings.TrimSpace(width))
	parsedHeight, heightErr := strconv.Atoi(strings.TrimSpace(height))
	if widthErr != nil || heightErr != nil || parsedWidth <= 0 || parsedHeight <= 0 {
		return 0, 0, false
	}
	return parsedWidth, parsedHeight, true
}

// image2DEffectiveText is the text value a parameter takes under a purpose: its
// override when it sets one, else the declaration's default.
func image2DEffectiveText(
	offering schemacontract.Offering,
	name string,
	overrides map[string]json.RawMessage,
) (string, bool) {
	raw, overridden := overrides[name]
	if !overridden {
		parameter, declared := offeringParameter(offering.Parameters, name)
		if !declared {
			return "", false
		}
		raw = parameter.Default
	}
	var value string
	if len(bytes.TrimSpace(raw)) == 0 || json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}
