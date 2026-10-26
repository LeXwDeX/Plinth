package resolve

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/assets/modelsource"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
)

// SourceView exposes owned upload metadata only. Storage paths, owners and
// execution settings remain private; absent or foreign sources expose no row.
type SourceView struct {
	Kind             harness.SourceKind `json:"kind"`
	AssetID          generation.AssetID `json:"asset_id"`
	OriginalFilename string             `json:"original_filename"`
	MediaType        string             `json:"media_type"`
	Size             int64              `json:"size"`
	ProfileID        string             `json:"profile_id,omitempty"`
	State            string             `json:"state"`
	Reason           string             `json:"reason,omitempty"`
}

func (s Service) uploadedSource(ctx context.Context, selected harness.Selection, offering generation.Offering) (generation.InputAsset, modelinput.Profile, error) {
	if len(selected.Sources) != 1 {
		return generation.InputAsset{}, modelinput.Profile{}, invalid("exactly one uploaded source is required")
	}
	source := selected.Sources[0]
	if source.Kind != harness.SourceInputAsset || source.TaskID != "" || source.AssetID == "" {
		return generation.InputAsset{}, modelinput.Profile{}, invalid("invalid uploaded source identity")
	}
	if s.UploadedModels == nil {
		return generation.InputAsset{}, modelinput.Profile{}, ErrUnavailable
	}
	var parameter *generation.Parameter
	for _, p := range offering.Parameters {
		if p.Name == source.Parameter {
			copy := p
			parameter = &copy
			break
		}
	}
	if parameter == nil || parameter.UploadedModel == nil {
		return generation.InputAsset{}, modelinput.Profile{}, invalid("uploaded source route is not declared")
	}
	if (parameter.UploadedModel.ProfileRevision == "") != (parameter.UploadedModel.ProfileLimits == nil) {
		return generation.InputAsset{}, modelinput.Profile{}, invalid("incomplete uploaded profile snapshot")
	}
	var asset generation.InputAsset
	var profile modelinput.Profile
	var err error
	if inspector, ok := s.UploadedModels.(ParameterUploadedModels); ok {
		asset, profile, err = inspector.InspectForParameter(ctx, generation.OwnerID(selected.OwnerID), generation.AssetID(source.AssetID), *parameter)
	} else {
		if parameter.UploadedModel.ProfileRevision != "" || parameter.UploadedModel.ProfileLimits != nil {
			return generation.InputAsset{}, modelinput.Profile{}, ErrUnavailable
		}
		asset, profile, err = s.UploadedModels.Inspect(ctx, generation.OwnerID(selected.OwnerID), generation.AssetID(source.AssetID))
	}

	if err != nil {
		return generation.InputAsset{}, modelinput.Profile{}, uploadedPolicyError(*parameter, err)
	}
	if asset.OwnerID != generation.OwnerID(selected.OwnerID) || asset.ID != generation.AssetID(source.AssetID) || asset.SHA256 == "" || asset.SHA256 != profile.SHA256 || asset.Size != profile.Size {
		return generation.InputAsset{}, modelinput.Profile{}, harness.ErrStale
	}
	if err := modelsource.ValidateRoute(*parameter, profile); err != nil {
		return generation.InputAsset{}, modelinput.Profile{}, uploadedPolicyError(*parameter, err)
	}
	return asset, profile, nil
}

// A malformed frozen declaration is a snapshot rejection, not a transient
// database/read failure. Preserve every infrastructure error unchanged.
func uploadedPolicyError(parameter generation.Parameter, err error) error {
	if parameter.UploadedModel != nil && (parameter.UploadedModel.ProfileRevision != "" || parameter.UploadedModel.ProfileLimits != nil) {
		var domain *generation.Error
		if errors.As(err, &domain) && domain.Code == generation.ErrInvalidInput {
			return invalid("invalid uploaded profile snapshot")
		}
	}
	return err
}

func (s Service) resolveUploadedSnapshot(ctx context.Context, snapshot *Snapshot, offering generation.Offering) (bool, error) {
	asset, profile, err := s.uploadedSource(ctx, snapshot.Selection, offering)
	if err != nil {
		var domain *generation.Error
		if errors.As(err, &domain) {
			switch domain.Code {
			case generation.ErrNotFound, generation.ErrInvalidModelInput, generation.ErrUnsupportedModelInput, generation.ErrModelInputLimit, generation.ErrConflict:
				return false, nil
			}
		}
		if errors.Is(err, harness.ErrStale) {
			return false, nil
		}
		return false, err
	}
	source := snapshot.Selection.Sources[0]
	snapshot.Input[source.Parameter], err = json.Marshal(string(asset.ID))
	if err != nil {
		return false, err
	}
	snapshot.SourceFingerprints = append(snapshot.SourceFingerprints, SourceFingerprint{Kind: harness.SourceInputAsset, Parameter: source.Parameter, AssetID: asset.ID, SHA256: asset.SHA256, Size: asset.Size, ProfileID: profile.ProfileID, ProfileRevision: profile.Revision})
	snapshot.SourceViews = append(snapshot.SourceViews, SourceView{Kind: harness.SourceInputAsset, AssetID: asset.ID, OriginalFilename: asset.OriginalFilename, MediaType: asset.MediaType, Size: asset.Size, ProfileID: profile.ProfileID, State: StatusReady})
	return true, nil
}

// VerifyUploadedSource compares fresh owned bytes with the frozen turn proof.
// It never replaces the original snapshot or consults a newer selection.
func (s Service) VerifyUploadedSource(ctx context.Context, owner string, snapshot Snapshot, offering generation.Offering, input map[string]json.RawMessage) error {
	if len(snapshot.SourceFingerprints) != 1 || len(snapshot.Selection.Sources) != 1 {
		return harness.ErrStale
	}
	selected := snapshot.Selection
	selected.OwnerID = owner
	source, proof := selected.Sources[0], snapshot.SourceFingerprints[0]
	if proof.Kind != harness.SourceInputAsset || proof.TaskID != "" || proof.Variant != "" || proof.Parameter != source.Parameter || proof.AssetID != generation.AssetID(source.AssetID) {
		return harness.ErrStale
	}
	asset, profile, err := s.uploadedSource(ctx, selected, offering)
	if err != nil {
		return err
	}
	if proof.SHA256 != asset.SHA256 || proof.Size != asset.Size || proof.ProfileID != profile.ProfileID || proof.ProfileRevision != profile.Revision {
		return harness.ErrStale
	}
	var value string
	if json.Unmarshal(input[source.Parameter], &value) != nil || value != string(asset.ID) {
		return harness.ErrStale
	}
	return nil
}
