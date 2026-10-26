package httpapi_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// 这些测试锁定公开边界：签名资产只能按有效令牌交付，后台聚合路由先认证
// 再鉴权，任务/对话/画布资源按 owner 隔离。它们验证现有分层契约，不改变
// 产品行为，也不访问外部上游。

func boundaryRequest(
	t *testing.T,
	handler http.Handler,
	method, target, owner, contentType, body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	if owner != "" {
		request.Header.Set("X-User-ID", owner)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func boundaryDeniedHandler(t *testing.T) http.Handler {
	t.Helper()
	_, handler, _ := newHTTPHarnessWithAuthentication(
		t,
		nil,
		nil,
		func(config *authentication.Config) {
			config.Fallback = deniedFallbackIdentity{}
		},
	)
	return handler
}

func boundarySignedToken(t *testing.T, assetID, owner string, expires time.Time) string {
	t.Helper()
	payload, err := json.Marshal(struct {
		AssetID generation.AssetID `json:"asset_id"`
		OwnerID generation.OwnerID `json:"owner_id"`
		Expires int64              `json:"expires"`
	}{generation.AssetID(assetID), generation.OwnerID(owner), expires.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(strings.Repeat("k", 32)))
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func TestProviderAssetDeliveryRequiresValidSignature(t *testing.T) {
	t.Parallel()
	_, handler, inputs := newHTTPHarness(t)
	server := httptest.NewServer(handler)
	defer server.Close()
	assetID := uploadInputAsset(t, server.URL, "owner-a")
	resolved, err := inputs.Resolve(
		context.Background(),
		"owner-a",
		generation.Offering{Parameters: []generation.Parameter{{
			Name: "source_asset",
			Type: "asset",
		}}},
		[]byte(`{"source_asset":"`+assetID+`"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	resolvedAssets := resolved["source_asset"]
	if len(resolvedAssets) != 1 {
		t.Fatalf("resolved assets = %#v", resolved)
	}
	asset := resolvedAssets[0]
	externalURL, err := inputs.ExternalURL(context.Background(), asset, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(externalURL)
	if err != nil {
		t.Fatal(err)
	}
	prefix := parsed.Path[:strings.LastIndex(parsed.Path, "/")+1]
	token := strings.TrimPrefix(parsed.Path, prefix)

	delivered := boundaryRequest(t, handler, http.MethodGet, parsed.Path, "", "", "")
	if delivered.Code != http.StatusOK {
		t.Fatalf("signed delivery status=%d body=%s", delivered.Code, delivered.Body)
	}
	if delivered.Header().Get("Content-Type") != asset.MediaType ||
		delivered.Header().Get("Content-Length") != strconv.FormatInt(asset.Size, 10) ||
		delivered.Header().Get("ETag") != `"`+asset.SHA256+`"` ||
		delivered.Header().Get("Cache-Control") != "private, no-store" ||
		delivered.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("signed delivery headers = %#v", delivered.Header())
	}
	if !bytes.Equal(delivered.Body.Bytes(), []byte("\x89PNG\r\n\x1a\nreference")) {
		t.Fatalf("signed delivery body = %q", delivered.Body.Bytes())
	}

	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 || len(parts[1]) == 0 {
		t.Fatalf("signed token = %q", token)
	}
	tamperedSignature := []byte(parts[1])
	if tamperedSignature[0] == 'A' {
		tamperedSignature[0] = 'B'
	} else {
		tamperedSignature[0] = 'A'
	}
	for _, tc := range []struct {
		name  string
		token string
	}{
		{"tampered signature", parts[0] + "." + string(tamperedSignature)},
		{"expired token", boundarySignedToken(t, assetID, "owner-a", time.Now().Add(-time.Minute))},
		{"unknown asset", boundarySignedToken(t, "missing-asset", "owner-a", time.Now().Add(time.Hour))},
		{"malformed token", "not-a-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := boundaryRequest(t, handler, http.MethodGet, prefix+tc.token, "", "", "")
			if response.Code != http.StatusNotFound {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
		})
	}

	// 正控：手工构造的令牌格式必须能被接受，否则上面的过期/未知资产
	// 负例可能只是因为签名格式漂移而始终 404。
	handSigned := boundaryRequest(t, handler, http.MethodGet,
		prefix+boundarySignedToken(t, assetID, "owner-a", time.Now().Add(time.Hour)),
		"", "", "")
	if handSigned.Code != http.StatusOK {
		t.Fatalf("hand-signed status=%d body=%s", handSigned.Code, handSigned.Body)
	}
}

func TestAdministrationAggregatesAuthenticateThenAuthorize(t *testing.T) {
	t.Parallel()
	routes := []struct {
		method string
		path   string
		// wantAllowed 固定管理员通过权限门禁后的业务状态：user-assets
		// 查询目录中不存在的 local-user 时按业务返回 404，其余为 200。
		wantAllowed int
	}{
		{http.MethodGet, "/api/admin/usage", http.StatusOK},
		{http.MethodGet, "/api/admin/balance-summary", http.StatusOK},
		{http.MethodGet, "/api/admin/user-assets/local-user", http.StatusNotFound},
		{http.MethodPost, "/api/admin/backfill-status", http.StatusOK},
	}
	_, regular, _ := newHTTPHarnessWithRoles(t, []string{"user"})
	_, admin, _ := newHTTPHarnessWithRoles(t, []string{"admin"})
	unauthenticated := boundaryDeniedHandler(t)
	for _, route := range routes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			denied := administrationRequest(regular, route.method, route.path, "", nil)
			if denied.Code != http.StatusForbidden {
				t.Fatalf("regular status=%d body=%s", denied.Code, denied.Body)
			}
			anonymous := administrationRequest(unauthenticated, route.method, route.path, "", nil)
			if anonymous.Code != http.StatusUnauthorized {
				t.Fatalf("anonymous status=%d body=%s", anonymous.Code, anonymous.Body)
			}
			// 管理员已通过权限门禁；具体资源是否存在由各自业务决定
			// （例如 user-assets 对目录中不存在的用户返回 404）。
			allowed := administrationRequest(admin, route.method, route.path, "", nil)
			if allowed.Code != route.wantAllowed {
				t.Fatalf("admin status=%d want=%d body=%s", allowed.Code, route.wantAllowed, allowed.Body)
			}
		})
	}
}

func TestTaskCancelEventsAndStreamAreOwnerScoped(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarness(t)
	accepted, err := submitHarnessTask(t, module, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.TextToImage,
		OfferingID: "openai/gpt-image-2/text-to-image",
		Input:      json.RawMessage(`{"prompt":"owner scoped task","size":"1024x1024","quality":"max"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := string(accepted.TaskID)

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"cancel", http.MethodPost, "/api/v1/tasks/" + taskID + "/cancel", ""},
		{"events", http.MethodGet, "/api/v1/tasks/" + taskID + "/events", ""},
		{"task stream", http.MethodGet, "/api/meshy/stream/text-to-image/" + taskID, ""},
	} {
		t.Run("foreign owner "+tc.name, func(t *testing.T) {
			response := boundaryRequest(t, handler, tc.method, tc.path, "owner-b", "application/json", tc.body)
			if response.Code != http.StatusNotFound {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
		})
	}

	// owner-a 仍可正常读取事件；取消后任务进入终态。
	events := boundaryRequest(t, handler, http.MethodGet, "/api/v1/tasks/"+taskID+"/events", "owner-a", "", "")
	if events.Code != http.StatusOK {
		t.Fatalf("owner events status=%d body=%s", events.Code, events.Body)
	}
	canceled := boundaryRequest(t, handler, http.MethodPost, "/api/v1/tasks/"+taskID+"/cancel", "owner-a", "application/json", "")
	if canceled.Code != http.StatusOK {
		t.Fatalf("owner cancel status=%d body=%s", canceled.Code, canceled.Body)
	}
	var canceledTask struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(canceled.Body.Bytes(), &canceledTask); err != nil || canceledTask.State != "CANCELED" {
		t.Fatalf("canceled task = %s err=%v", canceled.Body.Bytes(), err)
	}
	// 正控：所有者仍可订阅已取消任务的终态流，防止“所有 owner 都 404”
	// 的回归伪装成隔离生效。
	streamed := boundaryRequest(t, handler, http.MethodGet,
		"/api/meshy/stream/text-to-image/"+taskID, "owner-a", "", "")
	if streamed.Code != http.StatusOK ||
		streamed.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("owner stream status=%d headers=%v body=%s", streamed.Code, streamed.Header(), streamed.Body)
	}
}

func TestTaskAndChatStreamsRejectMissingIdentity(t *testing.T) {
	t.Parallel()
	handler := boundaryDeniedHandler(t)
	for _, path := range []string{
		"/api/meshy/stream/text-to-image/task-id",
		"/api/chat/sessions/session-id/stream",
	} {
		t.Run(path, func(t *testing.T) {
			response := boundaryRequest(t, handler, http.MethodGet, path, "", "", "")
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
		})
	}
}

func TestChatStreamIsOwnerScoped(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	created := boundaryRequest(t, handler, http.MethodPost, "/api/chat/sessions", "owner-a", "application/json", `{"title":"boundary"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create session status=%d body=%s", created.Code, created.Body)
	}
	var session struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil || session.ID == "" {
		t.Fatalf("session = %s err=%v", created.Body.Bytes(), err)
	}
	path := "/api/chat/sessions/" + session.ID + "/stream"
	foreign := boundaryRequest(t, handler, http.MethodGet, path, "owner-b", "", "")
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign stream status=%d body=%s", foreign.Code, foreign.Body)
	}
}

func TestCanvasProjectsAreOwnerScoped(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithRoles(t, []string{"admin"})
	created := boundaryRequest(t, handler, http.MethodPost, "/api/canvas/projects", "owner-a", "application/json", `{"name":"boundary board"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body)
	}
	var project struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &project); err != nil || project.ID == "" {
		t.Fatalf("project = %s err=%v", created.Body.Bytes(), err)
	}
	path := "/api/canvas/projects/" + project.ID

	ownerRead := boundaryRequest(t, handler, http.MethodGet, path, "owner-a", "", "")
	if ownerRead.Code != http.StatusOK {
		t.Fatalf("owner read status=%d body=%s", ownerRead.Code, ownerRead.Body)
	}
	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"get", http.MethodGet, path, ""},
		{"patch", http.MethodPatch, path, `{"name":"hijack"}`},
		{"nodes", http.MethodPut, path + "/nodes", `{"revision":1,"nodes":[]}`},
		{"delete", http.MethodDelete, path, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := boundaryRequest(t, handler, tc.method, tc.path, "owner-b", "application/json", tc.body)
			if response.Code != http.StatusNotFound {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
		})
	}
}
