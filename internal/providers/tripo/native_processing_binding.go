package tripo

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// nativeSourceRig is the Schema-declared frozen Rig configuration a Retarget
// source must carry. Retarget sends no model of its own: the upstream uses the
// source Rig's version, so the exact source model and skeleton are part of the
// Retarget contract rather than an inference from same-provider rigging. It is
// the historical single-configuration form: any action is allowed.
type nativeSourceRig struct {
	Model   string `json:"model"`
	RigType string `json:"rig_type"`
	Spec    string `json:"spec"`
}

// nativeSourceRigRule is one frozen Rig configuration a Retarget source may
// carry and the actions that configuration takes (#277): a biped skeleton takes
// the biped presets, a creature skeleton its own walk or march. The Rig's
// skeleton type is the user's choice now, so the Retarget contract lists the
// configurations instead of naming one.
type nativeSourceRigRule struct {
	Model      string   `json:"model"`
	RigType    string   `json:"rig_type"`
	Spec       string   `json:"spec"`
	Animations []string `json:"animations"`
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

// nativeActionParameter is the Offering's action parameter, whose wire value
// decides which Rig configurations a Retarget takes.
func nativeActionParameter(offering generation.Offering) (generation.Parameter, bool) {
	for _, p := range offering.Parameters {
		if p.Semantic == generation.SemanticAction {
			return p, true
		}
	}
	return generation.Parameter{}, false
}

// validNativeSourceRigRules checks the declared configurations: each is named
// by model, skeleton type and naming once, and lists at least one action that
// is one of the action parameter's own options.
func validNativeSourceRigRules(offering generation.Offering, rules []nativeSourceRigRule) bool {
	action, ok := nativeActionParameter(offering)
	if !ok || action.Type != "select" || len(rules) == 0 {
		return false
	}
	options := map[string]bool{}
	for _, option := range action.Options {
		options[option.Value] = true
	}
	configurations := map[[3]string]bool{}
	for _, rule := range rules {
		key := [3]string{rule.Model, rule.RigType, rule.Spec}
		if strings.TrimSpace(rule.Model) == "" || strings.TrimSpace(rule.RigType) == "" || strings.TrimSpace(rule.Spec) == "" || configurations[key] || len(rule.Animations) == 0 {
			return false
		}
		configurations[key] = true
		listed := map[string]bool{}
		for _, animation := range rule.Animations {
			if !options[animation] || listed[animation] {
				return false
			}
			listed[animation] = true
		}
	}
	return true
}

// nativeSourceRigDeclaration checks that a source_rig or source_rigs contract
// is only used by a model-less Retarget whose single exact source is
// provider-bound, and that the two forms are not mixed.
func nativeSourceRigDeclaration(offering generation.Offering, options providerOptions, source generation.Parameter) error {
	if options.SourceRig == nil && len(options.SourceRigs) == 0 {
		if source.TaskSourceBinding != "" {
			return nativeProcessingProtocolError()
		}
		return nil
	}
	if offering.Capability != generation.Animation || options.Model != "" || source.TaskSourceBinding != "provider" || len(source.TaskOfferings) != 1 || len(source.TaskCapabilities) != 1 || source.TaskCapabilities[0] != generation.Rigging {
		return nativeProcessingProtocolError()
	}
	if options.SourceRig != nil && len(options.SourceRigs) > 0 {
		return nativeProcessingProtocolError()
	}
	if options.SourceRig != nil && !validNativeSourceRig(options.SourceRig) {
		return nativeProcessingProtocolError()
	}
	if len(options.SourceRigs) > 0 && !validNativeSourceRigRules(offering, options.SourceRigs) {
		return nativeProcessingProtocolError()
	}
	return nil
}

// verifyNativeSourceRig compares the frozen dependency binding with the exact
// resolved source and the declared Rig configuration before any dispatch. With
// declared configurations the binding must be one of them and the requested
// action one that configuration takes (#277); action is the wire value of the
// Offering's action parameter.
func verifyNativeSourceRig(offering generation.Offering, options providerOptions, source generation.TaskDependency, action string) error {
	if options.SourceRig == nil && len(options.SourceRigs) == 0 {
		return nil
	}
	var binding nativeRigBinding
	if strictUploadedJSON(source.ProviderBinding, &binding) != nil || binding.TaskID != source.TaskID || binding.AttemptID == "" || binding.RemoteID != source.UpstreamJob.RemoteID || binding.OfferingID != source.OfferingID || binding.OfferingRevision == "" || !uploadedHashValid(binding.OfferingSHA256) {
		return nativeProcessingSourceMismatch(offering)
	}
	if options.SourceRig != nil {
		if binding.Model != options.SourceRig.Model || binding.RigType != options.SourceRig.RigType || binding.Spec != options.SourceRig.Spec {
			return nativeProcessingSourceMismatch(offering)
		}
		return nil
	}
	for _, rule := range options.SourceRigs {
		if rule.Model != binding.Model || rule.RigType != binding.RigType || rule.Spec != binding.Spec {
			continue
		}
		if slices.Contains(rule.Animations, action) {
			return nil
		}
		// The Rig is one this Retarget animates, but not with this action.
		if parameter, ok := nativeActionParameter(offering); ok {
			return nativeProcessingParameterError(offering, parameter, nil)
		}
		break
	}
	return nativeProcessingSourceMismatch(offering)
}

// nativeRigSelection reads the skeleton choice a Rig Task was frozen with
// (#277: the Offering no longer fixes it). The frozen input is the truth; a
// request frozen without it falls back to the value its frozen Offering fixed.
// The value must be one the frozen Offering declares.
func nativeRigSelection(r generation.DependencyBindingRequest, name string) (string, bool) {
	var declared generation.Parameter
	found := false
	for _, p := range r.Offering.Parameters {
		if p.Name == name {
			declared, found = p, true
			break
		}
	}
	if !found || declared.Type != "select" {
		return "", false
	}
	if len(r.Input) > 0 {
		var values map[string]json.RawMessage
		if strictUploadedJSON(r.Input, &values) != nil {
			return "", false
		}
		if raw, present := values[name]; present {
			var value string
			if strictUploadedJSON(raw, &value) != nil || strings.TrimSpace(value) == "" {
				return "", false
			}
			for _, option := range declared.Options {
				if option.Value == value {
					return value, true
				}
			}
			return "", false
		}
	}
	return fixedNativeString(r.Offering, name)
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
// explicit model and a skeleton choice the frozen Offering declares are trusted:
// the choice is read from the source Task's frozen input, or from the value the
// frozen Offering fixed when the input holds none (nativeRigSelection). Every
// required declared output must already be a stored, required asset.
func nativeRigDependencyBinding(r generation.DependencyBindingRequest) (json.RawMessage, error) {
	_, options, err := nativeProcessingDeclaration(r.Offering)
	if err != nil || r.Offering.Capability != generation.Rigging || options.Endpoint != "animations/rig" || strings.TrimSpace(options.Model) == "" || r.TaskID == "" || r.AttemptID == "" || r.Job.ProviderID != r.Offering.ProviderID || !uploadedTokenValid(r.Job.RemoteID) {
		return nil, nativeProcessingProtocolError()
	}
	rigType, okType := nativeRigSelection(r, "rig_type")
	spec, okSpec := nativeRigSelection(r, "spec")
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
