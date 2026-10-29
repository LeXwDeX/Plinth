package httpjson

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// 错误契约样例来源（2026-09-17 检索）：
//   - 网宿网关 chat/completions 与 images/generations：401/403/500，原因在 message。
//   - 即梦/火山方舟 api/v3/images/generations：OpenAI 兼容 {error:{code,message}}。
//   - Tripo：{code:<int>,message,suggestion}。
//   - Meshy：请求错误 {message}。
func TestClassifiedErrorsCarryUpstreamEvidence(t *testing.T) {
	cases := []struct {
		name          string
		status        int
		body          string
		submitting    bool
		wantClass     generation.FailureClass
		wantCode      string
		wantRetryable bool
	}{
		{
			name:          "网宿网关401鉴权失败",
			status:        http.StatusUnauthorized,
			body:          `{"message":"InvalidToken: access token is invalid"}`,
			wantClass:     generation.FailureAuthentication,
			wantRetryable: false,
		},
		{
			// 通用分类把 403 归为鉴权（Meshy CORS/权限语义）；
			// 网宿网关的 403（请求体问题）由 ClassifyGatewayError 专属处理。
			name:          "通用403鉴权语义",
			status:        http.StatusForbidden,
			body:          `{"message":"AccessDenied"}`,
			wantClass:     generation.FailureAuthentication,
			wantRetryable: false,
		},
		{
			name:          "网宿网关500非提交",
			status:        http.StatusInternalServerError,
			body:          `{"message":"InternalGatewayError"}`,
			wantClass:     generation.FailureUpstreamUnavailable,
			wantRetryable: true,
		},
		{
			name:          "火山方舟OpenAI信封400",
			status:        http.StatusBadRequest,
			body:          `{"error":{"code":"InvalidParameter.Size","message":"size is not supported"}}`,
			wantClass:     generation.FailureInvalidInput,
			wantCode:      "InvalidParameter.Size",
			wantRetryable: false,
		},
		{
			name:          "火山方舟OpenAI信封429",
			status:        http.StatusTooManyRequests,
			body:          `{"error":{"code":"ExceededLimitation.RPM","message":"too many requests"}}`,
			wantClass:     generation.FailureRateLimited,
			wantCode:      "ExceededLimitation.RPM",
			wantRetryable: true,
		},
		{
			name:          "Tripo整型code",
			status:        http.StatusBadRequest,
			body:          `{"code":1004,"message":"One or more of your parameter is invalid","suggestion":"Refer to the API documentation"}`,
			wantClass:     generation.FailureInvalidInput,
			wantCode:      "1004",
			wantRetryable: false,
		},
		{
			name:          "Meshy消息体",
			status:        http.StatusNotFound,
			body:          `{"message":"Task not found"}`,
			wantClass:     generation.FailureRejected,
			wantRetryable: false,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.status)
				_, _ = w.Write([]byte(testCase.body))
			}))
			defer server.Close()
			client, _ := New(server.URL, "key", server.Client())
			err := client.DoJSON(
				context.Background(),
				http.MethodPost,
				"tasks",
				map[string]string{"prompt": "test"},
				"key",
				&struct{}{},
				testCase.submitting,
			)
			var providerErr *generation.ProviderError
			if !errors.As(err, &providerErr) {
				t.Fatalf("not a provider error: %v", err)
			}
			failure := providerErr.Failure
			if failure.Class != testCase.wantClass || failure.Retryable != testCase.wantRetryable {
				t.Fatalf("classification: class=%s retryable=%v", failure.Class, failure.Retryable)
			}
			if failure.UpstreamStatus != testCase.status {
				t.Fatalf("upstream status: %d", failure.UpstreamStatus)
			}
			if failure.UpstreamCode != testCase.wantCode {
				t.Fatalf("upstream code: %q want %q", failure.UpstreamCode, testCase.wantCode)
			}
			if failure.UpstreamExcerpt == "" {
				t.Fatalf("upstream excerpt missing")
			}
			if failure.SafeDetail == "" {
				t.Fatal("safe detail fallback missing")
			}
		})
	}
}

