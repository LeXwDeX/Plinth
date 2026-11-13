package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// The parameter values a session's production brief gives an Offering: the
// purpose's own overrides, the target platform's variant of them, the fit of the
// purpose budget and the values a select field of the brief gives (#103, #129,
// #139, #275, #277). The Agent's generation tools and the direct task API (#295)
// resolve them here, so one session freezes the same values, sources and
// reasons whichever path starts the step.

// PurposeProfile is the part of a 3D or 2D purpose the model-choice and
// parameter layers read: its identity, recommendations and public parameter
// overrides. Using one shape lets the same layers serve both domains (#103,
// #239).
type PurposeProfile struct {
	ID                   string
	Label                string
	Reason               string
	RecommendedOfferings map[string][]string
	ParameterOverrides   map[string]map[string]json.RawMessage
}

// AdvisedAdjustment is the reason and the source ("profile:<id>") the frozen
// adjustment of an advised parameter carries when this purpose supplied its
// value. Only an advised change is frozen as an adjustment: a purpose may also
// set a public parameter the Offering does not mark advised, which needs no
// reason, and the kernel would refuse a reason given for one (#103).
func (profile PurposeProfile) AdvisedAdjustment(offering generation.Offering, name string) (reason, source string, ok bool) {
	if profile.ID == "" || !offering.AdvisedParameter(name) {
		return "", "", false
	}
	return profile.Reason, "profile:" + profile.ID, true
}

// PurposeParameters resolves the parameter values the production brief gives an
// Offering from the Schema's 3D and 2D usage tables. It holds no session state:
// the brief is passed in, frozen on the turn (Agent) or read from the session
// (direct request).
type PurposeParameters struct {
	Model3D catalog.ChatModel3DUsageSchema
	Image2D catalog.ChatImage2DUsageSchema
}

// PurposeQuery is one resolution of the values the brief gives an Offering.
type PurposeQuery struct {
	Brief ProductionBrief
	// Domain is the workspace domain of the step. The image domain follows the
	// 2D purpose and has no triangle or texture budget; every other domain
	// follows the 3D purpose (#239).
	Domain   string
	Offering generation.Offering
	// ToolDefaults are the values the calling tool gives every Offering. They
	// are the baseline the purpose budget fits when the purpose names none; a
	// request that goes through no tool has none.
	ToolDefaults map[string]json.RawMessage
	// Current holds the values the step already has, which the budget measures a
	// face count against (a remesh topology, Tripo's smart low poly and quad).
	Current map[string]json.RawMessage
	// ExplicitPurposeOnly leaves a 2D call without an image purpose in its brief
	// unchanged. The Agent's 2D call falls back to the Schema's default purpose
	// for an open question; a person who set no purpose asked for none.
	ExplicitPurposeOnly bool
}

// Model3DProfile returns the 3D purpose the brief selects. A brief without one
// has none: nothing is recommended or overridden by a purpose. The profile is
// already the variant for the brief's target platform when the purpose declares
// one (#275), so its budget and parameter overrides are what every later layer
// reads; a brief without a platform gets the purpose as declared.
func (resolver PurposeParameters) Model3DProfile(brief ProductionBrief) (catalog.ChatModel3DUsageProfile, bool) {
	usage := resolver.Model3D
	if len(usage.Profiles) == 0 || brief.Empty() {
		return catalog.ChatModel3DUsageProfile{}, false
	}
	id := brief.Text(usage.Brief.FieldNameOfType("profile"))
	for _, profile := range usage.Profiles {
		if profile.ID == id {
			profile, _ = usage.ForPlatform(profile, brief.Text(usage.PlatformField))
			return profile, true
		}
	}
	return catalog.ChatModel3DUsageProfile{}, false
}

