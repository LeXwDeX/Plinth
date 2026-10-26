package httpapi_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestAdministrationUsersRequireConfiguredPermission(t *testing.T) {
	t.Parallel()
	_, regular, _ := newHTTPHarnessWithRoles(t, []string{"user"})
	forbidden := administrationRequest(
		regular,
		http.MethodGet,
		"/api/admin/users",
		"",
		nil,
	)
	assertAdministrationError(
		t,
		forbidden,
		http.StatusForbidden,
		"error",
		"无权执行此操作",
	)

	_, unauthenticated, _ := newHTTPHarnessWithAuthentication(
		t,
		nil,
		nil,
		func(config *authentication.Config) {
			config.Fallback = deniedFallbackIdentity{}
		},
	)
	denied := administrationRequest(
		unauthenticated,
		http.MethodGet,
		"/api/admin/users",
		"",
		nil,
	)
	assertAdministrationError(
		t,
		denied,
		http.StatusUnauthorized,
		"error",
		"未登录或登录已过期",
	)
}

func TestAdministrationUserCRUDAndSessionRevocationMatchProductContract(
	t *testing.T,
) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithRoles(t, []string{"admin"})

	invalid := administrationRequest(
		handler,
		http.MethodPost,
		"/api/admin/users",
		`{"username":"a","password":"secret12","email":"a@example.test"}`,
		nil,
	)
	assertAdministrationError(
		t,
		invalid,
		http.StatusBadRequest,
		"error",
		"用户名至少需要 2 个字符",
	)

	created := administrationRequest(
		handler,
		http.MethodPost,
		"/api/admin/users",
		`{
			"username":"schema-admin",
			"password":"correct-horse",
			"name":"Schema Admin",
			"email":"schema@example.test",
			"isAdmin":true
		}`,
		nil,
	)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body)
	}
	createdBody := decodeAdministrationBody(t, created)
	user, ok := createdBody["data"].(map[string]any)
	if !ok {
		t.Fatalf("create body=%#v", createdBody)
	}
	userID, _ := user["id"].(string)
	if userID == "" ||
		user["username"] != "schema-admin" ||
		user["name"] != "Schema Admin" ||
		user["email"] != "schema@example.test" ||
		user["isAdmin"] != true ||
		user["isActive"] != true ||
		user["createdAt"] == "" ||
		user["lastLogin"] != nil ||
		user["openId"] != nil {
		t.Fatalf("created user=%#v", user)
	}

	duplicate := administrationRequest(
		handler,
		http.MethodPost,
		"/api/admin/users",
		`{"username":"schema-admin","password":"different","email":"schema-admin@example.test"}`,
		nil,
	)
	assertAdministrationError(
		t,
		duplicate,
		http.StatusConflict,
		"error",
		"用户名已存在",
	)

	listed := administrationRequest(
		handler,
		http.MethodGet,
		"/api/admin/users",
		"",
		nil,
	)
	if listed.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body)
	}
	listedBody := decodeAdministrationBody(t, listed)
	users, ok := listedBody["data"].([]any)
	if !ok || len(users) != 1 {
		t.Fatalf("list body=%#v", listedBody)
	}

	login := administrationRequest(
		handler,
		http.MethodPost,
		"/api/auth/login",
		`{"username":"schema-admin","password":"correct-horse"}`,
		nil,
	)
	if login.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body)
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login cookies=%#v", cookies)
	}
	sessionCookie := cookies[0]

	selfDeactivate := administrationRequest(
		handler,
		http.MethodPatch,
		"/api/admin/users/"+userID,
		`{"isActive":false}`,
		sessionCookie,
	)
	assertAdministrationError(
		t,
		selfDeactivate,
		http.StatusBadRequest,
		"error",
		"不能停用自己的账号",
	)
	selfDelete := administrationRequest(
		handler,
		http.MethodDelete,
		"/api/admin/users/"+userID,
		"",
		sessionCookie,
	)
	assertAdministrationError(
		t,
		selfDelete,
		http.StatusBadRequest,
		"error",
		"不能删除自己的账号",
	)

	// 停用会减少可用管理员数量，先补一个冗余管理员，否则该不变量会正确
	// 地把这次停用拒掉（见 TestAdministrationRefusesRemovingLastAdministrator）。
	spare := administrationRequest(
		handler,
		http.MethodPost,
		"/api/admin/users",
		`{"username":"spare-admin","password":"secret12",`+
			`"email":"spare@example.test","isAdmin":true}`,
		nil,
	)
	if spare.Code != http.StatusCreated {
		t.Fatalf("建冗余管理员 status=%d body=%s", spare.Code, spare.Body)
	}

	updated := administrationRequest(
		handler,
		http.MethodPatch,
		"/api/admin/users/"+userID,
		`{
			"name":"Updated Admin",
			"email":"updated@example.test",
			"isActive":false,
			"password":"new-correct-horse"
		}`,
		nil,
	)
	if updated.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", updated.Code, updated.Body)
	}
	updatedUser := decodeAdministrationBody(t, updated)["data"].(map[string]any)
	if updatedUser["name"] != "Updated Admin" ||
		updatedUser["nickname"] != "Updated Admin" ||
		updatedUser["email"] != "updated@example.test" ||
		updatedUser["isActive"] != false {
		t.Fatalf("updated user=%#v", updatedUser)
	}

	revoked := administrationRequest(
		handler,
		http.MethodGet,
		"/api/admin/users",
		"",
		sessionCookie,
	)
	assertAdministrationError(
		t,
		revoked,
		http.StatusUnauthorized,
		"error",
		"未登录或登录已过期",
	)

	deleted := administrationRequest(
		handler,
		http.MethodDelete,
		"/api/admin/users/"+userID,
		"",
		nil,
	)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleted.Code, deleted.Body)
	}
	if body := decodeAdministrationBody(t, deleted); body["success"] != true {
		t.Fatalf("delete body=%#v", body)
	}
	missing := administrationRequest(
		handler,
		http.MethodDelete,
		"/api/admin/users/"+userID,
		"",
		nil,
	)
	assertAdministrationError(
		t,
		missing,
		http.StatusNotFound,
		"error",
		"用户不存在",
	)
}