func TestSubmittingServerErrorKeepsEvidenceForSubmissionUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"Fail to invoke pose estimation"}`))
	}))
	defer server.Close()
	client, _ := New(server.URL, "key", server.Client())
	err := client.DoJSON(
		context.Background(),
		http.MethodPost,
		"rigging",
		map[string]string{"input_task_id": "x"},
		"key",
		&struct{}{},
		true,
	)
	var providerErr *generation.ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("not a provider error: %v", err)
	}
	failure := providerErr.Failure
	if failure.Class != generation.FailureSubmissionUnknown || !failure.Ambiguous || failure.Retryable {
		t.Fatalf("classification: %#v", failure)
	}
	if failure.UpstreamStatus != http.StatusInternalServerError {
		t.Fatalf("upstream status lost: %d", failure.UpstreamStatus)
	}
	if !strings.Contains(failure.UpstreamExcerpt, "Fail to invoke pose estimation") {
		t.Fatalf("upstream excerpt lost: %q", failure.UpstreamExcerpt)
	}
}

func TestInvalidJSONKeepsEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("<html>gateway error page</html>"))
	}))
	defer server.Close()
	client, _ := New(server.URL, "key", server.Client())
	err := client.DoJSON(
		context.Background(),
		http.MethodGet,
		"tasks/t",
		nil,
		"",
		&struct {
			Result string `json:"result"`
		}{},
		false,
	)
	var providerErr *generation.ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("not a provider error: %v", err)
	}
	failure := providerErr.Failure
	if failure.Class != generation.FailureProtocol {
		t.Fatalf("classification: %s", failure.Class)
	}
	if failure.UpstreamStatus != http.StatusOK || failure.UpstreamExcerpt == "" {
		t.Fatalf("evidence lost: status=%d excerpt=%q", failure.UpstreamStatus, failure.UpstreamExcerpt)
	}
}

func TestBoundedExcerptSanitizesAndTruncates(t *testing.T) {
	long := `{"data":"` + strings.Repeat("x", 900) + `"}`
	excerpt := boundedExcerpt([]byte(long))
	if len(excerpt) > 400 {
		t.Fatalf("excerpt too long: %d", len(excerpt))
	}
	control := boundedExcerpt([]byte("a\x00b\nc\r\td"))
	if strings.ContainsAny(control, "\x00") {
		t.Fatalf("control characters kept: %q", control)
	}
}

// Every Provider that uses this client stores the same sanitized excerpt as
// Meshy does (A4, #121): URLs with signed queries, bearer/key tokens and
// e-mail addresses an upstream error body repeats never reach the database.
func TestBoundedExcerptUsesTheSharedSanitizer(t *testing.T) {
	body := `{"error":{"message":"bad input for owner@example.com; see https://api.example.test/v1/files/abc?X-Amz-Signature=beef",` +
		`"detail":"Authorization: Bearer abcdefghijklmnop token=zzz999 key sk-secretsecretsecret"}}`
	excerpt := boundedExcerpt([]byte(body))
	for _, leaked := range []string{"owner@example.com", "api.example.test", "X-Amz", "beef", "abcdefghijklmnop", "zzz999", "sk-secretsecretsecret"} {
		if strings.Contains(excerpt, leaked) {
			t.Fatalf("excerpt leaked %q: %q", leaked, excerpt)
		}
	}
	if !strings.Contains(excerpt, "bad input for") {
		t.Fatalf("excerpt lost its forensic text: %q", excerpt)
	}
	multibyte := boundedExcerpt([]byte(strings.Repeat("面", 500)))
	if utf8.RuneCountInString(multibyte) != generation.UpstreamExcerptRunes || !utf8.ValidString(multibyte) {
		t.Fatalf("multi-byte excerpt cut mid-character or at the wrong size: %d runes", utf8.RuneCountInString(multibyte))
	}
}

func TestErrorEvidencePrefersNestedOpenAIMessage(t *testing.T) {
	message, code := errorEvidence([]byte(`{"error":{"code":"ModelServiceException","message":"upstream busy"},"message":"ignored"}`))
	if message != "upstream busy" || code != "ModelServiceException" {
		t.Fatalf("message=%q code=%q", message, code)
	}
}

// 网宿网关专属分类器：401 鉴权、403 请求体问题（我方）、其余回退通用分类。
func TestClassifyGatewayErrorMatchesGatewaySemantics(t *testing.T) {
	cases := []struct {
		status    int
		body      string
		wantClass generation.FailureClass
	}{
		{http.StatusUnauthorized, `{"message":"token expired"}`, generation.FailureAuthentication},
		{http.StatusForbidden, `{"message":"MissingPrompt"}`, generation.FailureInvalidInput},
	}
	for _, testCase := range cases {
		response := &http.Response{StatusCode: testCase.status}
		err := ClassifyGatewayError(response, []byte(testCase.body))
		var providerErr *generation.ProviderError
		if !errors.As(err, &providerErr) {
			t.Fatalf("status %d: not a provider error: %v", testCase.status, err)
		}
		failure := providerErr.Failure
		if failure.Class != testCase.wantClass {
			t.Fatalf("status %d: class=%s", testCase.status, failure.Class)
		}
		if failure.UpstreamStatus != testCase.status || failure.UpstreamExcerpt == "" {
			t.Fatalf("status %d: evidence lost", testCase.status)
		}
	}
	if ClassifyGatewayError(&http.Response{StatusCode: 429}, nil) != nil {
		t.Fatal("unhandled status should fall back")
	}
}
