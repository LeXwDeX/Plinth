package meshy

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

type resultIdentity struct {
	ValueField  string `json:"value_field"`
	PairedField string `json:"paired_field"`
	Role        string `json:"role"`
	Variant     string `json:"variant"`
}

type resultIdentityEnvelope struct {
	ValueField  string                `json:"value_field"`
	PairedField string                `json:"paired_field"`
	Version     int                   `json:"version"`
	TaskID      generation.TaskID     `json:"task_id"`
	AttemptID   generation.AttemptID  `json:"attempt_id"`
	OfferingID  generation.OfferingID `json:"offering_id"`
	Revision    string                `json:"revision"`
	ProviderID  string                `json:"provider_id"`
	RemoteID    string                `json:"remote_id"`
	ClaimID     string                `json:"claim_id"`
	Role        string                `json:"role"`
	Variant     string                `json:"variant"`
	Candidate   string                `json:"candidate"`
}

type resultDependencyBinding struct {
	Identity resultIdentityEnvelope `json:"identity"`
	AssetID  generation.AssetID     `json:"asset_id"`
	SHA256   string                 `json:"sha256"`
	Size     int64                  `json:"size"`
}

func decodePrivateIdentity(raw json.RawMessage, target any) error {
	canonical, err := generation.CanonicalDependencyBinding(raw)
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(canonical))
	d.DisallowUnknownFields()
	return d.Decode(target)
}

func validateResultIdentity(offering generation.Offering, options wireOptions) error {
	i := options.ResultIdentity
	if i == nil {
		for _, o := range offering.Outputs {
			if o.RetainDependencyIdentity {
				return errors.New("retained output requires result_identity")
			}
		}
		return nil
	}
	if !inputPathSegment.MatchString(i.ValueField) || !inputPathSegment.MatchString(i.PairedField) || i.ValueField == i.PairedField || i.Role == "" || i.Variant != "1" || options.TaskType == "" {
		return errors.New("invalid result_identity declaration")
	}
	count := 0
	for _, p := range options.ResultAssets {
		if p.Path == i.PairedField && p.Mode == "array" && p.Role == i.Role {
			count++
		}
	}
	slot := false
	for _, o := range offering.Outputs {
		if o.RetainDependencyIdentity && (o.Role != i.Role || o.Variant != i.Variant) {
			return errors.New("unpaired retained output")
		}
		if o.Role == i.Role && o.Variant == i.Variant && o.Minimum != nil && *o.Minimum == 1 && o.Delivery == "required" && o.RetainDependencyIdentity {
			slot = true
		}
	}
	if count != 1 || !slot {
		return errors.New("result_identity requires an exact required preview slot")
	}
	return nil
}

func captureResultIdentity(request generation.ObserveRequest, response taskResponse, options wireOptions, claims []generation.ArtifactClaim) error {
	i := options.ResultIdentity
	if i == nil {
		return nil
	}
	var candidates, previews []string
	if request.TaskID == "" || request.AttemptID == "" || request.Offering.ID == "" || request.Offering.Revision == "" || request.Job.ProviderID != request.Offering.ProviderID || response.ID != request.Job.RemoteID || json.Unmarshal(response.Raw[i.ValueField], &candidates) != nil || len(candidates) != 1 || !validSourceResultValue(candidates[0]) || json.Unmarshal(response.Raw[i.PairedField], &previews) != nil || len(previews) != 1 || strings.TrimSpace(previews[0]) == "" || len(previews[0]) > 16384 {
		return errors.New("invalid result identity evidence")
	}
	match := -1
	for n, c := range claims {
		if c.Role == i.Role && c.Variant == i.Variant {
			if match != -1 || c.ID == "" || c.Locator != previews[0] {
				return errors.New("ambiguous paired preview claim")
			}
			match = n
		}
	}
	if match < 0 {
		return errors.New("missing paired preview claim")
	}
	c := &claims[match]
	raw, err := json.Marshal(resultIdentityEnvelope{ValueField: i.ValueField, PairedField: i.PairedField, Version: 1, TaskID: request.TaskID, AttemptID: request.AttemptID, OfferingID: request.Offering.ID, Revision: request.Offering.Revision, ProviderID: request.Offering.ProviderID, RemoteID: request.Job.RemoteID, ClaimID: c.ID, Role: c.Role, Variant: c.Variant, Candidate: candidates[0]})
	if err != nil {
		return err
	}
	c.Metadata, err = generation.CanonicalDependencyBinding(raw)
	return err
}

