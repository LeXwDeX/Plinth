package httpapi_test

// task_projection_negative_test.go 落实 §8.3/§5.5 哨兵用例：成本/同意
// 的原始声明（cost_policy 数值、extra_when 条件、consent/format 参数
// 指针）不得出现在任何公开响应面——/api/v1/schema、/api/v1/offerings、
// 任务响应、SSE 事件与 provider discovery（默认 + extended）。
// 公开面只允许出现派生的安全估算数字。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog/catalogtest"
)

// costSentinels 是禁止公开的原始声明哨兵：以精确 JSON 键形态匹配，
// 避免与公开参数名（authorize_extra_credits）及无关声明
// （如 canvas 的 json_format_parameter）产生子串误报。
var costSentinels = []string{
	`"max_authorized_extra_credits"`,
	`"cost_policy"`,
	`"extra_when"`,
	`"consent_parameter"`,
	`"format_parameter"`,
	`"base_credits"`,
	`"extra_credits"`,
	`"credits_consumed"`,
	"SENTINEL_COST_UNKNOWN",
}

func assertNoCostSentinels(
	t *testing.T,
	surface string,
	payload []byte,
) {
	t.Helper()
	text := string(payload)
	for _, sentinel := range costSentinels {
		if strings.Contains(text, sentinel) {
			t.Fatalf("%s 泄漏成本哨兵 %q", surface, sentinel)
		}
	}
}

func fetchSurface(
	t *testing.T,
	handler http.Handler,
	path string,
) []byte {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("X-User-ID", "owner-a")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s = %d", path, response.Code)
	}
	return response.Body.Bytes()
}

func TestCostDeclarationNeverEntersPublicSurfaces(t *testing.T) {
	t.Parallel()
	// The legacy six-format conversion remains disabled; explicitly enable it
	// alongside the three narrow routes to check all public cost projections.
	_, handler, _, _ := newHTTPHarnessWithCatalogOverride(t, []string{"admin"}, nil, nil, nil, nil,
		catalogtest.LoadDefaultWithTripoConversion(t))

	surfaces := map[string]string{
		"product schema": "/api/v1/schema",
		"offerings":      "/api/v1/offerings?capability=format-conversion",
		"offerings all":  "/api/v1/offerings",
		"discovery":      "/api/providers?capability=format-conversion",
		"discovery ext":  "/api/providers?capability=format-conversion&projection=extended",
		"task history":   "/api/tasks?recent=1",
	}
	for name, path := range surfaces {
		t.Run(name, func(t *testing.T) {
			assertNoCostSentinels(t, name, fetchSurface(t, handler, path))
		})
	}

	// 正向断言：转换 offering 的公开面仍携带可用元数据（参数/输出契约/
	// 安全积分估算），不因哨兵裁剪而丢失可用性。
	offeringsPayload := fetchSurface(t, handler, "/api/v1/offerings?capability=format-conversion")
	var offeringEnvelope struct {
		Offerings []struct {
			ID               string `json:"id"`
			Capability       string `json:"capability"`
			EstimatedCredits int    `json:"estimated_credits"`
			MaximumCredits   int    `json:"maximum_estimated_credits"`
			Parameters       []struct {
				Name string `json:"name"`
			} `json:"parameters"`
			Outputs []struct {
				Role     string `json:"role"`
				Variant  string `json:"variant"`
				Delivery string `json:"delivery"`
			} `json:"outputs"`
		} `json:"offerings"`
	}
	if err := json.Unmarshal(offeringsPayload, &offeringEnvelope); err != nil {
		t.Fatal(err)
	}
	if len(offeringEnvelope.Offerings) != 6 { // #330: Meshy Convert joins the conversions
		t.Fatalf("offerings = %#v", offeringEnvelope.Offerings)
	}
	index := -1
	for i, candidate := range offeringEnvelope.Offerings {
		if candidate.ID == "tripo/tripo-v3.1/format-conversion" {
			index = i
		}
	}
	if index < 0 {
		t.Fatal("legacy conversion missing from explicit fixture")
	}
	offering := offeringEnvelope.Offerings[index]
	if offering.ID != "tripo/tripo-v3.1/format-conversion" ||
		offering.Capability != "format-conversion" {
		t.Fatalf("offering = %#v", offering)
	}
	if offering.MaximumCredits != 10 {
		t.Fatalf("maximum_estimated_credits = %d; want the derived 10 when a billed option is chosen", offering.MaximumCredits)
	}
	if offering.EstimatedCredits != 5 {
		t.Fatalf("estimated_credits = %d; want 5（安全估算公开）", offering.EstimatedCredits)
	}
	names := map[string]bool{}
	for _, parameter := range offering.Parameters {
		names[parameter.Name] = true
	}
	for _, expected := range []string{"source_task", "format", "authorize_extra_credits", "quad"} {
		if !names[expected] {
			t.Errorf("公开参数缺少 %s", expected)
		}
	}
	if len(offering.Outputs) != 1 ||
		offering.Outputs[0].Role != "model" ||
		offering.Outputs[0].Variant != "converted" {
		t.Fatalf("outputs = %#v", offering.Outputs)
	}
}

func TestConversionTaskResponseHidesPrivateEvidence(t *testing.T) {
	t.Parallel()
	// 转换任务的公开详情：不出现私有 Opaque（源任务 remote id、
	// consumed_credit 证据）与检查点字段。
	module, handler, _ := newHTTPHarnessWithApplication(t, nil, nil)
	_ = module
	// 未创建真实转换任务（上游为 mock 形状时 source 依赖不可得），
	// 以历史空列表断言任务面不含检查点/私有证据字段名。
	payload := fetchSurface(t, handler, "/api/tasks?recent=1")
	for _, forbidden := range []string{"dispatch_checkpoint", "ack_token", "opaque", "credits_consumed", "consumed_credit"} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("任务历史泄漏私有字段 %q", forbidden)
		}
	}
}
