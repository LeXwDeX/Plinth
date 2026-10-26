package app

import (
	"fmt"

	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/assets/modelsource"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func modelInputValidation(profile *catalog.ModelInputProfile) (inputassets.UploadValidator, modelinput.Policy, error) {
	if profile == nil {
		return nil, modelinput.Policy{}, nil
	}
	l := profile.Limits
	policy := modelinput.Policy{ProfileID: profile.ID, Revision: profile.Revision, Limits: modelinput.Limits{
		MaxBytes: l.MaxBytes, MaxJSONBytes: l.MaxJSONBytes, MaxDepth: l.MaxDepth, MaxElements: l.MaxElements,
		MaxVertices: l.MaxVertices, MaxTriangles: l.MaxTriangles, MaxNodes: l.MaxNodes, MaxWork: l.MaxWork,
		MaxImagePixels: l.MaxImagePixels, MaxTotalImagePixels: l.MaxTotalImagePixels}}
	if err := policy.Validate(); err != nil {
		return nil, modelinput.Policy{}, err
	}
	if !policy.Uploadable() {
		return nil, modelinput.Policy{}, fmt.Errorf("model_profile %q is not an upload profile", profile.ID)
	}
	return modelsource.UploadValidator(policy, profile.AssetType, profile.Extensions, profile.MediaTypes), policy, nil
}
