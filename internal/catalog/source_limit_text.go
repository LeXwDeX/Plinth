package catalog

import (
	"fmt"
	"strconv"
	"strings"
)

// sourceLimitPlaceholder stands for the triangle ceiling an Offering declares
// once in source_model_limit (#121). Requirement copy spells it as {limit}
// instead of repeating the number, so the number has one place to change.
const sourceLimitPlaceholder = "{limit}"

// expandSourceModelLimitTexts fills {limit} in uploaded_model.requirements
// from the same Offering's source_model_limit, and gives every translation of
// such a text its expanded key and value. Translations are looked up by the
// exact default-language text, so they must be expanded together with it; this
// runs before any validation, which then sees ordinary finished copy. A
// placeholder with no source_model_limit to fill it from is an error.
func expandSourceModelLimitTexts(doc *document) error {
	type expansion struct{ text, limit string }
	expansions := map[string][]expansion{}
	for index := range doc.Offerings {
		offering := &doc.Offerings[index]
		for _, parameter := range offering.Parameters {
			model := parameter.UploadedModel
			if model == nil || !strings.Contains(model.Requirements, sourceLimitPlaceholder) {
				continue
			}
			if offering.SourceModelLimit == nil {
				return fmt.Errorf("offerings[%d]: 参数 %q 的 uploaded_model.requirements 使用 %s，但 Offering 没有声明 source_model_limit", index, parameter.Name, sourceLimitPlaceholder)
			}
			template := model.Requirements
			limit := strconv.FormatInt(offering.SourceModelLimit.MaxTriangles, 10)
			model.Requirements = strings.ReplaceAll(template, sourceLimitPlaceholder, limit)
			known := false
			for _, existing := range expansions[template] {
				known = known || existing.limit == limit
			}
			if !known {
				expansions[template] = append(expansions[template], expansion{model.Requirements, limit})
			}
		}
	}
	if len(expansions) == 0 || doc.Application == nil || doc.Application.Localization == nil {
		return nil
	}
	for _, bundle := range doc.Application.Localization.Bundles {
		for template, variants := range expansions {
			translated, translatedOK := bundle.OfferingTexts[template]
			if !translatedOK {
				continue
			}
			delete(bundle.OfferingTexts, template)
			for _, variant := range variants {
				bundle.OfferingTexts[variant.text] = strings.ReplaceAll(translated, sourceLimitPlaceholder, variant.limit)
			}
		}
	}
	return nil
}
