package modeldelivery

import "github.com/LeXwDeX/Plinth/internal/schema"

func (v Conversion) MarshalJSON() ([]byte, error) {
	type plain Conversion
	return schema.MarshalPriceProjection(plain(v), v.NativeQuote != nil, "estimated_credits")
}
func (v Step) MarshalJSON() ([]byte, error) {
	type plain Step
	return schema.MarshalPriceProjection(plain(v), v.NativeQuote != nil, "estimated_credits")
}
func (v Coverage) MarshalJSON() ([]byte, error) {
	type plain Coverage
	return schema.MarshalPriceProjection(plain(v), len(v.PriceTotals) > 0, "completion_credits")
}
