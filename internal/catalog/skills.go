package catalog

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
	"io"
	"slices"
	"strings"
)

//go:embed default_skills.json
var defaultSkillCatalogJSON []byte

type SkillCatalogSchema struct {
	Revision string        `json:"revision"`
	Skills   []SkillSchema `json:"skills"`
}

type SkillSchema struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	NameZH               string   `json:"name_zh,omitempty"`
	Category             string   `json:"category"`
	RecommendedSize      string   `json:"recommended_size"`
	ThumbnailPath        string   `json:"thumbnail_path"`
	RawImageURL          string   `json:"raw_image_url"`
	Prompt               string   `json:"prompt"`
	ImageToImageTemplate string   `json:"image_to_image_template,omitempty"`
	Tags                 []string `json:"tags,omitempty"`
	// Tool binds the Skill to an Agent tool (Product Schema system Skills
	// only). Choosing it directs the Agent to that tool; it never styles a
	// generation prompt.
	Tool string `json:"tool,omitempty"`
	// RequiresImage: the composer must have a picture (stage selection or
	// attachment) before the turn is sent.
	RequiresImage bool `json:"requires_image,omitempty"`
	// DefaultMessage is sent as the message text when the composer is empty.
	DefaultMessage string `json:"default_message,omitempty"`
	// OneShot: once the server accepts the message that used the Skill, the
	// session's Skill selection returns to none.
	OneShot bool `json:"one_shot,omitempty"`
}

func LoadDefaultSkillCatalog() (SkillCatalogSchema, error) {
	var document SkillCatalogSchema
	if err := json.Unmarshal(defaultSkillCatalogJSON, &document); err != nil {
		return SkillCatalogSchema{}, fmt.Errorf(
			"解析默认 Skill Catalog Schema: %w",
			err,
		)
	}
	if err := ValidateSkillCatalog(document); err != nil {
		return SkillCatalogSchema{}, fmt.Errorf(
			"默认 Skill Catalog Schema 无效: %w",
			err,
		)
	}
	return document, nil
}

func LoadSkillCatalog(reader io.Reader) (SkillCatalogSchema, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var document SkillCatalogSchema
	if err := decoder.Decode(&document); err != nil {
		return SkillCatalogSchema{}, fmt.Errorf(
			"解析 Skill Catalog Schema: %w",
			err,
		)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = errors.New("存在多个 JSON 文档")
		}
		return SkillCatalogSchema{}, fmt.Errorf(
			"解析 Skill Catalog Schema 尾部内容: %w",
			err,
		)
	}
	if err := ValidateSkillCatalog(document); err != nil {
		return SkillCatalogSchema{}, fmt.Errorf(
			"Skill Catalog Schema 无效: %w",
			err,
		)
	}
	return document, nil
}

func ValidateSkillCatalog(document SkillCatalogSchema) error {
	if strings.TrimSpace(document.Revision) == "" {
		return errors.New("skill catalog revision 不能为空")
	}
	ids := make(map[string]struct{}, len(document.Skills))
	for index, skill := range document.Skills {
		required := map[string]string{
			"id":               skill.ID,
			"name":             skill.Name,
			"recommended_size": skill.RecommendedSize,
			"prompt":           skill.Prompt,
		}
		for field, value := range required {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("skills[%d].%s 不能为空", index, field)
			}
		}
		if (skill.ThumbnailPath == "") != (skill.RawImageURL == "") {
			return fmt.Errorf("skills[%d] 的图片路径必须同时为空或同时提供", index)
		}
		if _, exists := ids[skill.ID]; exists {
			return fmt.Errorf("skill id 重复: %q", skill.ID)
		}
		ids[skill.ID] = struct{}{}
	}
	return nil
}

func ValidateSkillCatalogAgainstProductSchema(
	document SkillCatalogSchema,
	product ApplicationSchema,
	offerings []schemacontract.Offering,
) error {
	if err := ValidateSkillCatalog(document); err != nil {
		return err
	}
	categories := make(
		map[string]struct{},
		len(product.Chat.Skills.Categories),
	)
	for _, category := range product.Chat.Skills.Categories {
		categories[category.ID] = struct{}{}
	}
	for index, skill := range document.Skills {
		if skill.Category == "" {
			continue
		}
		if _, exists := categories[skill.Category]; !exists {
			return fmt.Errorf(
				"skills[%d] %q 引用了 Product Schema 中不存在的 category %q",
				index,
				skill.ID,
				skill.Category,
			)
		}
	}
	skillOfferings := make([]schemacontract.Offering, 0, len(offerings))
	for _, offering := range offerings {
		if slices.Contains(offering.Features, product.Chat.Skills.Feature) {
			skillOfferings = append(skillOfferings, offering)
		}
	}
	if len(skillOfferings) == 0 {
		return fmt.Errorf(
			"不存在声明 feature %q 的 Offering",
			product.Chat.Skills.Feature,
		)
	}
	for capability := range product.Chat.Skills.Execution.PromptTemplates {
		found := false
		for _, offering := range skillOfferings {
			if offering.Capability == capability {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf(
				"skill prompt capability %q 没有匹配 Offering",
				capability,
			)
		}
	}
	for _, offering := range skillOfferings {
		parameters := make(
			map[schemacontract.ParameterSemantic]schemacontract.Parameter,
			len(offering.Parameters),
		)
		for _, parameter := range offering.Parameters {
			if parameter.Semantic != "" {
				parameters[parameter.Semantic] = parameter
			}
		}
		for semantic := range product.Chat.Skills.Execution.ParameterOverrides {
			if !semantic.SkillSettable() {
				return fmt.Errorf(
					"Skill parameter_overrides 的 semantic %q 不属于 skill 可覆盖闭包（来源与动作由规划器和用户决定）",
					semantic,
				)
			}
			if _, exists := parameters[semantic]; !exists {
				return fmt.Errorf(
					"Skill Offering %q 缺少 override semantic %q",
					offering.ID,
					semantic,
				)
			}
		}
		sizeParameter, hasSize := parameters[schemacontract.SemanticOutputSize]
		if !hasSize {
			continue
		}
		for _, skill := range document.Skills {
			allowed := false
			for _, option := range sizeParameter.Options {
				if option.Value == skill.RecommendedSize {
					allowed = true
					break
				}
			}
			if !allowed {
				return fmt.Errorf(
					"Skill %q 的 recommended_size %q 不在 Offering %q 的 output-size Schema 中",
					skill.ID,
					skill.RecommendedSize,
					offering.ID,
				)
			}
		}
	}
	return nil
}
