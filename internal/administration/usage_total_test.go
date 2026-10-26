package administration

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

func tripoQuote(amount string) *schema.NativeQuote {
	return &schema.NativeQuote{Version: 1, Unit: "tripo.credit", UnitLabel: "Tripo credits",
		BaseAmount: amount, ExtraAmount: "0", TotalAmount: amount}
}

// #327: the administration home card showed "≈报价未知" as soon as any user had a Provider-native
// quote (Tripo, Hyper3D or Meshy credits on dev-test), although every row could be added up per
// unit. The overview totals all users per unit, the way each row does, leaving out uncharged Tasks.
func TestUsageOverviewTotalsAllUsersPerUnit(t *testing.T) {
	now := time.Date(2026, time.October, 22, 6, 0, 0, 0, time.UTC)
	repository := &memoryUsageRepository{buckets: []UsageBucket{
		{UserID: "a", TaskType: "text-to-3d", TaskStatus: "DONE", TaskCount: 2, EstimatedCredits: 80, LastActiveAt: now},
		{UserID: "a", TaskType: "segment", TaskStatus: "DONE", TaskCount: 1, NativeQuote: tripoQuote("90"), LastActiveAt: now},
		{UserID: "b", TaskType: "segment", TaskStatus: "DONE", TaskCount: 1, NativeQuote: tripoQuote("105"), LastActiveAt: now},
		{UserID: "b", TaskType: "retexture", TaskStatus: "BROKEN", TaskCount: 1, Uncharged: true, LastActiveAt: now},
		{UserID: "c", TaskType: "text-to-image", TaskStatus: "DONE", TaskCount: 1, EstimatedCredits: 15, LastActiveAt: now},
	}}
	service := newTestUsageService(t, repository, now)
	overview, err := service.Overview(context.Background(), UsageRequest{Days: 7})
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Users) != 3 {
		t.Fatalf("users = %#v", overview.Users)
	}
	want := UsageTotal{EstimatedCredits: 95, PriceTotals: []schema.NativeAmount{
		{Unit: "legacy.credits", UnitLabel: "legacy.credits", Amount: "95"},
		{Unit: "tripo.credit", UnitLabel: "Tripo credits", Amount: "195"},
	}}
	if !reflect.DeepEqual(overview.Total, want) {
		t.Fatalf("total = %#v, want %#v", overview.Total, want)
	}

	// Without a native quote the total is the plain credit estimate, as before.
	repository.buckets = repository.buckets[4:]
	overview, err = service.Overview(context.Background(), UsageRequest{Days: 7})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(overview.Total, UsageTotal{EstimatedCredits: 15}) {
		t.Fatalf("legacy total = %#v", overview.Total)
	}
}
