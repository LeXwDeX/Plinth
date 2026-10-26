package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// TestAdministrationRefusesRemovingLastAdministrator：HTTP 层必须把"最后一个
// 管理员"报成 409 而不是 400——请求字段本身合法，只是不变量不允许，前端据此
// 才能提示"先建另一个管理员"。降权与停用两条路径都要拦住。
func TestAdministrationRefusesRemovingLastAdministrator(t *testing.T) {
	t.Parallel()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	routes := application.Runtime.HTTP.LegacyRoutes
	users := application.Administration.Users
	_, handler, _ := newHTTPHarnessWithRoles(t, []string{"admin"})

	// 造出唯一的管理员：动它就会清空可用管理员。
	created := administrationRequest(
		handler, http.MethodPost, routes.AdminUsers,
		`{"username":"only-admin","password":"secret12",`+
			`"email":"only-admin@example.test","isAdmin":true}`,
		nil,
	)
	if created.Code != http.StatusCreated {
		t.Fatalf("建管理员 status=%d body=%s", created.Code, created.Body)
	}
	createdBody := decodeAdministrationBody(t, created)
	createdUser, _ := createdBody["data"].(map[string]any)
	lastAdminID, _ := createdUser[users.API.Fields.ID].(string)
	if lastAdminID == "" {
		t.Fatalf("建管理员未返回 id：%#v", createdBody)
	}
	userPath := strings.Replace(routes.AdminUser, "{userID}", lastAdminID, 1)

	for _, tc := range []struct {
		name   string
		method string
		body   string
	}{
		{name: "demote", method: http.MethodPatch, body: `{"isAdmin":false}`},
		{name: "deactivate", method: http.MethodPatch, body: `{"isActive":false}`},
		{name: "delete", method: http.MethodDelete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := administrationRequest(
				handler, tc.method, userPath, tc.body, nil,
			)
			assertAdministrationError(
				t, response, http.StatusConflict,
				users.API.Fields.Error,
				users.Messages.LastAdministrator,
			)
		})
	}
}

// passwordDirectory 在既有 authDirectory 上补自助改密所需能力。
type passwordDirectory struct {
	*authDirectory
	passwords map[string]string
}

func (d *passwordDirectory) FindByID(
	ctx context.Context,
	id string,
) (authentication.UserRecord, bool, error) {
	return d.authDirectory.FindByID(ctx, id)
}

func (d *passwordDirectory) UpdatePassword(
	_ context.Context,
	id string,
	hash string,
	_ time.Time,
) error {
	d.passwords[id] = hash
	// 必须同步到用户名索引，否则登录仍会读到旧哈希，改密"看起来没生效"。
	for username, record := range d.byUsername {
		if record.ID == id {
			record.PasswordHash = hash
			d.byUsername[username] = record
		}
	}
	for key, record := range d.byID {
		if record.ID == id {
			record.PasswordHash = hash
			d.byID[key] = record
		}
	}
	return nil
}

// TestChangeOwnPasswordEnforcesCurrentPassword：自助改密必须先验当前密码。
// 当前密码错误与弱密码都是 401/400 的明确拒绝；成功则换掉口令、清掉当前
// 会话，并写审计。
func TestChangeOwnPasswordEnforcesCurrentPassword(t *testing.T) {
	t.Parallel()
	record := authentication.UserRecord{
		ID: "password-user", Username: "password-user",
		// "correct-horse" 的 bcrypt 结果。
		PasswordHash: "$2b$04$UVs2NmHqVLnDFtkcPXeOhuCLZNX27fLFYc/TbHf7w5wkWsWZXCP2.",
		Nickname:     "改密用户", Email: "password@example.test",
		Admin: false, Active: true,
	}
	directory := &passwordDirectory{
		authDirectory: &authDirectory{
			byUsername: map[string]authentication.UserRecord{
				record.Username: record,
			},
			byID: map[string]authentication.UserRecord{record.ID: record},
		},
		passwords: map[string]string{},
	}
	application, handler := newPasswordHarness(t, directory)

	route := application.Runtime.HTTP.LegacyRoutes.AuthPassword
	messages := application.Authentication.Messages

	// 匿名不能改密。
	anonymous := passwordRequest(handler, route,
		`{"currentPassword":"correct-horse","newPassword":"bbbbbbbb"}`, nil)
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("匿名改密 status=%d body=%s", anonymous.Code, anonymous.Body)
	}

	cookie := loginForPasswordTest(t, handler, "password-user", "correct-horse")

	wrong := passwordRequest(handler, route,
		`{"currentPassword":"wrong","newPassword":"bbbbbbbb"}`, cookie)
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("当前密码错误 status=%d body=%s", wrong.Code, wrong.Body)
	}
	if !strings.Contains(wrong.Body.String(), messages.InvalidCurrentPassword) {
		t.Fatalf("未复用当前密码文案：%s", wrong.Body)
	}
	if len(directory.passwords) != 0 {
		t.Fatal("当前密码错误时仍改了口令")
	}

	weak := passwordRequest(handler, route,
		`{"currentPassword":"correct-horse","newPassword":"abc"}`, cookie)
	if weak.Code != http.StatusBadRequest {
		t.Fatalf("弱密码 status=%d body=%s", weak.Code, weak.Body)
	}
	if len(directory.passwords) != 0 {
		t.Fatal("弱密码时仍改了口令")
	}

	missing := passwordRequest(handler, route,
		`{"currentPassword":"correct-horse"}`, cookie)
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("缺字段 status=%d body=%s", missing.Code, missing.Body)
	}

	changed := passwordRequest(handler, route,
		`{"currentPassword":"correct-horse","newPassword":"bbbbbbbb"}`, cookie)
	if changed.Code != http.StatusOK {
		t.Fatalf("改密 status=%d body=%s", changed.Code, changed.Body)
	}
	if len(directory.passwords) != 1 {
		t.Fatalf("改密未落库：%#v", directory.passwords)
	}
	// 改密后当前会话必须失效——否则被盗会话会比密码活得久。
	// 会话端点对已失效会话返回 200 + user:null，因此按 user 判定。
	if user := currentSessionUser(t, handler, application, cookie); user != nil {
		t.Fatalf("改密后会话仍然有效：user=%#v", user)
	}
	// 旧口令失效、新口令可用——这是改密真正要达到的效果。
	if code := loginStatus(t, handler, "password-user", "correct-horse"); code == http.StatusOK {
		t.Fatal("改密后旧口令仍可登录")
	}
	if code := loginStatus(t, handler, "password-user", "bbbbbbbb"); code != http.StatusOK {
		t.Fatalf("新口令无法登录：status=%d", code)
	}
}

