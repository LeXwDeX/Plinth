package chat

import (
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #380 step 2: the turn plan.
//
// One turn has one model decision per capability of the workspace domains. The
// service computes it when the turn starts, from the frozen session state and
// the Catalog's availability, and freezes it on the turn; the generation tools
// and the production plan read it instead of resolving the same layers again.
// The layers are the ones of ResolveModel, so a turn that runs after a restart
// decides its models like it did at its start.
//
// What the plan does not decide: the Agent's own model argument, the input
// shape of one call (its pictures, its views and the frozen mask) and the plan
// pin. Those belong to the call, so the tools still resolve them through
// ResolveModel, with the lock and the workspace value the plan froze.
//
// The plan decides nothing about the turn's own structured multiview request
// either: that request replaces the lock of its own tool and is not a session
// decision (see StartTurn).

// TurnPlan is the model decision of one turn's workspace domains, frozen with
// the turn. Nil for a turn stored before it and for a service with no Catalog
// wired.
type TurnPlan struct {
	// Domains holds one entry per capability a workspace domain declares, in
	// Catalog order.
	Domains []TurnPlanDomain `json:"Domains,omitempty"`
	// Tools names the tools the turn's frozen state already decides: the tool
	// the frozen Harness operation pins. The full tool table (scope, Harness,
	// Skill, canvas and mask narrowing) is #382.
	Tools []string `json:"Tools,omitempty"`
	// Harness is the semantics of the turn's frozen Harness operation: its
	// label, the tool it runs, whether it can be called with the input it
	// still misses, and which inputs those are. #383 renders it.
	Harness *TurnPlanHarness `json:"Harness,omitempty"`
	// MaskCapableDomains are the workspace domains with an enabled,
	// runtime-available Offering whose parameters bind a frozen selection mask
	// (#244). A turn that carries a mask needs one of them.
	MaskCapableDomains []string `json:"MaskCapableDomains,omitempty"`
}

// TurnPlanDomain is one capability's model decision of a workspace domain.
type TurnPlanDomain struct {
	Domain     string `json:"Domain"`
	Capability string `json:"Capability"`
	// Source is the layer that chose Offering; empty when Conflict is set.
	Source     ModelSource `json:"Source,omitempty"`
	OfferingID string      `json:"OfferingID,omitempty"`
	ModelID    string      `json:"ModelID,omitempty"`
	// Family reports that the Offering is the lock's model in another shape
	// (#417).
	Family  bool   `json:"Family,omitempty"`
	Profile string `json:"Profile,omitempty"`
	Reason  string `json:"Reason,omitempty"`
	// Lock is the person's lock as it applies to this capability (see
	// ApplicableModelLock); empty when the session locked nothing here.
	Lock string `json:"Lock,omitempty"`
	// Workspace is the model the workspace picker showed for the domain when
	// the turn started.
	Workspace string `json:"Workspace,omitempty"`
	// Candidates are the Offering IDs the capability can run now, in Catalog
	// order.
	Candidates []string `json:"Candidates,omitempty"`
	// Conflict is why the capability has no model; nil when it has one.
	Conflict *TurnPlanConflict `json:"Conflict,omitempty"`
}

// TurnPlanConflict is one refusal of a frozen capability: which layer refused,
// what it named and which Offering it resolved, with the Offering IDs the
// capability could run instead.
type TurnPlanConflict struct {
	Kind       ModelConflictKind `json:"Kind"`
	Layer      ModelSource       `json:"Layer"`
	Named      string            `json:"Named,omitempty"`
	OfferingID string            `json:"OfferingID,omitempty"`
	LockedID   string            `json:"LockedID,omitempty"`
	Profile    string            `json:"Profile,omitempty"`
	Candidates []string          `json:"Candidates,omitempty"`
}

// TurnPlanHarness is the frozen Harness semantics of a turn (#380 step 2). The
// snapshot itself stays TurnOptions.Harness; this is what the prompt and the
// tool table read, so neither has to know the snapshot's shape.
type TurnPlanHarness struct {
	Label               string   `json:"Label,omitempty"`
	Tool                string   `json:"Tool,omitempty"`
	Status              string   `json:"Status,omitempty"`
	CallableWithMissing bool     `json:"CallableWithMissing,omitempty"`
	MissingParameters   []string `json:"MissingParameters,omitempty"`
}

// Domain is the frozen entry of one capability of a workspace domain.
func (plan *TurnPlan) Domain(domain, capability string) (TurnPlanDomain, bool) {
	if plan == nil {
		return TurnPlanDomain{}, false
	}
	for _, entry := range plan.Domains {
		if entry.Domain == domain && entry.Capability == capability {
			return entry, true
		}
	}
	return TurnPlanDomain{}, false
}

// MaskCapable reports whether a workspace domain has an Offering that binds a
// frozen selection mask.
func (plan *TurnPlan) MaskCapable(domain string) bool {
	if plan == nil {
		return false
	}
	for _, candidate := range plan.MaskCapableDomains {
		if candidate == domain {
			return true
		}
	}
	return false
}

// freezeTurnPlan computes the turn's plan and freezes it on the options. A
// service with no Catalog wired (a hand-built Service, or an assembly without
// the generation module) freezes nothing, and every reader keeps reading the
// frozen session options as before.
func (service *Service) freezeTurnPlan(options *TurnOptions) {
	options.TurnPlan = nil
	if service.turnPlanOfferings == nil {
		return
	}
	offerings := service.turnPlanOfferings()
	plan := TurnPlan{}
	if snapshot := options.Harness; snapshot != nil {
		plan.Harness = &TurnPlanHarness{
			Label:               snapshot.Label,
			Tool:                snapshot.ToolName,
			Status:              snapshot.Status,
			CallableWithMissing: snapshot.CallableWithMissing,
			MissingParameters:   snapshot.MissingParameters,
		}
		if snapshot.ToolName != "" {
			plan.Tools = []string{snapshot.ToolName}
		}
	}
	resolver := PurposeParameters{Model3D: service.model3DUsage, Image2D: service.image2DUsage}
	type capabilityKey struct{ domain, capability string }
	order := make([]capabilityKey, 0, len(offerings))
	groups := map[capabilityKey][]generation.Offering{}
	for _, offering := range offerings {
		// A hand-built Catalog leaves the domain out; without one there is no
		// workspace the entry could belong to. Only the two workspace domains
		// are planned: the processing domain belongs to the direct task API,
		// whose capabilities no chat tool of a workspace runs.
		if !offering.Enabled || offering.Capability == "" || !workspaceDomain(offering.Domain) {
			continue
		}
		key := capabilityKey{domain: string(offering.Domain), capability: string(offering.Capability)}
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], offering)
	}
	for _, key := range order {
		enabled := groups[key]
		candidates := make([]generation.Offering, 0, len(enabled))
		ids := make([]string, 0, len(enabled))
		maskCapable := false
		for _, offering := range enabled {
			if !service.turnPlanOfferingAvailable(offering.ID) {
				continue
			}
			if offeringBindsMask(offering) {
				maskCapable = true
			}
			candidates = append(candidates, offering)
			ids = append(ids, string(offering.ID))
		}
		if maskCapable && !plan.MaskCapable(key.domain) {
			plan.MaskCapableDomains = append(plan.MaskCapableDomains, key.domain)
		}
		entry := TurnPlanDomain{
			Domain:     key.domain,
			Capability: key.capability,
			Lock:       ApplicableModelLock(offerings, options.ExplicitModelFor(key.domain), key.capability),
			Workspace:  strings.TrimSpace(options.ModelFor(key.domain)),
			Candidates: ids,
		}
		profile, _ := resolver.Profile(key.domain, options.ProductionBrief, false)
		recommended := RecommendedOfferings(profile, ModelShapeOfCapability(generation.Capability(key.capability)))
		resolution := ResolveModel(ModelRequest{
			Candidates:  candidates,
			Lock:        entry.Lock,
			Recommended: recommended,
			Profile:     profile.ID,
			Reason:      profile.Reason,
			// The plan quotes what the executor will run, so it reads the same
			// fallback: an inferred purpose never quotes over the picker's value.
			PurposeIsDefault: profile.Default,
			Workspace:        entry.Workspace,
			// The plan words its refusals for the person, like the tools do:
			// the same Schema text decides both.
			ConfirmsWorkspace: service.turnPlanConfirmsWorkspace,
		})
		if resolution.Conflict != nil {
			entry.Conflict = turnPlanConflict(resolution.Conflict)
		} else {
			entry.Source = resolution.Source
			entry.OfferingID = string(resolution.Offering.ID)
			entry.ModelID = resolution.Offering.ModelID
			entry.Family = resolution.Family
			entry.Profile = resolution.Profile
			entry.Reason = resolution.Reason
		}
		plan.Domains = append(plan.Domains, entry)
	}
	if len(plan.Domains) > 0 {
		options.TurnPlan = &plan
	}
}

