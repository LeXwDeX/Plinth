package processing

import (
	"encoding/json"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func processingPrice(o generation.Offering, input json.RawMessage) (int, *schema.NativeQuote, error) {
	if o.NativePricing != nil {
		q, e := o.DefaultNativeQuote(input)
		return 0, q, e
	}
	return o.EstimateTotalCredits(input), nil, nil
}
func (s Step) MarshalJSON() ([]byte, error) {
	type plain Step
	return schema.MarshalPriceProjection(plain(s), s.NativeQuote != nil, "estimated_credits")
}
