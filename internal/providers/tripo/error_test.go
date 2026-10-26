package tripo_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
)

// TestErrorCodeClassification 验证 Tripo 错误码到 generation.FailureClass 的映射。
// 错误码表真源：https://docs.tripo3d.ai/get-started/errors-and-error-handling.html
func TestErrorCodeClassification(t *testing.T) {
	cases := []struct {
		name       string
		httpStatus int
		errorCode  int
		wantClass  generation.FailureClass
		wantRetry  bool
	}{
		{
			name:       "1002 认证失败 → authentication 不可重试",
			httpStatus: http.StatusUnauthorized,
			errorCode:  1002,
			wantClass:  generation.FailureAuthentication,
			wantRetry:  false,
		},
		{
			name:       "1004 参数无效 → invalid_input 不可重试",
			httpStatus: http.StatusBadRequest,
			errorCode:  1004,
			wantClass:  generation.FailureInvalidInput,
			wantRetry:  false,
		},
		{
			name:       "1005 无权限访问 → authentication 不可重试",
			httpStatus: http.StatusForbidden,
			errorCode:  1005,
			wantClass:  generation.FailureAuthentication,
			wantRetry:  false,
		},
		{
			name:       "1007 限流 → rate_limited 可重试",
			httpStatus: http.StatusTooManyRequests,
			errorCode:  1007,
			wantClass:  generation.FailureRateLimited,
			wantRetry:  true,
		},
		{
			name:       "2000 生成额度超限 → rate_limited 可重试",
			httpStatus: http.StatusTooManyRequests,
			errorCode:  2000,
			wantClass:  generation.FailureRateLimited,
			wantRetry:  true,
		},
		{
			name:       "2001 任务不存在 → rejected 不可重试",
			httpStatus: http.StatusNotFound,
			errorCode:  2001,
			wantClass:  generation.FailureRejected,
			wantRetry:  false,
		},
		{
			name:       "2008 内容违规 → rejected 不可重试",
			httpStatus: http.StatusBadRequest,
			errorCode:  2008,
			wantClass:  generation.FailureRejected,
			wantRetry:  false,
		},
		{
			name:       "2010 余额不足 → insufficient_balance 不可重试（原被误判为 authentication，后来只是 rejected）",
			httpStatus: http.StatusForbidden,
			errorCode:  2010,
			wantClass:  generation.FailureInsufficientBalance,
			wantRetry:  false,
		},
		{
			name:       "2015 版本弃用 → invalid_input 不可重试",
			httpStatus: http.StatusBadRequest,
			errorCode:  2015,
			wantClass:  generation.FailureInvalidInput,
			wantRetry:  false,
		},
		{
			name:       "2019 文件不存在 → rejected 不可重试",
			httpStatus: http.StatusNotFound,
			errorCode:  2019,
			wantClass:  generation.FailureRejected,
			wantRetry:  false,
		},
		// 以下为按官方文档补全的遗漏错误码
		{
			name:       "2002 任务类型不支持 → invalid_input",
			httpStatus: http.StatusBadRequest,
			errorCode:  2002,
			wantClass:  generation.FailureInvalidInput,
			wantRetry:  false,
		},
		{
			name:       "2004 图片类型不支持 → invalid_input",
			httpStatus: http.StatusBadRequest,
			errorCode:  2004,
			wantClass:  generation.FailureInvalidInput,
			wantRetry:  false,
		},
		{
			name:       "2005 draft 任务非成功态 → rejected",
			httpStatus: http.StatusBadRequest,
			errorCode:  2005,
			wantClass:  generation.FailureRejected,
			wantRetry:  false,
		},
		{
			name:       "2006 原任务类型不支持 → rejected",
			httpStatus: http.StatusBadRequest,
			errorCode:  2006,
			wantClass:  generation.FailureRejected,
			wantRetry:  false,
		},
		{
			name:       "2007 原任务非成功态 → rejected",
			httpStatus: http.StatusBadRequest,
			errorCode:  2007,
			wantClass:  generation.FailureRejected,
			wantRetry:  false,
		},
		{
			name:       "2018 模型复杂度过高 → rejected",
			httpStatus: http.StatusBadRequest,
			errorCode:  2018,
			wantClass:  generation.FailureRejected,
			wantRetry:  false,
		},
		{
			name:       "2014 审核出错 → rejected，重试不会自愈（官方建议联系我们）",
			httpStatus: http.StatusBadRequest,
			errorCode:  2014,
			wantClass:  generation.FailureRejected,
			wantRetry:  false,
		},
		{
			name:       "2013 优先级无效(403) → invalid_input，不得误判为 authentication",
			httpStatus: http.StatusForbidden,
			errorCode:  2013,
			wantClass:  generation.FailureInvalidInput,
			wantRetry:  false,
		},
		// 以下为补齐全部 27 个官方错误码的测试锚定（含唯一 retryable 服务端分支）
		{
			name:       "1000 创建结果未知 → submission_unknown 禁止重发",
			httpStatus: http.StatusInternalServerError,
			errorCode:  1000,
			wantClass:  generation.FailureSubmissionUnknown,
			wantRetry:  false,
		},
		{
			name:       "1001 创建结果未知 → submission_unknown 禁止重发",
			httpStatus: http.StatusInternalServerError,
			errorCode:  1001,
			wantClass:  generation.FailureSubmissionUnknown,
			wantRetry:  false,
		},
		{
			name:       "1003 请求体格式错误 → invalid_input",
			httpStatus: http.StatusBadRequest,
			errorCode:  1003,
			wantClass:  generation.FailureInvalidInput,
			wantRetry:  false,
		},
		{
			name:       "2003 图片文件为空 → invalid_input",
			httpStatus: http.StatusBadRequest,
			errorCode:  2003,
			wantClass:  generation.FailureInvalidInput,
			wantRetry:  false,
		},
		{
			name:       "2009 提示词含非法字符 → invalid_input",
			httpStatus: http.StatusBadRequest,
			errorCode:  2009,
			wantClass:  generation.FailureInvalidInput,
			wantRetry:  false,
		},
		{
			name:       "2011 预绑定任务缺少模型 → rejected",
			httpStatus: http.StatusBadRequest,
			errorCode:  2011,
			wantClass:  generation.FailureRejected,
			wantRetry:  false,
		},
		{
			name:       "2012 输入任务类型无效 → rejected",
			httpStatus: http.StatusBadRequest,
			errorCode:  2012,
			wantClass:  generation.FailureRejected,
			wantRetry:  false,
		},
		{
			name:       "2016 请求类型已弃用 → invalid_input",
			httpStatus: http.StatusBadRequest,
			errorCode:  2016,
			wantClass:  generation.FailureInvalidInput,
			wantRetry:  false,
		},
		{
			name:       "2017 版本值无效 → invalid_input",
			httpStatus: http.StatusBadRequest,
			errorCode:  2017,
			wantClass:  generation.FailureInvalidInput,
			wantRetry:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(
				w http.ResponseWriter,
				_ *http.Request,
			) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.httpStatus)
				_, _ = w.Write([]byte(`{"code":` + strconv.Itoa(tc.errorCode) + `,"message":"test error","suggestion":"fix it"}`))
			}))
			defer upstream.Close()

			errorMapping, statusMapping := declaredTripoMappings(t)
			adapter, err := tripo.New(tripo.Config{
				BaseURL:       upstream.URL,
				APIKey:        "tripo-key",
				HTTPClient:    upstream.Client(),
				ErrorMapping:  errorMapping,
				StatusMapping: statusMapping,
			})
			if err != nil {
				t.Fatal(err)
			}

			_, err = adapter.Start(context.Background(), generation.StartRequest{
				Offering: generation.Offering{
					Capability:      generation.TextTo3D,
					ProviderOptions: []byte(`{"model":"v-test","endpoint":"generation/text-to-model","observe_endpoint":"tasks/{task_id}","upload_endpoint":"files"}`),
				},
				Input: []byte(`{"prompt":"ship"}`),
			})

			var providerErr *generation.ProviderError
			if !errors.As(err, &providerErr) || providerErr.Failure == nil {
				t.Fatalf("期望 ProviderError，实际 %v", err)
			}
			if providerErr.Failure.Class != tc.wantClass {
				t.Errorf("class = %q, want %q", providerErr.Failure.Class, tc.wantClass)
			}
			if providerErr.Failure.Retryable != tc.wantRetry {
				t.Errorf("retryable = %v, want %v", providerErr.Failure.Retryable, tc.wantRetry)
			}
		})
	}
}

