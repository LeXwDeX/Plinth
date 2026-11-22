package catalog_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #332: the default Schema says which sessions came from where (the source
// member) and how the browser groups the session list by it (the groups, their
// copy and their order); both reach the browser through the public projection.
func TestDefaultCatalogDeclaresSessionGroups(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	chat := application.Chat
	if chat.API.ResponseFields.Source == "" {
		t.Fatal("the default Schema declares no session source member")
	}
	groups := chat.API.SessionGroups
	if len(groups) != 2 || groups[0].Source != "chat" || groups[1].Source != "mcp" {
		t.Fatalf("session groups = %+v", groups)
	}
	for _, group := range groups {
		if strings.TrimSpace(application.Content.Messages[group.Label]) == "" {
			t.Fatalf("group %q names %q, which is not declared", group.Source, group.Label)
		}
	}
	public := application.Public().Chat
	if len(public.API.SessionGroups) != len(groups) ||
		public.API.SessionGroups[0].Label != groups[0].Label {
		t.Fatalf("public session groups = %+v", public.API.SessionGroups)
	}
}

// #332: the grouping is validated, so a source the code never writes, a source
// without a group and a group without copy cannot be declared.
func TestSessionGroupsAreValidated(t *testing.T) {
	source, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct{ old, new, detail string }{
		"unknown source": {
			`{"source": "mcp", "label": "ui.studio.session.group.mcp"}`,
			`{"source": "cron", "label": "ui.studio.session.group.mcp"}`,
			"未知会话来源",
		},
		"a source without a group": {
			`{"source": "chat", "label": "ui.studio.session.group.chat"},
          {"source": "mcp", "label": "ui.studio.session.group.mcp"}`,
			`{"source": "chat", "label": "ui.studio.session.group.chat"}`,
			"缺少会话来源",
		},
		"a group without copy": {
			`{"source": "mcp", "label": "ui.studio.session.group.mcp"}`,
			`{"source": "mcp", "label": "ui.studio.session.group.nope"}`,
			"不在 content.messages",
		},
	} {
		changed := bytes.Replace(source, []byte(test.old), []byte(test.new), 1)
		if bytes.Equal(changed, source) {
			t.Fatalf("%s: fixture %q not found", name, test.old)
		}
		// A repeated source is the same edit twice; the first one already fails.
		if _, err := catalog.Load(bytes.NewReader(changed)); err == nil || !strings.Contains(err.Error(), test.detail) {
			t.Errorf("%s: error = %v, want %q", name, err, test.detail)
		}
	}
	// A grouping that names one source twice is refused.
	changed := bytes.Replace(source,
		[]byte(`{"source": "mcp", "label": "ui.studio.session.group.mcp"}`),
		[]byte(`{"source": "chat", "label": "ui.studio.session.group.chat"}`), 1)
	if _, err := catalog.Load(bytes.NewReader(changed)); err == nil || !strings.Contains(err.Error(), "重复声明会话来源") {
		t.Errorf("a repeated source: error = %v", err)
	}
}
