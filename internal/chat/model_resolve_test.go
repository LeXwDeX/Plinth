package chat_test

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #380 step 1: chat.ResolveModel is the one rule the generation tools and the
// production plan share. The layers, highest first: the person's lock (or this
// turn's structured request), the Agent's model argument, the plan's pin, the
// purpose recommendation, the workspace picker and the Offering default.
//
// The tests below fix each layer, the conflicts, and the one substitution the
// rule allows: the same model in another step shape, as the Catalog declares a
// model family (#417).

const (
	resolveSingleID    = "meshy/meshy-7.1/image-to-3d"
	resolveMultiviewID = "meshy/meshy-7.1-multiview/image-to-3d"
	resolveTripoID     = "tripo/tripo-v3.1-full/image-to-3d"
	resolveTripoMulti  = "tripo/tripo-v3.1-multiview/image-to-3d"
	resolveDefaultID   = "hyper3d/rodin-gen-2.5/image-to-3d"
)

func resolveOffering(id, modelID, providerID, family string, defaultFlag bool) generation.Offering {
	return generation.Offering{
		ID: generation.OfferingID(id), ModelID: modelID, ProviderID: providerID,
		ModelFamily: family, Capability: generation.ImageTo3D, Domain: generation.DomainModel3D,
		Enabled: true, Default: defaultFlag,
	}
}

func resolveCandidates() []generation.Offering {
	return []generation.Offering{
		resolveOffering(resolveSingleID, "meshy-7.1", "meshy", "meshy-7.1", false),
		resolveOffering(resolveMultiviewID, "meshy-7.1-multiview", "meshy", "meshy-7.1", false),
		resolveOffering(resolveTripoID, "tripo-v3.1-full", "tripo", "tripo-v3.1", false),
		resolveOffering(resolveTripoMulti, "tripo-v3.1-multiview", "tripo", "tripo-v3.1", false),
		resolveOffering(resolveDefaultID, "rodin-gen-2.5", "hyper3d", "", true),
	}
}

// resolveViewsOnly is the view test of a multiview step: the Offering must
// declare the views, and the single-picture shapes do not.
func resolveViewsOnly(offering generation.Offering) bool {
	return offering.ModelID == "meshy-7.1-multiview" || offering.ModelID == "tripo-v3.1-multiview"
}

