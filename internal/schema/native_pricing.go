package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const nativeScale int64 = 1000000
const maxNativeJobAmount int64 = 1000000000 * nativeScale

var nativeUnitPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)
var nativeComponentPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// NativePricing is an optional, provider-native price declaration. Its amounts
// are decimal strings, never legacy integer credits or an exchange rate.
type NativePricing struct {
	Version                  int                    `json:"version"`
	Unit                     string                 `json:"unit"`
	UnitLabel                string                 `json:"unit_label"`
	BaseAmount               string                 `json:"base_amount"`
	Components               []NativePriceComponent `json:"components,omitempty"`
	Selection                *NativePriceSelection  `json:"selection,omitempty"`
	ConsentParameter         string                 `json:"consent_parameter,omitempty"`
	MaxAuthorizedExtraAmount string                 `json:"max_authorized_extra_amount,omitempty"`
}
type NativePriceComponent struct {
	ID      string               `json:"id"`
	Label   string               `json:"label"`
	Amount  string               `json:"amount"`
	When    *ParameterCondition  `json:"when,omitempty"`
	WhenAll []ParameterCondition `json:"when_all,omitempty"`
}

// NativePriceSelection prices each distinct declared string-array choice once.
// The frozen quote contains one aggregate component, bounded by MaxItems.
type NativePriceSelection struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Amount    string `json:"amount"`
	Parameter string `json:"parameter"`
	MaxItems  int    `json:"max_items"`
}
type NativeCharge struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Amount string `json:"amount"`
}

// NativeQuote is immutable approval evidence, not a settled upstream charge.
type NativeQuote struct {
	Version     int            `json:"version"`
	Unit        string         `json:"unit"`
	UnitLabel   string         `json:"unit_label"`
	BaseAmount  string         `json:"base_amount"`
	ExtraAmount string         `json:"extra_amount"`
	TotalAmount string         `json:"total_amount"`
	Components  []NativeCharge `json:"components,omitempty"`
}
type NativeAmount struct {
	Unit      string `json:"unit"`
	UnitLabel string `json:"unit_label"`
	Amount    string `json:"amount"`
}