// turnPlanOfferingAvailable reports whether an Offering can run now. A service
// that wires no availability check treats every enabled Offering as available,
// like the Catalog itself does.
func (service *Service) turnPlanOfferingAvailable(id generation.OfferingID) bool {
	if service.turnPlanAvailable == nil {
		return true
	}
	return service.turnPlanAvailable(id)
}

// turnPlanConflict is the frozen form of a resolution's refusal.
func turnPlanConflict(conflict *ModelConflict) *TurnPlanConflict {
	result := &TurnPlanConflict{
		Kind: conflict.Kind, Layer: conflict.Layer, Named: conflict.Named,
		OfferingID: string(conflict.Offering.ID), LockedID: string(conflict.Locked.ID),
		Profile: conflict.Profile,
	}
	for _, candidate := range conflict.Candidates {
		result.Candidates = append(result.Candidates, string(candidate.ID))
	}
	return result
}

// workspaceDomain reports whether a Catalog domain is one of the two workspace
// domains a chat turn generates in; the processing domain is the direct task
// API's.
func workspaceDomain(domain schema.CapabilityDomain) bool {
	return domain == generation.DomainModel3D || domain == generation.DomainImage
}

// offeringBindsMask reports whether an Offering declares the mask parameter a
// canvas turn's frozen selection mask is written into (#244).
func offeringBindsMask(offering generation.Offering) bool {
	for _, parameter := range offering.Parameters {
		if parameter.Semantic == generation.SemanticMask {
			return true
		}
	}
	return false
}
