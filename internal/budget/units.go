package budget

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// LegacyUnit is the bucket of historical integer credit estimates: the quotes
// of Offerings without a provider-native price, and every plan amount stored
// before plans were budgeted per unit (#120). It is a label for estimates, not
// a provider currency, and it is never converted into or added to one.
const LegacyUnit = "legacy.credits"

// microsPerCredit is the scale of an Amount: the six decimal places a native
// amount may carry (schema.ParseNativeAmount).
const microsPerCredit int64 = 1000000

// ErrAmount means an amount, its unit or its label is malformed.
var ErrAmount = errors.New("budget: invalid amount")

// Amount is an exact nonnegative amount in one credit unit, in millionths. A
// production plan limits, reserves and charges each unit on its own: amounts
// of different units are never added or compared (#120, docs/product.md).
type Amount struct {
	Unit      string
	UnitLabel string
	Micros    int64
}

// Legacy is a whole number of historical integer credits.
func Legacy(credits int) Amount {
	if credits < 0 {
		credits = 0
	}
	return Amount{Unit: LegacyUnit, UnitLabel: LegacyUnit, Micros: int64(credits) * microsPerCredit}
}

// ParseAmount reads a decimal amount of a unit. The legacy bucket only holds
// whole credits, as its historical estimates always did.
func ParseAmount(unit, label, decimal string) (Amount, error) {
	unit, label = strings.TrimSpace(unit), strings.TrimSpace(label)
	if unit == "" || label == "" {
		return Amount{}, ErrAmount
	}
	micros, err := schema.ParseNativeAmount(decimal)
	if err != nil || (unit == LegacyUnit && micros%microsPerCredit != 0) {
		return Amount{}, ErrAmount
	}
	return Amount{Unit: unit, UnitLabel: label, Micros: micros}, nil
}

// Decimal is the canonical decimal text of the amount, without its unit.
func (a Amount) Decimal() string { return schema.FormatNativeAmount(a.Micros) }

type amountWire struct {
	Unit      string `json:"unit"`
	UnitLabel string `json:"unit_label"`
	Amount    string `json:"amount"`
}

// MarshalJSON writes the price-total shape the browser already reads for
// per-unit totals: {"unit", "unit_label", "amount"} with a decimal string.
func (a Amount) MarshalJSON() ([]byte, error) {
	return json.Marshal(amountWire{Unit: a.Unit, UnitLabel: a.UnitLabel, Amount: a.Decimal()})
}

func (a *Amount) UnmarshalJSON(data []byte) error {
	var wire amountWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.Unit == "" && wire.UnitLabel == "" && (wire.Amount == "" || wire.Amount == "0") {
		// The zero Amount (no quote at all) round-trips as itself.
		*a = Amount{}
		return nil
	}
	parsed, err := ParseAmount(wire.Unit, wire.UnitLabel, wire.Amount)
	if err != nil {
		return err
	}
	*a = parsed
	return nil
}

// NormalizeLimits validates a plan's per-unit limits and orders them by unit.
// Every limit is positive and names its unit once.
func NormalizeLimits(limits []Amount) ([]Amount, error) {
	if len(limits) == 0 {
		return nil, ErrAmount
	}
	result := append([]Amount(nil), limits...)
	sort.Slice(result, func(i, j int) bool { return result[i].Unit < result[j].Unit })
	for index, limit := range result {
		if limit.Unit == "" || strings.TrimSpace(limit.UnitLabel) == "" || limit.Micros <= 0 ||
			(limit.Unit == LegacyUnit && limit.Micros%microsPerCredit != 0) ||
			(index > 0 && result[index-1].Unit == limit.Unit) {
			return nil, ErrAmount
		}
	}
	return result, nil
}

// SumByUnit totals amounts per unit, ordered by unit. The label of a unit is
// the first one seen; amounts of different units stay apart.
func SumByUnit(amounts []Amount) []Amount {
	index := map[string]int{}
	var result []Amount
	for _, amount := range amounts {
		if amount.Unit == "" {
			continue
		}
		if at, seen := index[amount.Unit]; seen {
			result[at].Micros += amount.Micros
			continue
		}
		index[amount.Unit] = len(result)
		result = append(result, amount)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Unit < result[j].Unit })
	return result
}

// UnitBudget is one unit of a plan's budget: the frozen limit next to what the
// plan charged, holds in reservations and still has, all in millionths.
type UnitBudget struct {
	Unit      string
	UnitLabel string
	Limit     int64
	Spent     int64
	Reserved  int64
	Remaining int64
}

