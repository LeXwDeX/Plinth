package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// freezeLocalConversionProfile expands a single Product Schema profile into the
// immutable Offering. Recovery therefore uses the accepted resource contract,
// even if a later Catalog changes the current upload profile's limits.
func freezeLocalConversionProfile(offering *schema.Offering, application ApplicationSchema) error {
	if ProtocolByAdapterKey(application.Runtime.ProviderConnections)[offering.AdapterKey] != "local-model" {
		return nil
	}
	var options schema.ModelConversionOptions
	decoder := json.NewDecoder(bytes.NewReader(offering.ProviderOptions))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&options); err != nil {
		return fmt.Errorf("local model conversion options: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("local model conversion options must be one JSON object")
	}
	if options.ProfileLimits != nil {
		return fmt.Errorf("local model conversion cannot override model_profile limits")
	}
	// Upload-profile conversions keep the upload limits. Only the package
	// direction, whose source is a stored Task model, uses the generated profile.
	var limits schema.ModelInputLimits
	switch options.Direction {
	case schema.ModelConversionGLBToGLTF, schema.ModelConversionGLTFToGLB:
		profile := application.Runtime.InputAssets.ModelProfile
		if profile == nil || options.ProfileID != profile.ID || options.ProfileRevision != profile.Revision {
			return fmt.Errorf("local model conversion references an unknown model_profile")
		}
		limits = profile.Limits
	case schema.ModelConversionGLBToGLTFPackage:
		profile := application.Runtime.InputAssets.GeneratedModelProfile
		if profile == nil || options.ProfileID != profile.ID || options.ProfileRevision != profile.Revision {
			return fmt.Errorf("local model package conversion references an unknown generated_model_profile")
		}
		limits = profile.Limits
	default:
		return fmt.Errorf("unknown local model conversion direction")
	}
	if options.MaxWork <= 0 || options.MaxWork > 1<<32 {
		return fmt.Errorf("invalid local model conversion work bound")
	}
	if offering.Capability != schema.FormatConversion || offering.CreditsMetered || offering.EstimatedCredits != 0 {
		return fmt.Errorf("local model conversion must be an unmetered format conversion")
	}
	options.ProfileLimits = &limits
	encoded, err := json.Marshal(options)
	if err != nil {
		return err
	}
	offering.ProviderOptions = encoded
	return nil
}
