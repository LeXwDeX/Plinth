package catalog_test

import (
	"encoding/json"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/schema"
	"strings"
	"testing"
)

func TestRetentionPolicyIsExplicitPrivateAndCloned(t *testing.T) {
	c, e := catalog.LoadDefault()
	if e != nil {
		t.Fatal(e)
	}
	a := c.Application()
	if a.Runtime.TaskRetention.Enabled || a.Runtime.TaskRetention.MinimumAgeSeconds != 0 {
		t.Fatal("retention became implicitly enabled")
	}
	public, e := json.Marshal(a.Public())
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(public), "task_retention") {
		t.Fatal("private retention policy leaked to browser")
	}
	a.Runtime.TaskRetention.Enabled = true
	if e = catalog.ValidateApplicationSchema(a); e == nil {
		t.Fatal("retention enabled without explicit age")
	}
	a.Runtime.TaskRetention.MinimumAgeSeconds = 86400
	if e = catalog.ValidateApplicationSchema(a); e != nil {
		t.Fatal(e)
	}
	a.Runtime.TaskRetention.TaskStates[0] = "RUNNING"
	if e = catalog.ValidateApplicationSchema(a); e == nil {
		t.Fatal("active task deletion policy accepted")
	}
	if c.Application().Runtime.TaskRetention.TaskStates[0] == "RUNNING" {
		t.Fatal("retention clone mutated catalog")
	}
}

func TestRetentionOmittedPolicyRemainsDisabled(t *testing.T) {
	c, e := catalog.LoadDefault()
	if e != nil {
		t.Fatal(e)
	}
	a := c.Application()
	a.Runtime.TaskRetention = schema.TaskRetentionPolicy{}
	if e = catalog.ValidateApplicationSchema(a); e != nil {
		t.Fatal(e)
	}
	a.Runtime.TaskRetention.Enabled = true
	if e = catalog.ValidateApplicationSchema(a); e == nil {
		t.Fatal("omitted policy acquired execution defaults")
	}
}
