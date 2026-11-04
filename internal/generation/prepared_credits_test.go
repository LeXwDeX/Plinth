package generation

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// A metered input whose native quote came out zero is priced from the
// Offering's declared estimate instead of being frozen as a free step; a
// metered input with no declared price stays zero and is left to a person by
// the execution policy (#103 review B1).
func TestMeteredPreparedEstimateFallsBackToTheOfferingQuote(t *testing.T) {
	offering := Offering{CreditsMetered: true, NativePricing: &schema.NativePricing{
		Version: 1, Unit: "meshy.credit", UnitLabel: "Meshy credits", BaseAmount: "2",
	}}
	zeroQuote := &NativeQuote{Version: 1, Unit: "meshy.credit", UnitLabel: "Meshy credits", TotalAmount: "0"}
	if got := estimatePreparedCredits(offering, json.RawMessage(`{}`), zeroQuote); got != 2 {
		t.Fatalf("metered zero quote estimate = %d, want the declared 2", got)
	}
	// A quote above zero is still the exact frozen price.
	pricedQuote := &NativeQuote{Version: 1, Unit: "meshy.credit", UnitLabel: "Meshy credits", TotalAmount: "3"}
	if got := estimatePreparedCredits(offering, json.RawMessage(`{}`), pricedQuote); got != 3 {
		t.Fatalf("metered priced quote estimate = %d, want 3", got)
	}
	// Nothing to fall back to: the policy, not this estimate, keeps the step
	// from spending an unpriced zero.
	unpriced := Offering{CreditsMetered: true}
	if got := estimatePreparedCredits(unpriced, json.RawMessage(`{}`), nil); got != 0 {
		t.Fatalf("unpriced metered estimate = %d, want 0", got)
	}
	// An unmetered zero-cost step is genuinely free.
	free := Offering{}
	if got := estimatePreparedCredits(free, json.RawMessage(`{}`), nil); got != 0 {
		t.Fatalf("free estimate = %d, want 0", got)
	}
}
