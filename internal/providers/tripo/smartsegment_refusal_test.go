package tripo_test

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

var han = regexp.MustCompile(`\p{Han}`)

// #358: the reachable Smart Segmentation size refusal goes through the real
// Adapter.ValidateInput, keeps the size/limit values and the original
// diagnostic, and reads in English.
func TestSmartSegmentSizeRefusalUsesSchemaCopy(t *testing.T) {
	f := newSmartSegmentFixture(t, "image")
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	texts, err := catalog.NewTextLocalizer(product.Application())
	if err != nil {
		t.Fatal(err)
	}
	for i := range f.request.Offering.Parameters {
		if f.request.Offering.Parameters[i].Name == "image" {
			f.request.Offering.Parameters[i].MaxBytes = int64(len(f.input.body)) - 1
		}
	}
	input := string(f.request.Input)
	request := generation.InputValidationRequest{
		OwnerID: f.request.OwnerID, Offering: f.request.Offering, Input: f.request.Input,
		InputAssets: f.request.InputAssets, TaskDependencies: f.request.TaskDependencies,
	}
	err = f.adapter.ValidateInput(context.Background(), request)
	refusal := generation.UserFacingError(err)
	if refusal == nil || refusal.DetailTemplate != f.request.Offering.ExecutionMessages.InputSourceTooLarge {
		t.Fatalf("error = %v", err)
	}
	var provider *generation.ProviderError
	if !errors.As(err, &provider) || provider.Failure == nil ||
		!strings.Contains(provider.Failure.SafeDetail, "超过 Tripo Smart Segmentation 的上限") {
		t.Fatalf("diagnostic lost: %v", err)
	}
	english := refusal.LocalizedDetail(func(text string) string { return texts.Text("en", text) })
	if han.MatchString(english) || !strings.Contains(english, "The source file is") {
		t.Fatalf("English refusal = %q", english)
	}
	if string(request.Input) != input {
		t.Fatalf("input changed: %s", request.Input)
	}
	if len(refusal.DetailValues) != 4 || refusal.DetailValues[0] != "{size}" ||
		!strings.Contains(refusal.DetailValues[1], "B") || refusal.DetailValues[2] != "{limit}" {
		t.Fatalf("values = %v", refusal.DetailValues)
	}
}
