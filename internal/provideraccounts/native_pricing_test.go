package provideraccounts

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

type exactBalanceFixture struct{ amount schema.NativeAmount }

func (e exactBalanceFixture) Balance(context.Context) (float64, error) {
	return 0, errors.New("legacy float must not be used for native balance")
}
func (e exactBalanceFixture) NativeBalance(context.Context) (schema.NativeAmount, error) {
	return e.amount, nil
}
func TestNativeBalanceDoesNotRebuildDecimalFromFloat(t *testing.T) {
	ctx := context.Background()
	s, e := New(Config{Readers: map[string]BalanceReader{"legacy": FixedBalanceReader(0.5), "exact": exactBalanceFixture{schema.NativeAmount{Unit: "hyper3d.credit", UnitLabel: "Hyper3D credits", Amount: "0.500000"}}, "bad": exactBalanceFixture{schema.NativeAmount{Unit: "bad unit", UnitLabel: "invalid", Amount: "0.5"}}}})
	if e != nil {
		t.Fatal(e)
	}
	if a, e := s.NativeBalance(ctx, "legacy"); e != nil || a != nil {
		t.Fatalf("legacy float became exact %+v %v", a, e)
	}
	if a, e := s.NativeBalance(ctx, "exact"); e != nil || a.Amount != "0.5" {
		t.Fatalf("exact lost fraction %+v %v", a, e)
	}
	if _, e := s.NativeBalance(ctx, "bad"); e == nil {
		t.Fatal("invalid native balance identity")
	}
}