// TestChangeOwnPasswordEndsEveryUserSession：改密要吊销该用户的全部会话，
// 不只是当前这一个。
func TestChangeOwnPasswordEndsEveryUserSession(t *testing.T) {
	t.Parallel()
	record := authentication.UserRecord{
		ID: "password-user", Username: "password-user",
		PasswordHash: "$2b$04$UVs2NmHqVLnDFtkcPXeOhuCLZNX27fLFYc/TbHf7w5wkWsWZXCP2.",
		Nickname:     "改密用户", Email: "password@example.test",
		Admin: false, Active: true,
	}
	directory := &passwordDirectory{
		authDirectory: &authDirectory{
			byUsername: map[string]authentication.UserRecord{
				record.Username: record,
			},
			byID: map[string]authentication.UserRecord{record.ID: record},
		},
		passwords: map[string]string{},
	}
	application, handler := newPasswordHarness(t, directory)
	route := application.Runtime.HTTP.LegacyRoutes.AuthPassword

	first := loginForPasswordTest(t, handler, "password-user", "correct-horse")
	second := loginForPasswordTest(t, handler, "password-user", "correct-horse")
	// 改密前两个会话都有效。
	for i, cookie := range []*http.Cookie{first, second} {
		probe := httptest.NewRequest(
			http.MethodGet,
			application.Runtime.HTTP.LegacyRoutes.AuthSession,
			nil,
		)
		probe.AddCookie(cookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, probe)
		if response.Code != http.StatusOK {
			t.Fatalf("改密前第 %d 个会话已失效：%d", i, response.Code)
		}
	}
	changed := passwordRequest(handler, route,
		`{"currentPassword":"correct-horse","newPassword":"bbbbbbbb"}`, first)
	if changed.Code != http.StatusOK {
		t.Fatalf("改密 status=%d body=%s", changed.Code, changed.Body)
	}
	if user := currentSessionUser(
		t, handler, application, second,
	); user != nil {
		t.Fatalf("另一个会话在改密后仍然有效：user=%#v", user)
	}
}

func newPasswordHarness(
	t *testing.T,
	directory *passwordDirectory,
) (catalog.ApplicationSchema, http.Handler) {
	t.Helper()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	_, handler, _ := newHTTPHarnessWithAuthentication(
		t,
		[]string{"user"},
		nil,
		func(config *authentication.Config) {
			config.Directory = directory
			config.SessionSecret = strings.Repeat("p", 32)
			config.Clock = func() time.Time {
				return time.Date(2026, 7, 26, 18, 0, 0, 0, time.UTC)
			}
		},
	)
	return application, handler
}

func loginForPasswordTest(
	t *testing.T,
	handler http.Handler,
	username string,
	password string,
) *http.Cookie {
	t.Helper()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/login",
		bytes.NewBufferString(
			`{"username":"`+username+`","password":"`+password+`"}`,
		),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Forwarded-For", "203.0.113.31")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("登录 status=%d body=%s", response.Code, response.Body)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("登录未下发会话：%#v", cookies)
	}
	return cookies[0]
}

func passwordRequest(
	handler http.Handler,
	route string,
	body string,
	cookie *http.Cookie,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(
		http.MethodPost, route, bytes.NewBufferString(body),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Forwarded-For", "203.0.113.31")
	if cookie != nil {
		request.AddCookie(cookie)
		request.Header.Set("Origin", "http://example.com")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// currentSessionUser 返回会话端点解析出的用户；会话已失效时返回 nil。
func currentSessionUser(
	t *testing.T,
	handler http.Handler,
	application catalog.ApplicationSchema,
	cookie *http.Cookie,
) any {
	t.Helper()
	probe := httptest.NewRequest(
		http.MethodGet,
		application.Runtime.HTTP.LegacyRoutes.AuthSession,
		nil,
	)
	probe.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, probe)
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("会话响应不可解析：%s", response.Body)
	}
	return body[application.Authentication.API.UserField]
}

func loginStatus(
	t *testing.T,
	handler http.Handler,
	username string,
	password string,
) int {
	t.Helper()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/login",
		bytes.NewBufferString(
			`{"username":"`+username+`","password":"`+password+`"}`,
		),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Forwarded-For", "203.0.113.32")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response.Code
}