// NewUnitBudget is the budget of one unit from its limit and settled totals.
func NewUnitBudget(limit Amount, spent, reserved int64) UnitBudget {
	remaining := limit.Micros - spent - reserved
	if remaining < 0 {
		remaining = 0
	}
	return UnitBudget{Unit: limit.Unit, UnitLabel: limit.UnitLabel, Limit: limit.Micros,
		Spent: spent, Reserved: reserved, Remaining: remaining}
}

// Amount picks one of the unit's figures as an Amount of the unit.
func (u UnitBudget) Amount(micros int64) Amount {
	return Amount{Unit: u.Unit, UnitLabel: u.UnitLabel, Micros: micros}
}

type unitBudgetWire struct {
	Unit      string `json:"unit"`
	UnitLabel string `json:"unit_label"`
	Limit     string `json:"limit"`
	Spent     string `json:"spent"`
	Reserved  string `json:"reserved"`
	Remaining string `json:"remaining"`
}

func (u UnitBudget) MarshalJSON() ([]byte, error) {
	return json.Marshal(unitBudgetWire{Unit: u.Unit, UnitLabel: u.UnitLabel,
		Limit: schema.FormatNativeAmount(u.Limit), Spent: schema.FormatNativeAmount(u.Spent),
		Reserved: schema.FormatNativeAmount(u.Reserved), Remaining: schema.FormatNativeAmount(u.Remaining)})
}

func (u *UnitBudget) UnmarshalJSON(data []byte) error {
	var wire unitBudgetWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	parsed := UnitBudget{Unit: wire.Unit, UnitLabel: wire.UnitLabel}
	for _, field := range []struct {
		target *int64
		text   string
	}{{&parsed.Limit, wire.Limit}, {&parsed.Spent, wire.Spent}, {&parsed.Reserved, wire.Reserved}, {&parsed.Remaining, wire.Remaining}} {
		micros, err := schema.ParseNativeAmount(field.text)
		if err != nil {
			return ErrAmount
		}
		*field.target = micros
	}
	*u = parsed
	return nil
}

// UnitTotals is what a plan charged and holds reserved in one unit.
type UnitTotals struct {
	Spent    int64
	Reserved int64
}

// Totals are a plan's settled totals by unit.
type Totals map[string]UnitTotals

// AmountFormat renders amounts for the Agent and the session messages. The
// legacy bucket takes the Schema's label; a provider unit keeps its own.
type AmountFormat struct {
	LegacyLabel string
	Separator   string
}

// One renders one amount as "<decimal> <label>".
func (f AmountFormat) One(amount Amount) string {
	label := amount.UnitLabel
	if amount.Unit == LegacyUnit && strings.TrimSpace(f.LegacyLabel) != "" {
		label = f.LegacyLabel
	}
	if strings.TrimSpace(label) == "" {
		return amount.Decimal()
	}
	return amount.Decimal() + " " + label
}

// List renders amounts of different units side by side, never summed.
func (f AmountFormat) List(amounts []Amount) string {
	parts := make([]string, 0, len(amounts))
	for _, amount := range amounts {
		parts = append(parts, f.One(amount))
	}
	separator := f.Separator
	if separator == "" {
		separator = " · "
	}
	return strings.Join(parts, separator)
}

// Units renders one figure of every unit of a summary.
func (f AmountFormat) Units(units []UnitBudget, figure func(UnitBudget) int64) string {
	amounts := make([]Amount, 0, len(units))
	for _, unit := range units {
		amounts = append(amounts, unit.Amount(figure(unit)))
	}
	return f.List(amounts)
}

// LimitOf, SpentOf, ReservedOf and RemainingOf select a figure for Units.
func LimitOf(u UnitBudget) int64     { return u.Limit }
func SpentOf(u UnitBudget) int64     { return u.Spent }
func ReservedOf(u UnitBudget) int64  { return u.Reserved }
func RemainingOf(u UnitBudget) int64 { return u.Remaining }

// QuoteAmount is the server's frozen quote of one prepared step in its own
// unit: the native quote's total when the step carries one, else its legacy
// integer estimate.
func QuoteAmount(quote *schema.NativeQuote, legacyCredits int) Amount {
	if quote == nil {
		return Legacy(legacyCredits)
	}
	micros, err := schema.ParseNativeAmount(quote.TotalAmount)
	if err != nil || micros < 0 {
		micros = 0
	}
	return Amount{Unit: quote.Unit, UnitLabel: quote.UnitLabel, Micros: micros}
}
