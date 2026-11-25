package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
)

// #599: administrators set, change and clear a user's project group KEY through
// the administration user API. The wire field and the rejection text come from
// Product Schema; an ungrouped user carries null. The group is not a
// permission: changing it does not revoke the user's sessions, and the
// account's own session endpoint never shows it.
func TestAdministrationUserProjectGroupKeyWireContract599(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithRoles(t, []string{"admin"})

	invalid := administrationRequest(handler, http.MethodPost, "/api/admin/users",
		`{"username":"grouped","password":"secret12","email":"grouped@example.test","projectGroupKey":"Studio A"}`, nil)
	assertAdministrationError(t, invalid, http.StatusBadRequest, "error",
		"项目组 KEY 只能含小写字母、数字、- 和 _，须以字母或数字开头，最长 32 个字符")
	wrongType := administrationRequest(handler, http.MethodPost, "/api/admin/users",
		`{"username":"grouped","password":"secret12","email":"grouped@example.test","projectGroupKey":7}`, nil)
	assertAdministrationError(t, wrongType, http.StatusBadRequest, "error", "请求参数无效")

	created := administrationRequest(handler, http.MethodPost, "/api/admin/users",
		`{"username":"grouped","password":"secret12","email":"grouped@example.test","projectGroupKey":" studio-a "}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body)
	}
	user := decodeAdministrationBody(t, created)["data"].(map[string]any)
	if user["projectGroupKey"] != "studio-a" {
		t.Fatalf("created user=%#v", user)
	}
	userID := user["id"].(string)
	ungrouped := administrationRequest(handler, http.MethodPost, "/api/admin/users",
		`{"username":"loose","password":"secret12","email":"loose@example.test"}`, nil)
	if ungrouped.Code != http.StatusCreated {
		t.Fatalf("ungrouped create status=%d body=%s", ungrouped.Code, ungrouped.Body)
	}
	if body := decodeAdministrationBody(t, ungrouped)["data"].(map[string]any); body["projectGroupKey"] != nil {
		t.Fatalf("ungrouped user must carry null: %#v", body)
	} else if _, present := body["projectGroupKey"]; !present {
		t.Fatalf("ungrouped user must carry the field as null: %#v", body)
	}

	listGroups := func() map[string]any {
		t.Helper()
		listed := administrationRequest(handler, http.MethodGet, "/api/admin/users", "", nil)
		if listed.Code != http.StatusOK {
			t.Fatalf("list status=%d body=%s", listed.Code, listed.Body)
		}
		groups := map[string]any{}
		for _, item := range decodeAdministrationBody(t, listed)["data"].([]any) {
			row := item.(map[string]any)
			groups[row["username"].(string)] = row["projectGroupKey"]
		}
		return groups
	}
	if groups := listGroups(); groups["grouped"] != "studio-a" || groups["loose"] != nil {
		t.Fatalf("listed groups=%#v", groups)
	}

	login := administrationRequest(handler, http.MethodPost, "/api/auth/login",
		`{"username":"grouped","password":"secret12"}`, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body)
	}
	if strings.Contains(login.Body.String(), "projectGroupKey") || strings.Contains(login.Body.String(), "studio-a") {
		t.Fatalf("login response exposes the project group: %s", login.Body)
	}
	sessionCookie := login.Result().Cookies()[0]

	badUpdate := administrationRequest(handler, http.MethodPatch, "/api/admin/users/"+userID,
		`{"projectGroupKey":"-studio"}`, nil)
	assertAdministrationError(t, badUpdate, http.StatusBadRequest, "error",
		"项目组 KEY 只能含小写字母、数字、- 和 _，须以字母或数字开头，最长 32 个字符")
	changed := administrationRequest(handler, http.MethodPatch, "/api/admin/users/"+userID,
		`{"projectGroupKey":"studio-b"}`, nil)
	if changed.Code != http.StatusOK {
		t.Fatalf("change status=%d body=%s", changed.Code, changed.Body)
	}
	if body := decodeAdministrationBody(t, changed)["data"].(map[string]any); body["projectGroupKey"] != "studio-b" {
		t.Fatalf("changed user=%#v", body)
	}
	kept := administrationRequest(handler, http.MethodPatch, "/api/admin/users/"+userID,
		`{"name":"Grouped"}`, nil)
	if body := decodeAdministrationBody(t, kept)["data"].(map[string]any); kept.Code != http.StatusOK || body["projectGroupKey"] != "studio-b" {
		t.Fatalf("absent field must keep the group: %d %#v", kept.Code, body)
	}

	// Not a permission: the group change kept the session alive, and the
	// account's own session view does not carry the group.
	session := administrationRequest(handler, http.MethodGet, "/api/auth/session", "", sessionCookie)
	if session.Code != http.StatusOK {
		t.Fatalf("group change must not revoke sessions: %d %s", session.Code, session.Body)
	}
	if strings.Contains(session.Body.String(), "projectGroupKey") || strings.Contains(session.Body.String(), "studio-b") {
		t.Fatalf("account session exposes the project group: %s", session.Body)
	}

	for _, clear := range []string{`{"projectGroupKey":""}`, `{"projectGroupKey":null}`} {
		cleared := administrationRequest(handler, http.MethodPatch, "/api/admin/users/"+userID, clear, nil)
		if cleared.Code != http.StatusOK {
			t.Fatalf("clear %s status=%d body=%s", clear, cleared.Code, cleared.Body)
		}
		if body := decodeAdministrationBody(t, cleared)["data"].(map[string]any); body["projectGroupKey"] != nil {
			t.Fatalf("clear %s user=%#v", clear, body)
		}
		if groups := listGroups(); groups["grouped"] != nil {
			t.Fatalf("clear %s listed groups=%#v", clear, groups)
		}
		reset := administrationRequest(handler, http.MethodPatch, "/api/admin/users/"+userID,
			`{"projectGroupKey":"studio-b"}`, nil)
		if reset.Code != http.StatusOK {
			t.Fatalf("reset status=%d", reset.Code)
		}
	}
}

func TestAdministrationUserProjectGroupRequiresAdministrator599(t *testing.T) {
	t.Parallel()
	_, regular, _ := newHTTPHarnessWithRoles(t, []string{"user"})
	for _, request := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/admin/users", `{"username":"x1","password":"secret12","email":"x@example.test","projectGroupKey":"studio-a"}`},
		{http.MethodPatch, "/api/admin/users/someone", `{"projectGroupKey":"studio-a"}`},
	} {
		response := administrationRequest(regular, request.method, request.path, request.body, nil)
		assertAdministrationError(t, response, http.StatusForbidden, "error", "无权执行此操作")
	}
}
