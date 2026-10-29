package app

import (
	"encoding/json"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

type agentOfferingView struct {
	PriceMode          string              `json:"price_mode,omitempty"`
	DefaultNativeQuote *schema.NativeQuote `json:"default_native_quote,omitempty"`
	UpperNativeQuote   *schema.NativeQuote `json:"upper_native_quote,omitempty"`
	OfferingID         string              `json:"offering_id"`
	ModelID            string              `json:"model_id"`
	// An absent capacity is unknown, not a single-picture limit.
	ReferenceImageMax int `json:"reference_image_max,omitempty"`
	EstimatedCredits  int `json:"estimated_credits,omitempty"`
	// ExtraCredits are charged on top of EstimatedCredits when one of
	// ExtraCreditParameters takes a billed value.
	ExtraCredits          int      `json:"extra_credits,omitempty"`
	ExtraCreditParameters []string `json:"extra_credit_parameters,omitempty"`
	// DefaultQuote prices the step as it is frozen without further inputs
	// (declared and fixed defaults): ExtraCredits above is per billed item,
	// this is the whole amount (#321 I).
	DefaultQuote *agentQuoteView   `json:"default_quote,omitempty"`
	Sources      []agentSourceView `json:"sources,omitempty"`
	Inputs       []agentInputView  `json:"inputs,omitempty"`
}

// agentInputView is one public input the Agent may set, with the choices
// the Offering declares (for example the animation actions).
type agentInputView struct {
	ImageSize        *schema.ImageSizeConstraint `json:"image_size,omitempty"`
	Integer          bool                        `json:"integer,omitempty"`
	Default          json.RawMessage             `json:"default,omitempty"`
	AdjustmentPolicy string                      `json:"adjustment_policy,omitempty"`
	AdjustmentReason string                      `json:"adjustment_reason,omitempty"`
	MinItems         int                         `json:"min_items,omitempty"`
	MaxItems         int                         `json:"max_items,omitempty"`
	Name             string                      `json:"name"`
	Label            string                      `json:"label,omitempty"`
	Type             string                      `json:"type"`
	Required         bool                        `json:"required,omitempty"`
	Minimum          *float64                    `json:"minimum,omitempty"`
	Maximum          *float64                    `json:"maximum,omitempty"`
	MaxLength        int                         `json:"max_length,omitempty"`
	Options          []agentOptionView           `json:"options,omitempty"`
	// OptionsCompact replaces Options for a list longer than
	// agentCompactOptionThreshold: comma-separated "value:Name" entries, where
	// Name is the official name a label carries in trailing parentheses
	// (for example "4:Attack"). Chinese text and groups stay in the public
	// Schema; this keeps every list_offerings result small for the LLM.
	OptionsCompact string `json:"options_compact,omitempty"`
	// Supply is set on an image the person provides in the conversation, not
	// a value the Agent writes: "chat_image" inputs are passed as
	// reference_handles (a handle such as @img1). Required says whether the
	// step can run without it (#92).
	Supply     string   `json:"supply,omitempty"`
	MediaTypes []string `json:"media_types,omitempty"`
	MaxBytes   int64    `json:"max_bytes,omitempty"`
}

// agentCompactOptionThreshold is the largest option list the Agent projection
// still sends with full labels and groups.
const agentCompactOptionThreshold = 64

type agentOptionView struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
	Group string `json:"group,omitempty"`
}

// agentQuoteView splits a credit price: base, extra and what the extra pays
// for, and whether that extra is fixed (always charged) or follows a choice.
type agentQuoteView struct {
	BaseCredits  int    `json:"base_credits"`
	ExtraCredits int    `json:"extra_credits,omitempty"`
	ExtraFor     string `json:"extra_for,omitempty"`
	ExtraFixed   bool   `json:"extra_fixed,omitempty"`
	TotalCredits int    `json:"total_credits"`
}