// Image2DProfile returns the 2D purpose the brief follows (#239). The brief's
// image_profile field selects it; when that field is absent the Schema's
// default_profile applies, as the image_use description and the system prompt
// promise ("omit or leave empty to let the default 2D purpose apply"). With
// explicitOnly the default does not apply. A purpose a stale brief names but the
// Schema no longer declares is ignored the same way a session without one,
// matching the 3D usage check.
func (resolver PurposeParameters) Image2DProfile(brief ProductionBrief, explicitOnly bool) (PurposeProfile, bool) {
	usage := resolver.Image2D
	if len(usage.Profiles) == 0 {
		return PurposeProfile{}, false
	}
	id := brief.Text(resolver.Model3D.Brief.FieldNameOfType("image_profile"))
	for _, profile := range usage.Profiles {
		if id != "" && profile.ID == id {
			return image2DPurposeProfile(profile), true
		}
	}
	if explicitOnly {
		return PurposeProfile{}, false
	}
	for _, profile := range usage.Profiles {
		if profile.ID == usage.DefaultProfile {
			return image2DPurposeProfile(profile), true
		}
	}
	return PurposeProfile{}, false
}

// image2DPurposeProfile narrows a declared 2D purpose to the fields the layers
// read.
func image2DPurposeProfile(profile catalog.ChatImage2DUsageProfile) PurposeProfile {
	return PurposeProfile{
		ID: profile.ID, Label: profile.Label, Reason: profile.Reason,
		RecommendedOfferings: profile.RecommendedOfferings,
		ParameterOverrides:   profile.ParameterOverrides,
	}
}

// Profile is the purpose of one workspace domain: the 2D table for the image
// domain, the 3D table otherwise (#239).
func (resolver PurposeParameters) Profile(domain string, brief ProductionBrief, explicitOnly bool) (PurposeProfile, bool) {
	if domain == string(generation.DomainImage) {
		return resolver.Image2DProfile(brief, explicitOnly)
	}
	profile, found := resolver.Model3DProfile(brief)
	if !found {
		return PurposeProfile{}, false
	}
	return PurposeProfile{
		ID: profile.ID, Label: profile.Label, Reason: profile.Reason,
		RecommendedOfferings: profile.RecommendedOfferings,
		ParameterOverrides:   profile.ParameterOverrides,
	}, true
}

// Values are the values the brief applies to an Offering, with the purpose that
// gave them: its parameter overrides, the values its budget gives the bounded
// parameters (#129) and what a select field of the brief gives (#277). The
// Catalog has already checked every override at load time; this repeats the
// public-parameter rule so a hand-built resolver cannot apply a value the
// Offering does not expose to the Agent.
func (resolver PurposeParameters) Values(query PurposeQuery) (PurposeProfile, map[string]json.RawMessage) {
	profile, found := resolver.Profile(query.Domain, query.Brief, query.ExplicitPurposeOnly)
	values := map[string]json.RawMessage{}
	if found {
		overrides := profile.ParameterOverrides[string(query.Offering.ID)]
		for name, value := range overrides {
			if publicAgentParameter(query.Offering, name) {
				values[name] = value
			}
		}
		// The triangle and texture budget belongs to 3D purposes (#129); a 2D
		// purpose contributes only its declared parameter overrides.
		if query.Domain != string(generation.DomainImage) {
			if model3d, ok := resolver.Model3DProfile(query.Brief); ok {
				for name, value := range resolver.budgetValues(query, model3d, values) {
					values[name] = value
				}
			}
		}
	}
	// What a select field of the brief gives the Offering (#277) is the person's
	// own, more specific choice than the purpose's generic values, and holds with
	// or without a purpose.
	for name, value := range resolver.briefDefaultValues(query.Brief, query.Offering) {
		values[name] = value
	}
	if len(values) == 0 {
		return profile, nil
	}
	return profile, values
}

// publicAgentParameter reports whether an Offering declares name as a
// parameter the Agent may set: the rule every value a layer gives an Offering on
// its own must pass.
func publicAgentParameter(offering generation.Offering, name string) bool {
	for _, parameter := range offering.Parameters {
		if parameter.Name == name {
			return offering.AgentParameterAllowed(parameter)
		}
	}
	return false
}

