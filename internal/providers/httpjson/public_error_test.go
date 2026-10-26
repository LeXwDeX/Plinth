package httpjson

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestHTTPClassifiersKeepProviderAddressOutOfPublicDetail(t *testing.T) {
	payload := []byte(`{"code":"BAD","message":"download https://private.example/asset?token=secret remote-job-123"}`)
	response := &http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header)}
	mapping := &generation.ErrorMapping{
		ErrorFields: generation.ErrorFieldPaths{CodePath: "code", MessagePath: "message"},
		UpstreamCodeMap: map[string]generation.ErrorClassRule{
			"BAD": {Class: string(generation.FailureInvalidInput)},
		},
	}
	for name, err := range map[string]error{
		"default": classifyHTTP(response, payload),
		"mapping": ClassifyWithMapping(mapping)(response, payload),
	} {
		var providerErr *generation.ProviderError
		if !errors.As(err, &providerErr) {
			t.Fatalf("%s: expected provider error: %v", name, err)
		}
		if strings.Contains(providerErr.Failure.SafeDetail, "private.example") ||
			strings.Contains(providerErr.Failure.SafeDetail, "remote-job-123") {
			t.Fatalf("%s: provider locator leaked: %q", name, providerErr.Failure.SafeDetail)
		}
		if providerErr.Failure.UpstreamExcerpt == "" {
			t.Fatalf("%s: private evidence missing", name)
		}
	}
	failure := ClassifyPayloadWithMapping(mapping, payload, generation.FailureRejected, "上游任务失败")
	if failure.SafeDetail != "上游任务失败" {
		t.Fatalf("payload classifier exposed provider detail: %q", failure.SafeDetail)
	}
}