// agentDefaultQuote prices an Offering's declared defaults, fixed values
// included. Native-priced Offerings carry their own native quotes instead.
func agentDefaultQuote(offering generation.Offering) *agentQuoteView {
	if offering.NativePricing != nil || offering.Cost == nil {
		return nil
	}
	values := map[string]json.RawMessage{}
	fixedExtra := false
	for _, parameter := range offering.Parameters {
		if len(parameter.Default) > 0 && string(parameter.Default) != "null" {
			values[parameter.Name] = parameter.Default
		}
		if parameter.Fixed && parameter.Name == offering.Cost.SelectionParameter {
			fixedExtra = true
		}
	}
	input, _ := json.Marshal(values)
	total := offering.EstimateTotalCredits(input)
	detail := offering.ExtraCreditDetail(input)
	quote := &agentQuoteView{BaseCredits: total - detail.Credits, ExtraCredits: detail.Credits, TotalCredits: total}
	if detail.Credits > 0 {
		quote.ExtraFixed = fixedExtra
		quote.ExtraFor = detail.Label
		if len(detail.Items) > 0 {
			quote.ExtraFor = strings.TrimSpace(detail.Label + ": " + strings.Join(detail.Items, ", "))
		}
	}
	return quote
}

type agentSourceView struct {
	Parameter        string   `json:"parameter"`
	TaskCapabilities []string `json:"task_capabilities,omitempty"`
	SameProvider     bool     `json:"same_provider,omitempty"`
	MediaTypes       []string `json:"media_types,omitempty"`
}

type agentCapabilityView struct {
	Capability string              `json:"capability"`
	Domain     string              `json:"domain"`
	Offerings  []agentOfferingView `json:"offerings"`
}

type agentOfferingsSource interface {
	List(generation.Capability) []generation.Offering
}

// buildAgentOfferingsProjection 产出 schema→agent 的紧凑 offerings 投影，
// 供 list_offerings 工具喂给 LLM 做能力/模型选择（domain 区分 2D/3D）。
func buildAgentOfferingsProjection(
	capabilities []catalog.CapabilityPresentation,
	offerings agentOfferingsSource,
	available func(generation.OfferingID) bool,
) json.RawMessage {
	views := make([]agentCapabilityView, 0, len(capabilities))
	for _, capability := range capabilities {
		view := agentCapabilityView{
			Capability: string(capability.ID),
			Domain:     string(capability.Domain),
		}
		for _, offering := range offerings.List(capability.ID) {
			if available != nil && !available(offering.ID) {
				// list_offerings 只投影运行时可用项：LLM 不能选择
				// 未注册/未启用的模型（B14）。
				continue
			}
			offeringView := agentOfferingView{
				OfferingID: string(offering.ID),
				ModelID:    offering.ModelID,
			}
			for _, parameter := range offering.Parameters {
				if parameter.Semantic == generation.SemanticReferenceImage && parameter.Type == "asset-array" && parameter.MaxItems > 0 {
					offeringView.ReferenceImageMax = parameter.MaxItems
					break
				}
			}
			if offering.NativePricing != nil {
				offeringView.PriceMode = "native"
				offeringView.DefaultNativeQuote, _ = offering.DefaultNativeQuote(nil)
				offeringView.UpperNativeQuote, _ = offering.UpperNativeQuote()
			}
			offeringView.Inputs = agentInputViews(offering)
			if capability.Domain == generation.DomainProcessing {
				offeringView.Inputs = append(offeringView.Inputs, agentImageInputViews(offering)...)
				if offering.NativePricing == nil {
					// The quoted price includes the extras of fixed values (#324: Tripo's
					// complete export delivery), so the Agent never quotes the bare base.
					offeringView.EstimatedCredits = offering.QuotedCredits()
				}
				for _, parameter := range offering.Parameters {
					role, isSource := schema.SourceParameterRole(parameter)
					if !isSource {
						continue
					}
					chainSource := (role == schema.SemanticSourceTask && parameter.Type == "task") ||
						(role == schema.SemanticSourceAsset && parameter.Type == "asset")
					if !chainSource {
						continue
					}
					source := agentSourceView{Parameter: parameter.Name, SameProvider: parameter.SameProvider,
						MediaTypes: append([]string(nil), parameter.MediaTypes...)}
					for _, allowed := range parameter.TaskCapabilities {
						source.TaskCapabilities = append(source.TaskCapabilities, string(allowed))
					}
					offeringView.Sources = append(offeringView.Sources, source)
				}
				offeringView.DefaultQuote = agentDefaultQuote(offering)
				// Extra credits are a choice only when some billed option is not fixed.
				if cost := offering.Cost; cost != nil && cost.ExtraCredits > 0 && offering.MaximumEstimatedCredits() > offering.QuotedCredits() {
					offeringView.ExtraCredits = cost.ExtraCredits
					for _, condition := range cost.ExtraWhen {
						offeringView.ExtraCreditParameters = append(offeringView.ExtraCreditParameters, condition.Parameter)
					}
					if cost.SelectionParameter != "" {
						offeringView.ExtraCreditParameters = append(offeringView.ExtraCreditParameters, cost.SelectionParameter)
					}
				}
			}
			view.Offerings = append(view.Offerings, offeringView)
		}
		views = append(views, view)
	}
	out, err := json.Marshal(map[string]any{"capabilities": views})
	if err != nil {
		return json.RawMessage(`{"capabilities":[]}`)
	}
	return out
}

