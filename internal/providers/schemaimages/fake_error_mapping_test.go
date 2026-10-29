package schemaimages_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/schemaimages"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	geminiimagesv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/gemini/images-v1"
	jimengapiv3 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/jimeng/api-v3"
	openaiimagesv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/openai/images-v1"
)

// The three sync image connections each declare an error_mapping in the Product
// Schema. The same upstream answer must read the same way on all of them: an
// empty balance is insufficient_balance whichever vocabulary the upstream uses
// (#139: Gemini read OpenAI's insufficient_quota as a plain rejection, and
// Jimeng read Ark's QuotaExceeded as a rate limit that retrying cannot fix).
// These tests drive the real Adapter and the Catalog's declarations against the
// #96 fake upstream's fault injection (the fake-<scenario> key), and against a
// stub for the documented error codes the fake does not produce.

type imageConnection struct {
	name       string
	connection string
	offering   generation.OfferingID
	prefix     string
	version    func() fakeupstream.Version
}

var imageConnections = []imageConnection{
	{"openai", "openai-images", "openai/gpt-image-2/text-to-image", openaiimagesv1.Prefix, openaiimagesv1.Version},
	{"gemini", "gemini-images", "gemini/gemini-3-pro-image/text-to-image", geminiimagesv1.Prefix, geminiimagesv1.Version},
	{"jimeng", "jimeng-images", "jimeng/seedream-4-5/text-to-image", jimengapiv3.Prefix, jimengapiv3.Version},
}

func declaredImageMapping(t *testing.T, connection string) *generation.ErrorMapping {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, declared := range product.Application().Runtime.ProviderConnections {
		if declared.ID == connection {
			if declared.ErrorMapping == nil {
				t.Fatalf("connection %s declares no error_mapping", connection)
			}
			return declared.ErrorMapping
		}
	}
	t.Fatalf("connection %s is not declared", connection)
	return nil
}

// startImage submits one text-to-image request to baseURL with key and returns
// the failure the Adapter reports (nil when it succeeded).
func startImage(t *testing.T, c imageConnection, baseURL, key string) *generation.Failure {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, input := normalizedInput(t, product, c.offering, `{"prompt":"a plain red cube"}`)
	adapter, err := schemaimages.New(schemaimages.Config{
		BaseURL: baseURL, APIKey: key, HTTPClient: http.DefaultClient,
		ErrorMapping: declaredImageMapping(t, c.connection), InputAssets: officialAssetStore{},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Start(t.Context(), generation.StartRequest{
		TaskID: "task", AttemptID: "attempt", OwnerID: "owner", SubmissionKey: "submission", Offering: offering, Input: input,
	})
	if err == nil {
		return nil
	}
	var provider *generation.ProviderError
	if !errors.As(err, &provider) || provider.Failure == nil {
		t.Fatalf("%s: not a provider failure: %v", c.name, err)
	}
	return provider.Failure
}

func TestImageConnectionsReadTheFakeFaultsAlike(t *testing.T) {
	fake, err := fakeupstream.New(fakeupstream.Config{}, openaiimagesv1.Version(), geminiimagesv1.Version(), jimengapiv3.Version())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	type want struct {
		class     generation.FailureClass
		retryable bool
	}
	// scenario -> connection -> expected failure. The fake builds the quota
	// answers from the vendors' documented vocabularies: OpenAI and the Gemini
	// gateway answer 402 insufficient_quota, Ark answers 403 AccountOverdueError
	// for arrears and 429 QuotaExceeded for an exhausted quota.
	table := []struct {
		scenario fakeupstream.Scenario
		want     map[string]want
	}{
		{fakeupstream.ScenarioInsufficientBalance, map[string]want{
			"openai": {generation.FailureInsufficientBalance, false}, "gemini": {generation.FailureInsufficientBalance, false},
			"jimeng": {generation.FailureInsufficientBalance, false}}},
		{fakeupstream.ScenarioKeyQuota, map[string]want{
			"openai": {generation.FailureInsufficientBalance, false}, "gemini": {generation.FailureInsufficientBalance, false},
			"jimeng": {generation.FailureCredentialQuota, false}}},
		{fakeupstream.ScenarioRateLimit, map[string]want{
			"openai": {generation.FailureRateLimited, true}, "gemini": {generation.FailureRateLimited, true},
			"jimeng": {generation.FailureRateLimited, true}}},
		// A 5xx on a create that is not idempotent may have been accepted and charged:
		// the client stops, whatever the connection's vocabulary says.
		{fakeupstream.ScenarioServerError, map[string]want{
			"openai": {generation.FailureSubmissionUnknown, false}, "gemini": {generation.FailureSubmissionUnknown, false},
			"jimeng": {generation.FailureSubmissionUnknown, false}}},
		{fakeupstream.ScenarioTaskFailed, map[string]want{
			"openai": {generation.FailureInvalidInput, false}, "gemini": {generation.FailureInvalidInput, false},
			"jimeng": {generation.FailureInvalidInput, false}}},
	}
	for _, row := range table {
		for _, c := range imageConnections {
			t.Run(string(row.scenario)+"/"+c.name, func(t *testing.T) {
				base := server.URL + c.prefix
				if c.name == "jimeng" {
					base = server.URL + "/jimeng"
				}
				failure := startImage(t, c, base, fakeupstream.KeyPrefix+string(row.scenario))
				expected := row.want[c.name]
				if failure == nil || failure.Class != expected.class || failure.Retryable != expected.retryable {
					t.Fatalf("failure = %+v, want class %s retryable %v", failure, expected.class, expected.retryable)
				}
			})
		}
	}
}

// stubbedAnswer returns a fixed error body the way the connection's upstream
// shapes it, for codes the #96 fake has no scenario for.
type stubbedAnswer struct {
	name   string
	status int
	body   string
	class  generation.FailureClass
	retry  bool
}

func runStubbedAnswers(t *testing.T, c imageConnection, base func(string) string, answers []stubbedAnswer) {
	t.Helper()
	for _, answer := range answers {
		t.Run(answer.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(answer.status)
				_, _ = w.Write([]byte(answer.body))
			}))
			t.Cleanup(upstream.Close)
			failure := startImage(t, c, base(upstream.URL), "stub-key")
			if failure == nil || failure.Class != answer.class || failure.Retryable != answer.retry {
				t.Fatalf("HTTP %d %s: failure = %+v, want class %s retryable %v", answer.status, answer.body, failure, answer.class, answer.retry)
			}
		})
	}
}

