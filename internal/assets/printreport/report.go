// Package printreport defines the bounded public print-report delivery format.
// It contains no provider identifiers, locators, credentials or free text.
package printreport

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
)

const (
	MediaType            = "application/vnd.art-ai.print-report+json"
	Format               = "art-ai.print-report.v1"
	MaxBytes             = 64 << 10
	MaxDepth             = 8
	MaxFields            = 128
	MaxSafeInteger int64 = 1<<53 - 1
)

var ErrInvalid = errors.New("invalid print report")

type Report struct {
	Format   string    `json:"format"`
	Kind     string    `json:"kind"`
	Analysis *Analysis `json:"analysis,omitempty"`
	Repair   *Repair   `json:"repair,omitempty"`
}
type Analysis struct {
	Status        string         `json:"status"`
	IssueCount    *int64         `json:"issue_count,omitempty"`
	ErrorCount    *int64         `json:"error_count,omitempty"`
	WarningCount  *int64         `json:"warning_count,omitempty"`
	Metrics       *Metrics       `json:"metrics,omitempty"`
	IntendedPrint *IntendedPrint `json:"intended_print,omitempty"`
	ThinWalls     *ThinWalls     `json:"thin_walls,omitempty"`
	EvaluatedAtMS *int64         `json:"evaluated_at_ms,omitempty"`
}
type Metrics struct {
	IsWatertight     *bool    `json:"is_watertight"`
	VolumeM3         *float64 `json:"volume_m3"`
	NonManifoldEdges *int64   `json:"non_manifold_edges"`
	DegenerateFaces  *int64   `json:"degenerate_faces"`
	Holes            *int64   `json:"holes"`
}
type IntendedPrint struct {
	PrinterType     string    `json:"printer_type"`
	HeightMM        *float64  `json:"height_mm,omitempty"`
	LongestSideMM   *float64  `json:"longest_side_mm,omitempty"`
	WallThicknessMM *float64  `json:"wall_thickness_mm,omitempty"`
	DimensionsMM    []float64 `json:"dimensions_mm,omitempty"`
}
type ThinWalls struct {
	ThinAreaRatio *float64 `json:"thin_area_ratio,omitempty"`
	HasThinWalls  *bool    `json:"has_thin_walls,omitempty"`
	Status        string   `json:"status"`
}
type Repair struct {
	DetailsStatus       string         `json:"details_status"`
	IntendedPrint       *IntendedPrint `json:"intended_print,omitempty"`
	Route               string         `json:"route,omitempty"`
	Applied             *bool          `json:"applied,omitempty"`
	FacesBefore         *int64         `json:"faces_before,omitempty"`
	FacesAfter          *int64         `json:"faces_after,omitempty"`
	ThinAreaRatioBefore *float64       `json:"thin_area_ratio_before,omitempty"`
	ThinAreaRatioAfter  *float64       `json:"thin_area_ratio_after,omitempty"`
}

// CheckJSONBounds checks syntax, duplicate keys and structural budgets without
// exposing any input in its errors. Wire adapters can run it before projecting
// allowlisted fields; it does not make an arbitrary object a valid report.
func CheckJSONBounds(raw []byte) error {
	if len(raw) == 0 || len(raw) > MaxBytes {
		return ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	fields := 0
	var walk func(int) error
	walk = func(depth int) error {
		if depth > MaxDepth {
			return ErrInvalid
		}
		token, err := decoder.Token()
		if err != nil {
			return ErrInvalid
		}
		delim, compound := token.(json.Delim)
		if !compound {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return ErrInvalid
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return ErrInvalid
				}
				seen[name] = true
				fields++
				if fields > MaxFields {
					return ErrInvalid
				}
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return ErrInvalid
			}
		case '[':
			for decoder.More() {
				fields++
				if fields > MaxFields {
					return ErrInvalid
				}
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
		return nil
	}
	if err := walk(1); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrInvalid
	}
	return nil
}

