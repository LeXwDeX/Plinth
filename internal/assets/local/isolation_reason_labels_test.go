package local

import (
	"bytes"
	"errors"
	"math"
	"testing"
)

func inspectionReasonOf(err error) string {
	var inspection *inspectionRejection
	if errors.As(err, &inspection) {
		return inspection.reason
	}
	return ""
}

// The cap and numeric rejections used to carry no typed reason, so the log
// only named the wire family. Each now names the failing check; acceptance is
// unchanged.
func TestInspectionCapsAndNumericRejectionsCarryTypedReasons(t *testing.T) {
	if got := inspectionReasonOf(scanArchiveXML(bytes.NewReader(nil), maxArchiveXMLBytes+1, archiveXMLLimit(0))); got != "xml_size_limit" {
		t.Fatalf("xml cap reason=%q", got)
	}
	if got := inspectionReasonOf(inspectUSDC(bytes.NewReader(nil), maxUSDCBytes+1, nil, "", nil)); got != "usdc_size_limit" {
		t.Fatalf("usdc cap reason=%q", got)
	}
	if got := inspectionReasonOf(inspectionWorkError()); got != "inspection_work_limit" {
		t.Fatalf("work budget reason=%q", got)
	}
	inspector, err := newTypedRunInspector(4, typedRunFloat32, newInspectionWorkBudget())
	if err != nil {
		t.Fatal(err)
	}
	err = inspector.inspect(typedRunTestWord(4, uint64(math.Float32bits(float32(math.Inf(1))))))
	if got := inspectionReasonOf(err); got != "non_finite_float" {
		t.Fatalf("non-finite reason=%q", got)
	}
}
