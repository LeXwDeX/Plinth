package catalog

import (
	"regexp"
	"strings"
	"testing"
)

// #599: the project group KEY on users is declared by Product Schema: its
// wire field, format pattern, audit detail keys, rejection message and page
// text. The service and the page only consume these declarations.
func TestDefaultCatalogDeclaresUserProjectGroupContract599(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	users := taskCatalog.Application().Administration.Users
	if users.API.Fields.ProjectGroupKey != "projectGroupKey" {
		t.Fatalf("project group field = %q", users.API.Fields.ProjectGroupKey)
	}
	if users.Limits.ProjectGroupKeyPattern != `^[a-z0-9][a-z0-9_-]{0,31}$` {
		t.Fatalf("project group pattern = %q", users.Limits.ProjectGroupKeyPattern)
	}
	pattern := regexp.MustCompile(users.Limits.ProjectGroupKeyPattern)
	for _, valid := range []string{"a", "studio-a", "team_01", strings.Repeat("z", 32)} {
		if !pattern.MatchString(valid) {
			t.Fatalf("default pattern rejects %q", valid)
		}
	}
	for _, invalid := range []string{"Studio", "studio a", "-studio", "_studio", strings.Repeat("z", 33), ""} {
		if pattern.MatchString(invalid) {
			t.Fatalf("default pattern accepts %q", invalid)
		}
	}
	if users.Audit.DetailFields.ProjectGroupKey == "" ||
		users.Audit.DetailFields.PreviousProjectGroupKey == "" ||
		users.Audit.DetailFields.ProjectGroupKey == users.Audit.DetailFields.PreviousProjectGroupKey {
		t.Fatalf("project group audit detail fields = %+v", users.Audit.DetailFields)
	}
	if users.Messages.InvalidProjectGroup == "" {
		t.Fatal("messages.invalid_project_group is empty")
	}
	for _, key := range []string{
		"column_project_group", "project_group_label",
		"project_group_placeholder", "project_group_description",
	} {
		if users.Presentation.Text[key] == "" {
			t.Fatalf("presentation.text[%q] is empty", key)
		}
	}
}

func TestAdministrationUserSchemaRejectsBrokenProjectGroupContract599(t *testing.T) {
	cases := map[string]func(*AdministrationUsersSchema){
		"invalid pattern":   func(users *AdministrationUsersSchema) { users.Limits.ProjectGroupKeyPattern = `^[a-z` },
		"missing pattern":   func(users *AdministrationUsersSchema) { users.Limits.ProjectGroupKeyPattern = "  " },
		"missing field":     func(users *AdministrationUsersSchema) { users.API.Fields.ProjectGroupKey = "" },
		"duplicate field":   func(users *AdministrationUsersSchema) { users.API.Fields.ProjectGroupKey = users.API.Fields.Email },
		"missing message":   func(users *AdministrationUsersSchema) { users.Messages.InvalidProjectGroup = "" },
		"missing audit key": func(users *AdministrationUsersSchema) { users.Audit.DetailFields.ProjectGroupKey = "" },
		"missing previous audit key": func(users *AdministrationUsersSchema) {
			users.Audit.DetailFields.PreviousProjectGroupKey = ""
		},
		"audit keys collide": func(users *AdministrationUsersSchema) {
			users.Audit.DetailFields.PreviousProjectGroupKey = users.Audit.DetailFields.ProjectGroupKey
		},
		"audit key collides with username": func(users *AdministrationUsersSchema) {
			users.Audit.DetailFields.ProjectGroupKey = users.Audit.DetailFields.Username
		},
		"missing text": func(users *AdministrationUsersSchema) {
			delete(users.Presentation.Text, "project_group_label")
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			taskCatalog, err := LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			application := taskCatalog.Application()
			mutate(&application.Administration.Users)
			if err := ValidateApplicationSchema(application); err == nil {
				t.Fatal("broken project group contract accepted")
			}
		})
	}
}