func TestUpstreamErrorMessageNeverEntersSafeDetail(t *testing.T) {
	const secret = "https://cdn.tripo3d.ai/model.glb?token=private-token"
	for _, tc := range []struct{ status, code int }{
		{http.StatusBadRequest, 1004},
		{http.StatusOK, 1004},
		{http.StatusInternalServerError, 9999},
		{http.StatusBadGateway, 0},
	} {
		t.Run(strconv.Itoa(tc.status)+"-"+strconv.Itoa(tc.code), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"code":` + strconv.Itoa(tc.code) + `,"message":"` + secret + `"}`))
			}))
			defer upstream.Close()
			errorMapping, statusMapping := declaredTripoMappings(t)
			adapter, err := tripo.New(tripo.Config{
				BaseURL: upstream.URL, APIKey: "tripo-key", HTTPClient: upstream.Client(),
				ErrorMapping: errorMapping, StatusMapping: statusMapping,
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = adapter.Start(context.Background(), generation.StartRequest{
				Offering: generation.Offering{Capability: generation.TextTo3D,
					ProviderOptions: []byte(`{"model":"v-test","endpoint":"generation/text-to-model"}`)},
				Input: []byte(`{"prompt":"ship"}`),
			})
			var providerErr *generation.ProviderError
			if !errors.As(err, &providerErr) || providerErr.Failure == nil {
				t.Fatalf("expected ProviderError, got %v", err)
			}
			if strings.Contains(providerErr.Failure.SafeDetail, secret) ||
				providerErr.Failure.SafeDetail == "" {
				t.Fatalf("unsafe detail = %q", providerErr.Failure.SafeDetail)
			}
		})
	}
}

// TestRateLimitRetriesAndRetryAfter 验证限流码(1007/2000)可重试且遵守 Retry-After。
// 官方文档对 2000 明确提示 "You can also check Retry-After header"。
func TestRateLimitRetriesAndRetryAfter(t *testing.T) {
	for _, code := range []int{1007, 2000} {
		t.Run(strconv.Itoa(code)+" 带 Retry-After", func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "30")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"code":` + strconv.Itoa(code) + `,"message":"rate limited"}`))
			}))
			defer upstream.Close()

			errorMapping, statusMapping := declaredTripoMappings(t)
			adapter, err := tripo.New(tripo.Config{
				BaseURL:       upstream.URL,
				APIKey:        "tripo-key",
				HTTPClient:    upstream.Client(),
				ErrorMapping:  errorMapping,
				StatusMapping: statusMapping,
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = adapter.Start(context.Background(), generation.StartRequest{
				Offering: generation.Offering{
					Capability:      generation.TextTo3D,
					ProviderOptions: []byte(`{"model":"v-test","endpoint":"generation/text-to-model","observe_endpoint":"tasks/{task_id}","upload_endpoint":"files"}`),
				},
				Input: []byte(`{"prompt":"ship"}`),
			})
			var providerErr *generation.ProviderError
			if !errors.As(err, &providerErr) || providerErr.Failure == nil {
				t.Fatalf("期望 ProviderError，实际 %v", err)
			}
			if providerErr.Failure.Class != generation.FailureRateLimited {
				t.Errorf("class = %q, want rate_limited", providerErr.Failure.Class)
			}
			if !providerErr.Failure.Retryable {
				t.Errorf("限流应可重试")
			}
			if providerErr.Failure.RetryAfter != 30*time.Second {
				t.Errorf("RetryAfter = %v, want 30s", providerErr.Failure.RetryAfter)
			}
		})
	}
}