// ResolveDependencyBinding projects only the exact completed claim and its
// delivered local bytes. There is deliberately no HTTP fallback for old Tasks.
func (a *Adapter) ResolveDependencyBinding(request generation.DependencyBindingRequest) (json.RawMessage, error) {
	invalid := errors.New("invalid source result binding")
	var options wireOptions
	if request.TaskID == "" || request.AttemptID == "" || request.Job.RemoteID == "" || request.Offering.ID == "" || request.Offering.Revision == "" || json.Unmarshal(request.Offering.ProviderOptions, &options) != nil || options.ResultIdentity == nil || validateResultIdentity(request.Offering, options) != nil {
		return nil, invalid
	}
	i := options.ResultIdentity
	var envelope resultIdentityEnvelope
	var stored *generation.Asset
	claimCount := 0
	for _, c := range request.Claims {
		if c.Role != i.Role || c.Variant != i.Variant {
			continue
		}
		claimCount++
		stored = c.StoredAsset
		if decodePrivateIdentity(c.Metadata, &envelope) != nil || envelope.ClaimID != c.ID || envelope.Role != c.Role || envelope.Variant != c.Variant {
			return nil, invalid
		}
	}
	if claimCount != 1 || envelope.ValueField != i.ValueField || envelope.PairedField != i.PairedField || envelope.Version != 1 || envelope.TaskID != request.TaskID || envelope.AttemptID != request.AttemptID || envelope.OfferingID != request.Offering.ID || envelope.Revision != request.Offering.Revision || envelope.ProviderID != request.Offering.ProviderID || envelope.ProviderID != request.Job.ProviderID || envelope.RemoteID != request.Job.RemoteID || !validSourceResultValue(envelope.Candidate) {
		return nil, invalid
	}
	var paired generation.Asset
	count := 0
	for _, asset := range request.Assets {
		if asset.Role == i.Role && asset.Variant == i.Variant {
			paired = asset
			count++
		}
	}
	if count != 1 || !validPairedAsset(paired, envelope) || stored == nil || !sameRetainedAsset(paired, *stored) {
		return nil, invalid
	}
	raw, err := json.Marshal(resultDependencyBinding{Identity: envelope, AssetID: paired.ID, SHA256: paired.SHA256, Size: paired.Size})
	if err != nil {
		return nil, err
	}
	return generation.CanonicalDependencyBinding(raw)
}

func validPairedAsset(asset generation.Asset, identity resultIdentityEnvelope) bool {
	sha, err := hex.DecodeString(asset.SHA256)
	return asset.ID != "" && asset.TaskID == identity.TaskID && asset.AttemptID == identity.AttemptID && asset.Role == identity.Role && asset.Variant == identity.Variant && asset.Size > 0 && err == nil && len(sha) == 32 && strings.ToLower(asset.SHA256) == asset.SHA256
}

func frozenSourceCandidate(dependency generation.TaskDependency, declaration sourceResultBinding) (string, error) {
	var b resultDependencyBinding
	if decodePrivateIdentity(dependency.ProviderBinding, &b) != nil {
		return "", errors.New("missing frozen candidate")
	}
	i := b.Identity
	if i.ValueField != declaration.ValueField || i.PairedField != declaration.PairedField || i.Version != 1 || i.TaskID != dependency.TaskID || i.OfferingID != dependency.OfferingID || i.ProviderID != dependency.ProviderID || i.RemoteID != dependency.UpstreamJob.RemoteID || i.ProviderID != dependency.UpstreamJob.ProviderID || i.Revision == "" || i.ClaimID == "" || !validSourceResultValue(i.Candidate) {
		return "", errors.New("conflicting frozen candidate")
	}
	count := 0
	for _, asset := range dependency.Assets {
		if asset.Role == i.Role && asset.Variant == i.Variant {
			count++
			if !validPairedAsset(asset, i) || asset.ID != b.AssetID || asset.SHA256 != b.SHA256 || asset.Size != b.Size {
				return "", errors.New("conflicting frozen preview")
			}
		}
	}
	if count != 1 {
		return "", errors.New("missing frozen preview")
	}
	return i.Candidate, nil
}

func sameRetainedAsset(a, b generation.Asset) bool {
	sameTime := a.CreatedAt.Equal(b.CreatedAt)
	a.CreatedAt = b.CreatedAt
	return sameTime && a == b
}
