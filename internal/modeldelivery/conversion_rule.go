package modeldelivery

import (
	"encoding/json"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// SchemaConversionRule recognizes the Product Schema's declared format
// conversions for the task kernel. Generation never names a conversion
// Offering or Provider itself; the App injects this rule from
// application.model_delivery.conversions.
type SchemaConversionRule struct {
	conversions []catalog.ModelConversionSchema
}

func NewSchemaConversionRule(delivery catalog.ModelDeliverySchema) *SchemaConversionRule {
	return &SchemaConversionRule{conversions: append([]catalog.ModelConversionSchema(nil), delivery.Conversions...)}
}

func (r *SchemaConversionRule) IsConversion(offering generation.OfferingID, input json.RawMessage) bool {
	if r == nil {
		return false
	}
	_, ok := ConversionInput(r.conversions, offering, input)
	return ok
}

// ConversionIntent names the exact model source and target formats of a
// declared conversion. The frozen Offering supplies the source parameter's
// semantic; the conversion declaration supplies what the run provides. A per
// format run names its one format; a whole run names every provided format.
func (r *SchemaConversionRule) ConversionIntent(offering generation.Offering, input json.RawMessage) (generation.ConversionIntent, bool) {
	if r == nil {
		return generation.ConversionIntent{}, false
	}
	format, ok := ConversionInput(r.conversions, offering.ID, input)
	if !ok {
		return generation.ConversionIntent{}, false
	}
	formats := []string{}
	if format != "" {
		formats = append(formats, format)
	} else {
		for _, conversion := range r.conversions {
			if conversion.OfferingID == string(offering.ID) && len(conversion.Provides) > 0 {
				formats = append(formats, conversion.Provides...)
				break
			}
		}
	}
	if len(formats) == 0 {
		return generation.ConversionIntent{}, false
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(input, &values) != nil {
		return generation.ConversionIntent{}, false
	}
	taskID, assetID := conversionSource(offering, values)
	if taskID == "" && assetID == "" {
		return generation.ConversionIntent{}, false
	}
	return generation.ConversionIntent{SourceTaskID: taskID, SourceAssetID: assetID, Formats: formats}, true
}
