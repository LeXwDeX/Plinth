package generation

import (
	"encoding/json"
	"testing"
)

func TestNativeQuoteHashKeepsHistoricalNilBytes(t *testing.T) {
	p := PreparedInput{OwnerID: "owner", SessionID: "s", Capability: TextToImage, OfferingID: "old", OfferingRevision: "r", SchemaVersion: "1", NormalizedInput: json.RawMessage(`{}`)}
	old, err := hashPreparedInput(p)
	if err != nil || old != "e38c5dbf86a8172ec2bab50af2056827a1355435b490d869e01fdcb7941aa19e" {
		t.Fatalf("historical hash drift %s %v", old, err)
	}
	q := &NativeQuote{Version: 1, Unit: "hyper3d.credit", UnitLabel: "Hyper3D credits", BaseAmount: "0.5", ExtraAmount: "0", TotalAmount: "0.5"}
	p.NativeQuote = q
	hash, err := hashPreparedInput(p)
	if err != nil || hash == old {
		t.Fatal("quote not bound")
	}
	p.InputHash = hash
	if VerifyPreparedPricingHash(p) != nil {
		t.Fatal("valid price hash rejected")
	}
	p.NativeQuote.Unit = "other.credit"
	if VerifyPreparedPricingHash(p) == nil {
		t.Fatal("changed unit reused grant hash")
	}
}
