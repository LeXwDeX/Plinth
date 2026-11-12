package generation

import (
	"context"
	"encoding/json"
	"image"
	"io"
	"strings"

	// Decoders for every picture format an Offering can accept as a source, so
	// a header can be read whichever package registered first.
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

// applySourceSideDefaults lets a parameter that declares default_by_source_side
// take the value its Schema tiers give for the pictures the request submits
// (#284), instead of the static default Normalize filled in. A value the caller
// gave is never touched: raw is the input as the caller sent it, and a name it
// holds is the caller's. It runs after the input is normalized and fitted, so
// the pictures are the ones the Task will freeze; the result is normalized once
// more so the frozen input is canonical whichever way the value arrived.
//
// Pictures that cannot be measured (an unreadable header, no readable bytes)
// leave the static default, the behaviour before the rule existed. An Offering
// that declares no such parameter costs nothing here.
func (m *Module) applySourceSideDefaults(
	ctx context.Context,
	owner OwnerID,
	capability Capability,
	offering Offering,
	raw json.RawMessage,
	normalized json.RawMessage,
) (Offering, json.RawMessage, error) {
	var pending []Parameter
	for _, parameter := range offering.Parameters {
		if parameter.DefaultBySourceSide != nil {
			pending = append(pending, parameter)
		}
	}
	if len(pending) == 0 {
		return offering, normalized, nil
	}
	var given map[string]json.RawMessage
	_ = json.Unmarshal(raw, &given)
	var frozen map[string]json.RawMessage
	if err := json.Unmarshal(normalized, &frozen); err != nil {
		return offering, normalized, nil
	}
	var resolved map[string][]InputAsset
	changed := false
	for _, parameter := range pending {
		if _, explicit := given[parameter.Name]; explicit {
			continue
		}
		if resolved == nil {
			var err error
			if resolved, err = m.inputs.Resolve(ctx, owner, offering, normalized); err != nil {
				return Offering{}, nil, err
			}
		}
		side := m.longestSide(ctx, resolved[parameter.DefaultBySourceSide.Source])
		value, ok := parameter.DefaultBySourceSide.ValueFor(side)
		if !ok || strings.TrimSpace(string(frozen[parameter.Name])) == strings.TrimSpace(string(value)) {
			continue
		}
		frozen[parameter.Name] = value
		changed = true
	}
	if !changed {
		return offering, normalized, nil
	}
	merged, err := json.Marshal(frozen)
	if err != nil {
		return Offering{}, nil, &Error{Code: ErrInternal, Operation: "source_side_defaults", Cause: err}
	}
	renormalized, canonical, err := m.catalog.Normalize(capability, offering.ID, merged)
	if err != nil {
		return Offering{}, nil, err
	}
	if renormalized.ID != offering.ID || renormalized.Revision != offering.Revision {
		return Offering{}, nil, &Error{Code: ErrConflict, Operation: "source_side_defaults", Detail: "供给项修订已变化"}
	}
	return renormalized, canonical, nil
}

// longestSide is the largest long side, in pixels, among the pictures; 0 when
// there is none or any of them cannot be measured, so one unreadable picture
// never lets the others choose a size it was not measured for.
func (m *Module) longestSide(ctx context.Context, assets []InputAsset) int64 {
	var longest int64
	for _, asset := range assets {
		side := m.pictureLongSide(ctx, asset)
		if side == 0 {
			return 0
		}
		longest = max(longest, side)
	}
	return longest
}

func (m *Module) pictureLongSide(ctx context.Context, asset InputAsset) int64 {
	if m.inputs == nil {
		return 0
	}
	body, err := m.inputs.Open(ctx, asset)
	if err != nil {
		return 0
	}
	defer body.Close()
	config, _, err := image.DecodeConfig(io.LimitReader(body, headerReadLimit))
	if err != nil {
		return 0
	}
	return int64(max(config.Width, config.Height))
}
