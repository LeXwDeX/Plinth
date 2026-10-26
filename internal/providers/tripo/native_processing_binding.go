package tripo

import (
	"encoding/json"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// nativeSourceRig is the Schema-declared frozen Rig configuration a Retarget
// source must carry. Retarget sends no model of its own: the upstream uses the
// source Rig's version, so the exact source model and skeleton are part of the
// Retarget contract rather than an inference from same-provider rigging.
type nativeSourceRig struct {
	Model   string `json:"model"`
	RigType string `json:"rig_type"`
	Spec    string `json:"spec"`
}

// nativeRigBinding is private, read-only evidence projected from the source
// Rig Task's frozen Offering and delivered assets. It is frozen into the
// PreparedInput hash and compared again before dispatch.
type nativeRigBinding struct {
	TaskID           generation.TaskID     `json:"task_id"`
	AttemptID        generation.AttemptID  `json:"attempt_id"`
	RemoteID         string                `json:"remote_id"`
	OfferingID       generation.OfferingID `json:"offering_id"`
	OfferingRevision string                `json:"offering_revision"`
	OfferingSHA256   string                `json:"offering_sha256"`
	Model            string                `json:"model"`
	RigType          string                `json:"rig_type"`
	Spec             string                `json:"spec"`
}

func validNativeSourceRig(rig *nativeSourceRig) bool {
	return rig != nil && strings.TrimSpace(rig.Model) != "" && strings.TrimSpace(rig.RigType) != "" && strings.TrimSpace(rig.Spec) != ""
}

// nativeSourceRigDeclaration checks that a source_rig contract is only used by
// a model-less Retarget whose single exact source is provider-bound.
func nativeSourceRigDeclaration(offering generation.Offering, options providerOptions, source generation.Parameter) error {
	if options.SourceRig == nil {
		if source.TaskSourceBinding != "" {
			return nativeProcessingProtocolError()
		}
		return nil
	}
	if offering.Capability != generation.Animation || options.Model != "" || !validNativeSourceRig(options.SourceRig) || source.TaskSourceBinding != "provider" || len(source.TaskOfferings) != 1 || len(source.TaskCapabilities) != 1 || source.TaskCapabilities[0] != generation.Rigging {
		return nativeProcessingProtocolError()
	}
	return nil
}

// verifyNativeSourceRig compares the frozen dependency binding with the exact
// resolved source and the declared Rig configuration before any dispatch.
func verifyNativeSourceRig(offering generation.Offering, options providerOptions, source generation.TaskDependency) error {
	if options.SourceRig == nil {
		return nil
	}
	var binding nativeRigBinding
	if strictUploadedJSON(source.ProviderBinding, &binding) != nil || binding.TaskID != source.TaskID || binding.AttemptID == "" || binding.RemoteID != source.UpstreamJob.RemoteID || binding.OfferingID != source.OfferingID || binding.OfferingRevision == "" || !uploadedHashValid(binding.OfferingSHA256) || binding.Model != options.SourceRig.Model || binding.RigType != options.SourceRig.RigType || binding.Spec != options.SourceRig.Spec {
		return nativeProcessingSourceMismatch(offering)
	}
	return nil
}

func fixedNativeString(offering generation.Offering, name string) (string, bool) {
	for _, p := range offering.Parameters {
		if p.Name != name {
			continue
		}
		var value string
		if !p.Fixed || strictUploadedJSON(p.Default, &value) != nil || strings.TrimSpace(value) == "" {
			return "", false
		}
		return value, true
	}
	return "", false
}

// nativeRigDependencyBinding projects a successful native Rig source. Only an
// explicit model and fixed skeleton parameters are trusted, because the
// binding sees the frozen Offering rather than the source input. Every required
// declared output must already be a stored, required asset.
func nativeRigDependencyBinding(r generation.DependencyBindingRequest) (json.RawMessage, error) {
	_, options, err := nativeProcessingDeclaration(r.Offering)
	if err != nil || r.Offering.Capability != generation.Rigging || options.Endpoint != "animations/rig" || strings.TrimSpace(options.Model) == "" || r.TaskID == "" || r.AttemptID == "" || r.Job.ProviderID != r.Offering.ProviderID || !uploadedTokenValid(r.Job.RemoteID) {
		return nil, nativeProcessingProtocolError()
	}
	rigType, okType := fixedNativeString(r.Offering, "rig_type")
	spec, okSpec := fixedNativeString(r.Offering, "spec")
	if !okType || !okSpec {
		return nil, nativeProcessingProtocolError()
	}
	for _, output := range r.Offering.Outputs {
		// Mirrors the kernel: background/on-demand slots never block success,
		// and a missing minimum on a blocking slot means one asset.
		if output.Delivery == generation.AssetDeliveryBackground || output.Delivery == generation.AssetDeliveryOnDemand || output.Minimum != nil && *output.Minimum <= 0 {
			continue
		}
		minimum := 1
		if output.Minimum != nil {
			minimum = *output.Minimum
		}
		count := 0
		for _, asset := range r.Assets {
			if asset.TaskID == r.TaskID && asset.AttemptID == r.AttemptID && asset.Role == output.Role && asset.Variant == output.Variant && asset.Required && asset.Size > 0 && uploadedHashValid(asset.SHA256) {
				count++
			}
		}
		if count < minimum {
			return nil, nativeProcessingProtocolError()
		}
	}
	raw, _ := json.Marshal(nativeRigBinding{TaskID: r.TaskID, AttemptID: r.AttemptID, RemoteID: r.Job.RemoteID, OfferingID: r.Offering.ID, OfferingRevision: r.Offering.Revision, OfferingSHA256: nativeOfferingDigest(r.Offering), Model: options.Model, RigType: rigType, Spec: spec})
	return raw, nil
}
