package meshy_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
)

// task_error type 映射依据 Meshy 官方错误文档（2026-09-17 检索）：
// invalid_input（用户输入）/ timeout / service_unavailable / server_error（第三方，建议重试）。
func TestObserveTaskErrorTypeClassification(t *testing.T) {
	cases := []struct {
		name          string
		taskError     string
		wantClass     generation.FailureClass
		wantRetryable bool
		wantCode      string
	}{
		{
			name:          "timeout可重试",
			taskError:     `{"type":"timeout","message":"processing exceeded the time limit"}`,
			wantClass:     generation.FailureTimedOut,
			wantRetryable: true,
		},
		{
			name:          "service_unavailable可重试",
			taskError:     `{"type":"service_unavailable","message":"temporarily unavailable"}`,
			wantClass:     generation.FailureUpstreamUnavailable,
			wantRetryable: true,
		},
		{
			name:          "server_error可重试",
			taskError:     `{"type":"server_error","message":"internal error"}`,
			wantClass:     generation.FailureUpstreamUnavailable,
			wantRetryable: true,
		},
		{
			name:          "invalid_input不可重试",
			taskError:     `{"type":"invalid_input","code":"image_too_complex","message":"The uploaded image is too complex"}`,
			wantClass:     generation.FailureInvalidInput,
			wantRetryable: false,
			wantCode:      "image_too_complex",
		},
		{
			name:          "未知type兜底rejected",
			taskError:     `{"code":"moderation_blocked","message":"rejected by safety filters"}`,
			wantClass:     generation.FailureRejected,
			wantRetryable: false,
			wantCode:      "moderation_blocked",
		},
		{
			name:          "上游错误不得回显签名URL",
			taskError:     `{"code":"https://assets.meshy.ai/model.glb?token=secret","message":"remote-7 https://assets.meshy.ai/model.glb?token=secret"}`,
			wantClass:     generation.FailureRejected,
			wantRetryable: false,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(
				writer http.ResponseWriter,
				_ *http.Request,
			) {
				_ = json.NewEncoder(writer).Encode(map[string]any{
					"id":         "remote-7",
					"status":     "FAILED",
					"task_error": json.RawMessage(testCase.taskError),
				})
			}))
			defer upstream.Close()
			parsed, err := url.Parse(upstream.URL)
			if err != nil {
				t.Fatal(err)
			}
			adapter := newMeshyAdapter(t, meshy.Config{
				BaseURL:         upstream.URL,
				APIKey:          "meshy-key",
				HTTPClient:      upstream.Client(),
				AllowHTTPAssets: true,
			})
			offering := generation.Offering{
				ProviderID:   "meshy",
				ModelID:      "meshy-test",
				Capability:   generation.TextTo3D,
				PollInterval: 3 * time.Second,
				AssetHosts:   []string{parsed.Hostname()},
				ProviderOptions: json.RawMessage(
					`{"endpoint":"text-to-3d","api_version":"v2"}`,
				),
			}
			observation, err := adapter.Observe(context.Background(), generation.ObserveRequest{
				TaskID:    "task-local",
				AttemptID: "attempt-local",
				Offering:  offering,
				Job:       generation.UpstreamJob{ProviderID: "meshy", RemoteID: "remote-7"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if observation.Phase != generation.UpstreamFailed || observation.Failure == nil {
				t.Fatalf("observation = %#v", observation)
			}
			failure := observation.Failure
			if failure.Class != testCase.wantClass || failure.Retryable != testCase.wantRetryable {
				t.Fatalf("class=%s retryable=%v", failure.Class, failure.Retryable)
			}
			if failure.UpstreamCode != "" {
				t.Fatalf("upstream code escaped private provider evidence: %q", failure.UpstreamCode)
			}
			if failure.SafeDetail != "Meshy 任务执行失败" {
				t.Fatalf("unsafe detail = %q", failure.SafeDetail)
			}
		})
	}
}
