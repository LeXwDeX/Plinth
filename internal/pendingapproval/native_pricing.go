package pendingapproval

import (
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func (i Item) MarshalJSON() ([]byte, error) {
	type plain Item
	return schema.MarshalPriceProjection(plain(i), i.NativeQuote != nil, "estimated_credits", "base_credits", "extra_credits")
}
func legacyTaskCredits(t generation.Task) int {
	if t.NativeQuote != nil {
		return 0
	}
	return t.Offering.EstimateTotalCredits(t.Input)
}