func errorEnvelope(code, message string) string {
	return fmt.Sprintf(`{"error":{"code":%q,"message":%q,"type":"aigateway_error"}}`, code, message)
}

// Gemini's error codes (https://ai.google.dev/gemini-api/docs/api-errors,
// checked 2026-10-29): `error.code` is a snake_case string, and any code not
// listed is the snake_case of the HTTP status. The connection speaks to an
// OpenAI-compatible gateway, so OpenAI's insufficient_quota arrives too. The
// answers below are constructed from that page and the gateway envelope seen in
// the #96 sample; none was observed from the real service. 5xx answers are not
// listed: a create that is not idempotent ends in submission_unknown on any 5xx
// before the mapping is consulted.
func TestGeminiConnectionReadsTheDocumentedErrorCodes(t *testing.T) {
	gemini := imageConnections[1]
	answers := []stubbedAnswer{
		{"insufficient_quota 429 (OpenAI vocabulary)", 429, errorEnvelope("insufficient_quota", "You exceeded your current quota"), generation.FailureInsufficientBalance, false},
		{"insufficient_quota 402", 402, errorEnvelope("insufficient_quota", "insufficient balance"), generation.FailureInsufficientBalance, false},
		{"payment_required 402", 402, errorEnvelope("payment_required", "Prepay credit balance is depleted"), generation.FailureInsufficientBalance, false},
		{"402 without a code", 402, `{"message":"payment required"}`, generation.FailureInsufficientBalance, false},
		{"balance message without a code", 400, `{"message":"account is in arrears"}`, generation.FailureInsufficientBalance, false},
		{"quota_exceeded 429", 429, errorEnvelope("quota_exceeded", "Daily quota exceeded"), generation.FailureCredentialQuota, false},
		{"rate_limit_exceeded 429", 429, errorEnvelope("rate_limit_exceeded", "per-minute limit"), generation.FailureRateLimited, true},
		{"too_many_requests 429", 429, errorEnvelope("too_many_requests", "slow down"), generation.FailureRateLimited, true},
		{"429 without a code", 429, `{"message":"slow down"}`, generation.FailureRateLimited, true},
		{"failed_precondition 400", 400, errorEnvelope("failed_precondition", "billing is disabled"), generation.FailureRejected, false},
		{"invalid_request 400", 400, errorEnvelope("invalid_request", "bad parameter"), generation.FailureInvalidInput, false},
		{"out_of_range 416", 416, errorEnvelope("out_of_range", "size"), generation.FailureInvalidInput, false},
		{"authentication 401", 401, errorEnvelope("authentication", "bad key"), generation.FailureAuthentication, false},
		{"permission_denied 403 stays an input refusal like the other image connections", 403, errorEnvelope("permission_denied", "no"), generation.FailureInvalidInput, false},
	}
	// Every generation-blocked code asks for different input. The page lists no
	// HTTP status for them, so the class must not depend on one: 409 is a status
	// the default reading would call a plain rejection.
	for _, code := range []string{"safety", "recitation", "language", "prohibited_content", "spii", "blocklist",
		"image_safety", "image_prohibited_content", "image_recitation", "image_other", "content_blocked"} {
		answers = append(answers,
			stubbedAnswer{"blocked " + code + " 400", 400, errorEnvelope(code, "blocked"), generation.FailureInvalidInput, false},
			stubbedAnswer{"blocked " + code + " 409", 409, errorEnvelope(code, "blocked"), generation.FailureInvalidInput, false})
	}
	runStubbedAnswers(t, gemini, func(url string) string { return url + "/v1" }, answers)
}

