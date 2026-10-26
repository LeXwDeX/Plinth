package generation

import (
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #358: the kernel wrapper keeps the Provider's diagnostic as Detail and the
// UserFacing Schema refusal in the chain, so the Agent can localize the reason
// without losing the original evidence.
func TestInputValidationErrorKeepsUserFacingRefusal(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	messages := product.Application().Runtime.GenerationMessages
	refusal := messages.InputRefusal(schema.InputRefusalTooLarge, schema.InputRefusalValues{
		Size: "10.03 MB", Limit: "10 MB",
	})
	if refusal == nil {
		t.Fatal("Schema declares no input refusal")
	}
	const diagnostic = "来源文件 10.03 MB 超过上限 10 MB"
	err = inputValidationError(&ProviderError{
		Failure: &Failure{Class: FailureInvalidInput, SafeDetail: diagnostic},
		Cause:   refusal,
	})
	var domain *Error
	if !errors.As(err, &domain) || domain.Code != ErrInvalidInput || domain.Detail != diagnostic {
		t.Fatalf("error = %+v", err)
	}
	if got := UserFacingError(err); got != refusal {
		t.Fatalf("UserFacing refusal = %+v", got)
	}
	texts, localizerErr := catalog.NewTextLocalizer(product.Application())
	if localizerErr != nil {
		t.Fatal(localizerErr)
	}
	english := UserFacingError(err).LocalizedDetail(func(text string) string {
		return texts.Text("en", text)
	})
	if strings.Contains(english, "来源") || !strings.Contains(english, "10.03 MB") {
		t.Fatalf("English refusal = %q", english)
	}
}

func TestProviderInputRefusalKeepsDiagnosticWithoutSchemaCopy(t *testing.T) {
	const diagnostic = "Tripo Smart Segmentation 输入与冻结资产不一致"
	err := ProviderInputRefusal(Offering{}, schema.InputRefusalMismatch, schema.InputRefusalValues{},
		FailureInvalidInput, false, diagnostic)
	var provider *ProviderError
	if !errors.As(err, &provider) || provider.Failure == nil || provider.Failure.SafeDetail != diagnostic {
		t.Fatalf("error = %+v", err)
	}
	if UserFacingError(err) != nil {
		t.Fatal("a missing Schema text must not invent a UserFacing reason")
	}
}

func TestProviderInputRefusalWithSchemaCopy(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering := Offering{ExecutionMessages: product.Application().Runtime.GenerationMessages}
	err = ProviderInputRefusal(offering, schema.InputRefusalDimensions, schema.InputRefusalValues{
		Width: 4096, Height: 4096, MinWidth: 1, MinHeight: 1, MaxPixels: 16_000_000,
	}, FailureInvalidInput, false, "diag")
	if refusal := UserFacingError(err); refusal == nil {
		t.Fatalf("no UserFacing: %v", err)
	}
}

func TestProviderInputRefusalFailureKeepsOriginalCause(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering := Offering{ExecutionMessages: product.Application().Runtime.GenerationMessages}
	cause := errors.New("read source failed")
	err = ProviderInputRefusalFailure(&Failure{Class: FailureTransport, Retryable: true,
		UpstreamStatus: 503, UpstreamCode: "busy"}, offering,
		schema.InputRefusalUnavailable, schema.InputRefusalValues{}, "temporary", cause)
	if !errors.Is(err, cause) {
		t.Fatalf("original cause lost: %v", err)
	}
	var provider *ProviderError
	if !errors.As(err, &provider) || provider.Failure == nil ||
		provider.Failure.Class != FailureTransport || !provider.Failure.Retryable ||
		provider.Failure.UpstreamStatus != 503 || provider.Failure.UpstreamCode != "busy" {
		t.Fatalf("failure metadata lost: %+v", provider)
	}
	if refusal := UserFacingError(err); refusal == nil || refusal.Cause != cause {
		t.Fatalf("refusal cause lost: %+v", refusal)
	}
}
