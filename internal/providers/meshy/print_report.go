package meshy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/assets/printreport"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type resultReportProjection struct {
	Kind    string `json:"kind"`
	Role    string `json:"role"`
	Variant string `json:"variant"`
}

// Report decoding is an explicit protocol projection, never a generic JSON
// path copier. Only the normalized domain envelope enters a durable claim.
type wirePrintability struct {
	Version       string                     `json:"_version"`
	Status        string                     `json:"status"`
	IssueCount    *int64                     `json:"issue_count"`
	ErrorCount    *int64                     `json:"error_count"`
	WarningCount  *int64                     `json:"warning_count"`
	Metrics       *wirePrintMetrics          `json:"metrics"`
	IntendedPrint *printreport.IntendedPrint `json:"intended_print"`
	ThinWalls     *printreport.ThinWalls     `json:"thin_walls"`
	EvaluatedAt   *int64                     `json:"evaluated_at"`
}
type wirePrintMetrics struct {
	IsWatertight     *bool    `json:"is_watertight"`
	Volume           *float64 `json:"volume"`
	NonManifoldEdges *int64   `json:"non_manifold_edges"`
	DegenerateFaces  *int64   `json:"degenerate_faces"`
	Holes            *int64   `json:"holes"`
}
type wirePrintRepair struct {
	Route               string   `json:"route"`
	Applied             *bool    `json:"applied"`
	FacesBefore         *int64   `json:"faces_before"`
	FacesAfter          *int64   `json:"faces_after"`
	ThinAreaRatioBefore *float64 `json:"thin_area_ratio_before"`
	ThinAreaRatioAfter  *float64 `json:"thin_area_ratio_after"`
}

