package generation

import (
	"context"
	"encoding/json"
	"image"
	_ "image/jpeg" // read derived JPEG headers
	_ "image/png"  // read derived PNG headers
	"io"
	"strings"
)

// InputDerivationView is the read-only account of one frozen input image that
// was re-encoded to fit its Offering parameter (#276); Index is its position
// in that parameter's value. Only the Task owner's assets are named:
// SourceAssetID and SourceMediaType are omitted when the source is no longer
// the owner's readable asset. Storage keys, digests and
// upstream identifiers are never part of it. Width and Height come from the
// derived image's header and are omitted when it cannot be read; re-encoding
// never changes them, so they are also the source's dimensions.
type InputDerivationView struct {
	Parameter       string  `json:"parameter"`
	Index           int     `json:"index"`
	AssetID         AssetID `json:"asset_id"`
	MediaType       string  `json:"media_type"`
	Size            int64   `json:"size"`
	Width           int     `json:"width,omitempty"`
	Height          int     `json:"height,omitempty"`
	Method          string  `json:"method"`
	SourceAssetID   AssetID `json:"source_asset_id,omitempty"`
	SourceMediaType string  `json:"source_media_type,omitempty"`
	SourceSize      int64   `json:"source_size"`
}

// inputDerivationStore reads derivation records and assets within one owner.
type inputDerivationStore interface {
	InputAssetDerivation(context.Context, OwnerID, AssetID) (InputAssetDerivation, bool, error)
	LoadInputAssets(context.Context, OwnerID, []AssetID) (map[AssetID]InputAsset, error)
}

// headerReadLimit bounds the bytes read to find an image's dimensions.
const headerReadLimit = 1 << 20

// TaskInputDerivations lists, in parameter order, the frozen input images of
// a Task that are derived copies, with their source, method, sizes and
// dimensions. Every lookup is scoped to the Task owner. A Task without such
// inputs, or a repository that keeps no records, yields nil.
func (m *Module) TaskInputDerivations(ctx context.Context, task Task) ([]InputDerivationView, error) {
	store, ok := m.repository.(inputDerivationStore)
	if !ok || len(task.Input) == 0 || task.OwnerID == "" {
		return nil, nil
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(task.Input, &input); err != nil {
		return nil, nil
	}
	var views []InputDerivationView
	for _, parameter := range task.Offering.Parameters {
		if parameter.Type != "asset" && parameter.Type != "asset-array" {
			continue
		}
		for index, id := range frozenAssetIDs(input[parameter.Name]) {
			derivation, found, err := store.InputAssetDerivation(ctx, task.OwnerID, id)
			if err != nil {
				return nil, mapRepositoryError("input_derivations", err)
			}
			if !found {
				continue
			}
			assets, err := store.LoadInputAssets(ctx, task.OwnerID, []AssetID{id, derivation.SourceAssetID})
			if err != nil {
				return nil, mapRepositoryError("input_derivations", err)
			}
			derived, ok := assets[id]
			if !ok {
				continue
			}
			view := InputDerivationView{
				Parameter: parameter.Name, Index: index, AssetID: derived.ID, MediaType: derived.MediaType, Size: derived.Size,
				Method: derivation.Method, SourceSize: derivation.SourceSize,
			}
			if source, ok := assets[derivation.SourceAssetID]; ok {
				view.SourceAssetID, view.SourceMediaType = source.ID, source.MediaType
			}
			view.Width, view.Height = m.inputDimensions(ctx, derived)
			views = append(views, view)
		}
	}
	return views, nil
}

func (m *Module) inputDimensions(ctx context.Context, asset InputAsset) (int, int) {
	if m.inputs == nil {
		return 0, 0
	}
	body, err := m.inputs.Open(ctx, asset)
	if err != nil {
		return 0, 0
	}
	defer body.Close()
	config, _, err := image.DecodeConfig(io.LimitReader(body, headerReadLimit))
	if err != nil {
		return 0, 0
	}
	return config.Width, config.Height
}

func frozenAssetIDs(raw json.RawMessage) []AssetID {
	var single string
	if json.Unmarshal(raw, &single) == nil {
		if strings.TrimSpace(single) == "" {
			return nil
		}
		return []AssetID{AssetID(single)}
	}
	var values []string
	if json.Unmarshal(raw, &values) != nil {
		return nil
	}
	ids := make([]AssetID, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			ids = append(ids, AssetID(value))
		}
	}
	return ids
}
