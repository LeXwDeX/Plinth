package imagereview

// The 2D purpose checks of the delivery checklist (#282, #284, #287). The
// session's production brief names a 2D purpose (or the Schema's default
// purpose applies); the purpose declares a minimum short edge, whether the
// picture must tile and what its background may be. This file turns that
// declaration and the Schema's thresholds into imageinspect.Requirements and
// writes the Agent's next step for each failed check from the Schema's own
// words. The measurements and verdicts are imageinspect's; nothing here knows a
// model, a size or a threshold.

import (
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/imageinspect"
)

// Where the purpose came from, reported beside the verdicts so the Agent can
// say when the Schema's default judged the picture instead of the person's
// choice (the same words inspect_model uses).
const (
	purposeSourceBrief   = "production_brief"
	purposeSourceDefault = "default_profile"
	// purposeSourceArgument marks a purpose the caller named explicitly, which
	// judges this inspection only and is not written into a session (#332 7b-2).
	purposeSourceArgument = "argument"
)

// purposeJudgement is the purpose a delivery inspection judges against.
type purposeJudgement struct {
	profile      catalog.ChatImage2DUsageProfile
	source       string
	requirements imageinspect.Requirements
}

// OfferingLookup finds an Offering by ID, to name the models that reach a
// purpose's minimum short edge. Nil names none.
type OfferingLookup func(id string) (generation.Offering, bool)

// judgement resolves the purpose of a delivery inspection. It returns nil when
// the Schema declares no purpose checks, no purposes, or no purpose applies, so
// a Catalog without them keeps the historical checklist. A brief purpose the
// Schema no longer declares is ignored like a brief with none, matching the
// model choice and the 3D usage check.
func (service *Service) judgement(brief chat.ProductionBrief) *purposeJudgement {
	usage := service.review.Usage
	if usage == nil || len(service.usage.Profiles) == 0 {
		return nil
	}
	var profile catalog.ChatImage2DUsageProfile
	source, found := purposeSourceBrief, false
	if id := brief.Text(service.imageField); id != "" {
		for _, candidate := range service.usage.Profiles {
			if candidate.ID == id {
				profile, found = candidate, true
				break
			}
		}
	}
	if !found {
		source = purposeSourceDefault
		for _, candidate := range service.usage.Profiles {
			if candidate.ID == service.usage.DefaultProfile {
				profile, found = candidate, true
				break
			}
		}
	}
	if !found {
		return nil
	}
	return &purposeJudgement{profile: profile, source: source,
		requirements: service.judgementRequirements(profile, brief)}
}

// judgementForPurpose resolves an explicit purpose argument (#332 7b-2). The
// argument judges this inspection only; it is never written into a session. A
// name the Schema does not declare has no judgement, so the caller refuses it.
func (service *Service) judgementForPurpose(id string, brief chat.ProductionBrief) *purposeJudgement {
	if service.review.Usage == nil {
		return nil
	}
	for _, profile := range service.usage.Profiles {
		if profile.ID == id {
			return &purposeJudgement{profile: profile, source: purposeSourceArgument,
				requirements: service.judgementRequirements(profile, brief)}
		}
	}
	return nil
}

// judgementRequirements turns one purpose and the Schema's thresholds into the
// measurements the picture is judged by.
func (service *Service) judgementRequirements(profile catalog.ChatImage2DUsageProfile,
	brief chat.ProductionBrief,
) imageinspect.Requirements {
	usage := service.review.Usage
	requirements := imageinspect.Requirements{
		MinShortEdge: profile.Requirements.MinimumShortEdge,
		Tileable:     profile.Requirements.Tileable,
		Tiling: imageinspect.TilingLimits{
			Segments:     usage.Tiling.Segments,
			MaxSeamRatio: usage.Tiling.MaxSeamRatio,
			MinSeamDelta: usage.Tiling.MinSeamDelta,
		},
		Alpha: imageinspect.AlphaLimits{
			MinTransparentRatio: usage.Alpha.MinTransparentRatio,
			MaxBackgroundSpread: usage.Alpha.MaxBackgroundSpread,
		},
		Fringe: imageinspect.FringeLimits{
			BandRatio: usage.EdgeFringe.BandRatio,
			LightLuma: usage.EdgeFringe.LightLuma,
			LumaGap:   usage.EdgeFringe.LumaGap,
			MaxRatio:  usage.EdgeFringe.MaxRatio,
		},
	}
	switch profile.Requirements.Background {
	case "transparent":
		requirements.Transparency = imageinspect.TransparencyRequired
	case "solid_or_transparent":
		requirements.Transparency = imageinspect.TransparencyAllowed
	}
	// The person's own words win over what the purpose accepts: a transparent
	// background they asked for is judged as one whatever the purpose is.
	if usage.TransparentField != "" && brief.Flag(usage.TransparentField) {
		requirements.Transparency = imageinspect.TransparencyRequired
	}
	return requirements
}

// note says which purpose judged the picture. An explicit argument judges only
// this inspection and has no note to write.
func (service *Service) note(judgement *purposeJudgement) string {
	if judgement.source == purposeSourceArgument {
		return ""
	}
	usage := service.review.Usage
	template := usage.BriefPurposeNote
	if judgement.source == purposeSourceDefault {
		template = usage.DefaultPurposeNote
	}
	return strings.ReplaceAll(template, "{purpose}", judgement.profile.Label)
}

// addHints writes the Agent's next step into every failed purpose check, from
// the Schema's hint of that check. A check without a hint keeps none.
func (service *Service) addHints(checks []imageinspect.Check, judgement *purposeJudgement) {
	usage := service.review.Usage
	for index := range checks {
		check := &checks[index]
		if check.Status != imageinspect.StatusFail {
			continue
		}
		hint := usage.Hints[check.Name]
		if hint == "" {
			continue
		}
		replacer := []string{
			"{actual}", check.Actual,
			"{expected}", check.Expected,
			"{purpose}", judgement.profile.Label,
			"{minimum}", strconv.Itoa(judgement.profile.Requirements.MinimumShortEdge),
		}
		if strings.Contains(hint, "{models}") {
			replacer = append(replacer, "{models}", service.reachingModels(judgement.profile))
		}
		check.Hint = strings.NewReplacer(replacer...).Replace(hint)
	}
}

// reachingModels names the purpose's recommended models that the Schema shows
// reaching its minimum short edge, or says that none can be shown to. Models of
// both input shapes share a name, so each is listed once.
func (service *Service) reachingModels(profile catalog.ChatImage2DUsageProfile) string {
	usage := service.review.Usage
	if service.offerings == nil {
		return usage.NoModelsText
	}
	var names []string
	seen := map[string]bool{}
	for _, offering := range service.usage.ReachingOfferings(profile, service.offerings) {
		name := offering.ModelName
		if name == "" {
			name = offering.ModelID
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	if len(names) == 0 {
		return usage.NoModelsText
	}
	return strings.ReplaceAll(usage.ModelsTemplate, "{models}", strings.Join(names, usage.ModelsSeparator))
}
