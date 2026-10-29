package catalog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestPublicApplicationSchemaKeepsBrowserContractWithoutServerWiring(t *testing.T) {
	source, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(source.Application().Public())
	if err != nil {
		t.Fatal(err)
	}
	var public map[string]any
	if err := json.Unmarshal(encoded, &public); err != nil {
		t.Fatal(err)
	}
	for path, keys := range map[string][]string{
		"":             {"revision", "runtime", "content", "chat", "canvas", "task_history", "task_stream", "administration", "agent_configuration", "llm_settings", "capabilities", "task_states", "audit_actions", "delivery_formats", "authentication", "localization"},
		"llm_settings": {"routes", "ui"},
		"runtime":      {"http", "input_assets", "cache", "asset_delivery", "frontend", "generation_messages"},
		// #80: the composer's capacity text and the result limit pair.
		"runtime.generation_messages":       {"reference_capacity", "reference_limit", "reference_limit_one"},
		"chat":                              {"api", "defaults", "roles", "state", "frontend", "context", "stream", "tools", "skills", "presentation", "approval_policy", "agent_scope", "harness", "agent_status", "context_compaction"},
		"canvas":                            {"api", "defaults", "limits", "frontend", "presentation"},
		"task_stream":                       {"aliases"},
		"administration":                    {"navigation", "users", "audit_logs", "generations", "usage", "user_assets"},
		"administration.users":              {"api", "limits", "messages", "presentation"},
		"administration.audit_logs":         {"api", "limits", "messages", "presentation"},
		"administration.generations":        {"api", "limits", "assets", "messages", "presentation"},
		"administration.generations.assets": {"download_filename_template", "remote_thumbnail_url_query", "remote_thumbnail_task_id_query", "preview_image_extensions"},
		"administration.usage":              {"api", "defaults", "messages", "presentation"},
		"administration.user_assets":        {"api", "limits", "filters", "messages", "presentation"},
		"authentication":                    {"api", "public_paths", "session_retry", "messages", "login"},
		"localization":                      {"default_locale", "active_locale", "locales", "preference", "persisted_default_texts"},
	} {
		value := any(public)
		if path != "" {
			value, _ = publicPath(public, path)
		}
		object, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("public path %s is not an object", path)
		}
		actual := make([]string, 0, len(object))
		for key := range object {
			actual = append(actual, key)
		}
		slices.Sort(actual)
		slices.Sort(keys)
		if !slices.Equal(actual, keys) {
			t.Errorf("public path %s keys = %v, want %v", path, actual, keys)
		}
	}

	// Each positive path has a current browser reader. The HTTP endpoint and the
	// Go Web shell both serialize this same Public() DTO.
	for _, path := range []string{
		"runtime.http.routes.tasks",
		"runtime.http.routes.offerings",
		"runtime.http.legacy_routes.chat_sessions",
		"runtime.http.legacy_provider_balance.accounts",
		"runtime.input_assets.upload_field_name",
		"runtime.input_assets.upload_max_bytes",
		"runtime.input_assets.type_rules",
		"runtime.cache.bytes_per_megabyte",
		"runtime.cache.fields.active_downloads",
		"runtime.asset_delivery.proxy.api.query.url",
		"runtime.asset_delivery.proxy.security.allowed_schemes",
		"runtime.asset_delivery.proxy.thumbnail.request_value",
		"runtime.asset_delivery.asset_preview.query",
		"runtime.asset_delivery.asset_preview.request_value",
		"runtime.frontend.image_upload.maximum_pixels",
		"chat.api.fields.content",
		"chat.api.prompt_enhance.prompt_field",
		"chat.stream.event_types.done",
		"chat.context.image_index_base",
		"chat.skills.fields.skills",
		"chat.presentation.messages.internal_error",
		"canvas.api.fields.id",
		"canvas.defaults.bg_style",
		"canvas.limits.maximum_dimension",
		"canvas.limits.maximum_bitmap_nodes",
		"canvas.limits.maximum_placed_results",
		"canvas.limits.maximum_placed_result_key_characters",
		"canvas.api.fields.placed_results",
		"canvas.presentation.messages.placed_results_invalid",
		"canvas.api.fields.canvas_width",
		"canvas.api.fields.canvas_height",
		"canvas.api.fields.coordinate_version",
		"canvas.defaults.coordinate_version",
		"canvas.limits.minimum_dimension",
		"canvas.frontend.artboard.ratio_pixels",
		"canvas.frontend.artboard.legacy_max_edge",
		"canvas.presentation.messages.canvas_frame_invalid",
		"canvas.frontend.export.output_media_type",
		"canvas.frontend.export.artboard_background_color",
		"canvas.presentation.messages.internal_error",
		"task_history.labels.models",
		"task_history.hidden_fields",
		"task_stream.aliases",
		"administration.navigation",
		"administration.users.presentation",
		"administration.audit_logs.api.fields.data",
		"administration.generations.assets.preview_image_extensions",
		"administration.usage.defaults.days",
		"administration.user_assets.filters.download_actions",
		"authentication.api.username_field",
		"authentication.login.redirect_query",
		"authentication.messages.invalid_credentials",
		"authentication.login.title",
	} {
		if _, ok := publicPath(public, path); !ok {
			t.Errorf("browser contract path missing: %s", path)
		}
	}
	// Dot-containing message keys are single JSON object keys.
	for _, key := range []string{"error.consent_unavailable", "error.consent_not_pending"} {
		messages := public["content"].(map[string]any)["messages"].(map[string]any)
		if value, ok := messages[key].(string); !ok || value == "" {
			t.Errorf("public consent message %s missing", key)
		}
	}
	for _, path := range []string{
		"provider_webhooks", "provider_discovery",
		"runtime.configuration", "runtime.provider_connections", "runtime.provider_accounts",
		"runtime.coordinator", "runtime.task_deletion", "runtime.task_admission",
		"runtime.task_audit", "runtime.task_events",
		"runtime.generation_messages.submission_unknown", "runtime.generation_messages.retired_model",
		"runtime.input_assets.legacy_storage_root_setting", "runtime.input_assets.audit",
		"runtime.asset_delivery.proxy.cache", "runtime.asset_delivery.proxy.security.host_source",
		"chat.agent", "chat.agent_tools", "chat.pending_approvals", "chat.three_view", "chat.dsml", "chat.audit",
		"chat.api.prompt_enhance.profiles", "chat.stream.headers",
		"chat.skills.execution", "chat.context.asset_url_template",
		"canvas.audit", "canvas.defaults.project_id_prefix",
		"administration.backfill_status",
		"administration.users.permission", "administration.users.guards",
		"administration.users.defaults", "administration.users.audit",
		"administration.audit_logs.request_headers", "administration.audit_logs.write_policy",
		"administration.generations.assets.legacy_cache_root_setting",
		"administration.generations.assets.remote_download_timeout_milliseconds",
		"administration.generations.audit", "administration.usage.balance",
		"administration.usage.validation", "administration.user_assets.audit",
		"authentication.cookie", "authentication.session", "authentication.permissions",
		"authentication.roles", "authentication.sso",
		"runtime.http.legacy_routes.auth_sso_logout",
		"authentication.api.password_hash_algorithm",
	} {
		if _, ok := publicPath(public, path); ok {
			t.Errorf("server-only path leaked: %s", path)
		}
	}
}

