package modeldelivery

import (
	"encoding/json"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// The version chain folds a format conversion into the version it converted
// (#330 second follow-ups): a conversion does not change the model, so the
// source version keeps the original model for preview, the Formats tab and
// the ZIP, and the converted files are files of that version. These methods
// make the Service the lineage ConversionRule; they read the same Product
// Schema conversions the Formats tab plans with.

// ConversionOffering reports an Offering that a declared conversion runs on.
func (s *Service) ConversionOffering(offering generation.OfferingID) bool {
	if s == nil {
		return false
	}
	for _, conversion := range s.schema.Conversions {
		if conversion.OfferingID == string(offering) {
			return true
		}
	}
	return false
}

// IsConversion reports a Task whose Offering and frozen input run a declared
// conversion: a Formats-tab completion, or the Agent running the same
// conversion directly.
func (s *Service) IsConversion(offering generation.OfferingID, input json.RawMessage) bool {
	if s == nil {
		return false
	}
	_, ok := s.conversionInput(offering, input)
	return ok
}
