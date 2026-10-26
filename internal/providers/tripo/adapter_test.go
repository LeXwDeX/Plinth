package tripo_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
)

func TestTextTaskMapsBusinessEnvelopeAndAssets(t *testing.T) {
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/generation/text-to-model":
			var body map[string]any
			_ = json.NewDecoder(request.Body).Decode(&body)
			if body["model"] != "v-test" ||
				body["texture"] != false ||
				body["geometry_quality"] != "detailed" ||
				body["output_formats"] != nil ||
				body["authorize_extra_credits"] != nil {
				t.Errorf("body = %#v", body)
			}
			writeEnvelope(writer, map[string]string{"task_id": "tripo-42"})
		case "/tasks/tripo-42":
			writeEnvelope(writer, map[string]any{
				"task_id":  "tripo-42",
				"status":   "success",
				"progress": 100,
				"output": map[string]string{
					"pbr_model_url":      upstream.URL + "/model.glb",
					"rendered_image_url": upstream.URL + "/preview.webp",
				},
			})
		case "/model.glb":
			writer.Header().Set("Content-Type", "model/gltf-binary")
			_, _ = writer.Write([]byte("glTF tripo"))
		case "/preview.webp":
			_, _ = writer.Write([]byte("preview"))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer upstream.Close()
	host := hostname(t, upstream.URL)
	errorMapping, statusMapping := declaredTripoMappings(t)
	adapter, err := tripo.New(tripo.Config{
		BaseURL:         upstream.URL,
		APIKey:          "tripo-key",
		HTTPClient:      upstream.Client(),
		AllowHTTPAssets: true,
		ErrorMapping:    errorMapping,
		StatusMapping:   statusMapping,
	})
	if err != nil {
		t.Fatal(err)
	}
	offering := generation.Offering{
		ProviderID:      "tripo",
		Capability:      generation.TextTo3D,
		PollInterval:    time.Second,
		AssetHosts:      []string{host},
		Cost:            &generation.CostDeclaration{SelectionParameter: "output_formats", ConsentParameter: "authorize_extra_credits"},
		ProviderOptions: []byte(`{"model":"v-test","endpoint":"generation/text-to-model","observe_endpoint":"tasks/{task_id}","upload_endpoint":"files"}`),
	}
	// W2：forced_when 由 catalog.Normalize 服务端裁决，adapter 原样转发规范化输入。
	normalized := normalizeTripoInput(t, generation.TextTo3D,
		`{"prompt":"ship","generate_parts":true,"texture":true,"authorize_extra_credits":true}`)
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		AttemptID:     "attempt-local",
		SubmissionKey: "submission-local",
		Offering:      offering,
		Input:         normalized,
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Job == nil || started.Job.RemoteID != "tripo-42" {
		t.Fatalf("started = %#v", started)
	}
	completed, err := adapter.Observe(context.Background(), generation.ObserveRequest{
		AttemptID: "attempt-local",
		Offering:  offering,
		Job:       *started.Job,
	})
	if err != nil {
		t.Fatal(err)
	}
	if completed.Phase != generation.UpstreamCompleted ||
		len(completed.Artifacts) != 2 ||
		completed.Artifacts[0].Role != "model" {
		t.Fatalf("completed = %#v", completed)
	}
	body, _, err := adapter.OpenAsset(context.Background(), generation.OpenAssetRequest{
		Offering: offering,
		Claim:    completed.Artifacts[0],
	})
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	content, _ := io.ReadAll(body)
	if string(content) != "glTF tripo" {
		t.Fatalf("content = %q", content)
	}
}

