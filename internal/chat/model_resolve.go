package chat

import (
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #380 step 1: one rule chooses the model of a generation step, shared by the
// generation tools and the production plan.
//
// The layers, highest first:
//
//  1. the person's lock: the session's explicit model for the step's workspace
//     domain, or this turn's structured request for its own tool (#373);
//  2. the Agent's model argument;
//  3. the approved production plan's step pin: the Offering the plan quoted;
//  4. the production purpose's recommendation;
//  5. the workspace picker's model;
//  6. the Offering's own default.
//
// Only the lock and the turn's structured request are the person's decisions.
// The Agent's argument is a decision too, so a call naming another model than
// the lock, or naming a model no available Offering carries, is refused
// instead of replaced. The plan pin is the plan's own quote: an unavailable
// pin is refused the same way, because another Offering would be charged
// outside the approved plan. The purpose recommendation only advises: it is
// chosen when nothing above it chose, and it never refuses a call. The
// workspace picker's value is not a decision (it may merely be restored from
// another session or a previous visit), so it runs only when nothing above it
// chose and never blocks a recommendation.
//
// One model may exist in several step shapes (text, single picture, several
// views). The Catalog declares those shapes as one model family, so a lock on
// Meshy 7.1 and a plan step that names Meshy 7.1 multiview are the same
// person's model in another shape, not a conflict (#417).

// ModelSource names the layer that chose a step's model.
type ModelSource string

const (
	// ModelSourceLock is the person's explicit session pick.
	ModelSourceLock ModelSource = "lock"
	// ModelSourceTurn is this turn's structured request (the multiview panel or
	// the three-view card), which replaces the lock for its own tool (#373).
	ModelSourceTurn ModelSource = "turn"
	// ModelSourceAgent is the Agent's model argument.
	ModelSourceAgent ModelSource = "agent"
	// ModelSourcePlan is the Offering the approved plan pins for the step.
	ModelSourcePlan ModelSource = "plan"
	// ModelSourcePurpose is the production purpose's recommendation.
	ModelSourcePurpose ModelSource = "purpose"
	// ModelSourceWorkspace is the model the workspace picker shows for the
	// step's domain.
	ModelSourceWorkspace ModelSource = "workspace"
	// ModelSourceDefault is the Offering's own default.
	ModelSourceDefault ModelSource = "default"
)

// ModelConflictKind names why no model was chosen.
type ModelConflictKind string

const (
	// ModelConflictUnavailable: the named model has no available Offering of
	// the capability, so nothing can run it.
	ModelConflictUnavailable ModelConflictKind = "unavailable"
	// ModelConflictLocked: the call named another model than the person's.
	ModelConflictLocked ModelConflictKind = "locked"
	// ModelConflictInput: the model that decides cannot run this request (its
	// views or the frozen selection mask), and no member of its family can.
	ModelConflictInput ModelConflictKind = "input"
	// ModelConflictWorkspace: the purpose recommends a model while the
	// workspace picker shows another one the person can see. The person
	// confirms which to keep instead of the recommendation replacing their
	// visible pick silently (#103 review C1).
	ModelConflictWorkspace ModelConflictKind = "workspace"
)

// ModelConflict is one refusal of the model resolution: which layer refused,
// what it named and which Offering it resolved, with every candidate so the
// caller can word what could run instead.
type ModelConflict struct {
	Kind ModelConflictKind
	// Layer is the layer that refused.
	Layer ModelSource
	// Named is the name the layer used: the lock's model, the Agent's
	// argument, the plan's pin, or "" when the request's input fits nothing.
	Named string
	// Offering is the Offering the layer resolved; zero when the name selected
	// none.
	Offering generation.Offering
	// Locked is the person's Offering when Layer named another model.
	Locked generation.Offering
	// Profile is the purpose whose recommendation conflicts with the workspace
	// value (ModelConflictWorkspace): the caller names it in the confirmation.
	Profile string
	// Candidates are the capability's available Offerings, in Catalog order.
	Candidates []generation.Offering
}

// ModelResolution is one step's model: the Offering, the layer that chose it
// and, for a purpose, its identity and reason. Family reports that the
// Offering is the lock's model in another shape. Exactly one of Offering and
// Conflict is set.
type ModelResolution struct {
	Offering generation.Offering
	Source   ModelSource
	Profile  string
	Reason   string
	Family   bool
	Conflict *ModelConflict
}

// ChoiceSource is the tool result's model_choice source of a resolution, in
// the Schema's public vocabulary: the person's choice ("user") for the lock,
// this turn's request, the approved plan and the workspace picker, the
// Agent's argument, the purpose recommendation, or the default.
func (resolution ModelResolution) ChoiceSource() string {
	switch resolution.Source {
	case ModelSourceAgent:
		return "agent"
	case ModelSourcePurpose:
		return "profile"
	case ModelSourceDefault:
		return "default"
	default:
		return "user"
	}
}

// ModelRequest is one generation request's model decision. The caller supplies
// the capability's available Offerings and the names every layer carries; the
// resolver owns the order, the conflicts and the family substitution.
type ModelRequest struct {
	// Candidates are the capability's enabled, runtime-available Offerings, in
	// Catalog order.
	Candidates []generation.Offering
	// Lock is the person's explicit model for the step's workspace domain; this
	// turn's structured request replaces it for its own tool.
	Lock string
	// LockSource is where Lock came from: ModelSourceLock (the session) or
	// ModelSourceTurn (this turn's structured request). Empty means the session.
	LockSource ModelSource
	// Named is the Agent's model argument.
	Named string
	// PlanPinned is the Offering the approved plan pins for the capability, in
	// any form a model is named by (Offering ID, model ID, provider/model). It
	// is empty when the turn runs no plan or the plan pins nothing here.
	PlanPinned string
	// Recommended are the purpose's recommended models for the request's shape,
	// in order; Profile and Reason name the purpose for the result.
	Recommended []string
	Profile     string
	Reason      string
	// Workspace is the model the workspace picker shows for the step's domain.
	Workspace string
	// ConfirmsWorkspace says the caller can word the confirmation of a purpose
	// recommendation that differs from the workspace value (it declares the
	// Schema's text, or asks in its own words). Then the recommendation never
	// replaces the visible pick silently: the two disagreeing is a conflict.
	// Without it a Catalog that declares no such text keeps the older rule,
	// where the recommendation runs.
	ConfirmsWorkspace bool
	// Fits reports whether an Offering can take the request's whole input: its
	// reference pictures, its views and the frozen selection mask. The purpose
	// recommendations are filtered by it, because a recommendation that cannot
	// run the step is not a recommendation. Nil fits every candidate.
	Fits func(generation.Offering) bool
	// Views reports whether an Offering takes this request's views; nil when the
	// request is no multiview step. It is not a refusal: a lock runs the same
	// model family's shape that takes the views (#417), and any other chosen
	// model is left to the step's own view check, which words what is wrong.
	Views func(generation.Offering) bool
	// SupportsMask reports whether an Offering binds the turn's frozen
	// selection mask; nil when the turn carries none. A mask is all-or-nothing,
	// so a lock, a named model, a plan pin or a workspace pick that cannot bind
	// it is refused rather than run without it (#244).
	SupportsMask func(generation.Offering) bool
	// Default picks the model when no layer above chose one, among the
	// candidates that run. Nil takes the Offering with the default flag, then
	// the one with the recommended flag, then the first, in Catalog order.
	Default func([]generation.Offering) (generation.Offering, bool)
}

// ResolveModel chooses the Offering of one generation request by the layers
// described on ModelSource: the person's lock, the Agent's argument, the
// plan's pin, the purpose's recommendation, the workspace picker and the
// default.
func ResolveModel(request ModelRequest) ModelResolution {
	candidates := request.Candidates
	views := request.Views
	if views == nil {
		views = func(generation.Offering) bool { return true }
	}
	bindsMask := request.SupportsMask
	if bindsMask == nil {
		bindsMask = func(generation.Offering) bool { return true }
	}
	fits := request.Fits
	if fits == nil {
		fits = func(offering generation.Offering) bool { return views(offering) && bindsMask(offering) }
	}
	if len(candidates) == 0 {
		return ModelResolution{Conflict: &ModelConflict{Kind: ModelConflictUnavailable, Candidates: nil}}
	}

	// 1. The person's lock, or this turn's structured request.
	if lock := strings.TrimSpace(request.Lock); lock != "" {
		source := request.LockSource
		if source != ModelSourceTurn {
			source = ModelSourceLock
		}
		locked, matched := MatchModel(candidates, lock)
		if !matched {
			return refuseModel(ModelConflictUnavailable, source, lock, candidates)
		}
		chosen := locked
		if named := strings.TrimSpace(request.Named); named != "" && !ModelNameMatches(locked, named) {
			// The same model family in another step shape is still the person's
			// model: a step naming Meshy 7.1 multiview runs under a lock on
			// Meshy 7.1 (#417). A model of another family is a conflict.
			sibling, found := MatchModel(candidates, named)
			if !found || !SameModelFamily(locked, sibling) {
				return ModelResolution{Conflict: &ModelConflict{
					Kind: ModelConflictLocked, Layer: ModelSourceAgent,
					Named: named, Offering: sibling, Locked: locked, Candidates: candidates,
				}}
			}
			chosen = sibling
		}
		family := chosen.ID != locked.ID
		// The shape that takes the request: a multiview step under a
		// single-picture lock runs the same model family's multiview shape.
		if !views(chosen) {
			if sibling, found := familyShape(candidates, chosen, views); found {
				chosen, family = sibling, true
			}
		}
		if !bindsMask(chosen) {
			return ModelResolution{Conflict: &ModelConflict{
				Kind: ModelConflictInput, Layer: source,
				Named: lock, Offering: chosen, Candidates: candidates,
			}}
		}
		return ModelResolution{Offering: chosen, Source: source, Family: family}
	}

	// 2. The Agent's model argument: the input checks refuse an Offering this
	// call cannot take with their own precise messages.
	if named := strings.TrimSpace(request.Named); named != "" {
		chosen, matched := MatchModel(candidates, named)
		if !matched {
			return refuseModel(ModelConflictUnavailable, ModelSourceAgent, named, candidates)
		}
		if !bindsMask(chosen) {
			return ModelResolution{Conflict: &ModelConflict{
				Kind: ModelConflictInput, Layer: ModelSourceAgent,
				Named: named, Offering: chosen, Candidates: candidates,
			}}
		}
		return ModelResolution{Offering: chosen, Source: ModelSourceAgent}
	}

	// 3. The plan's pin. The plan quoted this Offering, so another one would be
	// charged outside the approved budget.
	if pinned := strings.TrimSpace(request.PlanPinned); pinned != "" {
		chosen, matched := MatchModel(candidates, pinned)
		if !matched {
			return refuseModel(ModelConflictUnavailable, ModelSourcePlan, pinned, candidates)
		}
		if !bindsMask(chosen) {
			return ModelResolution{Conflict: &ModelConflict{
				Kind: ModelConflictInput, Layer: ModelSourcePlan,
				Named: pinned, Offering: chosen, Candidates: candidates,
			}}
		}
		return ModelResolution{Offering: chosen, Source: ModelSourcePlan}
	}

	// 4. The purpose's recommendation: it advises, so an Offering that cannot
	// take the input is skipped and nothing is refused.
	for _, name := range request.Recommended {
		chosen, matched := MatchModel(candidates, name)
		if !matched || !fits(chosen) {
			continue
		}
		// The workspace picker shows a model the person may already consider
		// chosen, even when it is not an explicit lock. A recommendation that
		// differs must not replace that visible pick silently (#103 review C1):
		// the person confirms which to keep. A visible pick that is itself one
		// of the purpose's recommendations runs instead (#282).
		if workspace := strings.TrimSpace(request.Workspace); request.ConfirmsWorkspace && workspace != "" &&
			!ModelNameMatches(chosen, workspace) {
			if visible, found := recommendedWorkspace(candidates, request.Recommended, workspace, fits); found {
				return ModelResolution{
					Offering: visible, Source: ModelSourcePurpose,
					Profile: request.Profile, Reason: request.Reason,
				}
			}
			return ModelResolution{Conflict: &ModelConflict{
				Kind: ModelConflictWorkspace, Layer: ModelSourcePurpose,
				Named: workspace, Offering: chosen, Profile: request.Profile, Candidates: candidates,
			}}
		}
		return ModelResolution{
			Offering: chosen, Source: ModelSourcePurpose,
			Profile: request.Profile, Reason: request.Reason,
		}
	}

	// 5. The workspace picker's model. It is not a decision, so it never
	// conflicts with a recommendation; a picker value that cannot bind the
	// frozen mask is refused instead, because running it would drop the mask
	// the person made (#244).
	if workspace := strings.TrimSpace(request.Workspace); workspace != "" {
		if chosen, matched := MatchModel(candidates, workspace); matched {
			if !bindsMask(chosen) {
				return ModelResolution{Conflict: &ModelConflict{
					Kind: ModelConflictInput, Layer: ModelSourceWorkspace,
					Named: workspace, Offering: chosen, Candidates: candidates,
				}}
			}
			if views(chosen) {
				return ModelResolution{Offering: chosen, Source: ModelSourceWorkspace}
			}
		}
	}

	// 6. The Offering's own default, among those that can run the request.
	running := make([]generation.Offering, 0, len(candidates))
	for _, candidate := range candidates {
		if views(candidate) && bindsMask(candidate) {
			running = append(running, candidate)
		}
	}
	pick := request.Default
	if pick == nil {
		pick = PreferredOffering
	}
	chosen, found := pick(running)
	if !found {
		return refuseModel(ModelConflictInput, ModelSourceDefault, "", candidates)
	}
	return ModelResolution{Offering: chosen, Source: ModelSourceDefault}
}

// refuseModel is the resolution that names nothing available.
func refuseModel(kind ModelConflictKind, layer ModelSource, named string, candidates []generation.Offering) ModelResolution {
	return ModelResolution{Conflict: &ModelConflict{
		Kind: kind, Layer: layer, Named: named, Candidates: candidates,
	}}
}

// familyShape finds the Offering of the same model family that passes accept,
// in Catalog order: the shape a lock needs when the person's own shape cannot
// take the request (a multiview step under a single-picture lock, #417).
func familyShape(candidates []generation.Offering, offering generation.Offering, accept func(generation.Offering) bool) (generation.Offering, bool) {
	if offering.ModelFamily == "" {
		return generation.Offering{}, false
	}
	for _, candidate := range candidates {
		if candidate.ID == offering.ID || !SameModelFamily(offering, candidate) {
			continue
		}
		if accept(candidate) {
			return candidate, true
		}
	}
	return generation.Offering{}, false
}

// SameModelFamily reports whether two Offerings are the same upstream model in
// different step shapes, as the Catalog declares it (Offering.ModelFamily).
func SameModelFamily(a, b generation.Offering) bool {
	if a.ModelFamily == "" || b.ModelFamily == "" || a.ModelFamily != b.ModelFamily {
		return false
	}
	return a.ProviderID == b.ProviderID && a.Domain == b.Domain
}

// ModelNameMatches reports whether name selects an Offering: its model ID, its
// Offering ID, or the provider and model ID pair the Catalog also shows.
func ModelNameMatches(offering generation.Offering, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	return name == offering.ModelID || name == string(offering.ID) ||
		(offering.ModelID != "" && offering.ProviderID != "" && name == offering.ProviderID+"/"+offering.ModelID)
}

// MatchModel finds the Offering a name selects among candidates.
func MatchModel(candidates []generation.Offering, name string) (generation.Offering, bool) {
	for _, offering := range candidates {
		if ModelNameMatches(offering, name) {
			return offering, true
		}
	}
	return generation.Offering{}, false
}

// recommendedWorkspace finds the workspace picker's model among the purpose's
// recommendations, available and fitting the input (#282): a visible pick the
// purpose already recommends needs no confirmation.
func recommendedWorkspace(candidates []generation.Offering, recommended []string, workspace string,
	fits func(generation.Offering) bool) (generation.Offering, bool) {
	visible, matched := MatchModel(candidates, workspace)
	if !matched || !fits(visible) {
		return generation.Offering{}, false
	}
	for _, name := range recommended {
		if ModelNameMatches(visible, name) {
			return visible, true
		}
	}
	return generation.Offering{}, false
}

// PreferredOffering picks the Offering to use when no layer named one: the one
// with the default flag first, then the one recommended, then the first, in
// Catalog order.
func PreferredOffering(candidates []generation.Offering) (generation.Offering, bool) {
	var recommended *generation.Offering
	for index := range candidates {
		offering := &candidates[index]
		if offering.Default {
			return *offering, true
		}
		if recommended == nil && offering.Recommended {
			recommended = offering
		}
	}
	switch {
	case recommended != nil:
		return *recommended, true
	case len(candidates) > 0:
		return candidates[0], true
	}
	return generation.Offering{}, false
}

// The keys of a purpose's recommended_offerings (#103, #239). They name the
// shape of the request: the 3-D usage recommends per input shape, the 2-D
// usage per capability.
const (
	ModelShapeTextTo3D      = "text-to-3d"
	ModelShapeImageTo3D     = "image-to-3d"
	ModelShapeMultiviewTo3D = "multiview-to-3d"
	ModelShapeTextToImage   = "text-to-image"
	ModelShapeImageToImage  = "image-to-image"
)

// ModelShapeOfCapability is the recommended_offerings key of a step declared
// by its capability, for a caller that knows no tool (the production plan). A
// multiview step is declared as image-to-3d, so it resolves the single-picture
// shape: a plan step that runs several views names its model itself.
func ModelShapeOfCapability(capability generation.Capability) string {
	switch capability {
	case generation.TextTo3D:
		return ModelShapeTextTo3D
	case generation.ImageTo3D:
		return ModelShapeImageTo3D
	case generation.TextToImage:
		return ModelShapeTextToImage
	case generation.ImageToImage:
		return ModelShapeImageToImage
	default:
		return ""
	}
}

// RecommendedOfferings is the declared recommendations of a purpose (3-D or
// 2-D) for a shape, in order; nil when the purpose declares none.
func RecommendedOfferings(profile PurposeProfile, shape string) []string {
	if shape == "" {
		return nil
	}
	return profile.RecommendedOfferings[shape]
}
