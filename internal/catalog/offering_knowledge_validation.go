package catalog

// Offering self-description validation (#249).
import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// Prompt over-limit policies (application.chat.agent.prompt_report).
const (
	promptOverLimitTruncate = "truncate"
	promptOverLimitRefuse   = "refuse"
)

const (
	// maximumOfferingKnowledgeItems bounds one statement list.
	maximumOfferingKnowledgeItems = 16
	// maximumOfferingKnowledgeRunes bounds one statement.
	maximumOfferingKnowledgeRunes = 600
	// maximumOfferingKnowledgeSources bounds the cited documents.
	maximumOfferingKnowledgeSources = 8
	// maximumOfferingKnowledgeURLRunes bounds one source URL.
	maximumOfferingKnowledgeURLRunes = 2048
	// maximumOfferingKnowledgeTitleRunes bounds one source title.
	maximumOfferingKnowledgeTitleRunes = 200
	// maximumOfferingPromptWords bounds the advised prompt length.
	maximumOfferingPromptWords = 10000
)

var offeringKnowledgeReferenceUses = []string{
	schemacontract.ReferenceUseNone,
	schemacontract.ReferenceUseGeometry,
	schemacontract.ReferenceUseTexture,
	schemacontract.ReferenceUseStyle,
	schemacontract.ReferenceUseIdentity,
	schemacontract.ReferenceUseComposition,
}

// validateOfferingKnowledge checks an Offering's self-description against its
// own parameter contract. The statements are optional, but a declared fact
// must agree with what the Offering can do: a prompt reader must have a prompt
// parameter, a negative prompt must exist, and a reference use must name the
// reference pictures the Offering takes. A mismatch is a declaration drift,
// so it fails Catalog loading instead of misleading the Agent at runtime.
func validateOfferingKnowledge(offering schemacontract.Offering) error {
	knowledge := offering.Knowledge
	if knowledge == nil {
		return nil
	}
	for name, items := range map[string][]string{
		"strengths":     knowledge.Strengths,
		"weaknesses":    knowledge.Weaknesses,
		"limitations":   knowledge.Limitations,
		"reference_use": knowledge.ReferenceUse,
	} {
		if len(items) > maximumOfferingKnowledgeItems {
			return fmt.Errorf("Offering %q 的 knowledge.%s 不能超过 %d 条", offering.ID, name, maximumOfferingKnowledgeItems)
		}
		for index, item := range items {
			if strings.TrimSpace(item) == "" {
				return fmt.Errorf("Offering %q 的 knowledge.%s[%d] 不能为空", offering.ID, name, index)
			}
			if utf8.RuneCountInString(item) > maximumOfferingKnowledgeRunes {
				return fmt.Errorf("Offering %q 的 knowledge.%s[%d] 超过 %d 个字符", offering.ID, name, index, maximumOfferingKnowledgeRunes)
			}
		}
	}
	if err := validateOfferingPrompt(offering, knowledge.Prompt); err != nil {
		return err
	}
	if err := validateOfferingReferenceUse(offering, knowledge.ReferenceUse); err != nil {
		return err
	}
	if len(knowledge.Sources) > maximumOfferingKnowledgeSources {
		return fmt.Errorf("Offering %q 的 knowledge.sources 不能超过 %d 条", offering.ID, maximumOfferingKnowledgeSources)
	}
	for index, source := range knowledge.Sources {
		if strings.TrimSpace(source.Title) == "" || utf8.RuneCountInString(source.Title) > maximumOfferingKnowledgeTitleRunes {
			return fmt.Errorf("Offering %q 的 knowledge.sources[%d].title 必须非空且不超过 %d 个字符", offering.ID, index, maximumOfferingKnowledgeTitleRunes)
		}
		if strings.TrimSpace(source.URL) == "" || utf8.RuneCountInString(source.URL) > maximumOfferingKnowledgeURLRunes {
			return fmt.Errorf("Offering %q 的 knowledge.sources[%d].url 必须非空且不超过 %d 个字符", offering.ID, index, maximumOfferingKnowledgeURLRunes)
		}
		if _, err := time.Parse(time.DateOnly, source.CheckedAt); err != nil {
			return fmt.Errorf("Offering %q 的 knowledge.sources[%d].checked_at 必须是 YYYY-MM-DD: %q", offering.ID, index, source.CheckedAt)
		}
	}
	return nil
}