func TestImageTaskUploadsInputAndUsesFileToken(t *testing.T) {
	image := []byte("\x89PNG\r\n\x1a\ntripo-input")
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/files":
			part, err := nextMultipartFile(request)
			if err != nil {
				t.Error(err)
				return
			}
			content, _ := io.ReadAll(part)
			if !bytes.Equal(content, image) {
				t.Errorf("upload = %q", content)
			}
			writeEnvelope(writer, map[string]string{"file_token": "file-token-7"})
		case "/generation/image-to-model":
			var body map[string]any
			_ = json.NewDecoder(request.Body).Decode(&body)
			if body["input"] != "file-token-7" {
				t.Errorf("body = %#v", body)
			}
			if _, leaked := body["reference_assets"]; leaked {
				t.Errorf("platform asset IDs leaked: %#v", body)
			}
			writeEnvelope(writer, map[string]string{"task_id": "tripo-image-8"})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer upstream.Close()
	inputs := memoryInputAssets{content: image}
	adapter, err := tripo.New(tripo.Config{
		BaseURL:     upstream.URL,
		APIKey:      "tripo-key",
		HTTPClient:  upstream.Client(),
		InputAssets: inputs,
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		AttemptID:     "attempt-image",
		SubmissionKey: "submission-image",
		Offering: generation.Offering{
			ProviderID:      "tripo",
			Capability:      generation.ImageTo3D,
			ProviderOptions: []byte(`{"model":"v-test","endpoint":"generation/image-to-model","observe_endpoint":"tasks/{task_id}","upload_endpoint":"files"}`),
		},
		Input: []byte(`{"reference_assets":["asset-input"],"texture":true}`),
		InputAssets: map[string][]generation.InputAsset{
			"reference_assets": {{
				ID:        "asset-input",
				MediaType: "image/png",
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Job == nil || started.Job.RemoteID != "tripo-image-8" {
		t.Fatalf("started = %#v", started)
	}
}

func TestBusinessRateLimitIsRetryable(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"code":    1007,
			"message": "rate limit exceeded",
			"data":    map[string]any{},
		})
	}))
	defer upstream.Close()
	errorMapping, statusMapping := declaredTripoMappings(t)
	adapter, err := tripo.New(tripo.Config{
		BaseURL:       upstream.URL,
		APIKey:        "tripo-key",
		HTTPClient:    upstream.Client(),
		ErrorMapping:  errorMapping,
		StatusMapping: statusMapping,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Start(context.Background(), generation.StartRequest{
		Offering: generation.Offering{
			Capability:      generation.TextTo3D,
			ProviderOptions: []byte(`{"model":"v-test","endpoint":"generation/text-to-model","observe_endpoint":"tasks/{task_id}","upload_endpoint":"files"}`),
		},
		Input: []byte(`{"prompt":"ship"}`),
	})
	var providerErr *generation.ProviderError
	if !errors.As(err, &providerErr) ||
		providerErr.Failure.Class != generation.FailureRateLimited ||
		!providerErr.Failure.Retryable {
		t.Fatalf("error = %#v", err)
	}
}

func writeEnvelope(writer http.ResponseWriter, data any) {
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"code":    0,
		"message": "ok",
		"data":    data,
	})
}

func nextMultipartFile(request *http.Request) (io.Reader, error) {
	reader, err := request.MultipartReader()
	if err != nil {
		return nil, err
	}
	for {
		part, err := reader.NextPart()
		if err != nil {
			return nil, err
		}
		if part.FormName() == "file" {
			return part, nil
		}
		part.Close()
	}
}

func hostname(t *testing.T, rawURL string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Hostname()
}

type memoryInputAssets struct {
	content []byte
}

func (memoryInputAssets) Resolve(
	context.Context,
	generation.OwnerID,
	generation.Offering,
	json.RawMessage,
) (map[string][]generation.InputAsset, error) {
	return nil, nil
}

func (m memoryInputAssets) Open(
	context.Context,
	generation.InputAsset,
) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(m.content)), nil
}

