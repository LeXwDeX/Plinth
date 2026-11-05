package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// MaxLocalModelPartsConcurrency bounds how many local split or merge jobs may
// run at once per Offering. The adapter enforces the same bound on the frozen
// ProviderOptions.
const MaxLocalModelPartsConcurrency = 8

// freezeLocalPartsProfile expands the generated model profile into the
// immutable local-model-parts Offering. Splits and merges always read Task
// model outputs, so they use the generated profile's limits, exactly like the
// local package conversion.
func freezeLocalPartsProfile(offering *schema.Offering, application ApplicationSchema) error {
	if ProtocolByAdapterKey(application.Runtime.ProviderConnections)[offering.AdapterKey] != "local-model-parts" {
		return nil
	}
	var options schema.ModelPartsOptions
	decoder := json.NewDecoder(bytes.NewReader(offering.ProviderOptions))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&options); err != nil {
		return fmt.Errorf("local model parts options: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("local model parts options must be one JSON object")
	}
	if options.ProfileLimits != nil {
		return fmt.Errorf("local model parts cannot override model_profile limits")
	}
	profile := application.Runtime.InputAssets.GeneratedModelProfile
	if profile == nil || options.ProfileID != profile.ID || options.ProfileRevision != profile.Revision {
		return fmt.Errorf("local model parts references an unknown generated_model_profile")
	}
	if options.MaxWork <= 0 || options.MaxWork > 1<<32 {
		return fmt.Errorf("invalid local model parts work bound")
	}
	if options.MaxConcurrent <= 0 || options.MaxConcurrent > MaxLocalModelPartsConcurrency {
		return fmt.Errorf("invalid local model parts concurrency bound")
	}
	if offering.Capability != schema.ModelSplit && offering.Capability != schema.ModelMerge {
		return fmt.Errorf("local model parts must offer model split or merge")
	}
	if offering.CreditsMetered || offering.EstimatedCredits != 0 {
		return fmt.Errorf("local model parts must be an unmetered zero-credit capability")
	}
	limits := profile.Limits
	options.ProfileLimits = &limits
	encoded, err := json.Marshal(options)
	if err != nil {
		return err
	}
	offering.ProviderOptions = encoded
	return nil
}
