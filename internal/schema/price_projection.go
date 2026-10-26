package schema

import (
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"
)

func MarshalPriceProjection(value any, native bool, legacyKeys ...string) ([]byte, error) {
	b, e := json.Marshal(value)
	if e != nil || !native {
		return b, e
	}
	var m map[string]json.RawMessage
	if e = json.Unmarshal(b, &m); e != nil {
		return nil, e
	}
	for _, k := range legacyKeys {
		delete(m, k)
	}
	return json.Marshal(m)
}

// PriceAccumulator never combines different economic units. The legacy bucket
// only labels historical estimates; it is not a provider currency conversion.
type PriceAccumulator struct {
	Legacy    int
	HasLegacy bool
	amounts   map[string]int64
	labels    map[string]string
}

func (a *PriceAccumulator) Add(legacy int, q *NativeQuote) error {
	if q != nil {
		if legacy != 0 {
			return errors.New("mixed price modes in one estimate")
		}
		if e := q.Validate(); e != nil {
			return e
		}
		if a.amounts == nil {
			a.amounts = map[string]int64{}
			a.labels = map[string]string{}
		}
		if label, ok := a.labels[q.Unit]; ok && label != q.UnitLabel {
			return errors.New("native unit label conflict")
		}
		n, _ := ParseNativeAmount(q.TotalAmount)
		sum, e := AddNativeAmounts(a.amounts[q.Unit], n)
		if e != nil {
			return e
		}
		a.amounts[q.Unit] = sum
		a.labels[q.Unit] = q.UnitLabel
		return nil
	}
	if legacy < 0 || a.Legacy > math.MaxInt-legacy {
		return errors.New("legacy price overflow")
	}
	a.Legacy += legacy
	a.HasLegacy = true
	return nil
}
func (a *PriceAccumulator) Totals() ([]NativeAmount, error) {
	if len(a.amounts) == 0 {
		return nil, nil
	}
	out := []NativeAmount{}
	for unit, n := range a.amounts {
		out = append(out, NativeAmount{unit, a.labels[unit], FormatNativeAmount(n)})
	}
	if a.HasLegacy {
		if _, e := ParseNativeAmount(strconv.Itoa(a.Legacy)); e != nil {
			return nil, e
		}
		out = append(out, NativeAmount{"legacy.credits", "legacy.credits", strconv.Itoa(a.Legacy)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Unit < out[j].Unit })
	return out, nil
}
func SameNativeQuote(a, b *NativeQuote) bool {
	x, e := json.Marshal(a)
	y, f := json.Marshal(b)
	return e == nil && f == nil && string(x) == string(y)
}
