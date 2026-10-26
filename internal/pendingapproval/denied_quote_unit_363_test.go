package pendingapproval

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

type quoteGateway struct {
	offering generation.Offering
	prepared generation.PreparedInput
}

func (g quoteGateway) Offerings(generation.Capability) []generation.Offering {
	return []generation.Offering{g.offering}
}
func (g quoteGateway) OfferingAvailable(generation.OfferingID) bool { return true }
func (g quoteGateway) GetPreparedInput(context.Context, generation.OwnerID, generation.PreparedInputID) (generation.PreparedInput, error) {
	return g.prepared, nil
}
func (g quoteGateway) SubmitPrepared(context.Context, generation.SubmitPreparedCommand) (generation.Accepted, error) {
	return generation.Accepted{}, nil
}
func (g quoteGateway) Get(context.Context, generation.OwnerID, generation.TaskID) (generation.Task, error) {
	return generation.Task{}, nil
}

// #363: the denial names the frozen extra credits. The unit label is Provider
// data and stays raw; fee item labels remain Schema copy and are translated.
func TestDeniedNativeExtraKeepsProviderUnitLabel(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	texts, err := catalog.NewTextLocalizer(product.Application())
	if err != nil {
		t.Fatal(err)
	}
	const unit = "即梦 AI"
	if translated := texts.Text("en", unit); translated == unit {
		t.Fatalf("fixture unit label is not translated by the dictionary: %q", translated)
	}
	quote := &generation.NativeQuote{Version: 1, Unit: "jimeng.credit", UnitLabel: unit,
		BaseAmount: "10", ExtraAmount: "5", TotalAmount: "15",
		Components: []schemacontract.NativeCharge{{ID: "standard_texture", Label: "标准纹理", Amount: "5"}}}
	gateway := quoteGateway{
		offering: generation.Offering{ID: "fake/offering", Revision: "rev-a", Capability: generation.TextTo3D, ModelName: "Fake Model"},
		prepared: generation.PreparedInput{Capability: generation.TextTo3D, OfferingID: "fake/offering", OfferingRevision: "rev-a", NativeQuote: quote},
	}
	service := &Service{gateway: gateway, texts: texts, schema: product.Application().Chat}
	operation := generationtool.Operation{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
		OfferingID: "fake/offering", OfferingRevision: "rev-a", PreparedInputID: "prepared-a"}

	content, ok := service.deniedExtraCredits(catalog.WithLocale(context.Background(), "en"), operation)
	if !ok {
		t.Fatal("native extra denial not produced")
	}
	if !strings.Contains(content, unit) || strings.Contains(content, "Jimeng AI") {
		t.Fatalf("unit label was translated: %q", content)
	}
	for _, amount := range []string{"5 " + unit, "10 " + unit, "15 " + unit} {
		if !strings.Contains(content, amount) {
			t.Fatalf("amount %q changed: %q", amount, content)
		}
	}
	if !strings.Contains(content, "Standard texture") {
		t.Fatalf("fee item label was not translated: %q", content)
	}
	if quote.UnitLabel != unit || quote.Components[0].Label != "标准纹理" {
		t.Fatalf("denial changed the frozen quote: %+v", quote)
	}
}
