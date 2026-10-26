package routing

import "testing"

func TestSchemaPathTemplatesRenderEscapedParameters(t *testing.T) {
	assetRoute, err := ParseOne(
		"/configured/assets/{assetID}",
		"assetID",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := assetRoute.Render("asset/with space"); got !=
		"/configured/assets/asset%2Fwith%20space" {
		t.Fatalf("asset route = %q", got)
	}

	taskAssetRoute, err := ParseTwo(
		"/configured/tasks/{taskID}/files/{assetID}",
		"taskID",
		"assetID",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := taskAssetRoute.Render("task/1", "asset?2"); got !=
		"/configured/tasks/task%2F1/files/asset%3F2" {
		t.Fatalf("task asset route = %q", got)
	}

	reorderedRoute, err := ParseTwo(
		"/configured/files/{assetID}/tasks/{taskID}",
		"taskID",
		"assetID",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := reorderedRoute.Render("task/1", "asset?2"); got !=
		"/configured/files/asset%3F2/tasks/task%2F1" {
		t.Fatalf("reordered task asset route = %q", got)
	}
}

func TestSchemaPathTemplatesRejectMissingOrUnexpectedParameters(t *testing.T) {
	for name, parse := range map[string]func() error{
		"missing": func() error {
			_, err := ParseOne("/configured/assets", "assetID")
			return err
		},
		"unexpected": func() error {
			_, err := ParseOne(
				"/configured/assets/{assetID}/{other}",
				"assetID",
			)
			return err
		},
		"duplicate": func() error {
			_, err := ParseOne(
				"/configured/assets/{assetID}/{assetID}",
				"assetID",
			)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := parse(); err == nil {
				t.Fatal("accepted invalid Schema path template")
			}
		})
	}
}

func TestSchemaPathTemplateRedactsOnlyMatchingRoute(t *testing.T) {
	route, err := ParseOne(
		"/configured/provider-assets/{token}/content",
		"token",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got, matched := route.Redact(
		"/configured/provider-assets/secret.token/content",
		"redacted",
	); !matched || got !=
		"/configured/provider-assets/{redacted}/content" {
		t.Fatalf("redacted route = %q, matched = %v", got, matched)
	}
	if got, matched := route.Redact(
		"/configured/provider-assets/secret.token/other",
		"redacted",
	); matched || got !=
		"/configured/provider-assets/secret.token/other" {
		t.Fatalf("ordinary route = %q, matched = %v", got, matched)
	}
}

func TestSchemaPathTemplateMatchesExactlyOneParameterSegment(t *testing.T) {
	route, err := ParseOne("/{webhookSecret}/wbh", "webhookSecret")
	if err != nil {
		t.Fatal(err)
	}
	if value, matched := route.Match("/candidate-secret/wbh"); !matched ||
		value != "candidate-secret" {
		t.Fatalf("value = %q, matched = %v", value, matched)
	}
	for _, path := range []string{
		"//wbh",
		"/nested/secret/wbh",
		"/candidate-secret/other",
	} {
		if value, matched := route.Match(path); matched || value != "" {
			t.Fatalf(
				"path %q produced value %q, matched = %v",
				path,
				value,
				matched,
			)
		}
	}
}
