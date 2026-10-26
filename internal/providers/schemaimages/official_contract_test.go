package schemaimages_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/schemaimages"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// 本文件是既有实现的追补覆盖（不是 test-first），验证实际接线的同步图片
// 协议。依据是 dev-test 测试栈的真实链路，不是按 OpenAI/Google 官网逐字推断：
//   - OPENAI_BASE_URL 指向 llms 网关的 /v1（测试栈 runtime.env），
//     llms 中转提供 POST /v1/images/generations（JSON）与
//     POST /v1/images/edits（multipart，image[]/mask）。OpenAI Images 官方
//     接口参考 https://platform.openai.com/docs/api-reference/images
//     （2026-10-18 核对；该页面对自动抓取返回 403，字段外形同时由
//     docs/image-api-parameters.md 的付费探针 G1–G5、E1–E2 与网关源码验证）。
//     2026-10-18 从 dev-test 只读探测这两条路由未带凭据均返回 401：401 证明
//     存在鉴权墙；路由存在性由网关源码
//     internal/proxy/path_capability.go 确认，该文件按
//     images/generations 与 images/edits 区分 openai_image 操作，
//     multipart_convert.go 接受 edits 的 multipart 请求。
//   - GEMINI_BASE_URL 显式指向同一 llms 网关的 /v1（不回落到 LLM_BASE_URL），
//     Gemini 图片经该中转的 POST /v1/chat/completions；这是网宿渠道的
//     eca_image_config 契约，不是 Google 原生 /v1beta 接口。
//   - JIMENG_BASE_URL 未配置，Seedream 仍直连 Ark 的
//     POST /api/v3/images/generations
//     https://ark.volcengine.com/region:cn-beijing/docs/image-generation-api
//
// 核对日期：2026-10-18。依据分类：
//   - 官方明文：OpenAI Images 的端点与字段外形（辅以付费探针），Seedream
//     直连 Ark 的端点、字段、默认值与范围；
//   - 真实运行观察：llms 中转的路由鉴权墙、网关操作路由与付费探针结果；
//   - 项目约束：forced_when / 固定参数（GPT 始终 PNG、Seedream 内嵌无水印）。
// 示例值均为固定合成数据，业务值可随 Offering 变化；测试只在本地 httptest
// 上运行，不调用付费上游。

var officialPNG = opaqueFixturePNG(3)
var officialJPEG = []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01synthetic-contract-image")
var officialReferencePNG = opaqueFixturePNG(2)

// officialAssetStore 按资产 ID 返回可区分的字节；未知 ID 直接报错，
// 以便发现适配器打开错误资产或忽略 reference_assets 的情况。
type officialAssetStore map[generation.AssetID][]byte

func (store officialAssetStore) Resolve(
	context.Context,
	generation.OwnerID,
	generation.Offering,
	json.RawMessage,
) (map[string][]generation.InputAsset, error) {
	return nil, nil
}

