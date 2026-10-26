package catalog

import (
	"slices"
	"testing"
)

func TestDefaultCatalogOwnsAdministrationUserContract(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	users := application.Administration.Users

	if got := application.Runtime.HTTP.LegacyRoutes.AdminUsers; got != "/api/admin/users" {
		t.Fatalf("admin users route = %q", got)
	}
	if got := application.Runtime.HTTP.LegacyRoutes.AdminUser; got != "/api/admin/users/{userID}" {
		t.Fatalf("admin user route = %q", got)
	}
	if users.API.PathParameters.UserID != "userID" {
		t.Fatalf("admin user path parameter = %q", users.API.PathParameters.UserID)
	}
	if users.Permission != "users.manage" {
		t.Fatalf("admin user permission = %q", users.Permission)
	}
	for name, value := range map[string]string{
		"data":       users.API.Fields.Data,
		"success":    users.API.Fields.Success,
		"error":      users.API.Fields.Error,
		"id":         users.API.Fields.ID,
		"username":   users.API.Fields.Username,
		"password":   users.API.Fields.Password,
		"nickname":   users.API.Fields.Nickname,
		"name":       users.API.Fields.Name,
		"email":      users.API.Fields.Email,
		"is_admin":   users.API.Fields.IsAdmin,
		"is_active":  users.API.Fields.IsActive,
		"created_at": users.API.Fields.CreatedAt,
		"last_login": users.API.Fields.LastLogin,
		"open_id":    users.API.Fields.OpenID,
	} {
		if value == "" {
			t.Fatalf("administration.users.api.fields.%s is empty", name)
		}
	}
	if users.Limits.UsernameMinimumCharacters != 2 {
		t.Fatalf(
			"username minimum = %d",
			users.Limits.UsernameMinimumCharacters,
		)
	}
	if users.Limits.PasswordMinimumCharacters != 6 {
		t.Fatalf(
			"password minimum = %d",
			users.Limits.PasswordMinimumCharacters,
		)
	}
	if users.Defaults.IDPrefix != "user_" || !users.Defaults.Active {
		t.Fatalf("unexpected admin user defaults: %+v", users.Defaults)
	}
	if users.Audit.ResourceType != "user" {
		t.Fatalf("admin user audit resource type = %q", users.Audit.ResourceType)
	}
	for _, action := range []string{
		users.Audit.CreateAction,
		users.Audit.UpdateAction,
		users.Audit.DeactivateAction,
		users.Audit.DeleteAction,
	} {
		if !slices.ContainsFunc(application.AuditActions, func(item ValueLabel) bool {
			return item.Value == action
		}) {
			t.Fatalf("admin user audit action %q is not declared", action)
		}
	}
	for _, key := range requiredAdministrationUserTextKeys {
		if users.Presentation.Text[key] == "" {
			t.Fatalf("administration.users.presentation.text[%q] is empty", key)
		}
	}
}

func TestAdministrationUserSchemaRequiresConfiguredItemRouteParameter(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	application.Runtime.HTTP.LegacyRoutes.AdminUser = "/custom/admin/users/{id}"

	if err := ValidateApplicationSchema(application); err == nil {
		t.Fatal("expected mismatched admin user path parameter to be rejected")
	}
}

func TestApplicationReturnsDefensiveAdministrationPresentationClone(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	first := taskCatalog.Application()
	first.Administration.Users.Presentation.Text["title"] = "mutated"
	second := taskCatalog.Application()

	if second.Administration.Users.Presentation.Text["title"] == "mutated" {
		t.Fatal("administration presentation map leaked through Application clone")
	}
}