func TestAdministrationUserWireAndTextChangeWithProductSchema(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithApplication(
		t,
		[]string{"admin"},
		func(application *catalog.ApplicationSchema) {
			application.Runtime.HTTP.LegacyRoutes.AdminUsers = "/custom/operators"
			application.Runtime.HTTP.LegacyRoutes.AdminUser =
				"/custom/operators/{account}"
			users := &application.Administration.Users
			users.API.PathParameters.UserID = "account"
			fields := &users.API.Fields
			fields.Data = "payload"
			fields.Success = "done"
			fields.Error = "problem"
			fields.ID = "account_id"
			fields.Username = "login"
			fields.Password = "credential"
			fields.Nickname = "display_alias"
			fields.Name = "display_name"
			fields.Email = "mailbox"
			fields.IsAdmin = "privileged"
			fields.IsActive = "enabled"
			fields.CreatedAt = "registered"
			fields.LastLogin = "seen"
			fields.OpenID = "external_id"
			users.Messages.InvalidUsername = "custom username {minimum}"
			users.Messages.SelfDelete = "custom self delete"
			users.Audit.DetailFields.Username = "audit_login"
			users.Audit.DetailFields.IsAdmin = "audit_privileged"
			users.Audit.DetailFields.IsActive = "audit_enabled"
		},
	)

	invalid := administrationRequest(
		handler,
		http.MethodPost,
		"/custom/operators",
		`{"login":"a","credential":"secret12"}`,
		nil,
	)
	assertAdministrationError(
		t,
		invalid,
		http.StatusBadRequest,
		"problem",
		"custom username 2",
	)
	created := administrationRequest(
		handler,
		http.MethodPost,
		"/custom/operators",
		`{
			"login":"operator",
			"credential":"secret12",
			"display_name":"Configured Operator",
			"mailbox":"operator@example.test",
			"privileged":false
		}`,
		nil,
	)
	if created.Code != http.StatusCreated {
		t.Fatalf("custom create status=%d body=%s", created.Code, created.Body)
	}
	body := decodeAdministrationBody(t, created)
	payload, ok := body["payload"].(map[string]any)
	if !ok ||
		payload["login"] != "operator" ||
		payload["display_name"] != "Configured Operator" ||
		payload["enabled"] != true {
		t.Fatalf("custom create body=%#v", body)
	}
	if _, leaked := body["data"]; leaked {
		t.Fatalf("default data field leaked: %#v", body)
	}
	id, _ := payload["account_id"].(string)
	deleted := administrationRequest(
		handler,
		http.MethodDelete,
		"/custom/operators/"+id,
		"",
		nil,
	)
	if deleted.Code != http.StatusOK {
		t.Fatalf("custom delete status=%d body=%s", deleted.Code, deleted.Body)
	}
	if body := decodeAdministrationBody(t, deleted); body["done"] != true {
		t.Fatalf("custom delete body=%#v", body)
	}
}

type deniedFallbackIdentity struct{}

func (deniedFallbackIdentity) Identify(
	*http.Request,
) (access.Principal, error) {
	return access.Principal{}, errors.New("denied")
}

func administrationRequest(
	handler http.Handler,
	method string,
	path string,
	body string,
	cookie *http.Cookie,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("x-forwarded-for", "198.51.100.4")
	request.Header.Set("user-agent", "administration-test")
	if cookie != nil {
		request.AddCookie(cookie)
		request.Header.Set("Origin", "http://example.com")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeAdministrationBody(
	t *testing.T,
	response *httptest.ResponseRecorder,
) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode body %q: %v", response.Body.String(), err)
	}
	return body
}

func assertAdministrationError(
	t *testing.T,
	response *httptest.ResponseRecorder,
	status int,
	field string,
	message string,
) {
	t.Helper()
	if response.Code != status {
		t.Fatalf(
			"status=%d want=%d body=%s",
			response.Code,
			status,
			response.Body,
		)
	}
	body := decodeAdministrationBody(t, response)
	if body[field] != message {
		t.Fatalf("error body=%#v", body)
	}
	for key := range body {
		if strings.HasPrefix(key, "wire_") {
			t.Fatalf("unexpected wire field in error body: %#v", body)
		}
	}
}