// briefDefaultValues are the values the brief's select fields give an Offering
// through the Schema's brief.parameter_defaults (#277): the target engine picks
// the skeleton naming of a rigging step. They fill only what the step left
// unset, so a value the person or the Agent set for the step stays theirs.
func (resolver PurposeParameters) briefDefaultValues(brief ProductionBrief, offering generation.Offering) map[string]json.RawMessage {
	if brief.Empty() {
		return nil
	}
	var values map[string]json.RawMessage
	for _, entry := range resolver.Model3D.Brief.ParameterDefaults {
		if brief.Text(entry.Field) != entry.Value {
			continue
		}
		for name, value := range entry.ParameterOverrides[string(offering.ID)] {
			if !publicAgentParameter(offering, name) {
				continue
			}
			if values == nil {
				values = map[string]json.RawMessage{}
			}
			values[name] = value
		}
	}
	return values
}

// UsageBudget applies the brief's triangle override to the profile's maximum.
// The field is located by its declared type, so a renamed Schema field keeps
// working (#103 review C3). The value is reported verbatim even when it is below
// the profile's minimum; the check then fails for the range it actually
// declares.
func UsageBudget(usage catalog.ChatModel3DUsageSchema, profile catalog.ChatModel3DUsageProfile, brief ProductionBrief) (catalog.ChatModel3DUsageBudget, *int) {
	budget := profile.Budget
	if value, exists := brief.Integer(usage.Brief.FieldNameOfType("integer")); exists && value > 0 {
		budget.MaxTriangles = value
		return budget, &value
	}
	return budget, nil
}

// budgetValues are the values the purpose budget gives an Offering's bounded
// public parameters whose baseline is over it. overrides are the purpose's own
// values; the query's Current holds values the step already has.
//
// The baseline is first moved into the range the parameter's maximum_when
// entries give the step's combination (#139), as normalization moves a default:
// the stricter of that range and the budget wins. A purpose override or tool
// default moved that way is returned even within the budget, since
// normalization would refuse it as given; an Offering default is left to
// normalization.
func (resolver PurposeParameters) budgetValues(query PurposeQuery, profile catalog.ChatModel3DUsageProfile,
	overrides map[string]json.RawMessage) map[string]json.RawMessage {
	usage := resolver.Model3D
	if len(usage.BudgetLimits) == 0 {
		return nil
	}
	offering := query.Offering
	budget, _ := UsageBudget(usage, profile, query.Brief)
	values := map[string]json.RawMessage{}
	for _, parameter := range offering.Parameters {
		if len(parameter.Default) > 0 {
			values[parameter.Name] = parameter.Default
		}
	}
	for _, layer := range []map[string]json.RawMessage{query.ToolDefaults, overrides, query.Current} {
		for name, value := range layer {
			values[name] = value
		}
	}
	combination := DecodedValues(values)
	result := map[string]json.RawMessage{}
	for _, parameter := range offering.Parameters {
		limit, bounded := usage.BudgetLimit(parameter.Semantic)
		if !bounded || !offering.AgentParameterAllowed(parameter) {
			continue
		}
		maximum, declared := limit.Maximum(budget)
		if !declared {
			continue
		}
		baseline, fromDefault := overrides[parameter.Name], false
		if baseline == nil {
			baseline = query.ToolDefaults[parameter.Name]
		}
		if baseline == nil {
			baseline, fromDefault = parameter.Default, true
		}
		converged := ConvergedValue(parameter, baseline, combination)
		if value, fits := limit.Fit(offering, parameter, converged, values, maximum); fits {
			result[parameter.Name] = value
		} else if !fromDefault && !sameJSON(converged, baseline) {
			result[parameter.Name] = converged
		}
	}
	return result
}

// ConvergedValue moves a number into the range the parameter's maximum_when
// entries give combination; any other value is returned as it is.
func ConvergedValue(parameter generation.Parameter, value json.RawMessage, combination map[string]any) json.RawMessage {
	var number float64
	if parameter.Type != "number" || len(parameter.MaximumWhen) == 0 || json.Unmarshal(value, &number) != nil {
		return value
	}
	clamped := parameter.ClampNumber(number, combination)
	if clamped == number {
		return value
	}
	encoded, err := json.Marshal(clamped)
	if err != nil {
		return value
	}
	return encoded
}

