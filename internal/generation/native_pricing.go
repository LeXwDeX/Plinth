package generation

import (
	"errors"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// VerifyPreparedPricingHash is also used by the persisted Grant reader. Legacy
// snapshots retain their existing contract; native snapshots cannot trust a
// mutable quoted amount merely because the stored input_hash string matches.
func VerifyPreparedPricingHash(p PreparedInput) error {
	if p.NativeQuote == nil {
		return nil
	}
	if p.NativeQuote != nil {
		if err := p.NativeQuote.Validate(); err != nil {
			return err
		}
	}
	hash, err := hashPreparedInput(p)
	if err != nil {
		return err
	}
	if hash != p.InputHash {
		return errors.New("native prepared quote hash mismatch")
	}
	return nil
}
func (p PreparedInput) HasExtraSpend() (bool, error) {
	if p.NativeQuote != nil {
		return p.NativeQuote.HasExtra()
	}
	return p.ExtraCredits > 0, nil
}
func (t Task) Price() (schema.PriceEstimate, error) {
	if t.NativeQuote != nil {
		if err := t.NativeQuote.Validate(); err != nil {
			return schema.PriceEstimate{}, err
		}
		return schema.PriceEstimate{NativeQuote: t.NativeQuote}, nil
	}
	if t.Offering.NativePricing != nil {
		return schema.PriceEstimate{}, errors.New("native Task quote missing")
	}
	return t.Offering.EstimatePrice(t.Input)
}
