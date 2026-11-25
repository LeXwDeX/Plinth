package administration

import (
	"context"
	"math/big"
	"reflect"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

func groupKey(value string) *string { return &value }

func basisPoints(value int) *int { return &value }

func meshyQuote(amount string) *schema.NativeQuote {
	return &schema.NativeQuote{Version: 1, Unit: "meshy.credit", UnitLabel: "Meshy credits",
		BaseAmount: amount, ExtraAmount: "0", TotalAmount: amount}
}

func usageGroupOverview(t *testing.T, buckets []UsageBucket) UsageOverview {
	t.Helper()
	now := time.Date(2026, time.November, 25, 6, 0, 0, 0, time.UTC)
	overview, err := newTestUsageService(t, &memoryUsageRepository{buckets: buckets}, now).
		Overview(context.Background(), UsageRequest{Days: 7})
	if err != nil {
		t.Fatal(err)
	}
	return overview
}

// assertGroupAmountsAddUpToTotal checks the #599 invariant: for every unit the
// group amounts, before any rounding, add up to the overview total of that unit.
func assertGroupAmountsAddUpToTotal(t *testing.T, overview UsageOverview) {
	t.Helper()
	totals := map[string]string{}
	for _, total := range overview.Total.PriceTotals {
		totals[total.Unit] = total.Amount
	}
	if len(overview.Total.PriceTotals) == 0 {
		totals["legacy.credits"] = big.NewInt(int64(overview.Total.EstimatedCredits)).String()
	}
	sums := map[string]*big.Rat{}
	for _, group := range overview.Groups {
		if len(group.Amounts) != len(totals) {
			t.Fatalf("group %v states %d units, the total %d", group.Key, len(group.Amounts), len(totals))
		}
		for _, amount := range group.Amounts {
			value, ok := new(big.Rat).SetString(amount.Amount)
			if !ok {
				t.Fatalf("group amount %q is not a decimal", amount.Amount)
			}
			if sums[amount.Unit] == nil {
				sums[amount.Unit] = new(big.Rat)
			}
			sums[amount.Unit].Add(sums[amount.Unit], value)
		}
	}
	for unit, total := range totals {
		want, _ := new(big.Rat).SetString(total)
		if sums[unit] == nil || sums[unit].Cmp(want) != 0 {
			t.Fatalf("unit %s: groups add up to %v, total is %s", unit, sums[unit], total)
		}
	}
}

func TestUsageGroupsSplitSingleLegacyUnitPerProjectGroup599(t *testing.T) {
	now := time.Date(2026, time.November, 25, 5, 0, 0, 0, time.UTC)
	overview := usageGroupOverview(t, []UsageBucket{
		{UserID: "a", ProjectGroupKey: "studio-b", TaskType: "text-to-3d", TaskStatus: "DONE", TaskCount: 2, EstimatedCredits: 60, LastActiveAt: now},
		{UserID: "b", ProjectGroupKey: "studio-a", TaskType: "text-to-3d", TaskStatus: "BROKEN", TaskCount: 1, EstimatedCredits: 30, LastActiveAt: now},
		{UserID: "c", TaskType: "text-to-image", TaskStatus: "DONE", TaskCount: 1, EstimatedCredits: 10, LastActiveAt: now},
		{UserID: "d", ProjectGroupKey: "studio-a", TaskType: "text-to-image", TaskStatus: "DONE", TaskCount: 1, EstimatedCredits: 0, LastActiveAt: now},
	})
	legacy := func(amount string, share int) []UsageGroupAmount {
		return []UsageGroupAmount{{Unit: "legacy.credits", UnitLabel: "legacy.credits", Amount: amount, ShareBasisPoints: basisPoints(share)}}
	}
	want := []UsageGroupSummary{
		{Key: groupKey("studio-a"), TotalTasks: 2, SucceededTasks: 1, FailedTasks: 1, Amounts: legacy("30", 3000)},
		{Key: groupKey("studio-b"), TotalTasks: 2, SucceededTasks: 2, Amounts: legacy("60", 6000)},
		{Key: nil, TotalTasks: 1, SucceededTasks: 1, Amounts: legacy("10", 1000)},
	}
	if !reflect.DeepEqual(overview.Groups, want) {
		t.Fatalf("groups = %#v, want %#v", overview.Groups, want)
	}
	if overview.Users[0].ProjectGroupKey != "studio-b" {
		t.Fatalf("user rows must carry their group: %#v", overview.Users)
	}
	assertGroupAmountsAddUpToTotal(t, overview)
}

func TestUsageGroupsComputeSharePerUnitWithoutAddingUnits599(t *testing.T) {
	now := time.Date(2026, time.November, 25, 5, 0, 0, 0, time.UTC)
	overview := usageGroupOverview(t, []UsageBucket{
		{UserID: "a", ProjectGroupKey: "studio-a", TaskType: "segment", TaskStatus: "DONE", TaskCount: 1, NativeQuote: tripoQuote("10"), LastActiveAt: now},
		{UserID: "a", ProjectGroupKey: "studio-a", TaskType: "text-to-3d", TaskStatus: "DONE", TaskCount: 1, NativeQuote: meshyQuote("0.5"), LastActiveAt: now},
		{UserID: "b", ProjectGroupKey: "studio-b", TaskType: "segment", TaskStatus: "DONE", TaskCount: 1, NativeQuote: tripoQuote("20"), LastActiveAt: now},
	})
	want := []UsageGroupSummary{
		{Key: groupKey("studio-a"), TotalTasks: 2, SucceededTasks: 2, Amounts: []UsageGroupAmount{
			{Unit: "meshy.credit", UnitLabel: "Meshy credits", Amount: "0.5", ShareBasisPoints: basisPoints(10000)},
			{Unit: "tripo.credit", UnitLabel: "Tripo credits", Amount: "10", ShareBasisPoints: basisPoints(3333)},
		}},
		{Key: groupKey("studio-b"), TotalTasks: 1, SucceededTasks: 1, Amounts: []UsageGroupAmount{
			{Unit: "meshy.credit", UnitLabel: "Meshy credits", Amount: "0", ShareBasisPoints: basisPoints(0)},
			{Unit: "tripo.credit", UnitLabel: "Tripo credits", Amount: "20", ShareBasisPoints: basisPoints(6667)},
		}},
	}
	if !reflect.DeepEqual(overview.Groups, want) {
		t.Fatalf("groups = %#v, want %#v", overview.Groups, want)
	}
	assertGroupAmountsAddUpToTotal(t, overview)
}

// Legacy estimates and Provider-native quotes in one range: legacy is one more
// unit ("legacy.credits"), the way the overview total already lists it.
func TestUsageGroupsTreatLegacyAsOneUnitBesideNativeUnits599(t *testing.T) {
	now := time.Date(2026, time.November, 25, 5, 0, 0, 0, time.UTC)
	overview := usageGroupOverview(t, []UsageBucket{
		{UserID: "a", ProjectGroupKey: "studio-a", TaskType: "text-to-3d", TaskStatus: "DONE", TaskCount: 1, EstimatedCredits: 40, LastActiveAt: now},
		{UserID: "b", TaskType: "segment", TaskStatus: "DONE", TaskCount: 1, NativeQuote: tripoQuote("90"), LastActiveAt: now},
		{UserID: "b", TaskType: "text-to-3d", TaskStatus: "DONE", TaskCount: 1, EstimatedCredits: 60, LastActiveAt: now},
	})
	want := []UsageGroupSummary{
		{Key: groupKey("studio-a"), TotalTasks: 1, SucceededTasks: 1, Amounts: []UsageGroupAmount{
			{Unit: "legacy.credits", UnitLabel: "legacy.credits", Amount: "40", ShareBasisPoints: basisPoints(4000)},
			{Unit: "tripo.credit", UnitLabel: "Tripo credits", Amount: "0", ShareBasisPoints: basisPoints(0)},
		}},
		{Key: nil, TotalTasks: 2, SucceededTasks: 2, Amounts: []UsageGroupAmount{
			{Unit: "legacy.credits", UnitLabel: "legacy.credits", Amount: "60", ShareBasisPoints: basisPoints(6000)},
			{Unit: "tripo.credit", UnitLabel: "Tripo credits", Amount: "90", ShareBasisPoints: basisPoints(10000)},
		}},
	}
	if !reflect.DeepEqual(overview.Groups, want) {
		t.Fatalf("groups = %#v, want %#v", overview.Groups, want)
	}
	assertGroupAmountsAddUpToTotal(t, overview)
}

// Uncharged Tasks count as tasks of their group but add no amount (#327).
func TestUsageGroupsCountUnchargedTasksWithoutAmounts599(t *testing.T) {
	now := time.Date(2026, time.November, 25, 5, 0, 0, 0, time.UTC)
	overview := usageGroupOverview(t, []UsageBucket{
		{UserID: "a", ProjectGroupKey: "studio-a", TaskType: "segment", TaskStatus: "DONE", TaskCount: 1, NativeQuote: tripoQuote("30"), LastActiveAt: now},
		{UserID: "b", ProjectGroupKey: "studio-b", TaskType: "segment", TaskStatus: "BROKEN", TaskCount: 3, Uncharged: true, LastActiveAt: now},
	})
	want := []UsageGroupSummary{
		{Key: groupKey("studio-a"), TotalTasks: 1, SucceededTasks: 1, Amounts: []UsageGroupAmount{
			{Unit: "tripo.credit", UnitLabel: "Tripo credits", Amount: "30", ShareBasisPoints: basisPoints(10000)},
		}},
		{Key: groupKey("studio-b"), TotalTasks: 3, FailedTasks: 3, Amounts: []UsageGroupAmount{
			{Unit: "tripo.credit", UnitLabel: "Tripo credits", Amount: "0", ShareBasisPoints: basisPoints(0)},
		}},
	}
	if !reflect.DeepEqual(overview.Groups, want) {
		t.Fatalf("groups = %#v, want %#v", overview.Groups, want)
	}
	assertGroupAmountsAddUpToTotal(t, overview)
}

// A unit whose total is 0 has no share: null, never 0% and never NaN.
func TestUsageGroupsLeaveShareNullWhenUnitTotalIsZero599(t *testing.T) {
	now := time.Date(2026, time.November, 25, 5, 0, 0, 0, time.UTC)
	overview := usageGroupOverview(t, []UsageBucket{
		{UserID: "a", ProjectGroupKey: "studio-a", TaskType: "text-to-image", TaskStatus: "DONE", TaskCount: 1, EstimatedCredits: 0, LastActiveAt: now},
		{UserID: "b", TaskType: "text-to-image", TaskStatus: "BROKEN", TaskCount: 1, Uncharged: true, LastActiveAt: now},
	})
	want := []UsageGroupSummary{
		{Key: groupKey("studio-a"), TotalTasks: 1, SucceededTasks: 1, Amounts: []UsageGroupAmount{
			{Unit: "legacy.credits", UnitLabel: "legacy.credits", Amount: "0"},
		}},
		{Key: nil, TotalTasks: 1, FailedTasks: 1, Amounts: []UsageGroupAmount{
			{Unit: "legacy.credits", UnitLabel: "legacy.credits", Amount: "0"},
		}},
	}
	if !reflect.DeepEqual(overview.Groups, want) {
		t.Fatalf("groups = %#v, want %#v", overview.Groups, want)
	}
	assertGroupAmountsAddUpToTotal(t, overview)
}

func TestUsageGroupsAreEmptyWithoutTasks599(t *testing.T) {
	overview := usageGroupOverview(t, nil)
	if overview.Groups == nil || len(overview.Groups) != 0 {
		t.Fatalf("groups = %#v, want an empty list", overview.Groups)
	}
}

// v1 attribution: a user's whole spending in the range belongs to the group the
// user is in now. The repository joins the current group onto every bucket, so
// the service sums by the key each bucket carries, never by a frozen one.
func TestUsageGroupsAttributeWholeHistoryToUsersCurrentGroup599(t *testing.T) {
	now := time.Date(2026, time.November, 25, 5, 0, 0, 0, time.UTC)
	buckets := []UsageBucket{
		{UserID: "a", ProjectGroupKey: "studio-a", TaskType: "text-to-3d", TaskStatus: "DONE", TaskCount: 1, EstimatedCredits: 20, LastActiveAt: now.Add(-48 * time.Hour)},
		{UserID: "a", ProjectGroupKey: "studio-a", TaskType: "text-to-3d", TaskStatus: "DONE", TaskCount: 1, EstimatedCredits: 30, LastActiveAt: now},
	}
	before := usageGroupOverview(t, buckets)
	for index := range buckets {
		buckets[index].ProjectGroupKey = "studio-b"
	}
	after := usageGroupOverview(t, buckets)
	if len(before.Groups) != 1 || *before.Groups[0].Key != "studio-a" ||
		len(after.Groups) != 1 || *after.Groups[0].Key != "studio-b" ||
		after.Groups[0].Amounts[0].Amount != "50" || after.Groups[0].TotalTasks != 2 {
		t.Fatalf("before = %#v, after = %#v", before.Groups, after.Groups)
	}
}

func TestUsageGroupsSortByKeyWithUngroupedLast599(t *testing.T) {
	now := time.Date(2026, time.November, 25, 5, 0, 0, 0, time.UTC)
	overview := usageGroupOverview(t, []UsageBucket{
		{UserID: "a", TaskType: "t", TaskStatus: "DONE", TaskCount: 1, EstimatedCredits: 900, LastActiveAt: now},
		{UserID: "b", ProjectGroupKey: "zeta", TaskType: "t", TaskStatus: "DONE", TaskCount: 1, EstimatedCredits: 5, LastActiveAt: now},
		{UserID: "c", ProjectGroupKey: "alpha", TaskType: "t", TaskStatus: "DONE", TaskCount: 1, EstimatedCredits: 1, LastActiveAt: now},
		{UserID: "d", ProjectGroupKey: "m-1", TaskType: "t", TaskStatus: "DONE", TaskCount: 1, EstimatedCredits: 1, LastActiveAt: now},
	})
	keys := []string{}
	for _, group := range overview.Groups {
		if group.Key == nil {
			keys = append(keys, "<ungrouped>")
			continue
		}
		keys = append(keys, *group.Key)
	}
	if !reflect.DeepEqual(keys, []string{"alpha", "m-1", "zeta", "<ungrouped>"}) {
		t.Fatalf("group order = %v", keys)
	}
}

// Shares round half up to whole basis points, independently per group, so they
// need not add up to 10000; the amounts still add up exactly.
func TestUsageGroupsRoundShareWithoutForcingHundredPercent599(t *testing.T) {
	now := time.Date(2026, time.November, 25, 5, 0, 0, 0, time.UTC)
	overview := usageGroupOverview(t, []UsageBucket{
		{UserID: "a", ProjectGroupKey: "a", TaskType: "t", TaskStatus: "DONE", TaskCount: 1, EstimatedCredits: 1, LastActiveAt: now},
		{UserID: "b", ProjectGroupKey: "b", TaskType: "t", TaskStatus: "DONE", TaskCount: 1, EstimatedCredits: 1, LastActiveAt: now},
		{UserID: "c", ProjectGroupKey: "c", TaskType: "t", TaskStatus: "DONE", TaskCount: 1, EstimatedCredits: 1, LastActiveAt: now},
	})
	sum := 0
	for _, group := range overview.Groups {
		if share := *group.Amounts[0].ShareBasisPoints; share != 3333 {
			t.Fatalf("share = %d, want 3333", share)
		}
		sum += *group.Amounts[0].ShareBasisPoints
	}
	if sum != 9999 {
		t.Fatalf("shares add up to %d; rounding must not be forced to 10000", sum)
	}
	assertGroupAmountsAddUpToTotal(t, overview)
}

// Amounts near the int64 micro-unit ceiling: a share computed as
// amount*10000/total in int64 would overflow; the share must stay exact.
func TestUsageGroupShareDoesNotOverflowOnLargeAmounts599(t *testing.T) {
	now := time.Date(2026, time.November, 25, 5, 0, 0, 0, time.UTC)
	overview := usageGroupOverview(t, []UsageBucket{
		{UserID: "a", ProjectGroupKey: "a", TaskType: "t", TaskStatus: "DONE", TaskCount: 1, NativeQuote: tripoQuote("1000000000"), LastActiveAt: now},
		{UserID: "b", ProjectGroupKey: "b", TaskType: "t", TaskStatus: "DONE", TaskCount: 1, NativeQuote: tripoQuote("1000000000"), LastActiveAt: now},
		{UserID: "b", ProjectGroupKey: "b", TaskType: "u", TaskStatus: "DONE", TaskCount: 1, NativeQuote: tripoQuote("1000000000"), LastActiveAt: now},
		{UserID: "b", ProjectGroupKey: "b", TaskType: "v", TaskStatus: "DONE", TaskCount: 1, NativeQuote: tripoQuote("1000000000"), LastActiveAt: now},
	})
	if *overview.Groups[0].Amounts[0].ShareBasisPoints != 2500 ||
		*overview.Groups[1].Amounts[0].ShareBasisPoints != 7500 {
		t.Fatalf("groups = %#v", overview.Groups)
	}
	assertGroupAmountsAddUpToTotal(t, overview)
}

func TestUsageShareBasisPointsRoundsHalfUp599(t *testing.T) {
	for _, item := range []struct {
		part, whole string
		want        *int
	}{
		{"1", "3", basisPoints(3333)},
		{"2", "3", basisPoints(6667)},
		{"1", "20000", basisPoints(1)},
		{"1", "20001", basisPoints(0)},
		{"0.000001", "0.000002", basisPoints(5000)},
		{"0", "0", nil},
		{"5", "5", basisPoints(10000)},
	} {
		got, err := usageShareBasisPoints(item.part, item.whole)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, item.want) {
			t.Fatalf("share(%s/%s) = %v, want %v", item.part, item.whole, got, item.want)
		}
	}
}