func TestResolveModelFollowsTheLayers(t *testing.T) {
	tests := []struct {
		name      string
		request   chat.ModelRequest
		wantID    generation.OfferingID
		source    chat.ModelSource
		family    bool
		conflict  chat.ModelConflictKind
		conflictN string // the conflict's Named
		// locked is the Offering a ModelConflictLocked resolved the person's pick to.
	}{
		{
			name:    "the default when nothing chose",
			request: chat.ModelRequest{},
			wantID:  resolveDefaultID, source: chat.ModelSourceDefault,
		},
		{
			// Everything above the lock's own shape loses to it; a name of the
			// same family is the step's shape of the person's model (#417).
			name:    "the person's lock outranks everything",
			request: chat.ModelRequest{Lock: resolveSingleID, Named: "meshy-7.1-multiview", Recommended: []string{resolveTripoID}, Workspace: resolveTripoMulti, PlanPinned: resolveTripoID},
			wantID:  resolveMultiviewID,
			source:  chat.ModelSourceLock, family: true,
		},
		{
			name:    "the lock alone",
			request: chat.ModelRequest{Lock: "meshy-7.1", Workspace: resolveTripoID},
			wantID:  resolveSingleID, source: chat.ModelSourceLock,
		},
		{
			name:    "this turn's structured request is the person's pick",
			request: chat.ModelRequest{Lock: resolveMultiviewID, LockSource: chat.ModelSourceTurn, Workspace: resolveTripoID},
			wantID:  resolveMultiviewID, source: chat.ModelSourceTurn,
		},
		{
			name:     "a lock the Catalog no longer serves is refused",
			request:  chat.ModelRequest{Lock: "meshy-7", Recommended: []string{resolveTripoID}},
			conflict: chat.ModelConflictUnavailable, conflictN: "meshy-7",
		},
		{
			name:     "a lock of another provider is refused, not replaced",
			request:  chat.ModelRequest{Lock: "openai/gpt-image-2.6-sunburst"},
			conflict: chat.ModelConflictUnavailable, conflictN: "openai/gpt-image-2.6-sunburst",
		},
		{
			name:     "another family named by the call conflicts with the lock",
			request:  chat.ModelRequest{Lock: "meshy-7.1", Named: resolveTripoID},
			conflict: chat.ModelConflictLocked, conflictN: resolveTripoID,
		},
		{
			name:    "the same family in another shape is the person's model",
			request: chat.ModelRequest{Lock: "meshy-7.1", Named: "meshy-7.1-multiview"},
			wantID:  resolveMultiviewID, source: chat.ModelSourceLock, family: true,
		},
		{
			name:    "the Agent's argument outranks the plan, purpose and workspace",
			request: chat.ModelRequest{Named: resolveTripoMulti, PlanPinned: resolveMultiviewID, Recommended: []string{resolveSingleID}, Workspace: resolveSingleID},
			wantID:  resolveTripoMulti, source: chat.ModelSourceAgent,
		},
		{
			name:     "an Agent model no Offering carries is refused",
			request:  chat.ModelRequest{Named: "missing-model", Workspace: resolveSingleID},
			conflict: chat.ModelConflictUnavailable, conflictN: "missing-model",
		},
		{
			name:    "the plan's pin outranks the purpose and the workspace",
			request: chat.ModelRequest{PlanPinned: resolveMultiviewID, Recommended: []string{resolveTripoID}, Workspace: resolveSingleID},
			wantID:  resolveMultiviewID, source: chat.ModelSourcePlan,
		},
		{
			name:     "a plan pin the Catalog no longer serves is refused",
			request:  chat.ModelRequest{PlanPinned: "removed/offering", Workspace: resolveSingleID},
			conflict: chat.ModelConflictUnavailable, conflictN: "removed/offering",
		},
		{
			name:    "the purpose recommendation outranks the workspace picker",
			request: chat.ModelRequest{Recommended: []string{resolveTripoID}, Profile: "render", Workspace: resolveSingleID},
			wantID:  resolveTripoID, source: chat.ModelSourcePurpose,
		},
		{
			name: "a recommendation the request does not fit is skipped",
			request: chat.ModelRequest{
				Recommended: []string{resolveSingleID, resolveMultiviewID},
				Fits:        func(offering generation.Offering) bool { return offering.ModelID != "meshy-7.1" },
			},
			wantID: resolveMultiviewID, source: chat.ModelSourcePurpose,
		},
		{
			name:    "the workspace picker runs below the purpose",
			request: chat.ModelRequest{Workspace: resolveTripoMulti},
			wantID:  resolveTripoMulti, source: chat.ModelSourceWorkspace,
		},
		{
			name:    "a workspace value no Offering carries falls to the default",
			request: chat.ModelRequest{Workspace: "retired-model"},
			wantID:  resolveDefaultID, source: chat.ModelSourceDefault,
		},
		{
			name:    "the recommended flag decides when no Offering is the default",
			request: chat.ModelRequest{},
			wantID:  resolveDefaultID, source: chat.ModelSourceDefault,
		},
	}
	candidates := resolveCandidates()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := test.request
			request.Candidates = candidates
			resolution := chat.ResolveModel(request)
			if test.conflict != "" {
				if resolution.Conflict == nil || resolution.Conflict.Kind != test.conflict ||
					resolution.Conflict.Named != test.conflictN || resolution.Offering.ID != "" {
					t.Fatalf("resolution = %+v, want a %s conflict naming %q", resolution, test.conflict, test.conflictN)
				}
				if len(resolution.Conflict.Candidates) != len(candidates) {
					t.Fatalf("conflict candidates = %d", len(resolution.Conflict.Candidates))
				}
				return
			}
			if resolution.Conflict != nil {
				t.Fatalf("resolution refused: %+v", resolution.Conflict)
			}
			if resolution.Offering.ID != test.wantID || resolution.Source != test.source || resolution.Family != test.family {
				t.Fatalf("resolution = %s from %s (family %v), want %s from %s (family %v)",
					resolution.Offering.ID, resolution.Source, resolution.Family, test.wantID, test.source, test.family)
			}
		})
	}
}