func validateOfferingPrompt(offering schemacontract.Offering, prompt *schemacontract.OfferingPrompt) error {
	if prompt == nil {
		return nil
	}
	switch prompt.Language {
	case "", schemacontract.PromptLanguageEnglish, schemacontract.PromptLanguageChinese, schemacontract.PromptLanguageUnknown:
	default:
		return fmt.Errorf("Offering %q 的 knowledge.prompt.language 无效: %q", offering.ID, prompt.Language)
	}
	switch prompt.Form {
	case "", schemacontract.PromptFormDescriptive, schemacontract.PromptFormKeywords, schemacontract.PromptFormUnknown:
	default:
		return fmt.Errorf("Offering %q 的 knowledge.prompt.form 无效: %q", offering.ID, prompt.Form)
	}
	switch prompt.Unit {
	case "", schemacontract.PromptUnitWords, schemacontract.PromptUnitCharacters:
	default:
		return fmt.Errorf("Offering %q 的 knowledge.prompt.unit 无效: %q", offering.ID, prompt.Unit)
	}
	if prompt.WordsMin < 0 || prompt.WordsMax < 0 ||
		prompt.WordsMin > maximumOfferingPromptWords || prompt.WordsMax > maximumOfferingPromptWords {
		return fmt.Errorf("Offering %q 的 knowledge.prompt 字数上限必须在 0 与 %d 之间", offering.ID, maximumOfferingPromptWords)
	}
	if prompt.WordsMin > prompt.WordsMax {
		return fmt.Errorf("Offering %q 的 knowledge.prompt.words_min 不能大于 words_max", offering.ID)
	}
	if (prompt.WordsMin > 0 || prompt.WordsMax > 0) && prompt.Unit == "" {
		return fmt.Errorf("Offering %q 的 knowledge.prompt 声明了字数却没有 unit", offering.ID)
	}
	hasPrompt := false
	hasNegativePrompt := false
	for _, parameter := range offering.Parameters {
		switch parameter.Semantic {
		case schemacontract.SemanticPrompt:
			hasPrompt = true
		case schemacontract.SemanticNegativePrompt:
			hasNegativePrompt = true
		}
	}
	if prompt.ReadsText != nil && *prompt.ReadsText != hasPrompt {
		return fmt.Errorf("Offering %q 的 knowledge.prompt.reads_text 与是否声明 prompt 参数不一致", offering.ID)
	}
	if prompt.NegativePrompt != nil && *prompt.NegativePrompt != hasNegativePrompt {
		return fmt.Errorf("Offering %q 的 knowledge.prompt.negative_prompt 与是否声明 negative-prompt 参数不一致", offering.ID)
	}
	return nil
}

