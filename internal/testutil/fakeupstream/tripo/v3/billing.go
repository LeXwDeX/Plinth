package tripov3

import (
	"encoding/json"
	"strconv"
)

// What a task is billed (#305). The committed samples hold credits_consumed as a
// placeholder, so the prices below are from the paid run of 2026-11-16 and from
// the pricing page; the task kinds that are not listed report the sample's value
// as before.
//
//   - A rig check is free and a rig on v3.0-20260909 costs 25 credits (observed).
//   - A conversion costs 5 credits, 10 when any of quad, face_limit,
//     flatten_bottom, flatten_bottom_threshold, texture_size, texture_format,
//     pivot_to_center_bottom or scale_factor leaves the page's default (texture
//     size 4096, texture format JPEG, the rest off). 10 is observed, with PNG
//     textures of 2048 pixels; fbx_preset and bake are not among the options
//     that raise it.
//   - A decimation costs 35 credits, 40 for a quad mesh on v3.0 (the page; 35
//     observed for a triangle mesh).
//
// A running task already reports the whole price as credits_consumed: the credits
// are frozen when the task is created, and the poll shows the frozen amount
// (observed, rig and conversions). The queued poll has no real sample and keeps
// the sample's value. A task that fails or is cancelled is not charged (the
// pricing page), so it reports 0. The account balance stays the fixed value of
// the fake: only the task object is billed.
const (
	creditsRig             = 25
	creditsConvertBasic    = 5
	creditsConvertAdvanced = 10
	creditsDecimate        = 35
	creditsDecimateQuad    = 40
)

// Defaults of the convert page whose change makes a conversion advanced.
const (
	convertDefaultTextureSize   = 4096
	convertDefaultTextureFormat = "JPEG"
)

// price is what the task costs, and whether the fake prices its kind.
func (t *task) price() (float64, bool) {
	switch t.kind.Type {
	case "animate_prerigcheck":
		return 0, true
	case "animate_rig":
		return creditsRig, true
	case "convert_model":
		if convertAdvanced(t.convert) {
			return creditsConvertAdvanced, true
		}
		return creditsConvertBasic, true
	case "highpoly_to_lowpoly":
		if t.decimatesToQuad() && stringOr(t.request["model"], "") == "v3.0" {
			return creditsDecimateQuad, true
		}
		return creditsDecimate, true
	}
	return 0, false
}

// convertAdvanced reports whether a convert request is billed as an advanced
// conversion.
func convertAdvanced(options map[string]any) bool {
	for _, name := range []string{"quad", "flatten_bottom", "pivot_to_center_bottom"} {
		if on, _ := options[name].(bool); on {
			return true
		}
	}
	for _, name := range []string{"face_limit", "flatten_bottom_threshold"} {
		if _, present := options[name]; present {
			return true
		}
	}
	if size, present := options["texture_size"].(json.Number); present {
		if edge, err := size.Int64(); err != nil || edge != convertDefaultTextureSize {
			return true
		}
	}
	if format, present := options["texture_format"].(string); present && format != convertDefaultTextureFormat {
		return true
	}
	if scale, present := options["scale_factor"].(json.Number); present {
		if factor, err := scale.Float64(); err != nil || factor != 1 {
			return true
		}
	}
	return false
}

// bill writes credits_consumed into a poll body: the frozen price once the task
// runs and when it succeeds, nothing for a task that ended without a result.
func (t *task) bill(data map[string]any, status string) {
	price, priced := t.price()
	if _, shown := data["credits_consumed"]; !priced || !shown {
		return
	}
	switch status {
	case statusRunning, statusSuccess:
		data["credits_consumed"] = json.Number(strconv.FormatFloat(price, 'f', -1, 64))
	case statusFailed, statusCancelled, statusUnknown:
		data["credits_consumed"] = json.Number("0")
	}
}