// TestTripoClaimsStayLegacyRoleAlignedWithCatalog 覆盖 W1 边界：
// Tripo 产物保持 role-only（无变体），当前目录的 tripo offering 输出
// 仍是旧语义（thumbnail/texture 显式声明移至 W6），因此旧匹配规则
// （任意变体）与产物天然对齐。
func TestTripoClaimsStayLegacyRoleAlignedWithCatalog(t *testing.T) {
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/generation/text-to-model":
			writeEnvelope(writer, map[string]string{"task_id": "tripo-77"})
		case "/tasks/tripo-77":
			writeEnvelope(writer, map[string]any{
				"task_id":  "tripo-77",
				"status":   "success",
				"progress": 100,
				"output": map[string]string{
					"pbr_model_url":      upstream.URL + "/model.glb",
					"rendered_image_url": upstream.URL + "/preview.webp",
				},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer upstream.Close()
	host := hostname(t, upstream.URL)
	errorMapping, statusMapping := declaredTripoMappings(t)
	adapter, err := tripo.New(tripo.Config{
		BaseURL:         upstream.URL,
		APIKey:          "tripo-key",
		HTTPClient:      upstream.Client(),
		AllowHTTPAssets: true,
		ErrorMapping:    errorMapping,
		StatusMapping:   statusMapping,
	})
	if err != nil {
		t.Fatal(err)
	}
	offering := generation.Offering{
		ProviderID:      "tripo",
		Capability:      generation.TextTo3D,
		PollInterval:    time.Second,
		AssetHosts:      []string{host},
		ProviderOptions: []byte(`{"model":"v-test","endpoint":"generation/text-to-model","observe_endpoint":"tasks/{task_id}","upload_endpoint":"files"}`),
	}
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		AttemptID:     "attempt-tripo-legacy",
		SubmissionKey: "submission-tripo-legacy",
		Offering:      offering,
		Input:         []byte(`{"prompt":"legacy alignment"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := adapter.Observe(context.Background(), generation.ObserveRequest{
		AttemptID: "attempt-tripo-legacy",
		Offering:  offering,
		Job:       *started.Job,
	})
	if err != nil {
		t.Fatal(err)
	}
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	declared, exists := taskCatalog.Get("tripo/tripo-v3.1/text-to-3d")
	if !exists {
		t.Fatal("tripo text-to-3d offering missing from default catalog")
	}
	for _, output := range declared.Outputs {
		if output.Role == "model" &&
			(output.Variant != "" || output.VariantPrefix != "" ||
				output.Delivery != "") {
			t.Fatalf(
				"tripo 主产物声明必须保持旧语义（W6 不收紧门槛）: %#v",
				output,
			)
		}
	}
	// W6 合法显式声明：converted. on-demand 槽位（生成期格式多选消费者）
	// 与缩略图 background 槽位（variant 与响应别名一致）；除此之外不得
	// 出现其他显式声明。
	declaredSlots := map[string]bool{}
	for _, output := range declared.Outputs {
		if output.Role == "model" {
			continue
		}
		declaredSlots[output.Role+"|"+output.Variant+output.VariantPrefix+"|"+output.Delivery] = true
	}
	if !declaredSlots["converted_model|converted.|on-demand"] {
		t.Fatalf("tripo offering 缺少 W6 converted. on-demand 声明: %#v", declared.Outputs)
	}
	if !declaredSlots["thumbnail|rendered_image|background"] {
		t.Fatalf("tripo offering 缺少 W6 thumbnail background 声明: %#v", declared.Outputs)
	}
	if len(declaredSlots) != 2 {
		t.Fatalf("tripo offering 出现未登记的显式声明: %#v", declared.Outputs)
	}
	declaredRoles := map[string]bool{}
	for _, output := range declared.Outputs {
		declaredRoles[output.Role] = true
	}
	sawDeclaredModel := false
	for _, artifact := range completed.Artifacts {
		if artifact.Variant != "" {
			t.Fatalf("tripo claim gained variant early: %#v", artifact)
		}
		// 未声明 role（如 thumbnail）按现状作为不约束的额外产物，
		// 不参与成功门槛；声明的 role 必须保持旧语义匹配。
		if !declaredRoles[artifact.Role] {
			continue
		}
		if artifact.Role == "model" {
			sawDeclaredModel = true
		}
	}
	if !sawDeclaredModel {
		t.Fatal("tripo claims missing declared model artifact")
	}
}

// normalizeTripoInput 走真实 Product Schema 的规范化路径（含 forced_when）。
func normalizeTripoInput(
	t *testing.T,
	capability generation.Capability,
	raw string,
) json.RawMessage {
	t.Helper()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offeringID := generation.OfferingID("tripo/tripo-v3.1-full/" + map[generation.Capability]string{
		generation.TextTo3D:  "text-to-3d",
		generation.ImageTo3D: "image-to-3d",
	}[capability])
	_, normalized, err := taskCatalog.Normalize(
		capability,
		offeringID,
		json.RawMessage(raw),
	)
	if err != nil {
		t.Fatal(err)
	}
	return normalized
}