// ParseNativeAmount parses bounded nonnegative decimals exactly into micros.
// The aggregate limit is int64; declarations/individual quotes have a lower cap.
func ParseNativeAmount(raw string) (int64, error) {
	parts := strings.Split(raw, ".")
	if len(parts) > 2 || parts[0] == "" || len(parts[0]) > 13 || (len(parts[0]) > 1 && parts[0][0] == '0') {
		return 0, errors.New("invalid native amount")
	}
	for _, c := range parts[0] {
		if c < '0' || c > '9' {
			return 0, errors.New("invalid native amount")
		}
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if len(fraction) == 0 || len(fraction) > 6 {
			return 0, errors.New("invalid native precision")
		}
		for _, c := range fraction {
			if c < '0' || c > '9' {
				return 0, errors.New("invalid native amount")
			}
		}
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole > math.MaxInt64/nativeScale {
		return 0, errors.New("native amount overflow")
	}
	fraction += strings.Repeat("0", 6-len(fraction))
	f, _ := strconv.ParseInt(fraction, 10, 64)
	return AddNativeAmounts(whole*nativeScale, f)
}
func FormatNativeAmount(micros int64) string {
	whole := strconv.FormatInt(micros/nativeScale, 10)
	fraction := micros % nativeScale
	if fraction == 0 {
		return whole
	}
	return whole + "." + strings.TrimRight(fmt.Sprintf("%06d", fraction), "0")
}
func AddNativeAmounts(a, b int64) (int64, error) {
	if a < 0 || b < 0 || a > math.MaxInt64-b {
		return 0, errors.New("native amount overflow")
	}
	return a + b, nil
}
func nativeHeader(version int, unit, label string) error {
	if version != 1 || unit == "legacy.credits" || !nativeUnitPattern.MatchString(unit) || strings.TrimSpace(label) == "" || len(label) > 128 {
		return errors.New("invalid native price identity")
	}
	return nil
}
func (p NativePricing) Validate() error {
	if err := nativeHeader(p.Version, p.Unit, p.UnitLabel); err != nil {
		return err
	}
	base, err := ParseNativeAmount(p.BaseAmount)
	if err != nil || base > maxNativeJobAmount {
		return errors.New("invalid native base price")
	}
	if len(p.Components) > 8 || len(p.Components)+boolInt(p.Selection != nil) > 8 {
		return errors.New("too many native price components")
	}
	ids := map[string]bool{}
	maximum := base
	for _, c := range p.Components {
		if !nativeComponentPattern.MatchString(c.ID) || ids[c.ID] || strings.TrimSpace(c.Label) == "" || len(c.Label) > 256 {
			return errors.New("invalid native price component")
		}
		ids[c.ID] = true
		n, e := ParseNativeAmount(c.Amount)
		if e != nil || n <= 0 {
			return errors.New("invalid native component amount")
		}
		maximum, e = AddNativeAmounts(maximum, n)
		if e != nil || maximum > maxNativeJobAmount {
			return errors.New("native job price exceeds bound")
		}
		if c.When != nil && len(c.WhenAll) != 0 {
			return errors.New("native price condition forms conflict")
		}
		if len(c.WhenAll) > 4 {
			return errors.New("too many native price conditions")
		}
		conditions := c.WhenAll
		if c.When != nil {
			conditions = []ParameterCondition{*c.When}
		}
		for _, condition := range conditions {
			if condition.Parameter == "" || !validNativeOperator(condition.Operator) {
				return errors.New("invalid native price condition")
			}
		}
	}
	if p.Selection != nil {
		s := p.Selection
		if !nativeComponentPattern.MatchString(s.ID) || ids[s.ID] || strings.TrimSpace(s.Label) == "" || len(s.Label) > 256 || s.Parameter == "" || s.MaxItems < 1 || s.MaxItems > 8 {
			return errors.New("invalid native selection price")
		}
		n, e := ParseNativeAmount(s.Amount)
		if e != nil || n <= 0 || n > maxNativeJobAmount/int64(s.MaxItems) {
			return errors.New("invalid native selection unit amount")
		}
		maximum, e = AddNativeAmounts(maximum, n*int64(s.MaxItems))
		if e != nil || maximum > maxNativeJobAmount {
			return errors.New("native job price exceeds bound")
		}
	}
	if len(p.Components) > 0 || p.Selection != nil {
		limit, e := ParseNativeAmount(p.MaxAuthorizedExtraAmount)
		if p.ConsentParameter == "" || e != nil || limit < maximum-base || limit > maxNativeJobAmount {
			return errors.New("native extra price lacks sufficient consent bound")
		}
	} else if p.ConsentParameter != "" || p.MaxAuthorizedExtraAmount != "" {
		return errors.New("native extra authorization without components")
	}
	return nil
}
func (o Offering) EvaluateNativeQuote(input json.RawMessage) (*NativeQuote, error) {
	p := o.NativePricing
	if p == nil {
		return nil, nil
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return nil, errors.New("native price requires a JSON object")
	}
	values, err := costValues(input)
	if err != nil {
		return nil, err
	}
	base, _ := ParseNativeAmount(p.BaseAmount)
	extra := int64(0)
	q := &NativeQuote{Version: p.Version, Unit: p.Unit, UnitLabel: p.UnitLabel, BaseAmount: FormatNativeAmount(base)}
	for _, c := range p.Components {
		conditions := c.WhenAll
		if c.When != nil {
			conditions = []ParameterCondition{*c.When}
		}
		matched := true
		for _, condition := range conditions {
			if _, exists := values[condition.Parameter]; !exists {
				return nil, fmt.Errorf("native price input %q unavailable", condition.Parameter)
			}
			if !ConditionMatches(condition, values) {
				matched = false
			}
		}
		if !matched {
			continue
		}
		n, _ := ParseNativeAmount(c.Amount)
		extra, err = AddNativeAmounts(extra, n)
		if err != nil {
			return nil, err
		}
		q.Components = append(q.Components, NativeCharge{c.ID, c.Label, FormatNativeAmount(n)})
	}
	if p.Selection != nil {
		count, e := o.nativeSelectionCount(values)
		if e != nil {
			return nil, e
		}
		if count > 0 {
			per, _ := ParseNativeAmount(p.Selection.Amount)
			amount := per * int64(count)
			extra, err = AddNativeAmounts(extra, amount)
			if err != nil {
				return nil, err
			}
			q.Components = append(q.Components, NativeCharge{p.Selection.ID, p.Selection.Label, FormatNativeAmount(amount)})
		}
	}
	total, err := AddNativeAmounts(base, extra)
	if err != nil {
		return nil, err
	}
	q.ExtraAmount = FormatNativeAmount(extra)
	q.TotalAmount = FormatNativeAmount(total)
	sort.Slice(q.Components, func(i, j int) bool { return q.Components[i].ID < q.Components[j].ID })
	return q, q.Validate()
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (o Offering) nativeSelectionCount(values map[string]any) (int, error) {
	s := o.NativePricing.Selection
	chosen, ok := values[s.Parameter].([]any)
	if !ok || len(chosen) > s.MaxItems {
		return 0, errors.New("invalid native price selection")
	}
	allowed := map[string]bool{}
	minimum := 0
	for _, parameter := range o.Parameters {
		if parameter.Name == s.Parameter {
			minimum = parameter.MinItems
			for _, option := range parameter.Options {
				allowed[option.Value] = true
			}
			break
		}
	}
	if len(chosen) < minimum || len(allowed) == 0 {
		return 0, errors.New("invalid native price selection")
	}
	seen := map[string]bool{}
	for _, raw := range chosen {
		item, ok := raw.(string)
		if !ok || !allowed[item] || seen[item] {
			return 0, errors.New("invalid native price selection")
		}
		seen[item] = true
	}
	return len(chosen), nil
}
func (q *NativeQuote) Validate() error {
	if q == nil {
		return errors.New("native quote missing")
	}
	if err := nativeHeader(q.Version, q.Unit, q.UnitLabel); err != nil {
		return err
	}
	base, err := ParseNativeAmount(q.BaseAmount)
	if err != nil || FormatNativeAmount(base) != q.BaseAmount {
		return errors.New("invalid native quote base")
	}
	extra, err := ParseNativeAmount(q.ExtraAmount)
	if err != nil || FormatNativeAmount(extra) != q.ExtraAmount {
		return errors.New("invalid native quote extra")
	}
	total, err := ParseNativeAmount(q.TotalAmount)
	if err != nil || FormatNativeAmount(total) != q.TotalAmount || total > maxNativeJobAmount {
		return errors.New("invalid native quote total")
	}
	want, e := AddNativeAmounts(base, extra)
	if e != nil || want != total || len(q.Components) > 8 {
		return errors.New("inconsistent native quote")
	}
	sum := int64(0)
	previous := ""
	for _, c := range q.Components {
		if !nativeComponentPattern.MatchString(c.ID) || c.ID <= previous || strings.TrimSpace(c.Label) == "" || len(c.Label) > 256 {
			return errors.New("invalid frozen native component")
		}
		previous = c.ID
		n, e := ParseNativeAmount(c.Amount)
		if e != nil || n <= 0 || FormatNativeAmount(n) != c.Amount {
			return errors.New("invalid frozen native amount")
		}
		sum, e = AddNativeAmounts(sum, n)
		if e != nil {
			return e
		}
	}
	if sum != extra {
		return errors.New("native quote components do not match extra")
	}
	return nil
}
func (q *NativeQuote) HasExtra() (bool, error) {
	if err := q.Validate(); err != nil {
		return false, err
	}
	n, _ := ParseNativeAmount(q.ExtraAmount)
	return n > 0, nil
}
func SumNativeQuotes(quotes []*NativeQuote) ([]NativeAmount, error) {
	amounts := map[string]int64{}
	labels := map[string]string{}
	for _, q := range quotes {
		if err := q.Validate(); err != nil {
			return nil, err
		}
		if old, ok := labels[q.Unit]; ok && old != q.UnitLabel {
			return nil, errors.New("native unit label conflict")
		}
		labels[q.Unit] = q.UnitLabel
		n, _ := ParseNativeAmount(q.TotalAmount)
		sum, err := AddNativeAmounts(amounts[q.Unit], n)
		if err != nil {
			return nil, err
		}
		amounts[q.Unit] = sum
	}
	out := make([]NativeAmount, 0, len(amounts))
	for unit, n := range amounts {
		out = append(out, NativeAmount{unit, labels[unit], FormatNativeAmount(n)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Unit < out[j].Unit })
	return out, nil
}

// PriceEstimate explicitly separates an actual legacy zero from a native quote.
type PriceEstimate struct {
	LegacyCredits *int
	NativeQuote   *NativeQuote
}

func (o Offering) EstimatePrice(input json.RawMessage) (PriceEstimate, error) {
	if o.NativePricing != nil {
		q, e := o.EvaluateNativeQuote(input)
		return PriceEstimate{NativeQuote: q}, e
	}
	n := o.EstimateTotalCredits(input)
	return PriceEstimate{LegacyCredits: &n}, nil
}
func (o Offering) ExtraConsentParameter() string {
	if o.NativePricing != nil {
		return o.NativePricing.ConsentParameter
	}
	if o.Cost != nil {
		return o.Cost.ConsentParameter
	}
	return ""
}

// PriceSelectionParameter identifies the frozen choice list used by private
// conversion work, regardless of the Offering's historical or native quote.
func (o Offering) PriceSelectionParameter() string {
	if o.NativePricing != nil && o.NativePricing.Selection != nil {
		return o.NativePricing.Selection.Parameter
	}
	if o.Cost != nil {
		return o.Cost.SelectionParameter
	}
	return ""
}

func validNativeOperator(op string) bool {
	switch op {
	case "equals", "not-equals", "truthy", "falsy", "present", "absent":
		return true
	}
	return false
}

func (o Offering) DefaultNativeQuote(input json.RawMessage) (*NativeQuote, error) {
	if o.NativePricing == nil {
		return nil, nil
	}
	values := map[string]json.RawMessage{}
	if len(input) > 0 {
		if e := json.Unmarshal(input, &values); e != nil || values == nil {
			return nil, errors.New("invalid price preview input")
		}
	}
	for _, p := range o.Parameters {
		if _, ok := values[p.Name]; !ok && len(p.Default) > 0 {
			values[p.Name] = p.Default
		}
	}
	body, e := json.Marshal(values)
	if e != nil {
		return nil, e
	}
	resolved, e := costValues(body)
	if e != nil {
		return nil, e
	}
	ApplyForcedValues(o.Parameters, resolved)
	body, e = json.Marshal(resolved)
	if e != nil {
		return nil, e
	}
	return o.EvaluateNativeQuote(body)
}
func (o Offering) UpperNativeQuote() (*NativeQuote, error) {
	if o.NativePricing == nil {
		return nil, nil
	}
	p := *o.NativePricing
	p.Components = append([]NativePriceComponent(nil), p.Components...)
	for i := range p.Components {
		p.Components[i].When = nil
		p.Components[i].WhenAll = nil
	}
	o.NativePricing = &p
	values := map[string]any{}
	if p.Selection != nil {
		items := make([]any, 0, p.Selection.MaxItems)
		for _, parameter := range o.Parameters {
			if parameter.Name == p.Selection.Parameter {
				for i, option := range parameter.Options {
					if i >= p.Selection.MaxItems {
						break
					}
					items = append(items, option.Value)
				}
			}
		}
		if len(items) != p.Selection.MaxItems {
			return nil, errors.New("native upper quote selection unavailable")
		}
		values[p.Selection.Parameter] = items
	}
	body, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	return o.EvaluateNativeQuote(body)
}

// Validate checks an exact provider-native amount (for example a balance).
func (a NativeAmount) Validate() error {
	if e := nativeHeader(1, a.Unit, a.UnitLabel); e != nil {
		return e
	}
	n, e := ParseNativeAmount(a.Amount)
	if e != nil || FormatNativeAmount(n) != a.Amount {
		return errors.New("invalid exact native amount")
	}
	return nil
}