// DecodedValues decodes a step's values for condition matching.
func DecodedValues(values map[string]json.RawMessage) map[string]any {
	decoded := make(map[string]any, len(values))
	for name, raw := range values {
		var value any
		if json.Unmarshal(raw, &value) == nil {
			decoded[name] = value
		}
	}
	return decoded
}

// sameJSON reports whether two encodings hold the same value.
func sameJSON(a, b json.RawMessage) bool {
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}

// RefreshedPurposeValues chooses the purpose's values of a step again once every
// layer has set its values (#139). first are the values the purpose layer put
// into the step, current is the step's values and final the purpose's values
// resolved against current. A value a later layer replaced is left alone; one
// that is still the purpose's takes its refreshed value, or is dropped when the
// purpose no longer needs it, which leaves it to the Offering's default.
func RefreshedPurposeValues(first, current, final map[string]json.RawMessage) (refreshed map[string]json.RawMessage, dropped []string) {
	refreshed = map[string]json.RawMessage{}
	for name, value := range first {
		if now, present := current[name]; !present || !sameJSON(now, value) {
			continue
		}
		next, kept := final[name]
		if !kept {
			dropped = append(dropped, name)
			continue
		}
		refreshed[name] = next
	}
	return refreshed, dropped
}

// DirectPurposeInput is a direct task request's input after the brief's values
// were applied, with the adjustment evidence the kernel freezes beside it.
type DirectPurposeInput struct {
	// Input is the request's input with the parameters it left unset filled in;
	// the request's own bytes when nothing was.
	Input json.RawMessage
	// Reasons are the caller's adjustment reasons plus the purpose's reason for
	// every advised value the purpose supplied.
	Reasons map[string]string
	// Sources labels the reasons the purpose gave with "profile:<id>"; a reason
	// without a source is the caller's own.
	Sources map[string]string
}

// ApplyToDirectInput gives a direct request the values the brief gives the
// Offering, for the parameters the request left unset only (#295): what the
// caller set stays the caller's even when it equals the brief's value, and then
// needs the caller's own reason when it is an advised change. The values are
// chosen as the Agent chooses them: once alone, then again against the whole
// input, because the purpose budget measures a face count by the step's
// topology (#139). The advised ones carry the purpose's reason and source, as an
// Agent step does, so the kernel freezes them as adjustments. A brief without
// any value for the Offering leaves the request exactly as it was.
func (resolver PurposeParameters) ApplyToDirectInput(brief ProductionBrief, offering generation.Offering,
	input json.RawMessage, reasons map[string]string) (DirectPurposeInput, error) {
	unchanged := DirectPurposeInput{Input: input, Reasons: reasons}
	if brief.Empty() {
		return unchanged, nil
	}
	values := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(input)) > 0 {
		if err := json.Unmarshal(input, &values); err != nil || values == nil {
			// The shape is not an input object; normalization reports it as before.
			return unchanged, nil
		}
	}
	query := PurposeQuery{
		Brief: brief, Domain: string(offering.Domain), Offering: offering, ExplicitPurposeOnly: true,
	}
	profile, first := resolver.Values(query)
	supplied := map[string]json.RawMessage{}
	for name, value := range first {
		if _, set := values[name]; !set {
			supplied[name] = value
		}
	}
	if len(supplied) == 0 {
		return unchanged, nil
	}
	for name, value := range supplied {
		values[name] = value
	}
	query.Current = values
	_, final := resolver.Values(query)
	refreshed, dropped := RefreshedPurposeValues(supplied, values, final)
	for _, name := range dropped {
		delete(values, name)
		delete(supplied, name)
	}
	for name, value := range refreshed {
		values[name] = value
		supplied[name] = value
	}
	merged := map[string]string{}
	for name, reason := range reasons {
		merged[name] = reason
	}
	sources := map[string]string{}
	for name := range supplied {
		if _, explained := merged[name]; explained {
			continue
		}
		if reason, source, ok := profile.AdvisedAdjustment(offering, name); ok {
			merged[name], sources[name] = reason, source
		}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return unchanged, err
	}
	return DirectPurposeInput{Input: encoded, Reasons: merged, Sources: sources}, nil
}