func TestCanvasBitmapLimitIsRequiredAndSeparateFromMergeLimit(t *testing.T) {
	document, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"maximum_bitmap_nodes": 50`, `"bitmap_limit_exceeded": "画板最多保存 50 张图片，请先移除部分图片"`} {
		mutated := bytes.Replace(document, []byte(key), []byte(strings.Replace(key, ": 50", ": 0", 1)), 1)
		if strings.Contains(key, "bitmap_limit_exceeded") {
			mutated = bytes.Replace(document, []byte(key), []byte(`"bitmap_limit_exceeded": ""`), 1)
		}
		if bytes.Equal(mutated, document) {
			t.Fatalf("default catalog field not found: %s", key)
		}
		if _, err := catalog.Load(bytes.NewReader(mutated)); err == nil {
			t.Fatalf("missing canvas bitmap declaration accepted: %s", key)
		}
	}
	source, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	if limits := source.Application().Canvas.Limits; limits.MaximumBitmapNodes != 50 || limits.MaximumMergeNodes != 20 {
		t.Fatalf("canvas limits = %#v", limits)
	}
}

func publicPath(value map[string]any, path string) (any, bool) {
	var current any = value
	for _, segment := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func TestAgentReconcileContentIsRequiredAndPrivate(t *testing.T) {
	document, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	const value = `"consent_reconcile_content": "the previous paid submission has an unknown outcome; recover or verify that original call before any new paid generation, and do not submit another paid request"`
	mutated := bytes.Replace(document, []byte(value), []byte(`"consent_reconcile_content": ""`), 1)
	if bytes.Equal(mutated, document) {
		t.Fatal("default catalog reconcile content not found")
	}
	if _, err := catalog.Load(bytes.NewReader(mutated)); err == nil || !strings.Contains(err.Error(), "consent_reconcile_content") {
		t.Fatalf("load with missing reconcile instruction = %v", err)
	}
	loaded, err := catalog.Load(bytes.NewReader(document))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Application().Chat.Agent.ConsentReconcileContent == "" {
		t.Fatal("reconcile instruction missing from server schema")
	}
	public, err := json.Marshal(loaded.Application().Public())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(public, []byte("consent_reconcile_content")) {
		t.Fatal("server agent instruction leaked to public schema")
	}
}

func TestCanvasPlacedResultsContractIsRequiredAndPublic(t *testing.T) {
	document, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	for original, broken := range map[string]string{
		`"placed_results": "placedResults"`:            `"placed_results": ""`,
		`"maximum_placed_results": 5000`:               `"maximum_placed_results": 0`,
		`"maximum_placed_result_key_characters": 512`:  `"maximum_placed_result_key_characters": -1`,
		`"placed_results_invalid": "已放置结果清单无效，请刷新后重试"`: `"placed_results_invalid": " "`,
	} {
		mutated := bytes.Replace(document, []byte(original), []byte(broken), 1)
		if bytes.Equal(mutated, document) {
			t.Fatalf("default catalog field not found: %s", original)
		}
		if _, err := catalog.Load(bytes.NewReader(mutated)); err == nil {
			t.Fatalf("invalid placed results declaration accepted: %s", broken)
		}
	}
	source, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := source.Application()
	if application.Canvas.API.Fields.PlacedResults != "placedResults" ||
		application.Canvas.Limits.MaximumPlacedResults != 5000 ||
		application.Canvas.Limits.MaximumPlacedResultKeyCharacters != 512 {
		t.Fatalf("canvas placed results contract = %#v %#v", application.Canvas.API.Fields, application.Canvas.Limits)
	}
}
