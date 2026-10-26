package meshy

import (
	"fmt"
	"mime"
	"slices"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// A declared scalar MIME binds one exact output slot independently of the
// locator extension or a generic CDN response. Byte validation stays in the
// materializer and asset store. Old snapshots keep their original inference.
func validateResultMediaType(offering generation.Offering, projection resultAssetProjection) error {
	if projection.MediaType == "" {
		return nil
	}
	media, parameters, err := mime.ParseMediaType(projection.MediaType)
	if err != nil || media != projection.MediaType || len(parameters) != 0 ||
		!strings.Contains(media, "/") || strings.Contains(media, "*") ||
		projection.Mode != "scalar" || projection.Variant == "" {
		return fmt.Errorf("media_type requires a scalar exact MIME and variant")
	}
	matched := 0
	for _, output := range offering.Outputs {
		if output.Role != projection.Role || output.Variant != projection.Variant || output.VariantPrefix != "" {
			continue
		}
		matched++
		if !slices.Contains(output.MediaTypes, media) {
			return fmt.Errorf("media_type differs from its output contract")
		}
	}
	if matched != 1 {
		return fmt.Errorf("media_type requires one exact output contract")
	}
	return nil
}
