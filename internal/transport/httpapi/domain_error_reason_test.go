package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/logging"
	"github.com/LeXwDeX/Plinth/internal/routing"
)

// #288: only a Detail its source marked UserFacing (Schema refusal text about
// the requester's own input) is returned, and only on a 400. Every other
// Detail or cause stays in the log behind the generic Schema message.
func TestDomainErrorReturnsOnlyUserFacingReasons(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	generic := application.Content.Messages["error.invalid_input"]
	reason := "「参考图」的图片为 8192×8192，超过当前模型的上限 36000000 像素，这一步没有提交。"
	secret := "upstream said: token=canary-token-123 at blobs/private/asset_9f3c"
	refusal := &generation.Error{Code: generation.ErrInvalidInput, Operation: "resolve_input_assets", Detail: reason, UserFacing: true}

	cases := []struct {
		name       string
		err        error
		status     int
		detail     string
		userFacing bool
	}{
		{"schema refusal", refusal, http.StatusBadRequest, reason, true},
		{"refusal under unmarked wrapper", &generation.Error{Code: generation.ErrInvalidInput, Operation: "prepare_input",
			Detail: "输入资产参数 \"reference_assets\" 无效", Cause: refusal}, http.StatusBadRequest, reason, true},
		{"refusal wrapped by fmt", errors.Join(errors.New(secret), refusal), http.StatusBadRequest, reason, true},
		{"unmarked detail", &generation.Error{Code: generation.ErrInvalidInput, Operation: "resolve_input_assets",
			Detail: "输入资产参数 \"reference_assets\" 的图片像素过多 asset_9f3c"}, http.StatusBadRequest, generic, false},
		{"provider cause", &generation.Error{Code: generation.ErrInvalidInput, Operation: "validate_provider_input",
			Detail: "输入未通过供给项校验", Cause: errors.New(secret)}, http.StatusBadRequest, generic, false},
		{"marked but not a 400", &generation.Error{Code: generation.ErrUnavailable, Operation: "fit_input_assets",
			Cause: refusal}, http.StatusServiceUnavailable, application.Content.Messages["error.unavailable"], false},
		{"marked internal", &generation.Error{Code: generation.ErrInternal, Operation: "x", Detail: secret, UserFacing: true},
			http.StatusInternalServerError, application.Content.Messages["error.internal"], false},
		{"plain error", errors.New(secret), http.StatusInternalServerError, application.Content.Messages["error.internal"], false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := logging.New(&logs, slog.LevelDebug)
			server := &Server{application: application, logger: logger}
			route, err := routing.ParseOne("/provider-assets/{token}", "token")
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			requestLogMiddleware(logger, route, nil, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				server.writeDomainError(writer, test.err)
			})).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/tasks/prepare", nil))
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if response.Code != test.status || body["detail"] != test.detail || (body["user_facing"] == true) != test.userFacing {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
			if id, _ := body["request_id"].(string); !strings.HasPrefix(id, "req_") {
				t.Fatalf("no request id: %s", response.Body)
			}
			for _, private := range []string{"canary-token-123", "blobs/private", "asset_9f3c", "reference_assets", "upstream said"} {
				if strings.Contains(response.Body.String(), private) {
					t.Fatalf("response leaked %q: %s", private, response.Body)
				}
			}
			// The cause is still logged for the request ID (#233).
			if !strings.Contains(logs.String(), `"cause"`) {
				t.Fatalf("access log lost the cause: %s", logs.String())
			}

			legacy := httptest.NewRecorder()
			server.writeLegacyDomainError(legacy, test.err)
			want := test.detail
			if !test.userFacing {
				want = application.Content.Messages["error."+string(codeOf(test.err))]
			}
			var legacyBody map[string]string
			if err := json.Unmarshal(legacy.Body.Bytes(), &legacyBody); err != nil ||
				legacy.Code != test.status || legacyBody[application.Authentication.API.ErrorField] != want {
				t.Fatalf("legacy status=%d body=%s", legacy.Code, legacy.Body)
			}
		})
	}
}

func codeOf(err error) generation.ErrorCode {
	var domainErr *generation.Error
	if errors.As(err, &domainErr) {
		return domainErr.Code
	}
	return generation.ErrInternal
}

// #329: a Schema-template reason reads in the request's language through both
// the normal problem answer and the legacy compatibility answer, so the two
// transports cannot drift.
func TestUserFacingTemplateReachesNormalAndLegacyErrorsInTheRequestLanguage(t *testing.T) {
	t.Parallel()
	server, application, english := localizedTestServer(t)
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	template := application.Runtime.GenerationMessages.ReferenceLimitExceeded
	if template == "" {
		t.Fatal("no reference-limit-exceeded template")
	}
	values := []string{"{max}", "4", "{actual}", "5", "{label}", "参考图"}
	refusal := &generation.Error{
		Code: generation.ErrInvalidInput, Operation: "normalize_input",
		Detail:         strings.NewReplacer(values...).Replace(template),
		UserFacing:     true,
		DetailTemplate: template,
		DetailValues:   values,
	}
	want := strings.NewReplacer(
		"{max}", "4", "{actual}", "5", "{label}", english.OfferingTexts["参考图"],
	).Replace(english.Fields["runtime.generation_messages.reference_limit_exceeded"])
	route, err := routing.ParseOne("/api/v1/tasks/{taskID}", "taskID")
	if err != nil {
		t.Fatal(err)
	}
	for name, write := range map[string]func(http.ResponseWriter, error){
		"problem": server.writeDomainError,
		"legacy":  server.writeLegacyDomainError,
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/task-x", nil)
			request.Header.Set("Accept-Language", "en")
			requestLogMiddleware(logging.New(&bytes.Buffer{}, slog.LevelDebug), route, nil, localeTraceMiddleware(application, texts,
				http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
					write(writer, refusal)
				}))).ServeHTTP(response, request)
			body := response.Body.String()
			if response.Code != http.StatusBadRequest || !strings.Contains(body, want) {
				t.Fatalf("%s = %d %s, want %q", name, response.Code, body, want)
			}
			for _, private := range []string{"{max}", "{actual}", "最多用"} {
				if strings.Contains(body, private) {
					t.Fatalf("%s leaked %q: %s", name, private, body)
				}
			}
		})
	}
}
