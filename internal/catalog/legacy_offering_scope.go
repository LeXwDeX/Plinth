package catalog

import (
	"fmt"
	"slices"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// LegacyOfferingApplies matches stable Offering identity, never a Provider name.
// An empty scope preserves existing capability-wide declarations.
func LegacyOfferingApplies(ids []schema.OfferingID, id schema.OfferingID) bool {
	return len(ids) == 0 || slices.Contains(ids, id)
}

func validateLegacyOfferingScopes(application ApplicationSchema, offerings generationMap) error {
	for _, capability := range application.Capabilities {
		legacy := capability.LegacyAPI
		if legacy == nil {
			continue
		}
		validate := func(ids []schema.OfferingID) error {
			seen := make(map[schema.OfferingID]bool, len(ids))
			for _, id := range ids {
				offering, exists := offerings[id]
				if strings.TrimSpace(string(id)) == "" || seen[id] || !exists || offering.Capability != capability.ID {
					return fmt.Errorf("能力 %q 的 legacy offering_ids 引用无效或重复: %q", capability.ID, id)
				}
				seen[id] = true
			}
			return nil
		}
		for _, field := range legacy.Fields {
			if err := validate(field.OfferingIDs); err != nil {
				return err
			}
		}
		for _, alias := range legacy.AssetAliases {
			if err := validate(alias.OfferingIDs); err != nil {
				return err
			}
		}
		for _, alias := range legacy.TaskAliases {
			if err := validate(alias.OfferingIDs); err != nil {
				return err
			}
		}
		if legacy.Multipart != nil {
			if err := validate(legacy.Multipart.OfferingIDs); err != nil {
				return err
			}
		}
		for _, collection := range legacy.ResultCollections {
			if err := validate(collection.OfferingIDs); err != nil {
				return err
			}
		}
		for _, asset := range legacy.ResultAssets {
			if err := validate(asset.OfferingIDs); err != nil {
				return err
			}
			for id, variant := range asset.OfferingVariants {
				offering, exists := offerings[id]
				if !exists || offering.Capability != capability.ID || len(asset.OfferingIDs) == 0 || !LegacyOfferingApplies(asset.OfferingIDs, id) || strings.TrimSpace(variant) == "" {
					return fmt.Errorf("能力 %q 的 legacy offering_variants 引用无效: %q", capability.ID, id)
				}
				matches := 0
				for _, output := range offering.Outputs {
					if output.Role == asset.Role && output.Variant == variant && output.VariantPrefix == "" {
						matches++
					}
				}
				if matches != 1 {
					return fmt.Errorf("能力 %q 的 legacy offering_variants %q 必须匹配唯一输出槽位", capability.ID, id)
				}
			}
		}
	}
	return nil
}