// Ark's error codes (https://docs.volcengine.com/docs/82379/1299023, checked
// 2026-10-29). QuotaExceeded (429) and SetLimitExceeded (429) are exhausted
// quotas and usage caps, not rate limits: waiting a minute does not help, so the
// Task must not look retryable.
func TestJimengConnectionReadsTheDocumentedErrorCodes(t *testing.T) {
	jimeng := imageConnections[2]
	body := func(code string) string {
		return fmt.Sprintf(`{"error":{"code":%q,"message":"m","param":"","type":"t"}}`, code)
	}
	answers := []stubbedAnswer{
		{"AccountOverdueError 403", 403, body("AccountOverdueError"), generation.FailureInsufficientBalance, false},
		{"QuotaExceeded 429", 429, body("QuotaExceeded"), generation.FailureCredentialQuota, false},
		{"SetLimitExceeded 429", 429, body("SetLimitExceeded"), generation.FailureCredentialQuota, false},
		{"RateLimitExceeded.EndpointRPMExceeded 429", 429, body("RateLimitExceeded.EndpointRPMExceeded"), generation.FailureRateLimited, true},
		{"ModelAccountRpmRateLimitExceeded 429", 429, body("ModelAccountRpmRateLimitExceeded"), generation.FailureRateLimited, true},
		{"ServerOverloaded 429", 429, body("ServerOverloaded"), generation.FailureRateLimited, true},
		{"InputTextSensitiveContentDetected 400", 400, body("InputTextSensitiveContentDetected"), generation.FailureInvalidInput, false},
		{"InvalidParameter 400", 400, body("InvalidParameter"), generation.FailureInvalidInput, false},
		{"AuthenticationError 401", 401, body("AuthenticationError"), generation.FailureAuthentication, false},
	}
	runStubbedAnswers(t, jimeng, func(url string) string { return url }, answers)
}

// The Catalog's declared code tables name only classes a Task can end in, and no
// image connection keeps a code a sibling reads differently without a reason.
func TestImageConnectionsDeclareTheSharedBalanceVocabulary(t *testing.T) {
	for _, c := range imageConnections {
		mapping := declaredImageMapping(t, c.connection)
		if c.name != "jimeng" {
			if rule, ok := mapping.UpstreamCodeMap["insufficient_quota"]; !ok || rule.Class != string(generation.FailureInsufficientBalance) {
				t.Errorf("%s: insufficient_quota = %+v, want insufficient_balance", c.name, rule)
			}
		}
		if rule := mapping.UpstreamCodeMap["AccountOverdueError"]; c.name == "jimeng" && rule.Class != string(generation.FailureInsufficientBalance) {
			t.Errorf("jimeng: AccountOverdueError = %+v", rule)
		}
		for code, rule := range mapping.UpstreamCodeMap {
			if strings.TrimSpace(code) == "" || rule.Class == "" {
				t.Errorf("%s: empty code table entry %q", c.name, code)
			}
		}
	}
}

// The OpenAI route is served by the same gateway as the Gemini one: a bare 402
// (no error code) is an empty balance there too.
func TestOpenAIConnectionReadsABare402AsAnEmptyBalance(t *testing.T) {
	runStubbedAnswers(t, imageConnections[0], func(url string) string { return url }, []stubbedAnswer{
		{"402 without a code", 402, `{"message":"payment required"}`, generation.FailureInsufficientBalance, false},
		{"insufficient_quota 429", 429, errorEnvelope("insufficient_quota", "You exceeded your current quota"), generation.FailureInsufficientBalance, false},
		{"rate_limit_exceeded 429", 429, errorEnvelope("rate_limit_exceeded", "slow down"), generation.FailureRateLimited, true},
	})
}