// #417: the session's lock is Meshy 7.1 and the step is a multiview step. The
// person's model is the family; the shape that takes the views is the multiview
// one, and nothing falls to another vendor's model.
func TestResolveModelRunsTheLockFamilysShapeForAMultiviewStep(t *testing.T) {
	resolution := chat.ResolveModel(chat.ModelRequest{
		Candidates: resolveCandidates(),
		Lock:       "meshy-7.1",
		Views:      resolveViewsOnly,
	})
	if resolution.Conflict != nil || resolution.Offering.ID != resolveMultiviewID ||
		resolution.Source != chat.ModelSourceLock || !resolution.Family {
		t.Fatalf("multiview resolution = %+v", resolution)
	}

	// The plan quoted the multiview Offering, so the budget covers exactly the
	// Offering the step now runs: the same function resolves both.
	planned := chat.ResolveModel(chat.ModelRequest{
		Candidates: resolveCandidates(),
		Lock:       "meshy-7.1",
		Named:      "meshy-7.1-multiview",
	})
	if planned.Conflict != nil || planned.Offering.ID != resolution.Offering.ID {
		t.Fatalf("plan resolution = %+v, execution = %+v", planned, resolution)
	}
}

// A model family of one provider: a lock never runs another vendor's model, and
// a shape without a family sibling keeps the person's Offering (the step's own
// view check then words the refusal, as it always has).
func TestResolveModelDoesNotLeaveTheLockFamily(t *testing.T) {
	noSiblings := []generation.Offering{
		resolveOffering("hyper3d/rodin-gen-2.5/image-to-3d", "rodin-gen-2.5", "hyper3d", "", true),
		resolveOffering(resolveTripoMulti, "tripo-v3.1-multiview", "tripo", "tripo-v3.1", false),
	}
	resolution := chat.ResolveModel(chat.ModelRequest{
		Candidates: noSiblings,
		Lock:       "rodin-gen-2.5",
		Views:      resolveViewsOnly,
	})
	if resolution.Conflict != nil || resolution.Offering.ID != "hyper3d/rodin-gen-2.5/image-to-3d" || resolution.Family {
		t.Fatalf("resolution = %+v, want the person's Offering", resolution)
	}
}

// A frozen selection mask is all-or-nothing (#244): a lock, an argument, a plan
// pin or the workspace picker that cannot bind it is refused rather than run
// without it.
func TestResolveModelRefusesAModelThatCannotBindTheMask(t *testing.T) {
	withMask := func(offering generation.Offering) bool {
		return offering.ProviderID == "tripo"
	}
	for _, test := range []struct {
		name    string
		request chat.ModelRequest
		layer   chat.ModelSource
	}{
		{name: "the lock", request: chat.ModelRequest{Lock: "meshy-7.1"}, layer: chat.ModelSourceLock},
		{name: "the Agent", request: chat.ModelRequest{Named: "meshy-7.1"}, layer: chat.ModelSourceAgent},
		{name: "the plan", request: chat.ModelRequest{PlanPinned: resolveSingleID}, layer: chat.ModelSourcePlan},
		{name: "the workspace", request: chat.ModelRequest{Workspace: resolveSingleID}, layer: chat.ModelSourceWorkspace},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := test.request
			request.Candidates = resolveCandidates()
			request.SupportsMask = withMask
			resolution := chat.ResolveModel(request)
			if resolution.Conflict == nil || resolution.Conflict.Kind != chat.ModelConflictInput ||
				resolution.Conflict.Layer != test.layer || resolution.Conflict.Offering.ID != resolveSingleID {
				t.Fatalf("resolution = %+v, want an input conflict at %s", resolution, test.layer)
			}
		})
	}
	// The purpose recommendation only advises: a mask-less recommendation is
	// skipped for one that binds the mask.
	resolution := chat.ResolveModel(chat.ModelRequest{
		Candidates:   resolveCandidates(),
		Recommended:  []string{resolveSingleID, resolveTripoID},
		SupportsMask: withMask,
	})
	if resolution.Conflict != nil || resolution.Offering.ID != resolveTripoID || resolution.Source != chat.ModelSourcePurpose {
		t.Fatalf("mask recommendation = %+v", resolution)
	}
}

func TestResolveModelChoiceSourceNamesThePublicLayer(t *testing.T) {
	tests := map[chat.ModelSource]string{
		chat.ModelSourceLock:      "user",
		chat.ModelSourceTurn:      "user",
		chat.ModelSourcePlan:      "user",
		chat.ModelSourceWorkspace: "user",
		chat.ModelSourceAgent:     "agent",
		chat.ModelSourcePurpose:   "profile",
		chat.ModelSourceDefault:   "default",
	}
	for source, want := range tests {
		if got := (chat.ModelResolution{Source: source}).ChoiceSource(); got != want {
			t.Fatalf("source %q maps to %q, want %q", source, got, want)
		}
	}
}