// ApplyPurposeToDirectInput reads the brief of the owner's session and applies
// it to a direct task request (#295): the same purpose, platform and engine
// values the Agent freezes for that session, filled into the parameters the
// request left unset. A session without a brief is left alone. A missing or
// foreign session is ErrSessionNotFound.
func (service *Service) ApplyPurposeToDirectInput(ctx context.Context, ownerID, sessionID string,
	offering generation.Offering, input json.RawMessage, reasons map[string]string) (DirectPurposeInput, error) {
	brief, err := service.SessionBrief(ctx, ownerID, sessionID)
	if err != nil {
		return DirectPurposeInput{}, err
	}
	resolver := PurposeParameters{Model3D: service.model3DUsage, Image2D: service.image2DUsage}
	return resolver.ApplyToDirectInput(brief, offering, input, reasons)
}

// followsPurposeParameter reports whether the Offering declares name as a
// parameter that follows the session purpose (presentation.follows_purpose,
// #292).
func followsPurposeParameter(offering generation.Offering, name string) bool {
	for _, parameter := range offering.Parameters {
		if parameter.Name == name {
			return parameter.Presentation != nil && parameter.Presentation.FollowsPurpose
		}
	}
	return false
}

// declaresFollowingParameter reports whether the Offering has any parameter that
// follows the session purpose.
func declaresFollowingParameter(offering generation.Offering) bool {
	for _, parameter := range offering.Parameters {
		if parameter.Presentation != nil && parameter.Presentation.FollowsPurpose {
			return true
		}
	}
	return false
}

// ApplyToPlanStep gives a processing plan step, as a person assembles it in the
// 加工 panel, the values the brief gives the parameters the Offering declares as
// following the purpose (#292), for the ones the request omitted only: what the
// request set stays its own even when it equals the Offering's default. The
// values are the ones the Agent and the direct task API resolve, taken from
// Values; every other value the purpose gives the Offering is left alone, so the
// request the panel sent, the price it quoted and the chain checks the plan passes
// stay what they were. A brief without a value for those parameters, or an input
// that is not a JSON object (the plan reports it), is returned exactly as it was.
func (resolver PurposeParameters) ApplyToPlanStep(brief ProductionBrief, offering generation.Offering,
	input json.RawMessage) (json.RawMessage, error) {
	if brief.Empty() || len(bytes.TrimSpace(input)) == 0 {
		return input, nil
	}
	values := map[string]json.RawMessage{}
	if err := json.Unmarshal(input, &values); err != nil || values == nil {
		return input, nil
	}
	_, given := resolver.Values(PurposeQuery{
		Brief: brief, Domain: string(offering.Domain), Offering: offering, Current: values, ExplicitPurposeOnly: true,
	})
	filled := false
	for name, value := range given {
		if _, set := values[name]; set || !followsPurposeParameter(offering, name) {
			continue
		}
		values[name] = value
		filled = true
	}
	if !filled {
		return input, nil
	}
	return json.Marshal(values)
}

// ApplyPurposeToPlanStep reads the brief of the owner's session and applies it to
// a step of a processing plan (#292). An Offering with no parameter that follows
// the purpose is returned untouched without reading the session. A missing or
// foreign session is ErrSessionNotFound.
func (service *Service) ApplyPurposeToPlanStep(ctx context.Context, ownerID, sessionID string,
	offering generation.Offering, input json.RawMessage) (json.RawMessage, error) {
	if !declaresFollowingParameter(offering) {
		return input, nil
	}
	brief, err := service.SessionBrief(ctx, ownerID, sessionID)
	if err != nil {
		return nil, err
	}
	resolver := PurposeParameters{Model3D: service.model3DUsage, Image2D: service.image2DUsage}
	return resolver.ApplyToPlanStep(brief, offering, input)
}