func validateOfferingReferenceUse(offering schemacontract.Offering, uses []string) error {
	if len(uses) == 0 {
		return nil
	}
	hasReferenceImage := false
	for _, parameter := range offering.Parameters {
		if parameter.Semantic == schemacontract.SemanticReferenceImage {
			hasReferenceImage = true
			break
		}
	}
	seen := map[string]bool{}
	hasNone := false
	for _, use := range uses {
		if seen[use] {
			return fmt.Errorf("Offering %q 的 knowledge.reference_use 重复: %q", offering.ID, use)
		}
		seen[use] = true
		known := false
		for _, allowed := range offeringKnowledgeReferenceUses {
			if use == allowed {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("Offering %q 的 knowledge.reference_use 无效: %q", offering.ID, use)
		}
		hasNone = hasNone || use == schemacontract.ReferenceUseNone
	}
	if hasNone && len(uses) > 1 {
		return fmt.Errorf("Offering %q 的 knowledge.reference_use 不能把 none 与其他用途混用", offering.ID)
	}
	if hasNone != !hasReferenceImage {
		return fmt.Errorf("Offering %q 的 knowledge.reference_use 必须在没有参考图参数时声明 none，有参考图参数时不得声明 none", offering.ID)
	}
	return nil
}

// validateOfferingKnowledgeCoverage requires every enabled Offering of a
// required capability to describe itself. A statement list or prompt craft
// counts; an empty object does not.
func validateOfferingKnowledgeCoverage(schema ChatOfferingKnowledgeSchema, offerings generationMap) error {
	if len(schema.RequiredCapabilities) == 0 {
		return nil
	}
	required := make(map[schemacontract.Capability]bool, len(schema.RequiredCapabilities))
	for _, capability := range schema.RequiredCapabilities {
		if !capability.Valid() {
			return fmt.Errorf("chat.agent.offering_knowledge.required_capabilities 含未知能力 %q", capability)
		}
		required[capability] = true
	}
	ids := make([]string, 0, len(offerings))
	for id := range offerings {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	for _, id := range ids {
		offering := offerings[schemacontract.OfferingID(id)]
		if !offering.Enabled || !required[offering.Capability] {
			continue
		}
		if offering.Knowledge == nil || offeringKnowledgeEmpty(*offering.Knowledge) {
			return fmt.Errorf("Offering %q（能力 %q）缺少 knowledge 自述", offering.ID, offering.Capability)
		}
	}
	return nil
}

// offeringKnowledgeEmpty reports whether an Offering carries no Agent-visible
// self-description. A sources-only object is maintainer evidence, not a
// description the Agent can use, so it does not satisfy required_capabilities.
func offeringKnowledgeEmpty(knowledge schemacontract.OfferingKnowledge) bool {
	return len(knowledge.Strengths) == 0 && len(knowledge.Weaknesses) == 0 &&
		len(knowledge.Limitations) == 0 && knowledge.Prompt == nil &&
		len(knowledge.ReferenceUse) == 0
}

// validateOfferingKnowledgeSchema checks the Agent-facing words that render an
// Offering's prompt craft: the template placeholders and the closed
// vocabularies every projection needs. A Catalog without the section stays
// valid (historical Catalogs).
func validateOfferingKnowledgeSchema(schema ChatOfferingKnowledgeSchema) error {
	if len(schema.RequiredCapabilities) == 0 && schema.GuidanceTemplate == "" &&
		schema.NoTextTemplate == "" && schema.UnknownText == "" {
		return nil
	}
	if strings.TrimSpace(schema.UnknownText) == "" {
		return fmt.Errorf("chat.agent.offering_knowledge.unknown_text 不能为空")
	}
	if schema.GuidanceTemplate != "" {
		for _, placeholder := range []string{"{model}", "{language}", "{form}", "{length}", "{negative_prompt}", "{reads_text}", "{reference_use}"} {
			if !strings.Contains(schema.GuidanceTemplate, placeholder) {
				return fmt.Errorf("chat.agent.offering_knowledge.guidance_template 缺少占位符 %s", placeholder)
			}
		}
	}
	if schema.NoTextTemplate != "" && !strings.Contains(schema.NoTextTemplate, "{model}") {
		return fmt.Errorf("chat.agent.offering_knowledge.no_text_template 缺少 {model}")
	}
	for name, texts := range map[string]map[string]string{
		"language_texts":        schema.LanguageTexts,
		"form_texts":            schema.FormTexts,
		"negative_prompt_texts": schema.NegativePromptTexts,
		"reads_text_texts":      schema.ReadsTextTexts,
		"reference_use_texts":   schema.ReferenceUseTexts,
	} {
		if len(texts) == 0 {
			return fmt.Errorf("chat.agent.offering_knowledge.%s 不能为空", name)
		}
		for key, text := range texts {
			if strings.TrimSpace(key) == "" || strings.TrimSpace(text) == "" {
				return fmt.Errorf("chat.agent.offering_knowledge.%s 的 %q 不能为空", name, key)
			}
		}
	}
	for _, language := range []string{schemacontract.PromptLanguageEnglish, schemacontract.PromptLanguageChinese, schemacontract.PromptLanguageUnknown} {
		if _, exists := schema.LanguageTexts[language]; !exists {
			return fmt.Errorf("chat.agent.offering_knowledge.language_texts 缺少 %q", language)
		}
	}
	for _, form := range []string{schemacontract.PromptFormDescriptive, schemacontract.PromptFormKeywords, schemacontract.PromptFormUnknown} {
		if _, exists := schema.FormTexts[form]; !exists {
			return fmt.Errorf("chat.agent.offering_knowledge.form_texts 缺少 %q", form)
		}
	}
	for _, key := range []string{"true", "false", "unknown"} {
		if _, exists := schema.ReadsTextTexts[key]; !exists {
			return fmt.Errorf("chat.agent.offering_knowledge.reads_text_texts 缺少 %q", key)
		}
		if _, exists := schema.NegativePromptTexts[key]; !exists {
			return fmt.Errorf("chat.agent.offering_knowledge.negative_prompt_texts 缺少 %q", key)
		}
	}
	for _, use := range offeringKnowledgeReferenceUses {
		if _, exists := schema.ReferenceUseTexts[use]; !exists {
			return fmt.Errorf("chat.agent.offering_knowledge.reference_use_texts 缺少 %q", use)
		}
	}
	if schema.GuidanceTemplate != "" && strings.TrimSpace(schema.LayerSeparator) == "" {
		return fmt.Errorf("chat.agent.offering_knowledge.layer_separator 不能为空")
	}
	return nil
}

// validatePromptReportSchema checks application.chat.agent.prompt_report.
// Historical Catalogs without it keep silent truncation.
func validatePromptReportSchema(schema ChatPromptReportSchema) error {
	switch schema.OverLimit {
	case "", promptOverLimitTruncate, promptOverLimitRefuse:
	default:
		return fmt.Errorf("chat.agent.prompt_report.over_limit 必须是 refuse 或 truncate: %q", schema.OverLimit)
	}
	if schema.OverLimit == promptOverLimitRefuse {
		for _, placeholder := range []string{"{model}", "{length}", "{maximum}", "{excess}", "{unit}"} {
			if !strings.Contains(schema.TooLongTemplate, placeholder) {
				return fmt.Errorf("chat.agent.prompt_report.too_long_template 缺少占位符 %s", placeholder)
			}
		}
	}
	if schema.TruncationTemplate != "" {
		for _, placeholder := range []string{"{length}", "{maximum}", "{excess}", "{unit}"} {
			if !strings.Contains(schema.TruncationTemplate, placeholder) {
				return fmt.Errorf("chat.agent.prompt_report.truncation_template 缺少占位符 %s", placeholder)
			}
		}
	}
	if schema.MaximumReportedCharacters < 0 {
		return fmt.Errorf("chat.agent.prompt_report.maximum_reported_characters 不能为负数")
	}
	if schema.OverLimit != "" && len(schema.Layers) == 0 {
		return fmt.Errorf("chat.agent.prompt_report.layers 不能为空")
	}
	for key, text := range schema.Layers {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(text) == "" {
			return fmt.Errorf("chat.agent.prompt_report.layers 的 %q 不能为空", key)
		}
	}
	return nil
}
