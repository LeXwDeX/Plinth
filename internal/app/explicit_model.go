package app

import (
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// explicitModel3DValid reports whether model names an enabled Offering of the
// 3D domain (#103 review C4). The browser's model picker sends the Offering's
// model_id; the generation tools also match the Offering id and the
// "provider/model" pair, so the lock accepts the same three forms. A model
// that names no 3D Offering would make every 3D tool refuse the session, so the
// chat service refuses it at write time.
func explicitModel3DValid(offerings []generation.Offering, model string) bool {
	return explicitModelValid(offerings, model, generation.DomainModel3D)
}

// explicitModelImageValid reports whether model names an enabled Offering of
// the image domain (#239), by the same three forms the picker and the tools
// use. A model that names no image Offering would make every image tool refuse
// the session, so the chat service refuses it at write time.
func explicitModelImageValid(offerings []generation.Offering, model string) bool {
	return explicitModelValid(offerings, model, generation.DomainImage)
}

func explicitModelValid(offerings []generation.Offering, model string, domain generation.CapabilityDomain) bool {
	model = strings.TrimSpace(model)
	if model == "" {
		return false
	}
	for _, offering := range offerings {
		if !offering.Enabled || offering.Domain != domain {
			continue
		}
		if model == offering.ModelID || model == string(offering.ID) {
			return true
		}
		if offering.ModelID != "" && offering.ProviderID != "" &&
			model == offering.ProviderID+"/"+offering.ModelID {
			return true
		}
	}
	return false
}
