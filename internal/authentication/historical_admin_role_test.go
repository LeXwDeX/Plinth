package authentication_test

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/authentication"
)

func TestHistoricalAdminRoleCannotGrantSessionPrivileges(t *testing.T) {
	for _, test := range []struct {
		name                     string
		admin, allowlisted, want bool
	}{
		{name: "historical role only"},
		{name: "directory flag", admin: true, want: true},
		{name: "email allowlist", allowlisted: true, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			schema := authenticationSchema(t)
			record := authentication.UserRecord{
				ID: "historical", Name: "Historical", Email: "historical@example.test",
				Roles: []string{"user", "admin", " admin "}, Active: true, Admin: test.admin,
			}
			directory := &memoryDirectory{byID: map[string]authentication.UserRecord{record.ID: record}}
			policy, err := access.NewPolicy(access.PolicyConfig{
				DefaultRole: schema.DefaultRole, AdminRole: schema.AdminRole,
				Roles: []access.RoleDefinition{
					{Key: "user"},
					{Key: "admin", Permissions: []access.Permission{"users.manage", "tasks.read.any"}},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			var allowlist []string
			if test.allowlisted {
				allowlist = []string{record.Email}
			}
			service := newService(t, authentication.Config{
				Schema: schema, SessionSecret: strings.Repeat("x", 32), Environment: "test",
				Policy: policy, Directory: directory, AdminAllowlist: allowlist,
			})
			writer := httptest.NewRecorder()
			if err := service.IssueSession(writer, httptest.NewRequest(http.MethodPost, "/login", nil),
				authentication.User{ID: record.ID, Name: record.Name, Email: record.Email, Roles: record.Roles}); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
			request.AddCookie(writer.Result().Cookies()[0])
			principal, err := service.Identify(request)
			if err != nil {
				t.Fatal(err)
			}
			if principal.IsAdmin() != test.want || principal.Can("users.manage") != test.want || principal.Can("tasks.read.any") != test.want {
				t.Fatalf("session privileges: admin=%v users.manage=%v tasks.read.any=%v; want %v",
					principal.IsAdmin(), principal.Can("users.manage"), principal.Can("tasks.read.any"), test.want)
			}
			if !reflect.DeepEqual(directory.byID[record.ID].Roles, []string{"user", "admin", " admin "}) {
				t.Fatal("historical role storage was modified")
			}
		})
	}
}
