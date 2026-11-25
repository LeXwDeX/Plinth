package administration

import (
	"errors"
	"math/big"
	"sort"
	"strconv"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// usageLegacyUnit is the unit PriceAccumulator.Totals lists historical credit
// estimates under; the project-group split states them the same way.
const usageLegacyUnit = "legacy.credits"

// usageShareScale is the whole of a share in basis points (100%).
const usageShareScale = 10000

// UsageGroupSummary is one project group's part of the overview (#599): every
// Task of the users who are in that group now, whenever the Task ran. Key is
// nil for users without a group, and for Tasks whose user record is gone.
type UsageGroupSummary struct {
	Key            *string
	TotalTasks     int
	SucceededTasks int
	FailedTasks    int
	// Amounts lists every unit of the overview total, in its order, with this
	// group's amount (possibly "0") and share of that unit's total.
	Amounts []UsageGroupAmount
}

// UsageGroupAmount is a group's amount in one unit and its share of that
// unit's total in basis points, rounded half up. Units are never added
// together. ShareBasisPoints is nil when the unit's total is 0.
type UsageGroupAmount struct {
	Unit             string
	UnitLabel        string
	Amount           string
	ShareBasisPoints *int
}

// usageGroupAccumulator sums the overview's buckets per project group. It sees
// exactly the buckets and the charged rule the overview total sees, so the
// group amounts of a unit add up to that unit's total.
type usageGroupAccumulator struct {
	summaries map[string]*UsageGroupSummary
	prices    map[string]*schema.PriceAccumulator
}

func newUsageGroupAccumulator() *usageGroupAccumulator {
	return &usageGroupAccumulator{
		summaries: map[string]*UsageGroupSummary{},
		prices:    map[string]*schema.PriceAccumulator{},
	}
}

func (groups *usageGroupAccumulator) summary(key string) *UsageGroupSummary {
	if summary := groups.summaries[key]; summary != nil {
		return summary
	}
	summary := &UsageGroupSummary{}
	if key != "" {
		owned := key
		summary.Key = &owned
	}
	groups.summaries[key] = summary
	return summary
}

func (groups *usageGroupAccumulator) amounts(key string) *schema.PriceAccumulator {
	if amounts := groups.prices[key]; amounts != nil {
		return amounts
	}
	amounts := &schema.PriceAccumulator{}
	groups.prices[key] = amounts
	return amounts
}

// overview states every group against the units of the overview total: the
// per-unit totals once a native quote is among them, otherwise the single
// legacy unit holding the plain credit estimate. Groups sort by KEY with the
// ungrouped row last.
func (groups *usageGroupAccumulator) overview(
	all *schema.PriceAccumulator,
	totals []schema.NativeAmount,
) ([]UsageGroupSummary, error) {
	units := totals
	if len(units) == 0 {
		units = []schema.NativeAmount{{
			Unit:      usageLegacyUnit,
			UnitLabel: usageLegacyUnit,
			Amount:    strconv.Itoa(all.Legacy),
		}}
	}
	result := make([]UsageGroupSummary, 0, len(groups.summaries))
	for key, summary := range groups.summaries {
		own, err := usageUnitAmounts(groups.prices[key])
		if err != nil {
			return nil, err
		}
		summary.Amounts = make([]UsageGroupAmount, 0, len(units))
		for _, unit := range units {
			amount, exists := own[unit.Unit]
			if !exists {
				amount = "0"
			}
			share, err := usageShareBasisPoints(amount, unit.Amount)
			if err != nil {
				return nil, err
			}
			summary.Amounts = append(summary.Amounts, UsageGroupAmount{
				Unit:             unit.Unit,
				UnitLabel:        unit.UnitLabel,
				Amount:           amount,
				ShareBasisPoints: share,
			})
		}
		result = append(result, *summary)
	}
	sort.Slice(result, func(left int, right int) bool {
		if result[left].Key == nil || result[right].Key == nil {
			return result[right].Key == nil && result[left].Key != nil
		}
		return *result[left].Key < *result[right].Key
	})
	return result, nil
}

// usageUnitAmounts is a group's amount per unit. Totals only lists units once a
// native quote is present, so the legacy estimate is read directly: a group may
// hold only legacy Tasks while another group holds native ones.
func usageUnitAmounts(amounts *schema.PriceAccumulator) (map[string]string, error) {
	result := map[string]string{}
	if amounts == nil {
		return result, nil
	}
	totals, err := amounts.Totals()
	if err != nil {
		return nil, err
	}
	for _, total := range totals {
		result[total.Unit] = total.Amount
	}
	if amounts.HasLegacy {
		result[usageLegacyUnit] = strconv.Itoa(amounts.Legacy)
	}
	return result, nil
}

// usageShareBasisPoints is part/whole in basis points, rounded half up, in
// exact rational arithmetic: amount*10000 in int64 micro-units would overflow
// near the aggregate ceiling. A whole of 0 has no share (nil).
func usageShareBasisPoints(part string, whole string) (*int, error) {
	numerator, ok := new(big.Rat).SetString(part)
	if !ok || numerator.Sign() < 0 {
		return nil, errors.New("administration: invalid usage amount")
	}
	denominator, ok := new(big.Rat).SetString(whole)
	if !ok || denominator.Sign() < 0 {
		return nil, errors.New("administration: invalid usage total")
	}
	if denominator.Sign() == 0 {
		return nil, nil
	}
	ratio := new(big.Rat).Mul(numerator, big.NewRat(usageShareScale, 1))
	ratio.Quo(ratio, denominator)
	// floor(n/d + 1/2) = floor((2n + d) / 2d) for a nonnegative ratio.
	doubled := new(big.Int).Lsh(ratio.Num(), 1)
	doubled.Add(doubled, ratio.Denom())
	rounded := doubled.Quo(doubled, new(big.Int).Lsh(ratio.Denom(), 1))
	if !rounded.IsInt64() || rounded.Int64() > usageShareScale {
		return nil, errors.New("administration: usage share exceeds its total")
	}
	share := int(rounded.Int64())
	return &share, nil
}
