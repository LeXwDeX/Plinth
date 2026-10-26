package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// RecoveryOptions is the local, zero-charge receipt protocol. Source identity
// and slot mappings are authored in Product Schema, never inferred from URLs.
type RecoveryOptions struct {
	ReceiptParameter string                   `json:"receipt_parameter"`
	DigestParameter  string                   `json:"digest_parameter"`
	Sources          []RecoverySourceContract `json:"sources"`
}
type RecoverySourceContract struct {
	OfferingID       OfferingID            `json:"offering_id"`
	OfferingRevision string                `json:"offering_revision"`
	Slots            []RecoverySlotMapping `json:"slots"`
}
type RecoverySlotMapping struct {
	SourceRole    string `json:"source_role"`
	SourceVariant string `json:"source_variant"`
	Role          string `json:"role"`
	Variant       string `json:"variant"`
}

// DecodeRecoveryOptions rejects network/cost/implicit-slot contracts. Every
// original slot has exactly one explicit replacement, including aliases that
// share bytes but retain distinct public slot identities.
func DecodeRecoveryOptions(o Offering) (RecoveryOptions, error) {
	var options RecoveryOptions
	invalid := func() (RecoveryOptions, error) { return options, errors.New("invalid local asset recovery contract") }
	if o.Capability != AssetRecovery || o.ID == "" || o.Revision == "" || o.ProviderID == "" || o.CreditsMetered || o.EstimatedCredits != 0 || o.EstimatedCreditsUnitParameter != "" || o.Cost != nil || o.CostPolicy != nil || o.PostSuccess != nil || len(o.AssetHosts) != 0 || !o.IdempotentSubmission || len(o.Parameters) != 2 || len(o.ProviderOptions) > 64<<10 {
		return invalid()
	}
	d := json.NewDecoder(bytes.NewReader(o.ProviderOptions))
	d.DisallowUnknownFields()
	if d.Decode(&options) != nil || d.Decode(new(any)) != io.EOF || options.ReceiptParameter == "" || options.DigestParameter == "" || options.ReceiptParameter == options.DigestParameter || len(options.Sources) == 0 || len(options.Sources) > 32 {
		return invalid()
	}
	for _, p := range o.Parameters {
		if (p.Name != options.ReceiptParameter && p.Name != options.DigestParameter) || p.Type != "text" || !p.Required || p.Semantic != "" || p.Presentation == nil || p.Presentation.Surface != SurfaceHidden || p.PromptDelivery != nil {
			return invalid()
		}
	}
	slots := map[string]bool{}
	for _, out := range o.Outputs {
		key := out.Role + "\x00" + out.Variant
		if out.Role == "" || out.Variant == "" || out.VariantPrefix != "" || out.VariantFrom != "" || out.Minimum == nil || *out.Minimum != 1 || out.MaxBytes <= 0 || out.MaxBytes > 1<<30 || len(out.MediaTypes) != 1 || out.Delivery != AssetDeliveryRequired && out.Delivery != AssetDeliveryBackground || slots[key] {
			return invalid()
		}
		slots[key] = true
	}
	if len(slots) == 0 || len(slots) > 32 {
		return invalid()
	}
	sources := map[string]bool{}
	for _, source := range options.Sources {
		key := string(source.OfferingID) + "\x00" + source.OfferingRevision
		if source.OfferingID == "" || strings.TrimSpace(source.OfferingRevision) == "" || sources[key] || len(source.Slots) != len(slots) {
			return invalid()
		}
		sources[key] = true
		seenSource := map[string]bool{}
		seenOutput := map[string]bool{}
		for _, slot := range source.Slots {
			a := slot.SourceRole + "\x00" + slot.SourceVariant
			b := slot.Role + "\x00" + slot.Variant
			if slot.SourceRole == "" || seenSource[a] || seenOutput[b] || !slots[b] {
				return invalid()
			}
			seenSource[a] = true
			seenOutput[b] = true
		}
	}
	return options, nil
}
