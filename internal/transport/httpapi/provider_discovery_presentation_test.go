package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// visibleTripoTextureCatalog 返回默认目录的副本：tripo-v3.1-full 文生 3D 的
// texture 参数改回可见开关（带 visible_when / forced_when），其余不变。
func visibleTripoTextureCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "catalog", "default_catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range doc["offerings"].([]any) {
		offering := item.(map[string]any)
		if offering["id"] != "tripo/tripo-v3.1-full/text-to-3d" {
			continue
		}
		for _, entry := range offering["parameters"].([]any) {
			parameter := entry.(map[string]any)
			if parameter["name"] != "texture" {
				continue
			}
			delete(parameter, "fixed")
			delete(parameter, "adjustment_policy")
			delete(parameter, "adjustment_reason")
			parameter["presentation"] = map[string]any{
				"surface": "provider", "control": "boolean",
				"visible_when": []any{map[string]any{"parameter": "generate_parts", "operator": "falsy"}},
			}
			found = true
		}
	}
	if !found {
		t.Fatal("tripo texture parameter not found")
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Load(strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestExtendedProviderDiscoveryProjectsPresentationPrimitives 锁定 W3 契约：
// projection=extended 携带 visible_when / forced_when / slider 控件；
// 默认投影保持旧站字节兼容，不出现这些新字段。
func TestExtendedProviderDiscoveryProjectsPresentationPrimitives(t *testing.T) {
	t.Parallel()
	// 默认目录里 Tripo 的 texture 是公开只读建议（extended 展示为 readonly）；
	// 这里在目录副本里把它恢复成可见开关，用来锁定扩展投影的线形状。
	_, handler, _, _ := newHTTPHarnessWithCatalogOverride(
		t, []string{"admin"}, nil, nil, nil, nil, visibleTripoTextureCatalog(t),
	)

	extended := fetchProviderParameters(
		t,
		handler,
		"/api/providers?capability=text-to-3d&projection=extended",
		"tripo-v3.1-full",
	)
	texture, exists := extended["texture"].(map[string]any)
	if !exists {
		t.Fatalf("extended texture parameter = %#v", extended["texture"])
	}
	visible, ok := texture["visible_when"].([]any)
	if !ok || len(visible) != 1 {
		t.Fatalf("texture visible_when = %#v", texture["visible_when"])
	}
	condition, ok := visible[0].(map[string]any)
	if !ok ||
		condition["parameter"] != "generate_parts" ||
		condition["operator"] != "falsy" {
		t.Fatalf("texture visible_when condition = %#v", visible[0])
	}
	forced, ok := texture["forced_when"].([]any)
	if !ok || len(forced) != 1 {
		t.Fatalf("texture forced_when = %#v", texture["forced_when"])
	}
	forcedEntry, ok := forced[0].(map[string]any)
	if !ok {
		t.Fatalf("texture forced_when entry = %#v", forced[0])
	}
	forcedWhen, ok := forcedEntry["when"].(map[string]any)
	if !ok ||
		forcedWhen["parameter"] != "generate_parts" ||
		forcedWhen["operator"] != "truthy" {
		t.Fatalf("texture forced_when.when = %#v", forcedEntry["when"])
	}
	if forcedEntry["value"] != false {
		t.Fatalf("texture forced_when.value = %#v", forcedEntry["value"])
	}

	mattingExtended := fetchProviderParameters(
		t,
		handler,
		"/api/providers?capability=background-removal&projection=extended",
		"matting-default",
	)
	// 去背景只露出“透明物体模式”，其余算法参数在 Schema 中隐藏（含滑块参数 sensitivity）。
	if _, exists := mattingExtended["sensitivity"]; exists {
		t.Fatalf("hidden sensitivity leaked into discovery: %#v", mattingExtended["sensitivity"])
	}
	if _, exists := mattingExtended["transparent_object"]; !exists {
		t.Fatalf("transparent_object missing: %#v", mattingExtended)
	}

	defaults := fetchProviderParameters(
		t,
		handler,
		"/api/providers?capability=text-to-3d",
		"tripo-v3.1-full",
	)
	for _, name := range []string{"texture", "auto_size", "generate_parts"} {
		parameter, exists := defaults[name].(map[string]any)
		if !exists {
			t.Fatalf("default projection parameter %s = %#v", name, defaults[name])
		}
		for _, field := range []string{"visible_when", "forced_when", "notices", "disabled_when", "validation"} {
			if _, exists := parameter[field]; exists {
				t.Fatalf(
					"默认投影泄漏扩展字段 %s（%s）: %#v",
					field,
					name,
					parameter[field],
				)
			}
		}
	}
}

func fetchProviderParameters(
	t *testing.T,
	handler http.Handler,
	path string,
	modelID string,
) map[string]any {
	t.Helper()
	// 能力键取查询串里的 capability（同一 Provider 现在可能声明多个
	// 能力，不得依赖 map 迭代顺序）。
	query := requestURLQuery(t, path)
	capability := query.Get("capability")
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("X-User-ID", "admin-user")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf(
			"%s status = %d, body = %s",
			path,
			response.Code,
			response.Body.String(),
		)
	}
	var body struct {
		Providers []struct {
			ID           string                      `json:"id"`
			ParamSchemas map[string][]map[string]any `json:"paramSchemas"`
		} `json:"providers"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	for _, provider := range body.Providers {
		if provider.ID != modelID {
			continue
		}
		parameters, exists := provider.ParamSchemas[capability]
		if !exists {
			t.Fatalf(
				"provider %q 缺少能力 %q 的参数投影（%s）",
				modelID,
				capability,
				path,
			)
		}
		result := make(map[string]any, len(parameters))
		for _, parameter := range parameters {
			result[parameter["name"].(string)] = parameter
		}
		return result
	}
	t.Fatalf("provider %q not found at %s", modelID, path)
	return nil
}

func requestURLQuery(t *testing.T, path string) url.Values {
	t.Helper()
	parsed, err := url.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Query()
}