func (store officialAssetStore) Open(
	_ context.Context,
	asset generation.InputAsset,
) (io.ReadCloser, error) {
	data, ok := store[asset.ID]
	if !ok {
		return nil, fmt.Errorf("unexpected input asset %q", asset.ID)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func officialAdapterAt(
	t *testing.T,
	upstream *httptest.Server,
	baseURL string,
	store officialAssetStore,
) *schemaimages.Adapter {
	t.Helper()
	adapter, err := schemaimages.New(schemaimages.Config{
		BaseURL:     baseURL,
		APIKey:      "official-key",
		HTTPClient:  upstream.Client(),
		InputAssets: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func officialAdapter(t *testing.T, upstream *httptest.Server) *schemaimages.Adapter {
	t.Helper()
	return officialAdapterAt(t, upstream, upstream.URL,
		officialAssetStore{"default": officialPNG})
}

func officialOffering(t *testing.T, c *catalog.Catalog, id generation.OfferingID) generation.Offering {
	t.Helper()
	offering, ok := c.Get(id)
	if !ok {
		t.Fatalf("offering %s is not declared", id)
	}
	return offering
}

func normalizedInput(t *testing.T, c *catalog.Catalog, id generation.OfferingID, raw string) (generation.Offering, []byte) {
	t.Helper()
	offering := officialOffering(t, c, id)
	_, input, err := c.Normalize(offering.Capability, offering.ID, []byte(raw))
	if err != nil {
		t.Fatalf("normalize %s: %v", id, err)
	}
	return offering, input
}

func requireProtocolFailure(t *testing.T, err error) *generation.Failure {
	t.Helper()
	var provider *generation.ProviderError
	if !errors.As(err, &provider) || provider.Failure.Class != generation.FailureProtocol {
		t.Fatalf("want protocol failure, got %v", err)
	}
	return provider.Failure
}

func requireNormalizationRejection(
	t *testing.T,
	c *catalog.Catalog,
	offering generation.Offering,
	raw string,
	wantDetail string,
) {
	t.Helper()
	_, _, err := c.Normalize(offering.Capability, offering.ID, []byte(raw))
	var schemaErr *schemacontract.Error
	if !errors.As(err, &schemaErr) ||
		schemaErr.Code != schemacontract.ErrInvalidInput ||
		!strings.Contains(schemaErr.Detail, wantDetail) {
		t.Fatalf("rejection = %v, want %s detail containing %q",
			err, schemacontract.ErrInvalidInput, wantDetail)
	}
}

func TestOpenAIImagesGenerationMatchesOfficialContract(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	// output_format=webp 是合法用户值，Offering 的 forced_when 必须改写为 png；
	// n=2 时上游返回两张图，适配器应保留全部产物。
	offering, input := normalizedInput(t, c, "openai/gpt-image-2/text-to-image",
		`{"prompt":"a red fox in snow","size":"1024x1024","quality":"high","n":2,"output_format":"webp"}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/images/generations" {
			t.Errorf("method/path = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type = %q", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("Authorization") != "Bearer official-key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
			http.Error(w, "decode body", http.StatusBadRequest)
			return
		}
		if body["model"] != "gpt-image-2.5-sunburst" {
			t.Errorf("model = %#v", body["model"])
		}
		if body["prompt"] != "a red fox in snow" {
			t.Errorf("prompt = %#v", body["prompt"])
		}
		if body["n"] != float64(2) {
			t.Errorf("n = %#v, want numeric 2", body["n"])
		}
		if body["size"] != "1024x1024" || body["quality"] != "high" {
			t.Errorf("declared enums changed: %#v", body)
		}
		if body["output_format"] != "png" {
			t.Errorf("output_format = %#v, want forced png", body["output_format"])
		}
		if _, exists := body["stream"]; exists {
			t.Errorf("stream must stay absent when the Offering does not declare it")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"created": 1760000000,
			"data": []any{
				map[string]any{
					"b64_json":       base64.StdEncoding.EncodeToString(officialPNG),
					"revised_prompt": "a red fox in snow",
				},
				map[string]any{
					"b64_json":       base64.StdEncoding.EncodeToString(officialPNG),
					"revised_prompt": "a red fox in snow",
				},
			},
			"usage": map[string]any{"total_tokens": 100, "input_tokens": 10, "output_tokens": 90},
		})
	}))
	defer upstream.Close()
	observation, err := officialAdapter(t, upstream).Start(context.Background(), generation.StartRequest{
		Offering: offering, Input: input, AttemptID: "openai-generation",
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.Phase != generation.UpstreamCompleted || len(observation.Artifacts) != 2 {
		t.Fatalf("observation = %#v", observation)
	}
	for _, artifact := range observation.Artifacts {
		if !bytes.Equal(artifact.Inline, officialPNG) || artifact.MediaType != "image/png" || artifact.Locator != "" {
			t.Fatalf("artifact = %#v", artifact)
		}
	}
}

func TestOpenAIImagesInvalidInputsAreRejectedByNormalization(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	generationOffering := officialOffering(t, c, "openai/gpt-image-2/text-to-image")
	edits := officialOffering(t, c, "openai/gpt-image-2/image-to-image")
	for _, tc := range []struct {
		name       string
		offering   generation.Offering
		raw        string
		wantDetail string
	}{
		{"generation missing required prompt", generationOffering, `{"size":"1024x1024"}`, `缺少必填参数 "prompt"`},
		{"generation wrong number type", generationOffering, `{"prompt":"fox","n":"two"}`, `参数 "n" 必须是`},
		{"generation unknown quality enum", generationOffering, `{"prompt":"fox","quality":"ultra"}`, `参数 "quality"`},
		{"generation unknown background enum", generationOffering, `{"prompt":"fox","background":"hologram"}`, `参数 "background"`},
		{"generation size not divisible by 16", generationOffering, `{"prompt":"fox","size":"1000x1000"}`, "宽高必须为 16 的倍数"},
		{"edits missing required reference", edits, `{"prompt":"edit"}`, `缺少必填参数 "reference_assets"`},
		{"edits wrong reference type", edits, `{"prompt":"edit","reference_assets":"a"}`, `参数 "reference_assets" 必须是数组`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireNormalizationRejection(t, c, tc.offering, tc.raw, tc.wantDetail)
		})
	}
}

func TestOpenAIImagesMissingResultIsNotSuccess(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, input := normalizedInput(t, c, "openai/gpt-image-2/text-to-image", `{"prompt":"fox"}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"created": 1760000000, "data": []any{}})
	}))
	defer upstream.Close()
	_, err = officialAdapter(t, upstream).Start(context.Background(), generation.StartRequest{
		Offering: offering, Input: input, AttemptID: "openai-empty",
	})
	requireProtocolFailure(t, err)
}

func TestSeedreamGenerationMatchesOfficialContract(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, input := normalizedInput(t, c, "jimeng/seedream-4-5/text-to-image",
		`{"prompt":"水墨山水","size":"2K","sequential_image_generation":"auto","max_images":3}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v3/images/generations" {
			t.Errorf("method/path = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type = %q", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("Authorization") != "Bearer official-key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
			http.Error(w, "decode body", http.StatusBadRequest)
			return
		}
		if body["model"] != "doubao-seedream-4-5-251128" || body["prompt"] != "水墨山水" {
			t.Errorf("model/prompt = %#v", body)
		}
		// #267：由模型自行决定比例的 2K/4K 已下线，旧取值经 legacy_values 落到明确的
		// 1:1 最高档宽x高，上游收到的是固定比例的像素尺寸。
		if body["size"] != "4096x4096" {
			t.Errorf("size = %#v, want explicit 4096x4096", body["size"])
		}
		if body["sequential_image_generation"] != "auto" {
			t.Errorf("sequential enum = %#v", body["sequential_image_generation"])
		}
		options, _ := body["sequential_image_generation_options"].(map[string]any)
		if options["max_images"] != float64(3) {
			t.Errorf("max_images = %#v, want numeric 3", options["max_images"])
		}
		// Ark 默认 response_format=url、watermark=true；产品显式改为内嵌、无水印。
		if body["response_format"] != "b64_json" || body["watermark"] != false {
			t.Errorf("result transport = %#v", body)
		}
		if promptOptions, _ := body["optimize_prompt_options"].(map[string]any); promptOptions["mode"] != "standard" {
			t.Errorf("optimize_prompt_options = %#v", body["optimize_prompt_options"])
		}
		if _, exists := body["image"]; exists {
			t.Errorf("text-to-image must not send reference images")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"created": 1760000000,
			"model":   "doubao-seedream-4-5-251128",
			"data": []any{map[string]any{
				"b64_json": base64.StdEncoding.EncodeToString(officialJPEG),
				"size":     "4096x4096",
			}},
			"usage": map[string]any{"generated_images": 1, "output_tokens": 100, "total_tokens": 100},
		})
	}))
	defer upstream.Close()
	observation, err := officialAdapter(t, upstream).Start(context.Background(), generation.StartRequest{
		Offering: offering, Input: input, AttemptID: "seedream-generation",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(observation.Artifacts) != 1 || !bytes.Equal(observation.Artifacts[0].Inline, officialJPEG) || observation.Artifacts[0].MediaType != "image/jpeg" {
		t.Fatalf("observation = %#v", observation)
	}
}

func TestSeedreamImageToImageMatchesOfficialContract(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, input := normalizedInput(t, c, "jimeng/seedream-4-5/image-to-image",
		`{"prompt":"把天空换成晚霞","reference_assets":["ref"],"sequential_image_generation":"disabled"}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v3/images/generations" {
			t.Errorf("method/path = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type = %q", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("Authorization") != "Bearer official-key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
			http.Error(w, "decode body", http.StatusBadRequest)
			return
		}
		if body["model"] != "doubao-seedream-4-5-251128" || body["prompt"] != "把天空换成晚霞" {
			t.Errorf("model/prompt = %#v", body)
		}
		images, ok := body["image"].([]any)
		if !ok || len(images) != 1 {
			t.Errorf("image references = %#v", body["image"])
			http.Error(w, "image references", http.StatusBadRequest)
			return
		}
		uri, _ := images[0].(string)
		const prefix = "data:image/png;base64,"
		if !strings.HasPrefix(uri, prefix) {
			t.Errorf("reference is not an official data URI: %q", uri)
			http.Error(w, "reference", http.StatusBadRequest)
			return
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, prefix))
		if err != nil || !bytes.Equal(decoded, officialReferencePNG) {
			t.Errorf("reference bytes = %q, err=%v", decoded, err)
			http.Error(w, "reference bytes", http.StatusBadRequest)
			return
		}
		if _, exists := body["sequential_image_generation_options"]; exists {
			t.Errorf("max_images must be omitted when sequential generation is disabled")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"created": 1760000000,
			"model":   "doubao-seedream-4-5-251128",
			"data":    []any{map[string]any{"b64_json": base64.StdEncoding.EncodeToString(officialJPEG)}},
		})
	}))
	defer upstream.Close()
	observation, err := officialAdapterAt(t, upstream, upstream.URL,
		officialAssetStore{"ref": officialReferencePNG}).Start(context.Background(), generation.StartRequest{
		Offering:    offering,
		Input:       input,
		AttemptID:   "seedream-edits",
		InputAssets: map[string][]generation.InputAsset{"reference_assets": {{ID: "ref", MediaType: "image/png"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(observation.Artifacts) != 1 ||
		!bytes.Equal(observation.Artifacts[0].Inline, officialJPEG) ||
		observation.Artifacts[0].MediaType != "image/jpeg" ||
		observation.Artifacts[0].Locator != "" {
		t.Fatalf("observation = %#v", observation)
	}
}

func TestSeedreamInvalidInputsAreRejectedByNormalization(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	generationOffering := officialOffering(t, c, "jimeng/seedream-4-5/text-to-image")
	edits := officialOffering(t, c, "jimeng/seedream-4-5/image-to-image")
	tooMany := make([]string, 15)
	for i := range tooMany {
		tooMany[i] = "ref"
	}
	tooManyJSON, _ := json.Marshal(map[string]any{"prompt": "edit", "reference_assets": tooMany})
	t.Run("edits above reference limit", func(t *testing.T) {
		_, normalized, err := c.Normalize(edits.Capability, edits.ID, tooManyJSON)
		reason := schemacontract.UserFacingError(err)
		if !schemacontract.IsCode(err, schemacontract.ErrInvalidInput) || normalized != nil || reason == nil {
			t.Fatalf("overflow must refuse normalization with a safe reason: input=%s err=%v", normalized, err)
		}
		if !strings.Contains(reason.Detail, "最多接受 14 张图片，这次提供了 15 张") ||
			strings.Contains(reason.Detail, "reference_assets") || reason.DetailTemplate == "" {
			t.Fatalf("reference limit reason = %#v", reason)
		}
		if reason.Cause == nil || !strings.Contains(reason.Cause.Error(), `"reference_assets"`) {
			t.Fatalf("internal diagnostic was lost: %v", reason.Cause)
		}
	})
	for _, tc := range []struct {
		name       string
		offering   generation.Offering
		raw        string
		wantDetail string
	}{
		{"generation missing required prompt", generationOffering, `{"size":"4K"}`, `缺少必填参数 "prompt"`},
		{"generation unknown sequential enum", generationOffering, `{"prompt":"fox","sequential_image_generation":"always"}`, `参数 "sequential_image_generation"`},
		{"generation max_images out of range", generationOffering, `{"prompt":"fox","sequential_image_generation":"auto","max_images":16}`, `"max_images"`},
		{"generation max_images wrong type", generationOffering, `{"prompt":"fox","sequential_image_generation":"auto","max_images":"three"}`, `参数 "max_images"`},
		{"generation size below pixel floor", generationOffering, `{"prompt":"fox","size":"1000x1000"}`, "总像素必须在"},
		{"edits missing required reference", edits, `{"prompt":"edit"}`, `缺少必填参数 "reference_assets"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireNormalizationRejection(t, c, tc.offering, tc.raw, tc.wantDetail)
		})
	}
}

// Gemini 图片经 llms 中转的 chat/completions 返回，图片在
// choices[].message.content[].image_url.url；正例已由 adapter_test 的
// TestGeminiChatImagesRoundTrip 覆盖，这里补结构错误的负控，并断言不同
// 失败分支的 safe detail 可区分。
func TestGeminiRelayMalformedResponsesFail(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, input := normalizedInput(t, c, "gemini/gemini-3-pro-image/text-to-image", `{"prompt":"狐狸"}`)
	for _, tc := range []struct {
		name       string
		body       string
		wantDetail string
	}{
		{"missing choices", `{}`, "上游完成但未返回图片"},
		{"empty choices", `{"choices":[]}`, "上游完成但未返回图片"},
		{"invalid image encoding", `{"choices":[{"message":{"content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,!!!"}}]}}]}`, "上游图片编码无效"},
		{"non-image payload", `{"choices":[{"message":{"content":[{"type":"image_url","image_url":{"url":"data:text/plain;base64,aGVsbG8="}}]}}]}`, "上游未返回有效图片"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
					t.Errorf("method/path = %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer official-key" {
					t.Errorf("authorization = %q", r.Header.Get("Authorization"))
				}
				_, _ = io.WriteString(w, tc.body)
			}))
			defer upstream.Close()
			_, err := officialAdapterAt(t, upstream, upstream.URL+"/v1",
				officialAssetStore{}).Start(context.Background(), generation.StartRequest{
				Offering: offering, Input: input, AttemptID: "gemini-malformed",
			})
			if failure := requireProtocolFailure(t, err); failure.SafeDetail != tc.wantDetail {
				t.Fatalf("safe detail = %q, want %q", failure.SafeDetail, tc.wantDetail)
			}
		})
	}
}
