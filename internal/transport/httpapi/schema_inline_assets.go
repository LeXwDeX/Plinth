package httpapi

import (
	"bytes"
	"context"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"strings"
)

// persistSchemaInlineAssets resolves any declared asset parameter, including optional masks.
// Existing asset IDs remain subject to the generation module's ownership checks.
func (s *Server) persistSchemaInlineAssets(ctx context.Context, owner generation.OwnerID, input map[string]any, offering generation.Offering) error {
	for _, parameter := range offering.Parameters {
		if parameter.Type != "asset" && parameter.Type != "asset-array" {
			continue
		}
		raw, exists := input[parameter.Name]
		if !exists {
			continue
		}
		var items []any
		if parameter.Type == "asset" {
			items = []any{raw}
		} else {
			var ok bool
			items, ok = raw.([]any)
			if !ok {
				continue
			}
		}
		for i, item := range items {
			value, ok := item.(string)
			if !ok || !strings.HasPrefix(value, "data:") {
				continue
			}
			if s.inputAssets == nil {
				return invalidLegacyInlineAsset()
			}
			decoded, err := decodeLegacyDataURI(value)
			if err != nil {
				return err
			}
			asset, err := s.inputAssets.UploadAsset(ctx, owner, generation.InputAssetUpload{Body: bytes.NewReader(decoded.content), AssetType: s.inputAssetType(decoded.mediaType, ""), MediaType: decoded.mediaType, MaxBytes: parameter.MaxBytes})
			if err != nil {
				return err
			}
			items[i] = string(asset.ID)
		}
		if parameter.Type == "asset" {
			input[parameter.Name] = items[0]
		} else {
			input[parameter.Name] = items
		}
	}
	return nil
}
