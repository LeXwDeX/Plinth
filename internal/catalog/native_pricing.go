package catalog

import (
	"encoding/json"
	"fmt"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

func validateNativePricing(o schema.Offering) error {
	p := o.NativePricing
	if err := p.Validate(); err != nil {
		return fmt.Errorf("Offering %q: %w", o.ID, err)
	}
	if o.Cost != nil || o.CostPolicy != nil || o.EstimatedCredits != 0 || o.EstimatedCreditsUnitParameter != "" || !o.CreditsMetered {
		return fmt.Errorf("Offering %q mixes native and legacy price", o.ID)
	}
	for _, c := range p.Components {
		conditions := c.WhenAll
		if c.When != nil {
			conditions = []schema.ParameterCondition{*c.When}
		}
		for _, condition := range conditions {
			parameter, ok := offeringParameter(o.Parameters, condition.Parameter)
			if !ok || !o.AgentParameterAllowed(parameter) || (parameter.Type != "text" && parameter.Type != "select" && parameter.Type != "boolean" && parameter.Type != "number") || parameter.Semantic == schema.SemanticPrompt || len(parameter.Default) == 0 {
				return fmt.Errorf("native price condition parameter %q invalid", condition.Parameter)
			}
			if err := validateParameterCondition(condition, parameter); err != nil {
				return err
			}
		}
	}
	if p.Selection != nil {
		parameter, ok := offeringParameter(o.Parameters, p.Selection.Parameter)
		if !ok || parameter.Type != "string-array" || !parameter.Required || len(parameter.Default) == 0 || parameter.MaxItems != p.Selection.MaxItems || len(parameter.Options) != p.Selection.MaxItems || parameter.MinItems < 1 {
			return fmt.Errorf("native price selection parameter %q invalid", p.Selection.Parameter)
		}
	}
	if len(p.Components) > 0 || p.Selection != nil {
		parameter, ok := offeringParameter(o.Parameters, p.ConsentParameter)
		if !ok || parameter.Type != "boolean" || parameter.WireName != "" || parameter.Fixed || parameter.AdjustmentPolicy != "" {
			return fmt.Errorf("native extra consent parameter invalid")
		}
	}
	if _, err := o.DefaultNativeQuote(nil); err != nil {
		return fmt.Errorf("Offering %q default native quote: %w", o.ID, err)
	}
	return nil
}
func authorizeNativeQuote(o schema.Offering, raw json.RawMessage) (*schema.NativeQuote, error) {
	q, err := o.EvaluateNativeQuote(raw)
	if err != nil {
		return nil, err
	}
	extra, err := q.HasExtra()
	if err != nil {
		return nil, err
	}
	if extra {
		var values map[string]any
		if json.Unmarshal(raw, &values) != nil || values[o.NativePricing.ConsentParameter] != true {
			return nil, consentError(o, "consent_required")
		}
		limit, err := schema.ParseNativeAmount(o.NativePricing.MaxAuthorizedExtraAmount)
		if err != nil {
			return nil, err
		}
		n, _ := schema.ParseNativeAmount(q.ExtraAmount)
		if n > limit {
			return nil, consentError(o, "over_limit")
		}
	}
	return q, nil
}

func validateNativeBalanceUnits(runtime RuntimeSchema, offerings generationMap) error {
	for accountID, account := range runtime.ProviderAccounts {
		if account.Balance.NativeUnit == "" {
			continue
		}
		for _, o := range offerings {
			if o.Enabled && o.ProviderID == account.ProviderID && o.NativePricing != nil && o.NativePricing.Unit != account.Balance.NativeUnit {
				return fmt.Errorf("provider_accounts[%q] native balance unit differs from Offering %q", accountID, o.ID)
			}
		}
	}
	return nil
}