// Decode accepts only this exact version and its named domain fields.
func Decode(raw []byte) (Report, error) {
	if err := CheckJSONBounds(raw); err != nil {
		return Report{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value Report
	if err := decoder.Decode(&value); err != nil {
		return Report{}, ErrInvalid
	}
	if err := validate(value); err != nil {
		return Report{}, err
	}
	return value, nil
}
func Marshal(value Report) ([]byte, error) {
	if err := validate(value); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > MaxBytes {
		return nil, ErrInvalid
	}
	return raw, nil
}
func ValidateReader(reader io.Reader, maxBytes int64) error {
	if reader == nil || maxBytes <= 0 || maxBytes > MaxBytes {
		return ErrInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return err
	}
	if int64(len(raw)) > maxBytes {
		return ErrInvalid
	}
	_, err = Decode(raw)
	return err
}
func finite(v *float64) bool   { return v == nil || (!math.IsNaN(*v) && !math.IsInf(*v, 0)) }
func count(v *int64) bool      { return v == nil || (*v >= 0 && *v <= MaxSafeInteger) }
func ratio(v *float64) bool    { return finite(v) && (v == nil || (*v >= 0 && *v <= 1)) }
func positive(v *float64) bool { return finite(v) && (v == nil || *v > 0) }
func intended(p *IntendedPrint) bool {
	if p == nil {
		return true
	}
	if p.PrinterType != "fdm" && p.PrinterType != "sla" && p.PrinterType != "full_color" {
		return false
	}
	if !positive(p.HeightMM) || !positive(p.LongestSideMM) || !positive(p.WallThicknessMM) {
		return false
	}
	if p.DimensionsMM != nil && len(p.DimensionsMM) != 3 {
		return false
	}
	for _, v := range p.DimensionsMM {
		if !finite(&v) || v < 0 {
			return false
		}
	}
	return true
}
func validate(value Report) error {
	if value.Format != Format {
		return ErrInvalid
	}
	switch value.Kind {
	case "analysis":
		if value.Analysis == nil || value.Repair != nil {
			return ErrInvalid
		}
		a := value.Analysis
		if a.Status != "healthy" && a.Status != "warning" && a.Status != "error" && a.Status != "unknown" {
			return ErrInvalid
		}
		if !count(a.IssueCount) || !count(a.ErrorCount) || !count(a.WarningCount) || !count(a.EvaluatedAtMS) || !intended(a.IntendedPrint) {
			return ErrInvalid
		}
		anyCount := a.IssueCount != nil || a.ErrorCount != nil || a.WarningCount != nil
		allCounts := a.IssueCount != nil && a.ErrorCount != nil && a.WarningCount != nil
		if anyCount && !allCounts {
			return ErrInvalid
		}
		if allCounts && (*a.ErrorCount > MaxSafeInteger-*a.WarningCount || *a.IssueCount != *a.ErrorCount+*a.WarningCount) {
			return ErrInvalid
		}
		if a.Status != "unknown" && (!allCounts || a.Metrics == nil) {
			return ErrInvalid
		}
		if allCounts && ((a.Status == "healthy" && *a.IssueCount != 0) || (a.Status == "warning" && (*a.ErrorCount != 0 || *a.WarningCount == 0)) || (a.Status == "error" && *a.ErrorCount == 0)) {
			return ErrInvalid
		}
		if m := a.Metrics; m != nil {
			if m.IsWatertight == nil || m.VolumeM3 == nil || m.NonManifoldEdges == nil || m.DegenerateFaces == nil || m.Holes == nil || !finite(m.VolumeM3) || !count(m.NonManifoldEdges) || !count(m.DegenerateFaces) || !count(m.Holes) {
				return ErrInvalid
			}
		}
		if walls := a.ThinWalls; walls != nil {
			if a.IntendedPrint == nil || !ratio(walls.ThinAreaRatio) || (walls.Status != "healthy" && walls.Status != "error" && walls.Status != "unknown") {
				return ErrInvalid
			}
			if walls.Status != "unknown" && (walls.ThinAreaRatio == nil || walls.HasThinWalls == nil) {
				return ErrInvalid
			}
		}
	case "repair":
		if value.Repair == nil || value.Analysis != nil {
			return ErrInvalid
		}
		r := value.Repair
		if !intended(r.IntendedPrint) || !count(r.FacesBefore) || !count(r.FacesAfter) || !ratio(r.ThinAreaRatioBefore) || !ratio(r.ThinAreaRatioAfter) {
			return ErrInvalid
		}
		switch r.DetailsStatus {
		case "not_reported":
			if r.Route != "" || r.Applied != nil || r.FacesBefore != nil || r.FacesAfter != nil || r.ThinAreaRatioBefore != nil || r.ThinAreaRatioAfter != nil {
				return ErrInvalid
			}
		case "reported":
			switch r.Route {
			case "thin_wall":
				if r.Applied == nil || r.FacesBefore == nil || r.FacesAfter == nil {
					return ErrInvalid
				}
			case "topology", "topology_unmeasured":
				if r.Applied != nil || r.FacesBefore != nil || r.FacesAfter != nil {
					return ErrInvalid
				}
			default:
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}
