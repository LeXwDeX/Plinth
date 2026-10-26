package httpjson_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/providers/httpjson"
)

// W4 §4.3：非幂等提交必须关闭传输层自动重放。Go http.Client 对带
// GetBody 的请求在 307/308 重定向时会自动重发请求体——submitting=true
// 的请求清除 GetBody 后，重定向不得再次投递付费提交（上游计数恒为 1）。
func TestNonIdempotentSubmitDoesNotReplayBodyOnRedirect(t *testing.T) {
	var submits atomic.Int32
	var redirects atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/submit":
			submits.Add(1)
			http.Redirect(w, r, "/submit-target", http.StatusTemporaryRedirect)
		case "/submit-target":
			redirects.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	client, err := httpjson.New(upstream.URL, "test-key", upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	err = client.DoJSON(
		context.Background(),
		http.MethodPost,
		"submit",
		map[string]any{"prompt": "paid submission"},
		"submission-key",
		&response,
		true,
	)
	// 307 被当作非 2xx 分类或不可重放错误都合法；唯一硬性断言是付费
	// 端点不被自动重放（计数=1，目标端点计数=0）。
	_ = err
	if count := submits.Load(); count != 1 {
		t.Fatalf("submit endpoint hits = %d; want 1（非幂等提交禁止自动重放）", count)
	}
	if count := redirects.Load(); count != 0 {
		t.Fatalf("redirect target hits = %d; want 0（请求体不得被重定向重发）", count)
	}
}
