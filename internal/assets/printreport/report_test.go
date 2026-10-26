package printreport

import (
	"bytes"
	"math"
	"strings"
	"testing"
)

func ptr[T any](v T) *T { return &v }
func validAnalysis() Report {
	return Report{Format: Format, Kind: "analysis", Analysis: &Analysis{Status: "error", IssueCount: ptr(int64(1)), ErrorCount: ptr(int64(1)), WarningCount: ptr(int64(0)), Metrics: &Metrics{IsWatertight: ptr(false), VolumeM3: ptr(-2.5), NonManifoldEdges: ptr(int64(0)), DegenerateFaces: ptr(int64(0)), Holes: ptr(int64(0))}}}
}
func TestReportRoundTripPreservesDiagnosticAndMissingRepairDetails(t *testing.T) {
	for _, value := range []Report{validAnalysis(), {Format: Format, Kind: "analysis", Analysis: &Analysis{Status: "unknown"}}, {Format: Format, Kind: "repair", Repair: &Repair{DetailsStatus: "not_reported"}}, {Format: Format, Kind: "repair", Repair: &Repair{DetailsStatus: "reported", Route: "thin_wall", Applied: ptr(false), FacesBefore: ptr(int64(0)), FacesAfter: ptr(int64(0)), ThinAreaRatioBefore: ptr(0.0)}}} {
		raw, err := Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := Decode(raw)
		if err != nil {
			t.Fatal(err)
		}
		again, err := Marshal(decoded)
		if err != nil || !bytes.Equal(again, raw) {
			t.Fatalf("unstable report=%s err=%v", again, err)
		}
		if value.Kind == "analysis" && value.Analysis.Metrics != nil && *decoded.Analysis.Metrics.VolumeM3 != -2.5 {
			t.Fatal("non-positive diagnostic volume changed")
		}
		if value.Kind == "repair" && value.Repair.DetailsStatus == "not_reported" && (strings.Contains(string(raw), "applied") || strings.Contains(string(raw), "faces")) {
			t.Fatalf("invented repair details %s", raw)
		}
	}
}
func TestReportRejectsMalformedVersionStructureAndValues(t *testing.T) {
	good, _ := Marshal(validAnalysis())
	for name, raw := range map[string][]byte{
		"unknown version":      bytes.Replace(good, []byte(Format), []byte("art-ai.print-report.v2"), 1),
		"duplicate":            []byte(`{"format":"art-ai.print-report.v1","format":"art-ai.print-report.v1","kind":"analysis","analysis":{"status":"unknown"}}`),
		"nested duplicate":     []byte(`{"format":"art-ai.print-report.v1","kind":"analysis","analysis":{"status":"unknown","status":"healthy"}}`),
		"unknown key":          []byte(`{"format":"art-ai.print-report.v1","kind":"analysis","analysis":{"status":"unknown","url":"https://SECRET"}}`),
		"trailing":             append(append([]byte{}, good...), []byte(` {}`)...),
		"large":                bytes.Repeat([]byte(" "), MaxBytes+1),
		"deep":                 []byte(strings.Repeat("[", MaxDepth+1) + "0" + strings.Repeat("]", MaxDepth+1)),
		"many elements":        []byte("[" + strings.Repeat("0,", MaxFields) + "0]"),
		"null":                 []byte(`null`),
		"wrong integer":        bytes.Replace(good, []byte(`"issue_count":1`), []byte(`"issue_count":1.5`), 1),
		"overflow":             bytes.Replace(good, []byte(`"issue_count":1`), []byte(`"issue_count":9007199254740992`), 1),
		"missing metrics":      []byte(`{"format":"art-ai.print-report.v1","kind":"analysis","analysis":{"status":"healthy","issue_count":0,"error_count":0,"warning_count":0}}`),
		"contradictory repair": []byte(`{"format":"art-ai.print-report.v1","kind":"repair","repair":{"details_status":"not_reported","applied":false}}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(raw); err != ErrInvalid {
				t.Fatalf("error=%v", err)
			}
			if strings.Contains(errString(raw), "SECRET") {
				t.Fatal("error echoed bytes")
			}
		})
	}
	for _, mutate := range []func(*Report){func(r *Report) { r.Analysis.Metrics.VolumeM3 = ptr(math.NaN()) }, func(r *Report) { r.Analysis.ErrorCount = ptr(int64(-1)) }, func(r *Report) { r.Analysis.IssueCount = ptr(int64(2)) }, func(r *Report) {
		r.Analysis.ThinWalls = &ThinWalls{Status: "error", ThinAreaRatio: ptr(1.1), HasThinWalls: ptr(true)}
	}} {
		value := validAnalysis()
		mutate(&value)
		if _, err := Marshal(value); err != ErrInvalid {
			t.Fatalf("invalid domain value accepted: %v", err)
		}
	}
}
func errString(raw []byte) string {
	_, err := Decode(raw)
	if err == nil {
		return ""
	}
	return err.Error()
}
func TestReportReaderBoundsAreEnforcedBeforeDecode(t *testing.T) {
	raw, _ := Marshal(validAnalysis())
	if err := ValidateReader(bytes.NewReader(raw), int64(len(raw))); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int64{0, int64(len(raw) - 1), MaxBytes + 1} {
		if ValidateReader(bytes.NewReader(raw), limit) == nil {
			t.Fatal("invalid limit accepted")
		}
	}
}
