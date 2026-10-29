package catalog

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// #118: the admin audit page shows audit_actions labels from the Schema and
// falls back to the raw key only for unknown keys. Every action the Schema
// itself declares as an audit action must therefore have a label in both
// languages, so a declared action never reaches the page as a raw key.
func TestEveryDeclaredAuditActionHasLocalizedLabels(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	application := document["application"].(map[string]any)
	labeled := map[string]bool{}
	for _, item := range application["audit_actions"].([]any) {
		entry := item.(map[string]any)
		if strings.TrimSpace(entry["label"].(string)) == "" {
			t.Fatalf("audit action %v has an empty label", entry["value"])
		}
		labeled[entry["value"].(string)] = true
	}
	// Audit action declarations: "audit" objects of the application sections
	// and the authentication password-change audit action.
	declared := map[string]string{}
	actionKey := regexp.MustCompile(`^[a-z_]*action$`)
	var walk func(node any, path string, inAudit bool)
	walk = func(node any, path string, inAudit bool) {
		switch value := node.(type) {
		case map[string]any:
			for key, child := range value {
				if text, ok := child.(string); ok && actionKey.MatchString(key) &&
					(inAudit || key == "password_change_audit_action") &&
					regexp.MustCompile(`^[a-z_]+$`).MatchString(text) {
					declared[text] = path + "/" + key
				}
				walk(child, path+"/"+key, inAudit || key == "audit")
			}
		case []any:
			for _, child := range value {
				walk(child, path, inAudit)
			}
		}
	}
	walk(application, "", false)
	if len(declared) < 8 {
		t.Fatalf("expected the Schema's audit action declarations, got %v", declared)
	}
	for action, where := range declared {
		if !labeled[action] {
			t.Errorf("audit action %q declared at %s has no audit_actions label", action, where)
		}
	}
	for action := range labeled {
		if !strings.Contains(string(raw), `"audit_actions[value=`+action+`].label"`) {
			t.Errorf("audit action %q has no English label overlay", action)
		}
	}
}
