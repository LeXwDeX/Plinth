package tripo

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #358: a transport failure while reading a source is still a temporary server
// condition, so it is explained with Schema copy. A bad parameter keeps its
// own diagnostic; a protocol failure is not an input refusal.
func TestTripoInputRefusalCoversOnlyTemporaryReadFailures(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering := generation.Offering{ExecutionMessages: product.Application().Runtime.GenerationMessages}
	const diagnostic = "读取来源文件失败"
	invalid := failure(generation.FailureInvalidInput, false, diagnostic)
	if got := tripoInputRefusal(offering, invalid); got != invalid {
		t.Fatalf("invalid input was rewritten: %v", got)
	}
	transport := &generation.ProviderError{Failure: &generation.Failure{
		Class: generation.FailureTransport, Retryable: true, Ambiguous: true, SafeDetail: diagnostic,
		UpstreamStatus: 429, UpstreamCode: "rate_limited", UpstreamExcerpt: "retry later",
		RetryAfter: 3 * time.Second,
	}}
	got := tripoInputRefusal(offering, transport)
	refusal := generation.UserFacingError(got)
	if refusal == nil || refusal.Code != generation.ErrUnavailable || refusal.DetailTemplate == "" {
		t.Fatalf("error = %v", got)
	}
	var provider *generation.ProviderError
	if !errors.As(got, &provider) || provider.Failure == nil {
		t.Fatalf("provider failure lost: %v", got)
	}
	gotFailure := provider.Failure
	if gotFailure.Class != generation.FailureTransport || !gotFailure.Retryable || !gotFailure.Ambiguous ||
		gotFailure.SafeDetail != diagnostic || gotFailure.UpstreamStatus != 429 ||
		gotFailure.UpstreamCode != "rate_limited" || gotFailure.UpstreamExcerpt != "retry later" ||
		gotFailure.RetryAfter != 3*time.Second {
		t.Fatalf("failure metadata lost: %+v", gotFailure)
	}
	protocol := failure(generation.FailureProtocol, false, "声明无效")
	if tripoInputRefusal(offering, protocol) != protocol {
		t.Fatal("protocol failure was turned into an input refusal")
	}
}

func TestSmartSegmentInvalidKeepsCauseOutOfSafeDetail(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering := generation.Offering{ExecutionMessages: product.Application().Runtime.GenerationMessages}
	cause := errors.New("decode failed")
	err = smartSegmentInvalid(offering, "source image invalid", cause)
	var provider *generation.ProviderError
	if !errors.As(err, &provider) || provider.Failure == nil ||
		strings.Contains(provider.Failure.SafeDetail, "decode failed") ||
		!errors.Is(err, cause) {
		t.Fatalf("error = %v", err)
	}
	refusal := generation.UserFacingError(err)
	if refusal == nil || refusal.Cause != cause {
		t.Fatalf("refusal = %+v", refusal)
	}
}
