package httpapi_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/etl"
	"github.com/LeXwDeX/Plinth/internal/etl/etltest"
)

// etlMigratedPasswordHash 复用 etltest 共享的旧库播种测试哈希。
const etlMigratedPasswordHash = etltest.PasswordHash

// TestETLMigratedUsersMatchAuthContract 把 ETL 迁移后的库直接交给
// 现有 HTTP 栈（切换票 02 验收）：原账号密码登录成功、禁用账号被拒、
// 旧栈同构签发的 Session Cookie 只在固定兼容截止之前通过。
func TestETLMigratedUsersMatchAuthContract(t *testing.T) {
	t.Parallel()
	fixture := etltest.NewFixture(t)
	fixture.Exec(t,
		`INSERT INTO users (
			id, username, passwordHash, nickname, email, isAdmin,
			isActive, createdAt, updatedAt
		 ) VALUES
		 ('legacy-admin-id', 'admin', ?, '管理员', 'admin@example.test',
		  1, 1, '2025-01-02T03:04:05.678+00:00',
		  '2025-01-02T03:04:05.678+00:00'),
		 ('legacy-disabled-id', 'disabled-user', ?, '', '',
		  0, 0, '2025-04-01T00:00:00+00:00', '2025-04-01T00:00:00+00:00')`,
		etlMigratedPasswordHash, etlMigratedPasswordHash,
	)
	sessionSecret := strings.Repeat("etl-session-secret-", 2)
	var now, legacyCutoff time.Time
	_, handler, _, _ := newHTTPHarnessWithServices(
		t,
		[]string{"user"},
		nil,
		func(config *authentication.Config) {
			config.SessionSecret = sessionSecret
			var err error
			legacyCutoff, err = time.Parse(time.RFC3339, config.Schema.Session.LegacyAcceptUntil)
			if err != nil {
				t.Fatal(err)
			}
			now = legacyCutoff.Add(-time.Hour)
			config.Clock = func() time.Time { return now }
		},
		nil,
		nil,
		func(t *testing.T, databasePath string) {
			t.Helper()
			var stdout, stderr bytes.Buffer
			if code := etl.Main([]string{
				"migrate",
				"--legacy-root", fixture.Root,
				"--target-db", databasePath,
			}, &stdout, &stderr); code != 0 {
				t.Fatalf(
					"ETL 迁移失败，退出码 %d:\n%s%s",
					code, stdout.String(), stderr.String(),
				)
			}
		},
	)

	assertJSONResponse(
		t,
		handler,
		http.MethodPost,
		"/api/auth/login",
		`{"username":"admin","password":"correct-horse"}`,
		http.StatusOK,
		map[string]any{
			"success": true,
			"user": map[string]any{
				"id":    "legacy-admin-id",
				"name":  "管理员",
				"email": "admin@example.test",
			},
		},
	)
	assertJSONResponse(
		t,
		handler,
		http.MethodPost,
		"/api/auth/login",
		`{"username":"disabled-user","password":"correct-horse"}`,
		http.StatusUnauthorized,
		map[string]any{"error": "用户名或密码错误"},
	)

	// 旧栈同构方式签发 Session Cookie：JSON payload + "." +
	// HMAC-SHA256 hex（见 src/lib/auth.ts signPayload），Next.js
	// cookies API 会对值做 URL 编码。
	payload := `{"id":"legacy-admin-id","name":"管理员",` +
		`"email":"admin@example.test"}`
	mac := hmac.New(sha256.New, []byte(sessionSecret))
	mac.Write([]byte(payload))
	signed := payload + "." + hex.EncodeToString(mac.Sum(nil))
	session := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
	request.AddCookie(&http.Cookie{
		Name:  "meshyai_session",
		Value: strings.ReplaceAll(url.QueryEscape(signed), "+", "%20"),
	})
	handler.ServeHTTP(session, request)
	if session.Code != http.StatusOK {
		t.Fatalf(
			"旧栈签发 Cookie 鉴权失败: status=%d body=%s",
			session.Code, session.Body,
		)
	}
	assertJSONBody(t, session.Body.Bytes(), map[string]any{
		"user": map[string]any{
			"id":    "legacy-admin-id",
			"name":  "管理员",
			"email": "admin@example.test",
		},
		"isAdmin": true,
	})

	// Compatibility expires at the declared instant, independent of the test
	// machine's clock. Reject the exact cookie that worked before the cutoff.
	now = legacyCutoff
	expired := httptest.NewRecorder()
	handler.ServeHTTP(expired, request)
	if expired.Code != http.StatusOK {
		t.Fatalf("expired legacy session status=%d body=%s", expired.Code, expired.Body)
	}
	assertJSONBody(t, expired.Body.Bytes(), map[string]any{"user": nil, "isAdmin": false})

	login := httptest.NewRecorder()
	loginRequest := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(
		`{"username":"admin","password":"correct-horse"}`,
	))
	loginRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(login, loginRequest)
	if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 {
		t.Fatalf("modern login after cutoff status=%d body=%s", login.Code, login.Body)
	}
	modern := httptest.NewRecorder()
	modernRequest := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
	modernRequest.AddCookie(login.Result().Cookies()[0])
	handler.ServeHTTP(modern, modernRequest)
	assertJSONBody(t, modern.Body.Bytes(), map[string]any{
		"user":    map[string]any{"id": "legacy-admin-id", "name": "管理员", "email": "admin@example.test"},
		"isAdmin": true,
	})
}
