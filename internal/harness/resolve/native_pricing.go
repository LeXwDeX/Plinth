package resolve

import "github.com/LeXwDeX/Plinth/internal/schema"

func (p Proposal) MarshalJSON() ([]byte, error) {
	type plain Proposal
	return schema.MarshalPriceProjection(plain(p), len(p.PriceTotals) > 0, "estimated_credits")
}
func (p ProposalStep) MarshalJSON() ([]byte, error) {
	type plain ProposalStep
	return schema.MarshalPriceProjection(plain(p), p.NativeQuote != nil, "estimated_credits")
}
