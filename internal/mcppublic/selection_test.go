package mcppublic

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #517: a call that names no service gets the first enabled, available
// service in the Schema's order that meets its need, the one marked default
// first; never a disabled or unconfigured one, and no match is a code.
func TestChooseOfferingTakesTheDefaultElseTheFirstThatMeetsTheNeed(t *testing.T) {
	mask := []generation.Parameter{{Name: "mask", Semantic: generation.SemanticMask}}
	views := []generation.Parameter{{Name: "views", Semantic: generation.SemanticReferenceViews}}
	offering := func(id string, enabled, isDefault bool, parameters []generation.Parameter) generation.Offering {
		return generation.Offering{ID: generation.OfferingID(id), Enabled: enabled, Default: isDefault, Parameters: parameters}
	}
	unavailable := map[generation.OfferingID]bool{"unconfigured": true}
	available := func(id generation.OfferingID) bool { return !unavailable[id] }
	for name, test := range map[string]struct {
		candidates []generation.Offering
		need       OfferingNeed
		want       generation.OfferingID
		code       SelectionCode
	}{
		"mask: the default": {[]generation.Offering{
			offering("plain-default", true, true, nil), offering("first-mask", true, false, mask), offering("mask-default", true, true, mask),
		}, OfferingNeed{Mask: true}, "mask-default", SelectionChosen},
		"mask: the first without a default": {[]generation.Offering{
			offering("disabled", false, true, mask), offering("unconfigured", true, true, mask),
			offering("first-mask", true, false, mask), offering("second-mask", true, false, mask),
		}, OfferingNeed{Mask: true}, "first-mask", SelectionChosen},
		"mask: none": {[]generation.Offering{offering("plain", true, true, nil)},
			OfferingNeed{Mask: true}, "", SelectionNoService},
		"several views": {[]generation.Offering{
			offering("single", true, true, nil), offering("views", true, false, views),
		}, OfferingNeed{SeveralViews: true}, "views", SelectionChosen},
		"several views: none available": {[]generation.Offering{offering("unconfigured", true, true, views)},
			OfferingNeed{SeveralViews: true}, "", SelectionNoService},
		"source: none accepts": {[]generation.Offering{offering("processing", true, true, nil)},
			OfferingNeed{Source: &generation.Task{State: generation.TaskFailed}}, "", SelectionSourceIncompatible},
	} {
		got := ChooseOffering(test.candidates, available, test.need)
		if got.Offering.ID != test.want || got.Code != test.code {
			t.Errorf("%s: chose %q code %q; want %q code %q", name, got.Offering.ID, got.Code, test.want, test.code)
		}
	}
}
