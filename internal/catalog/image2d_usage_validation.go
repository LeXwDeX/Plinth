package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	return validateImage2DRequirementsNote(usage.RequirementsNote)
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
func validateImage2DRequirementsNote(note ChatImage2DRequirementsNote) error {
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
