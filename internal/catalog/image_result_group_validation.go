package catalog

import (
	"fmt"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// A public image group must describe the Offering's real output identities.
// Delivery Group has a separate meaning and is not consulted here.
func validateImageResultGroupOffering(app ApplicationSchema, offering schema.Offering) error {
	var group *ImageResultGroupSchema
	for _, capability := range app.Capabilities {
		if capability.ID == offering.Capability {
			group = capability.ImageResultGroup
			break
		}
	}
	if group == nil {
		return nil
	}
	wanted := make(map[string]bool, len(group.Slots))
	for _, slot := range group.Slots {
		wanted[slot.Variant] = true
	}
	seen := map[string]bool{}
	for _, output := range offering.Outputs {
		if output.Role != "image" {
			continue
		}
		if !wanted[output.Variant] || seen[output.Variant] || output.VariantFrom != "" || output.VariantPrefix != "" || output.Delivery != schema.AssetDeliveryRequired || output.Minimum == nil || *output.Minimum != 1 {
			return fmt.Errorf("image_result_group 与图片输出槽位不一致: %q", output.Variant)
		}
		if group.SourceSelectable && output.RequiredWhen != nil {
			return fmt.Errorf("image_result_group 来源选择要求固定完整图片输出")
		}
		if output.RequiredWhen != nil && group.PartialMessage == "" {
			return fmt.Errorf("image_result_group 条件图片输出需要声明 partial_message")
		}
		seen[output.Variant] = true
	}
	if len(seen) != len(wanted) {
		return fmt.Errorf("image_result_group 缺少图片输出槽位")
	}
	return nil
}
