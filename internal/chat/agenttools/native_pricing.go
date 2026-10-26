package agenttools

import (
	"strconv"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func legacyThreeViewCredits(t generation.Task) int {
	if t.NativeQuote != nil {
		return 0
	}
	return t.Offering.EstimateTotalCredits(t.Input)
}

func threeViewPriceText(t generation.Task) string {
	if t.NativeQuote != nil {
		return t.NativeQuote.TotalAmount + " " + t.NativeQuote.UnitLabel
	}
	return strconv.Itoa(t.Offering.EstimateTotalCredits(t.Input))
}
