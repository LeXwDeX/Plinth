package generation

import "context"

// StepCost is what one step of one service costs, roughly, in its provider's
// own unit (#330, #332). It is derived from the Tasks an asset already ran: a
// service without a native price is left out, because its cost is not cheaply
// derivable.
type StepCost struct {
	OfferingID string
	Provider   string
	Capability string
	Amount     string
	Unit       string
	UnitLabel  string
}

// StepCosts names the counted services an asset's latest Tasks used and
// roughly what one step of each costs in the provider's own unit. window
// bounds how many of the session's latest Tasks are read; a service that
// appears more than once is listed once. The Tasks are read with the owner's
// scope, so a session of another owner answers no costs.
func (m *Module) StepCosts(
	ctx context.Context,
	owner OwnerID,
	sessionID string,
	counted []Capability,
	window int,
) ([]StepCost, error) {
	if m == nil || sessionID == "" || len(counted) == 0 || window <= 0 {
		return nil, nil
	}
	isCounted := make(map[Capability]bool, len(counted))
	for _, capability := range counted {
		isCounted[capability] = true
	}
	page, err := m.List(ctx, OwnTasks(owner), ListQuery{SessionID: sessionID, Limit: window})
	if err != nil {
		return nil, err
	}
	var costs []StepCost
	seen := map[OfferingID]bool{}
	for _, task := range page.Tasks {
		capability := task.Capability
		if !isCounted[capability] || seen[task.Offering.ID] {
			continue
		}
		seen[task.Offering.ID] = true
		offering := task.Offering
		if current, exists := m.Offering(task.Offering.ID); exists {
			offering = current
		}
		quote, quoteErr := offering.DefaultNativeQuote(nil)
		if quoteErr != nil || quote == nil {
			continue
		}
		costs = append(costs, StepCost{
			OfferingID: string(offering.ID), Provider: offering.ProviderID,
			Capability: string(capability), Amount: quote.TotalAmount,
			Unit: quote.Unit, UnitLabel: quote.UnitLabel,
		})
	}
	return costs, nil
}
