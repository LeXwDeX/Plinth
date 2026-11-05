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
	model = strings.TrimSpace(model)
	if model == "" {
		return false
	}
	for _, offering := range offerings {
		if !offering.Enabled || offering.Domain != generation.DomainModel3D {
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
