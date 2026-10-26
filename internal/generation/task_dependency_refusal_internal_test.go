package generation

import (
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #358: a refused task source keeps expired vs unsupported distinct, names
// only the public Schema label, and wraps ErrSourceExpired for callers.
func TestTaskDependencyRefusalIsUserFacingAndLabelOnly(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering := Offering{ExecutionMessages: product.Application().Runtime.GenerationMessages}
	parameter := Parameter{Name: "source_task", Label: "Source model"}
	expired := expiredTaskDependency(offering, parameter)
	expiredRefusal := UserFacingError(expired)
	if expiredRefusal == nil || !errors.Is(expired, ErrSourceExpired) {
		t.Fatalf("expired = %+v", expired)
	}
	if !strings.Contains(expiredRefusal.Detail, "Source model") || strings.Contains(expiredRefusal.Detail, "source_task") {
		t.Fatalf("expired detail = %q", expiredRefusal.Detail)
	}
	unsupported := invalidTaskDependency(offering, parameter)
	unsupportedRefusal := UserFacingError(unsupported)
	if unsupportedRefusal == nil || strings.Contains(unsupportedRefusal.Detail, "source_task") {
		t.Fatalf("unsupported = %+v", unsupported)
	}
	if unsupportedRefusal.DetailTemplate != offering.ExecutionMessages.InputSourceTaskUnsupported {
		t.Fatalf("unsupported template = %q", unsupportedRefusal.DetailTemplate)
	}
}

func TestTaskDependencyRefusalWithoutLabelUsesGenericParameters(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering := Offering{ExecutionMessages: product.Application().Runtime.GenerationMessages}
	parameter := Parameter{Name: "source_task"}
	err = invalidTaskDependency(offering, parameter)
	refusal := UserFacingError(err)
	if refusal == nil || refusal.DetailTemplate != offering.ExecutionMessages.InputParametersInvalid {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(refusal.Detail, "source_task") {
		t.Fatalf("detail names the internal parameter: %q", refusal.Detail)
	}
}
