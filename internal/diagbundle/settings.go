package diagbundle

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"slices"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// SettingsFromCatalog lists the runtime settings a Catalog declares, by name,
// and the variables it retired. The bundle's settings.json is built from this
// list, so it follows the Catalog the process runs and never a copy kept here.
func SettingsFromCatalog(schema catalog.ApplicationSchema) ([]Setting, []string) {
	declared := schema.Runtime.Configuration.Settings
	settings := make([]Setting, 0, len(declared))
	for _, name := range slices.Sorted(maps.Keys(declared)) {
		setting := Setting{Name: name, Secret: declared[name].Secret}
		for _, source := range declared[name].Environment {
			setting.Environment = append(setting.Environment, EnvironmentName{Name: source.Name, Deprecated: source.Deprecated})
		}
		settings = append(settings, setting)
	}
	return settings, slices.Clone(schema.Runtime.Configuration.RetiredEnvironment)
}

// DocumentSHA256 is the digest by which a Catalog document is named in the
// bundle.
func DocumentSHA256(document []byte) string {
	sum := sha256.Sum256(document)
	return hex.EncodeToString(sum[:])
}
