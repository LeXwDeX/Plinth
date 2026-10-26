package app

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #252: docs/provider-configuration.md and .env.example list exactly the
// Schema's Provider variables — canonical names, defaults and deprecated
// aliases — so the published contract cannot drift from the code.
const unifiedGatewayHeading = "## 统一网关 / Unified gateway"

func TestProviderConfigurationDocMatchesSchema(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	runtime := taskCatalog.Application().Runtime
	doc, err := os.ReadFile("../../docs/provider-configuration.md")
	if err != nil {
		t.Fatal(err)
	}
	example, err := os.ReadFile("../../.env.example")
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string][]string{}
	row := regexp.MustCompile("^\\|[^|]*\\| `([A-Z0-9_]+)` \\|[^|]*\\| ([^|]*) \\| ([^|]*) \\|$")
	for _, line := range strings.Split(string(doc), "\n") {
		if match := row.FindStringSubmatch(line); match != nil {
			rows[match[1]] = []string{strings.TrimSpace(match[2]), strings.TrimSpace(match[3])}
		}
	}
	documented := 0
	for _, connection := range runtime.ProviderConnections {
		for _, name := range []string{connection.BaseURLSetting, connection.APIKeySetting} {
			if name == "" {
				continue
			}
			setting := runtime.Configuration.Settings[name]
			canonical := setting.Environment[0].Name
			documented++
			cells, ok := rows[canonical]
			if !ok {
				t.Fatalf("%s (%s) is not documented", canonical, connection.ID)
			}
			wantDefault := "—"
			if setting.Default != "" {
				wantDefault = "`" + setting.Default + "`"
			}
			if cells[0] != wantDefault {
				t.Fatalf("%s default documented as %s, Schema has %s", canonical, cells[0], wantDefault)
			}
			var aliases []string
			for _, source := range setting.Environment[1:] {
				aliases = append(aliases, "`"+source.Name+"`")
			}
			wantAlias := "—"
			if len(aliases) > 0 {
				wantAlias = strings.Join(aliases, ", ")
			}
			if cells[1] != wantAlias {
				t.Fatalf("%s aliases documented as %s, Schema has %s", canonical, cells[1], wantAlias)
			}
			if !regexp.MustCompile("(?m)^" + canonical + "=").Match(example) {
				t.Fatalf("%s is missing from .env.example", canonical)
			}
		}
	}
	// Each connection reads only its own address, so a deployment with one
	// gateway for every model sets each address variable to it explicitly;
	// both the doc and .env.example say so.
	index := strings.Index(string(doc), unifiedGatewayHeading)
	if index < 0 {
		t.Fatalf("docs/provider-configuration.md lacks %q", unifiedGatewayHeading)
	}
	section := string(doc)[index:]
	if next := strings.Index(section[len(unifiedGatewayHeading):], "\n## "); next >= 0 {
		section = section[:len(unifiedGatewayHeading)+next]
	}
	gatewayComment := regexp.MustCompile(`(?mi)^# .*unified gateway.*$(\n^#.*$)*`).Find(example)
	for _, name := range []string{"LLM_BASE_URL", "OPENAI_BASE_URL", "GEMINI_BASE_URL"} {
		if !strings.Contains(section, "`"+name+"`") {
			t.Fatalf("%q does not name %s", unifiedGatewayHeading, name)
		}
		if !strings.Contains(string(gatewayComment), name) {
			t.Fatalf(".env.example needs a \"unified gateway\" comment telling to set %s to the gateway URL", name)
		}
	}
	for _, text := range [][]byte{doc, example} {
		if regexp.MustCompile("(?i)transitional|过渡").Match(text) {
			t.Fatal("the transitional GEMINI_BASE_URL <- LLM_BASE_URL alias is gone; docs must not describe it")
		}
	}
	if documented != len(rows) {
		var extra []string
		for name := range rows {
			extra = append(extra, name)
		}
		slices.Sort(extra)
		t.Fatalf("documented %d variables, Schema declares %d: %v", len(rows), documented, extra)
	}
}
