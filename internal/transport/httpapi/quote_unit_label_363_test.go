package httpapi

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #363: UnitLabel is Provider data, not Schema copy. It stays raw even when
// the dictionary happens to contain an identical source text. Fee item labels
// remain Schema copy and are translated.
func TestFrozenQuoteUnitLabelStaysProviderData(t *testing.T) {
	server, _, english := localizedTestServer(t)
	const unit = "即梦 AI"
	translated := english.OfferingTexts[unit]
	if translated == "" || translated == unit {
		t.Fatalf("fixture unit label is not translated by the dictionary: %q", translated)
	}
	frozen := &generation.NativeQuote{Version: 1, Unit: "jimeng.credit", UnitLabel: unit,
		BaseAmount: "30", ExtraAmount: "0", TotalAmount: "30"}
	localized := server.quoteIn("en", generation.Offering{}, frozen)
	if localized == frozen {
		t.Fatal("an English projection must copy the quote")
	}
	if localized.UnitLabel != unit {
		t.Fatalf("unit label = %q, want raw %q (dictionary has %q)", localized.UnitLabel, unit, translated)
	}
	if frozen.UnitLabel != unit {
		t.Fatalf("projection changed the frozen unit label to %q", frozen.UnitLabel)
	}
}