// agentInputViews lists the scalar inputs of a processing Offering. Source,
// file-valued and consent parameters are not the Agent's to set directly.
func agentInputViews(offering generation.Offering) []agentInputView {
	var views []agentInputView
	for _, parameter := range offering.Parameters {
		if _, isSource := schema.SourceParameterRole(parameter); isSource {
			continue
		}
		switch parameter.Type {
		case "asset", "asset-array", "task":
			continue
		}
		if parameter.Name == offering.ExtraConsentParameter() {
			continue
		}
		if !offering.AgentParameterAllowed(parameter) {
			continue
		}
		view := agentInputView{ImageSize: parameter.ImageSize, Integer: parameter.Integer, Name: parameter.Name, Label: parameter.Label, Type: parameter.Type,
			Required: parameter.Required, Minimum: parameter.Minimum, Maximum: parameter.Maximum,
			MaxLength: parameter.MaxLength, Default: parameter.Default, AdjustmentPolicy: parameter.AdjustmentPolicy, AdjustmentReason: parameter.AdjustmentReason, MinItems: parameter.MinItems, MaxItems: parameter.MaxItems}
		if compact, ok := compactAgentOptions(parameter.Options); ok {
			view.OptionsCompact = compact
		} else {
			for _, option := range parameter.Options {
				view.Options = append(view.Options, agentOptionView{Value: option.Value, Label: option.Label, Group: option.Group})
			}
		}
		views = append(views, view)
	}
	return views
}

// agentImageInputViews lists the reference images an Offering takes from the
// conversation, so the Agent sees that a service requires (or accepts) a
// picture instead of concluding it takes only a prompt (#92). They come from
// the Offering's own reference-image parameters; nothing is hard-coded.
func agentImageInputViews(offering generation.Offering) []agentInputView {
	var views []agentInputView
	for _, parameter := range offering.Parameters {
		if parameter.Semantic != generation.SemanticReferenceImage || parameter.Fixed || parameter.Name == offering.ExtraConsentParameter() ||
			(parameter.Type != "asset" && parameter.Type != "asset-array") {
			continue
		}
		if _, isSource := schema.SourceParameterRole(parameter); isSource {
			continue
		}
		if parameter.Presentation != nil && parameter.Presentation.Surface == schema.SurfaceHidden {
			continue
		}
		views = append(views, agentInputView{Name: parameter.Name, Label: parameter.Label, Type: parameter.Type, Required: parameter.Required,
			MinItems: parameter.MinItems, MaxItems: parameter.MaxItems, Supply: "chat_image",
			MediaTypes: append([]string(nil), parameter.MediaTypes...), MaxBytes: parameter.MaxBytes})
	}
	return views
}

func (o agentOfferingView) MarshalJSON() ([]byte, error) {
	type plain agentOfferingView
	return schema.MarshalPriceProjection(plain(o), o.PriceMode == "native", "estimated_credits", "extra_credits")
}

// compactAgentOptions encodes a large option list as "value:Name,...". Name is
// the text in a label's trailing " (...)", else the whole label. A list that
// cannot be encoded unambiguously keeps the full form.
func compactAgentOptions(options []schema.ParameterOption) (string, bool) {
	if len(options) <= agentCompactOptionThreshold {
		return "", false
	}
	entries := make([]string, 0, len(options))
	for _, option := range options {
		name := option.Label
		if strings.HasSuffix(name, ")") {
			if open := strings.LastIndex(name, " ("); open >= 0 {
				name = name[open+2 : len(name)-1]
			}
		}
		if name == "" || strings.ContainsAny(option.Value, ",:") || strings.Contains(name, ",") {
			return "", false
		}
		entries = append(entries, option.Value+":"+name)
	}
	return strings.Join(entries, ","), true
}