// Legacy model responses do not need another unbounded copy. Oversized report
// responses retain no raw body and fail the report decoder's explicit budget.
func boundedReportWire(raw []byte) json.RawMessage {
	if len(raw) > printreport.MaxBytes {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func reportRequirement(offering generation.Offering, projection resultReportProjection) (generation.AssetRequirement, error) {
	if projection.Role == "" || projection.Variant == "" || !validReportLabel(projection.Role) || !validReportLabel(projection.Variant) {
		return generation.AssetRequirement{}, printreport.ErrInvalid
	}
	found := []generation.AssetRequirement{}
	for _, output := range offering.Outputs {
		if output.Role == projection.Role && output.Variant == projection.Variant && output.VariantPrefix == "" {
			found = append(found, output)
		}
	}
	if len(found) != 1 {
		return generation.AssetRequirement{}, printreport.ErrInvalid
	}
	output := found[0]
	if output.Role == "model" || output.Role == "image" || output.Minimum == nil || *output.Minimum != 1 || (output.Delivery != "" && output.Delivery != "required") || len(output.MediaTypes) != 1 || output.MediaTypes[0] != printreport.MediaType || output.MaxBytes <= 0 || output.MaxBytes > printreport.MaxBytes {
		return generation.AssetRequirement{}, printreport.ErrInvalid
	}
	return output, nil
}
func validReportLabel(label string) bool {
	if len(label) > 96 || strings.TrimSpace(label) != label {
		return false
	}
	for _, part := range strings.Split(label, ".") {
		if !validResultKey(part) {
			return false
		}
	}
	return true
}
func validateResultReports(offering generation.Offering, options wireOptions) error {
	if len(options.ResultReports) == 0 {
		return nil
	}
	if len(options.ResultReports) != 1 {
		return fmt.Errorf("result_reports requires one normalized report")
	}
	projection := options.ResultReports[0]
	switch projection.Kind {
	case "analysis":
		if options.Endpoint != "print/analyze" {
			return fmt.Errorf("analysis report requires print/analyze")
		}
	case "repair":
		if options.Endpoint != "print/repair" {
			return fmt.Errorf("repair report requires print/repair")
		}
	default:
		return fmt.Errorf("unknown normalized report kind")
	}
	if projection.Kind == "repair" {
		modelRequired, modelProjected := false, false
		for _, output := range offering.Outputs {
			if output.Role == "model" && output.Minimum != nil && *output.Minimum >= 1 && (output.Delivery == "" || output.Delivery == "required") && output.MaxBytes > 0 {
				modelRequired = true
			}
		}
		for _, asset := range options.ResultAssets {
			if asset.Role == "model" {
				modelProjected = true
			}
		}
		if !modelRequired || !modelProjected {
			return fmt.Errorf("repair report requires a required repaired model projection")
		}
	}
	if _, err := reportRequirement(offering, projection); err != nil {
		return fmt.Errorf("result_reports requires one bounded required report output")
	}
	for _, asset := range options.ResultAssets {
		if asset.Role == projection.Role {
			return fmt.Errorf("report role cannot also project upstream URLs")
		}
	}
	return nil
}
func strictReportField(raw json.RawMessage, target any) error {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return printreport.ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return printreport.ErrInvalid
	}
	return nil
}
func (response taskResponse) reportClaims(attemptID generation.AttemptID, offering generation.Offering, options wireOptions) ([]generation.ArtifactClaim, error) {
	if len(options.ResultReports) == 0 {
		return nil, nil
	}
	if err := validateResultReports(offering, options); err != nil {
		return nil, providerFailure(generation.FailureInvalidInput, false, "Meshy 报告输出声明无效")
	}
	// The unmodified response exists in memory only. Check before map decoding can
	// erase duplicate top-level fields. The same bounds cover nested wire values.
	if err := printreport.CheckJSONBounds(response.RawJSON); err != nil {
		return nil, invalidPrintReport()
	}
	projection := options.ResultReports[0]
	requirement, _ := reportRequirement(offering, projection)
	envelope := printreport.Report{Format: printreport.Format, Kind: projection.Kind}
	switch projection.Kind {
	case "analysis":
		var wire wirePrintability
		if err := strictReportField(response.Raw["printability"], &wire); err != nil || wire.Version != "v1" {
			return nil, invalidPrintReport()
		}
		value := &printreport.Analysis{Status: wire.Status, IssueCount: wire.IssueCount, ErrorCount: wire.ErrorCount, WarningCount: wire.WarningCount, IntendedPrint: wire.IntendedPrint, ThinWalls: wire.ThinWalls, EvaluatedAtMS: wire.EvaluatedAt}
		if m := wire.Metrics; m != nil {
			value.Metrics = &printreport.Metrics{IsWatertight: m.IsWatertight, VolumeM3: m.Volume, NonManifoldEdges: m.NonManifoldEdges, DegenerateFaces: m.DegenerateFaces, Holes: m.Holes}
		}
		envelope.Analysis = value
	case "repair":
		value := &printreport.Repair{DetailsStatus: "not_reported"}
		if raw := response.Raw["intended_print"]; len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			value.IntendedPrint = &printreport.IntendedPrint{}
			if err := strictReportField(raw, value.IntendedPrint); err != nil {
				return nil, invalidPrintReport()
			}
		}
		if raw := response.Raw["print_repair_report"]; len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			var wire wirePrintRepair
			if err := strictReportField(raw, &wire); err != nil {
				return nil, invalidPrintReport()
			}
			value.DetailsStatus = "reported"
			value.Route = wire.Route
			value.Applied = wire.Applied
			value.FacesBefore = wire.FacesBefore
			value.FacesAfter = wire.FacesAfter
			value.ThinAreaRatioBefore = wire.ThinAreaRatioBefore
			value.ThinAreaRatioAfter = wire.ThinAreaRatioAfter
		}
		envelope.Repair = value
	}
	raw, err := printreport.Marshal(envelope)
	if err != nil || int64(len(raw)) > requirement.MaxBytes {
		return nil, invalidPrintReport()
	}
	return []generation.ArtifactClaim{{ID: fmt.Sprintf("%s:report:%s", attemptID, projection.Kind), Role: projection.Role, Variant: projection.Variant, MediaType: printreport.MediaType, Inline: raw}}, nil
}
func invalidPrintReport() error {
	return providerFailure(generation.FailureProtocol, false, "Meshy 返回的打印报告无效")
}

func (projection *resultReportProjection) UnmarshalJSON(raw []byte) error {
	if err := printreport.CheckJSONBounds(raw); err != nil {
		return printreport.ErrInvalid
	}
	type declaration resultReportProjection
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode((*declaration)(projection)); err != nil {
		return printreport.ErrInvalid
	}
	return nil
}
