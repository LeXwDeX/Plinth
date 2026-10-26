package httpapi

import (
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func nativePriceMode(o generation.Offering) string {
	if o.NativePricing != nil {
		return "native"
	}
	return ""
}
func defaultNativePrice(o generation.Offering) *generation.NativeQuote {
	q, e := o.DefaultNativeQuote(nil)
	if e != nil {
		return nil
	}
	return q
}
func upperNativePrice(o generation.Offering) *generation.NativeQuote {
	q, e := o.UpperNativeQuote()
	if e != nil {
		return nil
	}
	return q
}
func (o offeringResponse) MarshalJSON() ([]byte, error) {
	type plain offeringResponse
	return schema.MarshalPriceProjection(plain(o), o.PriceMode == "native", "estimated_credits", "maximum_estimated_credits", "estimated_credits_unit_parameter")
}
func (h harnessView) MarshalJSON() ([]byte, error) {
	type plain harnessView
	return schema.MarshalPriceProjection(plain(h), h.PriceMode == "native", "estimated_credits")
}
func (a accountUsageResponse) MarshalJSON() ([]byte, error) {
	type plain accountUsageResponse
	return schema.MarshalPriceProjection(plain(a), len(a.PriceTotals) > 0, "estimated_credits", "by_type")
}
func frozenQuoteWire(m map[string]any, q *generation.NativeQuote) map[string]any {
	if q != nil {
		m["native_quote"] = q
	}
	return m
}
