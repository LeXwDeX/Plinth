package mcppublic

import (
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// SelectionCode says why no service was chosen for a call. The transport words
// it with the Schema's message of the same problem code.
type SelectionCode string

const (
	// SelectionChosen: a service was chosen.
	SelectionChosen SelectionCode = ""
	// SelectionNoService: no enabled, available service takes the call.
	SelectionNoService SelectionCode = "no_service"
	// SelectionSourceIncompatible: no enabled, available service accepts the
	// processing step's source.
	SelectionSourceIncompatible SelectionCode = "source_incompatible"
)

// OfferingNeed is what a call that names no service needs of one. Exactly one
// need applies: several views of one model (image-to-3D of several pictures),
// an image edit's mask, or the source model of a processing step.
type OfferingNeed struct {
	SeveralViews bool
	Mask         bool
	// Source is the succeeded task a processing step takes, SourceAsset the
	// model file of it the caller chose ("" for the main one) and Prefer the
	// Schema's source variant preference.
	Source      *generation.Task
	SourceAsset generation.AssetID
	Prefer      []string
}

// Selection is the chosen service and, for a processing step, how the source
// feeds it. Code is SelectionChosen when a service was chosen.
type Selection struct {
	Offering generation.Offering
	Binding  *generation.ModelSourceBinding
	Code     SelectionCode
}

// ChooseOffering picks the service of a call that names none (#517). Among the
// capability's candidates, in the Schema's order, it keeps the enabled ones
// whose runtime configuration is available and that meet the need, and takes
// the first marked default, else the first. An unavailable service is never
// chosen and nothing is replaced by another model; no match is the need's
// SelectionCode.
func ChooseOffering(candidates []generation.Offering, available func(generation.OfferingID) bool, need OfferingNeed) Selection {
	var chosen *Selection
	for _, candidate := range candidates {
		if !candidate.Enabled || !available(candidate.ID) {
			continue
		}
		binding, accepted := need.accepts(candidate)
		if !accepted {
			continue
		}
		if candidate.Default {
			return Selection{Offering: candidate, Binding: binding}
		}
		if chosen == nil {
			chosen = &Selection{Offering: candidate, Binding: binding}
		}
	}
	if chosen != nil {
		return *chosen
	}
	if need.Source != nil {
		return Selection{Code: SelectionSourceIncompatible}
	}
	return Selection{Code: SelectionNoService}
}

// accepts says whether a service meets the need, with the source binding of a
// processing step.
func (need OfferingNeed) accepts(offering generation.Offering) (*generation.ModelSourceBinding, bool) {
	switch {
	case need.Source != nil:
		binding, err := generation.BindModelSource(offering, *need.Source, need.SourceAsset, need.Prefer)
		if err != nil {
			return nil, false
		}
		return &binding, true
	case need.Mask:
		for _, parameter := range offering.Parameters {
			if parameter.Semantic == generation.SemanticMask {
				return nil, true
			}
		}
		return nil, false
	case need.SeveralViews:
		return nil, generationtool.TakesSeveralViews(offering)
	}
	return nil, true
}
