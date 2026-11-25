package httpapi_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/accesskeys"
	"github.com/LeXwDeX/Plinth/internal/accountsettings"
	"github.com/LeXwDeX/Plinth/internal/adapters/catalogrecipes"
	"github.com/LeXwDeX/Plinth/internal/adapters/legacygeneration"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/administration"
	agentmock "github.com/LeXwDeX/Plinth/internal/agents/mock"
	"github.com/LeXwDeX/Plinth/internal/agentscope"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/assetdelivery"
	"github.com/LeXwDeX/Plinth/internal/assetlist"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/legacygenerated"
	"github.com/LeXwDeX/Plinth/internal/assets/legacyinput"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/assets/modelsource"
	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/cachemanagement"
	"github.com/LeXwDeX/Plinth/internal/canvas"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/chat/imagereview"
	"github.com/LeXwDeX/Plinth/internal/chat/references"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
	"github.com/LeXwDeX/Plinth/internal/lineage"
	"github.com/LeXwDeX/Plinth/internal/listcrop"
	"github.com/LeXwDeX/Plinth/internal/modeldelivery"
	"github.com/LeXwDeX/Plinth/internal/pendingapproval"
	"github.com/LeXwDeX/Plinth/internal/processing"
	"github.com/LeXwDeX/Plinth/internal/project"
	"github.com/LeXwDeX/Plinth/internal/promptenhance"
	"github.com/LeXwDeX/Plinth/internal/provideraccounts"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/assetrecovery"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
	"github.com/LeXwDeX/Plinth/internal/taskreroll"
	"github.com/LeXwDeX/Plinth/internal/taskretry"
	"github.com/LeXwDeX/Plinth/internal/threeview"
	"github.com/LeXwDeX/Plinth/internal/transport/httpapi"
)

// integrationProjectSource is the narrow chat port over the real project
// service. It returns the project ID as the frozen context; the generation
// tests that need style inheritance use the app assembly instead.
type integrationProjectSource struct{ service *project.Service }

func (source integrationProjectSource) Exists(ctx context.Context, ownerID, projectID string) (bool, error) {
	return source.service.Exists(ctx, ownerID, projectID)
}

func (source integrationProjectSource) Context(ctx context.Context, ownerID, projectID string) (chat.ProjectContext, error) {
	exists, err := source.service.Exists(ctx, ownerID, projectID)
	if err != nil {
		return chat.ProjectContext{}, err
	}
	if !exists {
		return chat.ProjectContext{}, chat.ErrProjectNotFound
	}
	value, err := source.service.Get(ctx, ownerID, projectID)
	if err != nil {
		return chat.ProjectContext{}, chat.ErrProjectNotFound
	}
	return chat.ProjectContext{ID: value.ID, Name: value.Name}, nil
}

func TestHTTPVerticalSliceUploadSubmitObserveAndDownload(t *testing.T) {
	t.Parallel()
	module, api, inputs := newHTTPHarness(t)
	server := httptest.NewServer(api)
	defer server.Close()

	assetID := uploadInputAsset(t, server.URL, "owner-a")
	resolved, err := inputs.Resolve(
		context.Background(),
		"owner-a",
		generation.Offering{Parameters: []generation.Parameter{{
			Name: "source_asset",
			Type: "asset",
		}}},
		[]byte(`{"source_asset":"`+assetID+`"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	externalURL, err := inputs.ExternalURL(
		context.Background(),
		resolved["source_asset"][0],
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	parsedExternalURL, _ := url.Parse(externalURL)
	response := doRequest(
		t,
		server.Client(),
		http.MethodGet,
		server.URL+parsedExternalURL.Path,
		"",
		"",
		nil,
	)
	if response.StatusCode != http.StatusOK ||
		len(readBody(t, response)) == 0 {
		t.Fatalf("signed Provider asset status = %d", response.StatusCode)
	}

	submitBody := `{
		"capability":"image-to-image",
		"offering_id":"openai/gpt-image-2/image-to-image",
		"input":{"prompt":"restyle","reference_assets":["` + assetID + `"]}
	}`
	response = doRequest(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/api/v1/tasks",
		"owner-a",
		"application/json",
		strings.NewReader(submitBody),
	)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("bare submit status = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	accepted, err := submitHarnessTask(t, module, context.Background(), generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.ImageToImage,
		OfferingID: "openai/gpt-image-2/image-to-image",
		Input:      json.RawMessage(`{"prompt":"restyle","reference_assets":["` + assetID + `"]}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	for range 8 {
		_, err := module.ReconcileOnce(context.Background(), generation.CoordinatorConfig{
			WorkerID:  "http-test",
			LeaseFor:  time.Minute,
			BatchSize: 10,
		})
		if err != nil {
			t.Fatal(err)
		}
		task, err := module.Get(context.Background(), "owner-a", accepted.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		if task.State.Terminal() {
			break
		}
		time.Sleep(12 * time.Millisecond)
	}
	response = doRequest(
		t,
		server.Client(),
		http.MethodGet,
		server.URL+"/api/v1/tasks/"+string(accepted.TaskID),
		"owner-a",
		"",
		nil,
	)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	payload := readBody(t, response)
	if bytes.Contains(payload, []byte("owner-a")) ||
		bytes.Contains(payload, []byte("storage_key")) ||
		bytes.Contains(payload, []byte(`"failure"`)) {
		t.Fatalf("private persistence fields leaked: %s", payload)
	}
	var task struct {
		State  generation.TaskState `json:"state"`
		Assets []struct {
			ID  generation.AssetID `json:"id"`
			URL string             `json:"url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(payload, &task); err != nil {
		t.Fatal(err)
	}
	if task.State != generation.TaskSucceeded || len(task.Assets) != 1 {
		t.Fatalf("task = %#v", task)
	}
	response = doRequest(
		t,
		server.Client(),
		http.MethodGet,
		server.URL+task.Assets[0].URL,
		"owner-a",
		"",
		nil,
	)
	if response.StatusCode != http.StatusOK || len(readBody(t, response)) == 0 {
		t.Fatalf("asset status = %d", response.StatusCode)
	}

	modelAccepted, err := submitHarnessTask(t, module,
		context.Background(),
		generation.SubmitCommand{
			OwnerID:    "owner-a",
			Capability: generation.TextTo3D,
			OfferingID: "meshy/meshy-7.1/text-to-3d",
			Input:      []byte(`{"prompt":"model for remesh"}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var modelTask generation.Task
	for range 8 {
		_, err = module.ReconcileOnce(
			context.Background(),
			generation.CoordinatorConfig{
				WorkerID:  "http-test-chain",
				LeaseFor:  time.Minute,
				BatchSize: 10,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		modelTask, err = module.Get(
			context.Background(),
			"owner-a",
			modelAccepted.TaskID,
		)
		if err != nil {
			t.Fatal(err)
		}
		if modelTask.State.Terminal() {
			break
		}
		time.Sleep(12 * time.Millisecond)
	}
	if modelTask.State != generation.TaskSucceeded ||
		len(modelTask.Assets) != 6 {
		t.Fatalf("model task = %#v", modelTask)
	}
	secondBody := `{
		"capability":"remesh",
		"offering_id":"meshy/processing/remesh",
		"input":{"source_asset":"` +
		string(modelTask.Assets[0].ID) + `"}
	}`
	response = doRequest(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/api/v1/tasks",
		"owner-a",
		"application/json",
		strings.NewReader(secondBody),
	)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf(
			"bare remesh submit status = %d, body = %s",
			response.StatusCode,
			readBody(t, response),
		)
	}
	response.Body.Close()
	if _, err := submitHarnessTask(t, module, context.Background(), generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.Remesh,
		OfferingID: "meshy/processing/remesh",
		Input:      json.RawMessage(`{"source_asset":"` + string(modelTask.Assets[0].ID) + `"}`),
	}); err != nil {
		t.Fatalf("owned output Asset could not be reused: %v", err)
	}
	if _, err := submitHarnessTask(t, module, context.Background(), generation.SubmitCommand{
		OwnerID: "owner-b", Capability: generation.Remesh,
		OfferingID: "meshy/processing/remesh",
		Input:      json.RawMessage(`{"source_asset":"` + string(modelTask.Assets[0].ID) + `"}`),
	}); err == nil {
		t.Fatal("cross-owner output Asset was accepted")
	}
	response = doRequest(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/api/v1/tasks",
		"owner-b",
		"application/json",
		strings.NewReader(secondBody),
	)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf(
			"cross-owner output Asset status = %d, body = %s",
			response.StatusCode,
			readBody(t, response),
		)
	}
	response.Body.Close()

	response = doRequest(
		t,
		server.Client(),
		http.MethodGet,
		server.URL+"/api/v1/tasks/"+string(accepted.TaskID),
		"owner-b",
		"",
		nil,
	)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-owner status = %d", response.StatusCode)
	}
	response.Body.Close()
}

func TestAccountResourcesAreDerivedFromAuthenticatedOwner(t *testing.T) {
	t.Parallel()
	module, api, inputs := newHTTPHarnessWithRoles(t, []string{"user"})
	localAsset, err := inputs.UploadAsset(context.Background(), "local-user", generation.InputAssetUpload{
		Body: strings.NewReader("local asset"), OriginalFilename: "local.png", MediaType: "image/png", AssetType: "image", MaxBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	otherAsset, err := inputs.UploadAsset(context.Background(), "other-user", generation.InputAssetUpload{
		Body: strings.NewReader("private asset"), OriginalFilename: "private.png", MediaType: "image/png", AssetType: "image", MaxBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"local-user", "other-user"} {
		if _, err := submitHarnessTask(t, module, context.Background(), generation.SubmitCommand{
			OwnerID: generation.OwnerID(owner), Capability: generation.TextToImage,
			OfferingID: "openai/gpt-image-2/text-to-image", Input: json.RawMessage(`{"prompt":"private"}`),
		}); err != nil {
			t.Fatal(err)
		}
	}

	resources := httptest.NewRecorder()
	api.ServeHTTP(resources, httptest.NewRequest(http.MethodGet,
		"/api/v1/account/resources?user_id=other-user&page=1&page_size=20", nil))
	if resources.Code != http.StatusOK {
		t.Fatalf("account resources status=%d body=%s", resources.Code, resources.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(resources.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	uploads := body["uploads"].(map[string]any)["data"].([]any)
	if len(uploads) != 1 || uploads[0].(map[string]any)["id"] != string(localAsset.ID) {
		t.Fatalf("account uploads escaped owner scope: %#v", body["uploads"])
	}
	if _, ok := uploads[0].(map[string]any)["storage_key"]; ok {
		t.Fatalf("storage key leaked: %#v", uploads[0])
	}
	tasks := body["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("account tasks escaped owner scope: %#v", tasks)
	}

	usage := httptest.NewRecorder()
	api.ServeHTTP(usage, httptest.NewRequest(http.MethodGet,
		"/api/v1/account/usage?user_id=other-user", nil))
	if usage.Code != http.StatusOK {
		t.Fatalf("account usage status=%d body=%s", usage.Code, usage.Body.String())
	}
	var usageBody map[string]any
	if err := json.Unmarshal(usage.Body.Bytes(), &usageBody); err != nil {
		t.Fatal(err)
	}
	if usageBody["user_id"] != "local-user" {
		t.Fatalf("caller-selected usage owner was honored: %#v", usageBody)
	}

	assetResponse := httptest.NewRecorder()
	api.ServeHTTP(assetResponse, httptest.NewRequest(http.MethodGet,
		"/api/v1/assets/"+string(localAsset.ID), nil))
	if assetResponse.Code != http.StatusOK {
		t.Fatalf("own upload download status=%d", assetResponse.Code)
	}
	otherAssetResponse := httptest.NewRecorder()
	api.ServeHTTP(otherAssetResponse, httptest.NewRequest(http.MethodGet,
		"/api/v1/assets/"+string(otherAsset.ID), nil))
	if otherAssetResponse.Code != http.StatusForbidden && otherAssetResponse.Code != http.StatusNotFound {
		t.Fatalf("cross-owner upload download status=%d body=%s", otherAssetResponse.Code, otherAssetResponse.Body.String())
	}
}

func TestTaskLocationsAssetsAndHistoryFollowProductSchemaRoutes(t *testing.T) {
	t.Parallel()
	module, api, _ := newHTTPHarnessWithApplication(
		t,
		nil,
		func(application *catalog.ApplicationSchema) {
			routes := &application.Runtime.HTTP.Routes
			routes.Tasks = "/configured/tasks"
			routes.TaskItem = "/configured/tasks/{taskID}"
			routes.TaskCancel = "/configured/tasks/{taskID}/cancel"
			routes.TaskEvents = "/configured/tasks/{taskID}/events"
			routes.TaskAsset =
				"/configured/tasks/{taskID}/files/{assetID}"
			routes.AssetItem = "/configured/assets/{assetID}"
		},
	)
	server := httptest.NewServer(api)
	defer server.Close()

	response := doRequest(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/configured/tasks",
		"owner-a",
		"application/json",
		strings.NewReader(`{
			"capability":"text-to-image",
			"offering_id":"openai/gpt-image-2/text-to-image",
			"input":{"prompt":"schema route"}
		}`),
	)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf(
			"bare submit status = %d, body = %s",
			response.StatusCode,
			readBody(t, response),
		)
	}
	accepted, err := submitHarnessTask(t, module, context.Background(), generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextToImage,
		OfferingID: "openai/gpt-image-2/text-to-image",
		Input:      json.RawMessage(`{"prompt":"schema route"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	expectedLocation := "/configured/tasks/" + string(accepted.TaskID)

	for range 8 {
		if _, err := module.ReconcileOnce(
			context.Background(),
			generation.CoordinatorConfig{
				WorkerID:  "schema-route-test",
				LeaseFor:  time.Minute,
				BatchSize: 10,
			},
		); err != nil {
			t.Fatal(err)
		}
		task, err := module.Get(
			context.Background(),
			"owner-a",
			accepted.TaskID,
		)
		if err != nil {
			t.Fatal(err)
		}
		if task.State.Terminal() {
			break
		}
		time.Sleep(12 * time.Millisecond)
	}

	response = doRequest(
		t,
		server.Client(),
		http.MethodGet,
		server.URL+expectedLocation,
		"owner-a",
		"",
		nil,
	)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("task status = %d", response.StatusCode)
	}
	var taskPayload struct {
		Assets []struct {
			ID  generation.AssetID `json:"id"`
			URL string             `json:"url"`
		} `json:"assets"`
	}
	decodeResponse(t, response, &taskPayload)
	if len(taskPayload.Assets) != 1 {
		t.Fatalf("assets = %#v", taskPayload.Assets)
	}
	expectedAssetURL := "/configured/tasks/" +
		string(accepted.TaskID) +
		"/files/" +
		string(taskPayload.Assets[0].ID)
	if taskPayload.Assets[0].URL != expectedAssetURL {
		t.Fatalf(
			"asset URL = %q, want %q",
			taskPayload.Assets[0].URL,
			expectedAssetURL,
		)
	}

	response = doRequest(
		t,
		server.Client(),
		http.MethodGet,
		server.URL+"/api/tasks?recent=1",
		"owner-a",
		"",
		nil,
	)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("history status = %d", response.StatusCode)
	}
	var history struct {
		Data []struct {
			ThumbnailURL string `json:"thumbnailUrl"`
		} `json:"data"`
	}
	decodeResponse(t, response, &history)
	if len(history.Data) != 1 ||
		history.Data[0].ThumbnailURL != expectedAssetURL {
		t.Fatalf("history = %#v", history.Data)
	}
}

func newHTTPHarness(
	t *testing.T,
) (*generation.Module, http.Handler, *inputassets.Service) {
	return newHTTPHarnessWithRoles(t, nil)
}

// httpMeshyFormatFixtureAdapter returns all six required Meshy 7.1 artifacts
// with self-contained model payloads. ZIP variants use actual ZIP framing.
// The compatibility tests exercise the
// public projection against the current required-output contract.
type httpMeshyFormatFixtureAdapter struct{ *mock.Adapter }

func (a *httpMeshyFormatFixtureAdapter) Observe(
	ctx context.Context,
	request generation.ObserveRequest,
) (generation.Observation, error) {
	observation, err := a.Adapter.Observe(ctx, request)
	if err != nil || observation.Phase != generation.UpstreamCompleted {
		return observation, err
	}
	if request.PostSuccess {
		if request.Offering.ID == "tripo/tripo-v3.1/text-to-3d" {
			for index := range observation.Artifacts {
				claim := &observation.Artifacts[index]
				claim.Inline, claim.MediaType, err = httpFixtureModelPayload(claim.Variant, "model/gltf-binary")
				if err != nil {
					return observation, err
				}
			}
		}
		return observation, nil
	}
	if request.Offering.ID == "meshy/meshy-7.1/text-to-3d" {
		formats := []string{"glb", "obj", "fbx", "stl", "usdz", "3mf"}
		observation.Artifacts = make([]generation.ArtifactClaim, 0, len(formats))
		for _, format := range formats {
			inline, mediaType, err := httpFixtureModelPayload(format, "model/gltf-binary")
			if err != nil {
				return observation, err
			}
			observation.Artifacts = append(observation.Artifacts, generation.ArtifactClaim{
				ID:        string(request.AttemptID) + ":" + format,
				Role:      "model",
				Variant:   format,
				Inline:    inline,
				MediaType: mediaType,
			})
		}
		return observation, nil
	}
	if request.Offering.ID == "tripo/tripo-v3.1/text-to-3d" {
		// This test-only historical Offering snapshot models tasks persisted
		// before migration; provide a valid base model so legacy post-success
		// resource polling can be exercised without weakening current defaults.
		observation.Artifacts = nil
		for _, output := range request.Offering.Outputs {
			if output.Minimum == nil || *output.Minimum == 0 {
				continue
			}
			variant := output.Variant
			if variant == "" {
				variant = output.VariantPrefix + "historical"
			}
			mediaType := "model/gltf-binary"
			if len(output.MediaTypes) > 0 {
				mediaType = output.MediaTypes[0]
			}
			inline, mediaType, err := httpFixtureModelPayload(variant, mediaType)
			if err != nil {
				return observation, err
			}
			observation.Artifacts = append(observation.Artifacts, generation.ArtifactClaim{
				ID: string(request.AttemptID) + ":historical:" + variant, Role: output.Role,
				Variant: variant, Inline: inline, MediaType: mediaType,
			})
		}
		return observation, nil
	}
	if request.Offering.ID == "tripo/tripo-v3.1-full/text-to-3d" {
		artifacts := make([]generation.ArtifactClaim, 0, len(request.Offering.Outputs))
		for _, output := range request.Offering.Outputs {
			if output.Minimum == nil || *output.Minimum == 0 {
				continue
			}
			variant := output.Variant
			mediaType := "model/gltf-binary"
			if len(output.MediaTypes) > 0 {
				mediaType = output.MediaTypes[0]
			}
			inline, mediaType, err := httpFixtureModelPayload(variant, mediaType)
			if err != nil {
				return observation, err
			}
			artifacts = append(artifacts, generation.ArtifactClaim{
				ID:        string(request.AttemptID) + ":" + variant,
				Role:      output.Role,
				Variant:   variant,
				Inline:    inline,
				MediaType: mediaType,
			})
		}
		observation.Artifacts = artifacts
		return observation, nil
	}
	if request.Offering.ID != "meshy/processing/rigging" &&
		request.Offering.ID != "meshy/processing/remesh" &&
		request.Offering.ID != "tripo/tripo-v3.1/format-conversion" {
		return observation, nil
	}
	artifacts := make([]generation.ArtifactClaim, 0, len(request.Offering.Outputs))
	for _, output := range request.Offering.Outputs {
		if output.Role != "model" ||
			(output.Delivery != "" && output.Delivery != "required") ||
			output.Minimum == nil || *output.Minimum == 0 {
			continue
		}
		variant := output.Variant
		if output.VariantPrefix != "" {
			variant = output.VariantPrefix + "fixture"
		}
		mediaType := "model/gltf-binary"
		if len(output.MediaTypes) > 0 {
			mediaType = output.MediaTypes[0]
		}
		inline, mediaType, err := httpFixtureModelPayload(variant, mediaType)
		if err != nil {
			return observation, err
		}
		if output.Fidelity != "" {
			// 声明了保真门槛的产物必须带真实骨架结构，否则会被 Generation 拒收。
			inline = httpFixtureGLBDocument(`{"asset":{"version":"2.0"},"nodes":[{"name":"` + variant + `"}],"skins":[{"joints":[0]}]}`)
		}
		artifacts = append(artifacts, generation.ArtifactClaim{
			ID:        string(request.AttemptID) + ":" + variant,
			Role:      "model",
			Variant:   variant,
			Inline:    inline,
			MediaType: mediaType,
		})
	}
	observation.Artifacts = artifacts
	return observation, nil
}

func httpFixtureModelPayload(variant, mediaType string) ([]byte, string, error) {
	format := strings.TrimPrefix(variant, "converted.")
	if format == "stl" {
		return fixtureBinarySTL(), "model/stl", nil
	}
	if format != "3mf" && format != "usdz" {
		return httpFixtureGLB(variant), mediaType, nil
	}
	entries := []struct {
		header  *zip.FileHeader
		content string
	}{
		{&zip.FileHeader{Name: "[Content_Types].xml", Method: zip.Deflate}, `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="model" ContentType="application/vnd.ms-package.3dmanufacturing-3dmodel+xml"/></Types>`},
		{&zip.FileHeader{Name: "3D/3dmodel.model", Method: zip.Deflate}, `<model xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02"><resources/><build/></model>`},
	}
	mediaType = "application/zip"
	if strings.HasSuffix(variant, "usdz") {
		// USDZ stores entries without compression and aligns their payloads.
		header := &zip.FileHeader{Name: "scene.usda", Method: zip.Store, Extra: make([]byte, 24)}
		binary.LittleEndian.PutUint16(header.Extra[:2], 0x1986)
		binary.LittleEndian.PutUint16(header.Extra[2:4], 20)
		entries = entries[:0]
		entries = append(entries, struct {
			header  *zip.FileHeader
			content string
		}{header, "#usda 1.0\ndef Xform \"fixture\" {}\n"})
		mediaType = "model/vnd.usdz+zip"
	}
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for _, item := range entries {
		entry, err := archive.CreateHeader(item.header)
		if err != nil {
			return nil, "", err
		}
		if _, err := io.WriteString(entry, item.content); err != nil {
			return nil, "", err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, "", err
	}
	return buffer.Bytes(), mediaType, nil
}

// fixtureBinarySTL is one finite triangle in the exact binary STL layout.
func fixtureBinarySTL() []byte {
	body := make([]byte, 84+50)
	copy(body, "fixture binary stl")
	binary.LittleEndian.PutUint32(body[80:84], 1)
	for i, value := range []float32{0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1, 0} {
		binary.LittleEndian.PutUint32(body[84+4*i:], math.Float32bits(value))
	}
	return body
}

func httpFixtureGLB(label string) []byte {
	return httpFixtureGLBDocument(`{"asset":{"version":"2.0"},"extras":{"label":"` + label + `"}}`)
}

func httpFixtureGLBDocument(document string) []byte {
	body := []byte(document)
	for len(body)%4 != 0 {
		body = append(body, ' ')
	}
	asset := make([]byte, 20+len(body))
	copy(asset[:4], "glTF")
	binary.LittleEndian.PutUint32(asset[4:8], 2)
	binary.LittleEndian.PutUint32(asset[8:12], uint32(len(asset)))
	binary.LittleEndian.PutUint32(asset[12:16], uint32(len(body)))
	copy(asset[16:20], "JSON")
	copy(asset[20:], body)
	return asset
}

// fixtureExecutions exists only in tests. Read-side legacy tests need a real
// Task; this helper explicitly simulates the human decision through the same
// prepared-input/grant transaction, without adding a production bypass.
var fixtureExecutions sync.Map // *generation.Module -> *fixtureExecutionState

type fixtureExecutionState struct {
	repository *sqlite.Repository
	// databasePath lets a test write a row the HTTP contract has no route
	// for (an asset list item already working, #327).
	databasePath string
	// pending is the pending-approval service the harness wired into the API,
	// so a test can run its background sweep deterministically.
	pending *pendingapproval.Service
	chat    *chat.Service
	// beforeDecide, when set, runs inside the API's grant repository right
	// before it records a decision: a test uses it to land another action (a
	// Stop) between the handler's checks and the decision.
	beforeDecide func(requestID string, decision executiongrant.Decision)
	mu           sync.Mutex
	sequence     int
	sessions     map[generation.OwnerID]string
	approvals    map[string]fixtureApproval
}

// harnessCropInputs adapts the harness's input asset service and repository to
// listcrop.InputAssets (#331), mirroring internal/app's cropInputAssets: the
// service owns uploads and opens real uploads, the repository projects a
// finished Task's outputs.
type harnessCropInputs struct {
	inputs *inputassets.Service
	loader *sqlite.Repository
}

func (store harnessCropInputs) UploadAsset(ctx context.Context, owner generation.OwnerID,
	upload generation.InputAssetUpload,
) (generation.InputAsset, error) {
	return store.inputs.UploadAsset(ctx, owner, upload)
}

func (store harnessCropInputs) OpenOwned(ctx context.Context, owner generation.OwnerID,
	id generation.AssetID,
) (generation.InputAsset, io.ReadCloser, error) {
	return store.inputs.OpenOwned(ctx, owner, id)
}

func (store harnessCropInputs) LoadInputAssets(ctx context.Context, owner generation.OwnerID,
	ids []generation.AssetID,
) (map[generation.AssetID]generation.InputAsset, error) {
	return store.loader.LoadInputAssets(ctx, owner, ids)
}

// hookedGrantRepository is the API's grant repository with a test hook before
// each decision.
type hookedGrantRepository struct {
	executiongrant.Repository
	state *fixtureExecutionState
}

// The optional capabilities of the SQLite grant store that the external MCP
// service needs (#332), forwarded so wrapping the repository does not hide them.

func (r *hookedGrantRepository) PreparePendingForAccessKey(ctx context.Context, identity executiongrant.Identity, accessKeyID string) (executiongrant.Grant, error) {
	return r.Repository.(executiongrant.AccessKeyGrantStore).PreparePendingForAccessKey(ctx, identity, accessKeyID)
}

func (r *hookedGrantRepository) ApproveExecutionGrantWithSteps(ctx context.Context, owner, requestID string, request executiongrant.StepRequest) (executiongrant.Grant, executiongrant.StepDecision, error) {
	return r.Repository.(executiongrant.StepGrantStore).ApproveExecutionGrantWithSteps(ctx, owner, requestID, request)
}

func (r *hookedGrantRepository) StepUsage(ctx context.Context, owner, sessionID string, request executiongrant.StepRequest) (executiongrant.StepSummary, error) {
	return r.Repository.(executiongrant.StepGrantStore).StepUsage(ctx, owner, sessionID, request)
}

func (r *hookedGrantRepository) SetSessionExtraSteps(ctx context.Context, owner, sessionID string, request executiongrant.StepRequest, target int) (executiongrant.StepExtraChange, error) {
	return r.Repository.(executiongrant.StepExtraStore).SetSessionExtraSteps(ctx, owner, sessionID, request, target)
}

func (r *hookedGrantRepository) GrantByKey(ctx context.Context, owner, key string) (executiongrant.Grant, bool, error) {
	return r.Repository.(interface {
		GrantByKey(context.Context, string, string) (executiongrant.Grant, bool, error)
	}).GrantByKey(ctx, owner, key)
}

func (r *hookedGrantRepository) DecideExecutionGrant(ctx context.Context, owner, requestID, actor string,
	decision executiongrant.Decision) (executiongrant.Grant, error) {
	if hook := r.state.beforeDecide; hook != nil {
		hook(requestID, decision)
	}
	return r.Repository.DecideExecutionGrant(ctx, owner, requestID, actor, decision)
}

type fixtureApproval struct {
	requestHash string
	preparedID  generation.PreparedInputID
	requestID   string
	operationID string
}

func submitHarnessTask(t *testing.T, module *generation.Module, ctx context.Context, command generation.SubmitCommand) (generation.Accepted, error) {
	t.Helper()
	value, found := fixtureExecutions.Load(module)
	if !found {
		return generation.Accepted{}, fmt.Errorf("missing HTTP test fixture repository")
	}
	state := value.(*fixtureExecutionState)
	state.mu.Lock()
	defer state.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	if command.SessionID == "" {
		if state.sessions == nil {
			state.sessions = make(map[generation.OwnerID]string)
		}
		if state.sessions[command.OwnerID] == "" {
			state.sessions[command.OwnerID] = fmt.Sprintf("http-fixture-session-%d", len(state.sessions)+1)
		}
		command.SessionID = state.sessions[command.OwnerID]
	}
	owned, err := state.repository.OwnsSession(ctx, string(command.OwnerID), command.SessionID)
	if err != nil {
		return generation.Accepted{}, err
	}
	if !owned {
		now := time.Now().UTC()
		if _, err := state.repository.CreateSession(ctx, chat.Session{
			ID: command.SessionID, OwnerID: string(command.OwnerID),
			OwnerName: "Test human", Title: "HTTP test fixture",
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return generation.Accepted{}, err
		}
	}
	if command.IdempotencyKey == "" {
		state.sequence++
		command.IdempotencyKey = fmt.Sprintf("http-fixture:%d", state.sequence)
	}
	if state.approvals == nil {
		state.approvals = make(map[string]fixtureApproval)
	}
	cacheKey := string(command.OwnerID) + "\x00" + command.SessionID + "\x00" + command.IdempotencyKey
	inputHash := fmt.Sprintf("%x", sha256.Sum256(command.Input))
	approval, exists := state.approvals[cacheKey]
	if !exists || approval.requestHash != inputHash {
		prepared, err := module.PrepareInput(ctx, generation.PrepareInputCommand{
			OwnerID: command.OwnerID, SessionID: command.SessionID,
			Capability: command.Capability, OfferingID: command.OfferingID,
			Input: command.Input,
		})
		if err != nil {
			return generation.Accepted{}, err
		}
		grants := executiongrant.Service{Grants: state.repository, Prepared: state.repository}
		principal := executiongrant.Principal{
			OwnerID: string(command.OwnerID), ActorID: "http-test-human",
			Source: executiongrant.HumanBrowser,
		}
		operationID := standaloneFixtureOperationID(prepared.ID)
		grant, err := grants.Prepare(ctx, executiongrant.PrepareCommand{
			Principal: principal, SessionID: command.SessionID,
			OfferingID: string(prepared.OfferingID), OfferingRevision: prepared.OfferingRevision,
			PreparedInputID: string(prepared.ID), PreparedInputHash: prepared.InputHash,
			IdempotencyKey: command.IdempotencyKey, OperationID: operationID,
		})
		if err != nil {
			return generation.Accepted{}, err
		}
		if _, err := grants.Decide(ctx, principal, grant.RequestID, executiongrant.Approve); err != nil {
			return generation.Accepted{}, err
		}
		approval = fixtureApproval{requestHash: inputHash, preparedID: prepared.ID,
			requestID: grant.RequestID, operationID: operationID}
		state.approvals[cacheKey] = approval
	}
	return module.SubmitPrepared(ctx, generation.SubmitPreparedCommand{
		OwnerID: command.OwnerID, SessionID: command.SessionID,
		PreparedInputID: approval.preparedID, IdempotencyKey: command.IdempotencyKey,
		OperationID: approval.operationID, AuthorizationRef: approval.requestID,
	})
}

func standaloneFixtureOperationID(id generation.PreparedInputID) string {
	return "http-fixture:" + string(id)
}

func newHTTPHarnessWithRoles(
	t *testing.T,
	roles []string,
) (*generation.Module, http.Handler, *inputassets.Service) {
	return newHTTPHarnessWithApplication(t, roles, nil)
}

func newHTTPHarnessWithApplication(
	t *testing.T,
	roles []string,
	customize func(*catalog.ApplicationSchema),
) (*generation.Module, http.Handler, *inputassets.Service) {
	return newHTTPHarnessWithAuthentication(t, roles, customize, nil)
}

func newHTTPHarnessWithChatScheduler(
	t *testing.T,
	scheduler httpapi.ChatTurnScheduler,
	customize func(*catalog.ApplicationSchema),
) (*generation.Module, http.Handler, *inputassets.Service) {
	module, handler, inputs, _ := newHTTPHarnessWithServices(
		t,
		nil,
		customize,
		nil,
		scheduler,
		nil,
	)
	return module, handler, inputs
}

func newHTTPHarnessWithAuthentication(
	t *testing.T,
	roles []string,
	customize func(*catalog.ApplicationSchema),
	customizeAuthentication func(*authentication.Config),
) (*generation.Module, http.Handler, *inputassets.Service) {
	module, handler, inputs, _ := newHTTPHarnessWithServices(
		t,
		roles,
		customize,
		customizeAuthentication,
		nil,
		nil,
	)
	return module, handler, inputs
}

func newHTTPHarnessWithServices(
	t *testing.T,
	roles []string,
	customize func(*catalog.ApplicationSchema),
	customizeAuthentication func(*authentication.Config),
	chatScheduler httpapi.ChatTurnScheduler,
	configuredAccountReaders map[string]provideraccounts.BalanceReader,
	databaseSetups ...func(*testing.T, string),
) (
	*generation.Module,
	http.Handler,
	*inputassets.Service,
	*cachemanagement.Service,
) {
	return newHTTPHarnessWithCatalogOverride(
		t, roles, customize, customizeAuthentication, chatScheduler,
		configuredAccountReaders, nil, databaseSetups...,
	)
}

func newHTTPHarnessWithCatalogOverride(
	t *testing.T,
	roles []string,
	customize func(*catalog.ApplicationSchema),
	customizeAuthentication func(*authentication.Config),
	chatScheduler httpapi.ChatTurnScheduler,
	configuredAccountReaders map[string]provideraccounts.BalanceReader,
	taskCatalogOverride *catalog.Catalog,
	databaseSetups ...func(*testing.T, string),
) (*generation.Module, http.Handler, *inputassets.Service, *cachemanagement.Service) {
	t.Helper()
	return newHTTPHarnessWithCatalogAndProviders(t, roles, customize, customizeAuthentication,
		chatScheduler, configuredAccountReaders, taskCatalogOverride, nil, databaseSetups...)
}

// Additional providers exercise real wire adapters against local HTTP stubs;
// existing callers retain the fixture registry and its original behavior.
func newHTTPHarnessWithCatalogAndProviders(
	t *testing.T,
	roles []string,
	customize func(*catalog.ApplicationSchema),
	customizeAuthentication func(*authentication.Config),
	chatScheduler httpapi.ChatTurnScheduler,
	configuredAccountReaders map[string]provideraccounts.BalanceReader,
	taskCatalogOverride *catalog.Catalog,
	additionalProviders map[string]generation.ProviderPort,
	databaseSetups ...func(*testing.T, string),
) (*generation.Module, http.Handler, *inputassets.Service, *cachemanagement.Service) {
	t.Helper()
	root := t.TempDir()
	databasePath := filepath.Join(root, "test.db")
	repository, err := httpapi.OpenMigratedTestRepository(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, setup := range databaseSetups {
		setup(t, databasePath)
	}
	store, err := local.New(filepath.Join(root, "storage"))
	if err != nil {
		t.Fatal(err)
	}
	legacyImagesRoot := filepath.Join(root, "legacy-generated")
	if err := os.MkdirAll(legacyImagesRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(legacyImagesRoot, "historical_0.webp"),
		[]byte(legacyHistoricalImage),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	legacyGeneratedImages, err := legacygenerated.New(legacyImagesRoot)
	if err != nil {
		t.Fatal(err)
	}
	taskCatalog := taskCatalogOverride
	if taskCatalog == nil {
		taskCatalog, err = catalog.LoadDefault()
		if err != nil {
			t.Fatal(err)
		}
	}
	skillCatalog, err := catalog.LoadDefaultSkillCatalog()
	if err != nil {
		t.Fatal(err)
	}
	applicationSchema := taskCatalog.Application()
	if customize != nil {
		customize(&applicationSchema)
	}
	var uploadValidator inputassets.UploadValidator
	profile := applicationSchema.Runtime.InputAssets.ModelProfile
	if profile != nil {
		uploadValidator = modelsource.UploadValidator(httpModelInputPolicy(t, profile), profile.AssetType, profile.Extensions, profile.MediaTypes)
	}
	inputs, err := inputassets.NewWithConfig(
		repository,
		store,
		inputassets.Config{
			UploadValidator: uploadValidator,
			PublicBaseURL:   "https://art-ai.test",
			SigningKey:      strings.Repeat("k", 32),
			ProviderAssetRoute: applicationSchema.Runtime.HTTP.Routes.
				ProviderAsset,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var generationInputs generation.InputAssetResolver = inputs
	var uploadedModels resolve.UploadedModels
	if profile != nil {
		models, err := modelsource.New(inputs, httpModelInputPolicy(t, profile))
		if err != nil {
			t.Fatal(err)
		}
		uploadedModels = models
		generationInputs = models
	}
	legacyInputsRoot := filepath.Join(root, "legacy-assets")
	if err := os.MkdirAll(legacyInputsRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyInputs, err := legacyinput.New(
		legacyinput.Config{Root: legacyInputsRoot},
		repository,
	)
	if err != nil {
		t.Fatal(err)
	}
	compatibleInputs, err := inputassets.NewCompatible(inputs, legacyInputs)
	if err != nil {
		t.Fatal(err)
	}
	roleDefinitions := make(
		[]access.RoleDefinition,
		0,
		len(applicationSchema.Authentication.Roles),
	)
	for _, role := range applicationSchema.Authentication.Roles {
		permissions := make([]access.Permission, 0, len(role.Permissions))
		for _, permission := range role.Permissions {
			permissions = append(permissions, access.Permission(permission))
		}
		roleDefinitions = append(roleDefinitions, access.RoleDefinition{
			Key:         role.Key,
			Permissions: permissions,
		})
	}
	accessPolicy, err := access.NewPolicy(access.PolicyConfig{
		DefaultRole: applicationSchema.Authentication.DefaultRole,
		AdminRole:   applicationSchema.Authentication.AdminRole,
		Roles:       roleDefinitions,
	})
	if err != nil {
		t.Fatal(err)
	}
	fallbackIdentity := httpapi.DevelopmentIdentity{
		DefaultClaims: access.Claims{
			ID:    "local-user",
			Roles: append([]string(nil), roles...),
		},
		Policy: accessPolicy,
	}
	authenticationConfig := authentication.Config{
		Schema:        applicationSchema.Authentication,
		SessionSecret: strings.Repeat("session-test-secret-", 2),
		Environment:   "test",
		Policy:        accessPolicy,
		Directory:     repository,
		Fallback:      fallbackIdentity,
	}
	if customizeAuthentication != nil {
		customizeAuthentication(&authenticationConfig)
	}
	authenticationService, err := authentication.New(authenticationConfig)
	if err != nil {
		t.Fatal(err)
	}
	administrationUsers := applicationSchema.Administration.Users
	administrationAuditLogs := applicationSchema.Administration.AuditLogs
	administrationAuditService, err := administration.NewAuditService(
		administration.AuditServiceConfig{
			Repository: repository,
			DefaultPage: administrationAuditLogs.Limits.
				DefaultPage,
			DefaultPageSize: administrationAuditLogs.Limits.
				DefaultPageSize,
			MaximumPageSize: administrationAuditLogs.Limits.
				MaximumPageSize,
			IDPrefix: administrationAuditLogs.Defaults.IDPrefix,
			WriteMaximumAttempts: administrationAuditLogs.WritePolicy.
				MaximumAttempts,
			WriteRetryDelay: time.Duration(
				administrationAuditLogs.WritePolicy.RetryDelayMilliseconds,
			) * time.Millisecond,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	administrationGenerations :=
		applicationSchema.Administration.Generations
	legacyGenerationProjector, err := legacygeneration.NewProjector(
		legacygeneration.ProjectorConfig{
			Capabilities: applicationSchema.Capabilities,
			Aliases:      applicationSchema.TaskStream.Aliases,
			ExcludedRoles: administrationGenerations.Assets.
				DownloadExcludedRoles,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	administrationGenerationService, err :=
		administration.NewGenerationService(
			administration.GenerationServiceConfig{
				Repository: repository,
				DefaultPage: administrationGenerations.Limits.
					DefaultPage,
				DefaultPageSize: administrationGenerations.Limits.
					DefaultPageSize,
				MaximumPageSize: administrationGenerations.Limits.
					MaximumPageSize,
				NativeAssetRoute: applicationSchema.Runtime.HTTP.Routes.
					TaskAsset,
				ThumbnailAssetRole: administrationGenerations.Assets.
					ThumbnailRole,
				LegacyAssetProjector: legacyGenerationProjector,
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	administrationUsage := applicationSchema.Administration.Usage
	succeededUsageStates := make([]string, 0)
	failedUsageStates := make([]string, 0)
	for _, state := range applicationSchema.TaskStates {
		switch state.Outcome {
		case "success":
			succeededUsageStates = append(
				succeededUsageStates,
				state.InternalState,
			)
		case "failure":
			failedUsageStates = append(
				failedUsageStates,
				state.InternalState,
			)
		}
	}
	administrationUsageService, err := administration.NewUsageService(
		administration.UsageServiceConfig{
			Repository:      repository,
			DefaultDays:     administrationUsage.Defaults.Days,
			DatePattern:     administrationUsage.Validation.DatePattern,
			DateLayout:      administrationUsage.Validation.DateLayout,
			Timezone:        administrationUsage.Validation.Timezone,
			SucceededStates: succeededUsageStates,
			FailedStates:    failedUsageStates,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	administrationUserAssets :=
		applicationSchema.Administration.UserAssets
	userAssetDownloadActions := make(
		[]string,
		0,
		len(administrationUserAssets.Filters.DownloadActions),
	)
	for _, action := range administrationUserAssets.Filters.DownloadActions {
		userAssetDownloadActions = append(
			userAssetDownloadActions,
			action.Action,
		)
	}
	administrationUserAssetsModule, err :=
		administration.NewUserAssetsModule(
			administration.UserAssetsModuleConfig{
				Repository: repository,
				DefaultPage: administrationUserAssets.Limits.
					DefaultPage,
				DefaultPageSize: administrationUserAssets.Limits.
					DefaultPageSize,
				MaximumPageSize: administrationUserAssets.Limits.
					MaximumPageSize,
				DownloadActions: userAssetDownloadActions,
				NativeAssetRoute: applicationSchema.Runtime.HTTP.Routes.
					TaskAsset,
				ThumbnailRole: administrationGenerations.Assets.
					ThumbnailRole,
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	administrationBackfillStatus :=
		applicationSchema.Administration.BackfillStatus
	administrationBackfillStatusModule, err :=
		administration.NewBackfillStatusModule(
			administration.BackfillStatusModuleConfig{
				Repository: repository,
				PendingStatus: administrationBackfillStatus.
					Statuses.Pending,
				SucceededStatus: administrationBackfillStatus.
					Statuses.Succeeded,
				SyncProviderIDs: administrationBackfillStatus.
					SyncProviderIDs,
				ResultFields: administration.BackfillResultFields{
					ModelURLs: administrationBackfillStatus.
						CacheResultFields.ModelURLs,
					ThumbnailURL: administrationBackfillStatus.
						CacheResultFields.ThumbnailURL,
					ImageURLs: administrationBackfillStatus.
						CacheResultFields.ImageURLs,
					TextureURLs: administrationBackfillStatus.
						CacheResultFields.TextureURLs,
					BaseColor: administrationBackfillStatus.
						CacheResultFields.BaseColor,
					VideoURL: administrationBackfillStatus.
						CacheResultFields.VideoURL,
				},
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	administrationService, err := administration.NewUserService(
		administration.UserServiceConfig{
			Repository: repository,
			// 与 app 装配同源：测试夹具曾各自拼一遍 UserServiceConfig，
			// 新增的 Schema 声明（邮箱必填、管理员保底）就会在这里静默丢失，
			// 表现为"服务层测试全绿、HTTP 层绕过了不变量"。
			Guards: administration.UserGuards{
				MinimumActiveAdministrators: administrationUsers.Guards.
					MinimumActiveAdministrators,
			},
			Identity: administration.UserIdentityRules{
				EmailRequired: administrationUsers.API.Identity.EmailRequired,
				EmailMustContainAt: administrationUsers.API.Identity.
					EmailMustContainAt,
			},
			Audit:            administrationAuditService,
			PasswordHashCost: applicationSchema.Authentication.API.PasswordHashCost,
			UsernameMinimumCharacters: administrationUsers.Limits.
				UsernameMinimumCharacters,
			PasswordMinimumCharacters: administrationUsers.Limits.
				PasswordMinimumCharacters,
			IDPrefix:      administrationUsers.Defaults.IDPrefix,
			DefaultRole:   applicationSchema.Authentication.DefaultRole,
			AdminRole:     applicationSchema.Authentication.AdminRole,
			DefaultActive: administrationUsers.Defaults.Active,
			AuditActions: administration.UserAuditActions{
				Create:     administrationUsers.Audit.CreateAction,
				Update:     administrationUsers.Audit.UpdateAction,
				Deactivate: administrationUsers.Audit.DeactivateAction,
				Delete:     administrationUsers.Audit.DeleteAction,
				Resource:   administrationUsers.Audit.ResourceType,
			},
			AuditFields: administration.UserAuditFields{
				Username: administrationUsers.Audit.DetailFields.Username,
				Admin:    administrationUsers.Audit.DetailFields.IsAdmin,
				Active:   administrationUsers.Audit.DetailFields.IsActive,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	cacheService, err := cachemanagement.New(cachemanagement.Config{
		Stats: repository,
	})
	if err != nil {
		t.Fatal(err)
	}
	approvalPolicy := harnessApprovalPolicy(applicationSchema)
	var projectService *project.Service
	var projectSource chat.ProjectSource
	if applicationSchema.Project != nil {
		profiles, imageProfiles := project.PurposeProfiles(applicationSchema.Chat)
		projectService, err = project.New(repository.ProjectRepository(), project.Config{
			Schema:        *applicationSchema.Project,
			Profiles:      profiles,
			ImageProfiles: imageProfiles,
			Sources:       repository.ProjectAssetSource(),
		})
		if err != nil {
			t.Fatal(err)
		}
		projectSource = integrationProjectSource{service: projectService}
	}
	chatService, err := chat.NewService(chat.ServiceConfig{
		Repository:                 repository,
		ApprovalPolicy:             approvalPolicy,
		AgentScope:                 agentscope.FromSchema(applicationSchema.Chat.AgentScope),
		Handles:                    chat.NewHandlePolicy(applicationSchema.Chat.Context),
		MaximumSessionNotes:        applicationSchema.Chat.Context.MaximumSessionNotesCharacters,
		MaximumStyleLockText:       applicationSchema.Chat.StyleLock.MaximumTextCharacters,
		MaximumStyleLockReferences: applicationSchema.Chat.StyleLock.MaximumReferenceImages,
		StyleLockSources:           applicationSchema.Chat.StyleLock.Sources,
		Projects:                   projectSource,
		Attachments:                harnessAttachmentVerifier{},
		// A canvas turn may carry a selection mask (#583): the harness verifier
		// measures only its "own-canvas-" snapshots and "own-mask-" masks.
		CanvasInputSource: applicationSchema.Chat.Context.CanvasInputSource,
		Preferences:                applicationSchema.Chat.Preferences,
		Model3DUsage:               applicationSchema.Chat.Model3DUsage,
		Image2DUsage:               applicationSchema.Chat.Image2DUsage,
		ExplicitModel3DValid: func(model string) bool {
			return httpExplicitModelValid(taskCatalog.List(""), model, generation.DomainModel3D)
		},
		ExplicitModelImageValid: func(model string) bool {
			return httpExplicitModelValid(taskCatalog.List(""), model, generation.DomainImage)
		},
		ChoiceKinds:      applicationSchema.Chat.Choice.Kinds,
		ChoiceMaxOptions: applicationSchema.Chat.Choice.MaxOptions,
		// A direction card option may switch the session to one of these (#374).
		HarnessOperations: applicationSchema.Chat.Harness.Operations,
		RetiredModels:     taskCatalog.RetiredModelSuccessors(),
		// The harness deployment chats with the DeepSeek profile (#322).
		Models:                chat.ModelPolicy{Model: "deepseek", Thinking: applicationSchema.Chat.Agent.Thinking},
		VisionBudget:          func(model string) int { return chat.EffectiveVisionImages(applicationSchema.Chat, model) },
		VisionTokenBudget:     func(model string) int { return chat.EffectiveVisionTokens(applicationSchema.Chat, model) },
		DefaultTitle:          applicationSchema.Chat.Defaults.Title,
		BranchTitleTemplate:   applicationSchema.Chat.Defaults.BranchTitleTemplate,
		CanvasProjectIDPrefix: applicationSchema.Canvas.Defaults.ProjectIDPrefix,
		UserRole:              applicationSchema.Chat.Roles.User,
		AssistantRole:         applicationSchema.Chat.Roles.Assistant,
		PendingContent:        applicationSchema.Chat.State.PendingContent,
		StreamActive:          applicationSchema.Chat.State.Active,
		StreamDone:            applicationSchema.Chat.State.Done,
		StreamError:           applicationSchema.Chat.State.Error,
		TurnQueued:            applicationSchema.Chat.State.TurnQueued,
		TurnRunning:           applicationSchema.Chat.State.TurnRunning,
		TurnWaitingTask:       applicationSchema.Chat.State.TurnWaitingTask,
		TurnDone:              applicationSchema.Chat.State.TurnDone,
		TurnError:             applicationSchema.Chat.State.TurnError,
		TurnEndReasons:        applicationSchema.Chat.State.TurnEndReasons,
		PlaceholderHealing: chat.PlaceholderHealingPolicy{
			StaleAfter: time.Duration(
				applicationSchema.Chat.State.PlaceholderStaleMilliseconds,
			) * time.Millisecond,
			PromptArgument: applicationSchema.Chat.Context.
				ToolPromptArgument,
			PromptPreviewCharacters: applicationSchema.Chat.State.
				FailedPromptPreviewChars,
			InterruptedMessage: applicationSchema.Chat.Presentation.Messages.
				Interrupted,
			InterruptedPromptTemplate: applicationSchema.Chat.Presentation.
				Messages.InterruptedPrompt,
		},
		StreamCleanupTTL: time.Duration(
			applicationSchema.Chat.Stream.CleanupTTLMilliseconds,
		) * time.Millisecond,
		SubscriberBufferSize: applicationSchema.Chat.Stream.
			SubscriberBufferSize,
		EventFields: chat.EventFieldPolicy{
			Type:      applicationSchema.Chat.Stream.EventFields.Type,
			MessageID: applicationSchema.Chat.Stream.EventFields.MessageID,
		},
		EventTypes: chat.EventTypePolicy{
			MessageCreated: applicationSchema.Chat.Stream.EventTypes.
				MessageCreated,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	chatService.SetChoices(repository, applicationSchema.Chat.Choice.ListLimit)
	chatService.SetContextStore(repository, applicationSchema.Chat.ContextCompaction.ListLimit)
	// #246: the production assembly installs the persisted self-check record;
	// the harness must serve the same route from the same store.
	chatService.SetSelfChecks(repository, applicationSchema.Chat.SelfCheck)
	canvasService, err := canvas.New(canvas.Config{
		Repository:           repository,
		Sessions:             chatService,
		DefaultName:          applicationSchema.Canvas.Defaults.ProjectName,
		DefaultViewport:      applicationSchema.Canvas.Defaults.Viewport,
		DefaultBGStyle:       applicationSchema.Canvas.Defaults.BGStyle,
		ProjectIDPrefix:      applicationSchema.Canvas.Defaults.ProjectIDPrefix,
		MaximumBitmapNodes:   applicationSchema.Canvas.Limits.MaximumBitmapNodes,
		MaximumPlacedResults: applicationSchema.Canvas.Limits.MaximumPlacedResults,
		MaximumPlacedResultKeyCharacters: applicationSchema.Canvas.Limits.
			MaximumPlacedResultKeyCharacters,
		MinimumFrameDimension: applicationSchema.Canvas.Limits.MinimumDimension,
		MaximumFrameDimension: applicationSchema.Canvas.Limits.MaximumDimension,
		CoordinateVersion:     applicationSchema.Canvas.Defaults.CoordinateVersion,
		NodeDefaults: canvas.Node{
			X:       applicationSchema.Canvas.Defaults.NodeX,
			Y:       applicationSchema.Canvas.Defaults.NodeY,
			Width:   applicationSchema.Canvas.Defaults.NodeWidth,
			Height:  applicationSchema.Canvas.Defaults.NodeHeight,
			ZIndex:  applicationSchema.Canvas.Defaults.NodeZIndex,
			Data:    applicationSchema.Canvas.Defaults.NodeData,
			Visible: applicationSchema.Canvas.Defaults.NodeVisible,
			Locked:  applicationSchema.Canvas.Defaults.NodeLocked,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	registry := providers.NewRegistry()
	adapter := &httpMeshyFormatFixtureAdapter{Adapter: mock.New()}
	for _, key := range []string{
		"openai-images.v1",
		"gemini-images.v1",
		"meshy.v2",
		"tripo.v3",
		"matting.v1",
	} {
		if _, replaced := additionalProviders[key]; replaced {
			continue // a test's own adapter (a real Provider wire) takes the key
		}
		if err := registry.Register(key, adapter); err != nil {
			t.Fatal(err)
		}
	}
	recoveryAdapter, err := assetrecovery.New(repository, store)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("local.recovery", recoveryAdapter); err != nil {
		t.Fatal(err)
	}
	for key, provider := range additionalProviders {
		if err := registry.Register(key, provider); err != nil {
			t.Fatal(err)
		}
	}
	module, err := generation.New(generation.Dependencies{
		Conversions: generation.NoConversions,
		Repository:  repository,
		Catalog:     taskCatalog,
		Providers:   registry,
		Assets:      store,
		Inputs:      generationInputs,
		Cache:       cacheService,
		// As the application sets it: the Schema's in-flight limit per person.
		PerOwnerMaxInflightTasks: applicationSchema.Runtime.HTTP.TaskAdmission.PerOwnerMaxInflightTasks,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixtureExecutions.Store(module, &fixtureExecutionState{repository: repository, databasePath: databasePath})
	t.Cleanup(func() { fixtureExecutions.Delete(module) })
	nativeAdministrationGenerationAssetReader :=
		administration.GenerationAssetReaderFunc(
			func(
				ctx context.Context,
				request administration.GenerationAssetRequest,
			) (administration.GenerationAssetContent, error) {
				content, err := module.OpenAsset(
					ctx,
					generation.OwnerID(request.OwnerID),
					generation.TaskID(request.TaskID),
					generation.AssetID(request.Asset.ID),
				)
				if err != nil {
					return administration.GenerationAssetContent{}, err
				}
				return administration.GenerationAssetContent{
					Body:      content.Body,
					MediaType: content.MediaType,
					Size:      content.Size,
				}, nil
			},
		)
	administrationGenerationAssetReader, err :=
		legacygeneration.NewReader(legacygeneration.ReaderConfig{
			Native:              nativeAdministrationGenerationAssetReader,
			GeneratedImages:     legacyGeneratedImages,
			GeneratedImageRoute: applicationSchema.Runtime.HTTP.LegacyRoutes.GeneratedImage,
			CacheRoot:           filepath.Join(root, "legacy-cache"),
			CachePrefixCharacters: administrationGenerations.Assets.
				LegacyCachePrefixCharacters,
			CacheMetadataSuffix: administrationGenerations.Assets.
				LegacyCacheMetadataSuffix,
			CacheContentTypeField: administrationGenerations.Assets.
				LegacyCacheContentTypeField,
			AllowedHosts: legacygeneration.CollectAssetHosts(
				taskCatalog.List(""),
			),
			DefaultMediaType: administrationGenerations.Assets.
				DefaultMediaType,
			UnknownExtension: administrationGenerations.Assets.
				UnknownExtension,
			MaximumBytes: administrationGenerations.Assets.
				MaximumPackageBytes,
			RemoteTimeoutMilliseconds: administrationGenerations.Assets.
				RemoteDownloadTimeoutMilliseconds,
			MaximumRedirects: administrationGenerations.Assets.
				MaximumRemoteRedirects,
			MediaTypeExtensions: administrationGenerations.Assets.
				MediaTypeExtensions,
			AssetDownloads: legacygeneration.CollectAssetDownloads(
				applicationSchema.Capabilities,
			),
		})
	if err != nil {
		t.Fatal(err)
	}
	administrationGenerationDownloadService, err :=
		administration.NewGenerationDownloadService(
			administration.GenerationDownloadServiceConfig{
				Details: administrationGenerationService,
				Assets:  administrationGenerationAssetReader,
				DownloadFilenameTemplate: administrationGenerations.Assets.
					DownloadFilenameTemplate,
				AssetFilenameTemplate: administrationGenerations.Assets.
					AssetFilenameTemplate,
				VariantSeparator: administrationGenerations.Assets.
					VariantSeparator,
				DuplicateSeparator: administrationGenerations.Assets.
					DuplicateSeparator,
				OrdinalBase: administrationGenerations.Assets.OrdinalBase,
				ArchiveMediaType: administrationGenerations.Assets.
					ArchiveMediaType,
				DefaultMediaType: administrationGenerations.Assets.
					DefaultMediaType,
				UnknownExtension: administrationGenerations.Assets.
					UnknownExtension,
				MaximumPackageBytes: administrationGenerations.Assets.
					MaximumPackageBytes,
				MediaTypeExtensions: administrationGenerations.Assets.
					MediaTypeExtensions,
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = module.Close()
	})
	referenceResolver, err := references.New(references.Config{
		Inputs: inputs,
		Tasks:  module,
		Legacy: legacyGeneratedImages,
		InputAssetRoutes: []string{
			applicationSchema.Runtime.HTTP.Routes.AssetItem,
			applicationSchema.Runtime.HTTP.LegacyRoutes.AssetItem,
		},
		TaskAssetRoute: applicationSchema.Runtime.HTTP.Routes.TaskAsset,
		GeneratedImageRoute: applicationSchema.Runtime.HTTP.
			LegacyRoutes.GeneratedImage,
		PublicBaseURL:  "https://art-ai.test",
		UploadMaxBytes: applicationSchema.Runtime.InputAssets.UploadMaxBytes,
		GeneratedReferenceMaxBytes: applicationSchema.Runtime.InputAssets.
			GeneratedReferenceMaxBytes,
		GeneratedReferenceTooLargeTemplate: applicationSchema.Runtime.InputAssets.
			Messages.GeneratedReferenceTooLargeTemplate,
		DefaultMediaType: applicationSchema.Runtime.InputAssets.DefaultMediaType,
		DefaultAssetType: applicationSchema.Runtime.InputAssets.DefaultAssetType,
		ReferenceFilename: applicationSchema.Chat.Context.
			ReferenceFilename,
		LegacyGeneratedImage: applicationSchema.Runtime.AssetDelivery.
			LegacyGeneratedImage,
	})
	if err != nil {
		t.Fatal(err)
	}
	canvasImageService, err := canvas.NewImageService(
		canvas.ImageServiceConfig{
			References:        referenceResolver,
			Assets:            inputs,
			MaximumNodes:      applicationSchema.Canvas.Limits.MaximumMergeNodes,
			MaximumDimension:  applicationSchema.Canvas.Limits.MaximumDimension,
			MaximumAssetBytes: applicationSchema.Runtime.HTTP.MultipartBodyMaxBytes,
			FilenamePrefix: applicationSchema.Canvas.Defaults.
				MergeFilenamePrefix,
			Extension: applicationSchema.Canvas.Defaults.MergeExtension,
			MediaType: applicationSchema.Canvas.Defaults.MergeMediaType,
			AssetType: applicationSchema.Canvas.Defaults.MergeAssetType,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if chatScheduler == nil {
		chatScheduler = noOpChatScheduler{}
	}
	promptAgent, err := agentmock.New(agentmock.Config{
		ToolName:       applicationSchema.Chat.Agent.MockToolName,
		PromptArgument: applicationSchema.Chat.Agent.MockPromptArgument,
		Summary:        applicationSchema.Chat.Agent.MockSummary,
		UserRole:       applicationSchema.Chat.Roles.User,
		ToolRole:       applicationSchema.Chat.Roles.Tool,
	})
	if err != nil {
		t.Fatal(err)
	}
	promptEnhancer, err := promptenhance.New(promptenhance.Config{
		Offerings: taskCatalog,
		Schema:    applicationSchema.Chat,
		Model:     "test-llm",
		Agent:     promptAgent,
	})
	if err != nil {
		t.Fatal(err)
	}
	accountReaders := configuredAccountReaders
	if accountReaders == nil {
		accountReaders = make(
			map[string]provideraccounts.BalanceReader,
			len(applicationSchema.Runtime.ProviderAccounts),
		)
		for accountID, account := range applicationSchema.Runtime.ProviderAccounts {
			if account.Balance.NativeUnit != "" {
				accountReaders[accountID] = provideraccounts.FixedNativeBalanceReader{FixedBalanceReader: provideraccounts.FixedBalanceReader(account.Balance.MockValue), Amount: generation.NativeAmount{Unit: account.Balance.NativeUnit, UnitLabel: account.Balance.NativeUnitLabel, Amount: account.Balance.MockNativeAmount}}
				continue
			}
			accountReaders[accountID] = provideraccounts.FixedBalanceReader(
				account.Balance.MockValue,
			)
		}
	}
	providerAccountService, err := provideraccounts.New(
		provideraccounts.Config{Readers: accountReaders},
	)
	if err != nil {
		t.Fatal(err)
	}
	// The web balance route reads through the shared cache the Schema declares,
	// as the application does (#385).
	var providerBalances httpapi.ProviderBalanceReader
	if policy := applicationSchema.StepPolicy; policy != nil &&
		policy.BalanceCacheSeconds > 0 && policy.BalanceReadTimeoutSeconds > 0 {
		balanceCache, cacheErr := provideraccounts.NewCache(providerAccountService, provideraccounts.CacheConfig{
			TTL:     time.Duration(policy.BalanceCacheSeconds) * time.Second,
			Timeout: time.Duration(policy.BalanceReadTimeoutSeconds) * time.Second,
			Logger:  harnessLogger(t),
		})
		if cacheErr != nil {
			t.Fatal(cacheErr)
		}
		providerBalances = balanceCache
	}
	administrationBalanceSummaryService, err :=
		administration.NewBalanceSummaryService(
			administration.BalanceSummaryServiceConfig{
				Accounts:  providerAccountService,
				Snapshots: repository,
				AccountID: administrationUsage.Balance.
					AccountID,
				HistoryDays: administrationUsage.Balance.
					HistoryDays,
				SnapshotIDPrefix: administrationUsage.Balance.
					SnapshotIDPrefix,
				SnapshotWritePolicy: administrationUsage.Balance.
					SnapshotWritePolicy,
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	skillRuns, err := skillrun.NewService(repository, catalogrecipes.New(taskCatalog))
	if err != nil {
		t.Fatal(err)
	}
	var grantRepository executiongrant.Repository = repository
	if state, ok := fixtureExecutions.Load(module); ok {
		grantRepository = &hookedGrantRepository{Repository: repository, state: state.(*fixtureExecutionState)}
	}
	executionGrants := &executiongrant.Service{Grants: grantRepository, Prepared: repository,
		Budgets: repository, Policy: approvalPolicy, Modes: chatService,
		PendingTTL: time.Duration(applicationSchema.Chat.PendingApprovals.TTLSeconds) * time.Second}
	// As the application assembles it: the plan store and its continuation
	// turn (#103, #134) use the same grant service.
	chatService.SetProductionPlans(chat.ProductionPlanPolicy{
		Budget:            applicationSchema.Chat.ApprovalPolicy.Budget,
		Offerings:         func() []generation.Offering { return module.Offerings("") },
		OfferingAvailable: module.OfferingAvailable,
		Store:             executionGrants,
	})
	approvalTexts, err := catalog.NewTextLocalizer(applicationSchema)
	if err != nil {
		t.Fatal(err)
	}
	pendingApprovals, err := pendingapproval.New(pendingapproval.Config{
		Texts: approvalTexts,
		// This fixture supplies historical calls only. Agent-source submission is
		// exercised through the production App verifier, never this legacy stub.
		VerifyPreparedSource: func(ctx context.Context, op generationtool.Operation, p generation.PreparedInput) error {
			turn, err := repository.GetTurn(ctx, op.OwnerID, op.SessionID, op.AssistantMessageID)
			if err != nil {
				return err
			}
			if turn.Options.Harness != nil && resolve.AgentSource(turn.Options.Harness.Selection) {
				return pendingapproval.ErrUnavailable
			}
			return nil
		},
		Operations: repository, Grants: executionGrants, Gateway: module,
		Chat: chatService, Schema: applicationSchema.Chat,
	})
	if err != nil {
		t.Fatal(err)
	}
	if state, ok := fixtureExecutions.Load(module); ok {
		state.(*fixtureExecutionState).pending = pendingApprovals
		state.(*fixtureExecutionState).chat = chatService
	}
	var modelDelivery *modeldelivery.Service
	var processingPlans *processing.Service
	if !applicationSchema.ModelDelivery.Disabled() {
		harnessPlans, err := processing.New(repository, harnessProcessingCatalog{Catalog: taskCatalog, module: module}, module, nil)
		if err != nil {
			t.Fatal(err)
		}
		harnessPlans.WithGrants(executionGrants)
		processingPlans = harnessPlans
		modelDelivery, err = modeldelivery.New(applicationSchema.ModelDelivery, module, harnessPlans, nil)
		if err != nil {
			t.Fatal(err)
		}
		// As in the application: the administration download names a repeated file
		// in the one-click ZIP's words (#300).
		administrationGenerationDownloadService.WithRepeatText(modelDelivery.RepeatText)
	}
	skillRuntime, err := skillrun.NewRuntime(skillRuns, module, executionGrants)
	if err != nil {
		t.Fatal(err)
	}
	lineageService, err := lineage.New(module, nil, repository, lineage.Config{
		MaxTasks: applicationSchema.Runtime.HTTP.MaximumLineageTasks,
	})
	if err != nil {
		t.Fatal(err)
	}
	threeViews, err := threeview.New(threeview.Config{
		Runtime: skillRuntime, Sessions: chatService, Schema: applicationSchema.Chat.ThreeView,
	})
	if err != nil {
		t.Fatal(err)
	}
	lineageService.SetGroups(threeViews)
	retryService, err := taskretry.New(taskretry.Config{
		Kernel:      module,
		Grants:      executionGrants,
		Origins:     repository,
		OperationID: httpapi.StandaloneOperationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	var rerollService *taskreroll.Service
	if rerollSchema := taskCatalog.Application().Chat.Reroll; rerollSchema.Enabled {
		rerollService, err = taskreroll.New(taskreroll.Config{
			Kernel: module, Grants: executionGrants, Origins: rerollTestOrigins{repository: repository},
			OperationID: httpapi.StandaloneOperationID, Schema: rerollSchema,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	harnessResolver := &resolve.Service{UploadedModels: uploadedModels, Store: repository, Schema: applicationSchema.Chat.Harness, Tools: applicationSchema.Chat.Tools, Catalog: taskCatalog, Gateway: module}
	chatService.SetHarnessSnapshotResolver(harnessResolver.Snapshot)
	assetPreviews, err := assetdelivery.NewPreviewStore(t.TempDir(), applicationSchema.Runtime.AssetDelivery.AssetPreview)
	if err != nil {
		t.Fatal(err)
	}
	accountSettingsService := harnessAccountSettings(t, repository, applicationSchema)
	accessKeysService := harnessAccessKeys(t, repository, applicationSchema)
	accessKeyAdminService := harnessAccessKeyAdmin(t, repository, administrationAuditService, applicationSchema)
	mcpParts := harnessMCP(t, repository, applicationSchema, accountSettingsService, accessKeysService, accessPolicy, providerAccountService, chatService)
	// The external MCP listener shares the platform inspect_image core, as the
	// application does (#332 step 7b).
	imageReview, err := imagereview.New(imagereview.Config{
		Context: applicationSchema.Chat.Context,
		Review:  applicationSchema.Chat.ImageReview,
		Inputs:  inputs,
		Tasks:   module,
		Usage:   applicationSchema.Chat.Image2DUsage,
		Brief:   applicationSchema.Chat.Model3DUsage.Brief,
		Offerings: func(id string) (generation.Offering, bool) {
			offering, found := taskCatalog.Get(generation.OfferingID(id))
			if !found || !module.OfferingAvailable(offering.ID) {
				return generation.Offering{}, false
			}
			return offering, true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Production lists (#327). The harness resolves availability without the
	// runtime Offerings so a test can assert both an available and an
	// unavailable step deterministically; the brief uses the real Schema
	// rules.
	var assetListService *assetlist.Service
	var assetListPresence assetlist.PresenceStore
	if listSchema := applicationSchema.AssetList; listSchema != nil &&
		applicationSchema.Project != nil && applicationSchema.StepPolicy != nil {
		counted := make(map[generation.Capability]bool, len(applicationSchema.StepPolicy.CountedCapabilities))
		for _, capability := range applicationSchema.StepPolicy.CountedCapabilities {
			counted[generation.Capability(capability)] = true
		}
		// #331: the edit crop instruction cuts through the same listcrop
		// service as the crop tool, so the harness assembles it beside the
		// list service.
		cropConfig := listcrop.Config{
			Inputs:          harnessCropInputs{inputs: inputs, loader: repository},
			Tasks:           module,
			Crops:           repository.InputAssetCropRepository(),
			SourceMaxBytes:  applicationSchema.Chat.ImageMask.Limits.MaxBytes,
			SourceMaxPixels: applicationSchema.Chat.ImageMask.Limits.MaxPixels,
			SourceAssetType: applicationSchema.Runtime.InputAssets.DefaultAssetType,
		}
		if listSchema.Decompose != nil {
			cropConfig.MaximumCropsPerSource = listSchema.Decompose.MaximumItemsPerImage * listcrop.StorageCropLimitMultiplier
			cropConfig.MinimumSide = listSchema.Decompose.Crop.MinimumShortEdge
			cropConfig.MaximumRegionPixels = listSchema.Decompose.Crop.MaximumRegionPixels
			cropConfig.UploadMaxBytes = listSchema.Decompose.Crop.MaximumPNGBytes
		}
		cropService, err := listcrop.New(cropConfig)
		if err != nil {
			t.Fatal(err)
		}
		assetListService, err = assetlist.New(assetlist.Config{
			Repository: repository.AssetListRepository(),
			Runs:       repository.AssetListRunRepository(),
			Reviews:    repository.AssetListReviewRepository(),
			References: assetListReferenceAssets{
				inputs: inputs,
				crops:  repository.InputAssetCropRepository(),
			},
			Crops:              cropService,
			Archive:            module,
			Schema:             *listSchema,
			ProjectMaxItems:    applicationSchema.Project.Limits.MaximumListItems,
			MaxListsPerProject: listSchema.Limits.MaxListsPerProject,
			Counted:            counted,
			Steps:              harnessAssetListSteps(executionGrants, applicationSchema.StepPolicy),
			ProjectAssets:      projectService,
			ProjectAssetLimits: assetlist.ProjectAssetLimits{
				MaximumAssets: applicationSchema.Project.Limits.MaximumAssetsPerProject,
				MaximumImages: applicationSchema.Project.Limits.MaximumAssetImages,
				MaximumModels: applicationSchema.Project.Limits.MaximumAssetModels,
			},
			ResolveStepFor: func(_ context.Context, _, _, capability string) (assetlist.RouteStep, error) {
				if capability == "remesh" {
					return assetlist.RouteStep{Capability: capability, Available: false,
						Reason: "remesh provider is disabled in the harness"}, nil
				}
				return assetlist.RouteStep{Capability: capability, OfferingID: "harness-" + capability, Available: true}, nil
			},
			ValidateBrief: chatService.ValidateAssetListBrief,
		})
		if err != nil {
			t.Fatal(err)
		}
		// Mirror the application assembly: item sub-sessions derive the
		// internal steps mode and its entry from this policy (#330).
		chatService.SetAssetLists(chat.AssetListPolicy{
			Service:      assetListService,
			Schema:       *listSchema,
			StepsEnabled: applicationSchema.StepPolicy != nil,
		})
		if listSchema.Execution != nil {
			assetListPresence = repository.AssetListPresenceRepository()
		}
	}
	api, err := httpapi.New(httpapi.Config{
		MCPIdentity:                       mcpParts.identity,
		MCPStatus:                         mcpParts.status,
		MCPAssets:                         mcpParts.assets,
		ModelInspector:                    mcpParts.inspector,
		ImageInspector:                    imageReview,
		PictureAssets:                     repository,
		Harness:                           harnessResolver,
		Retry:                             retryService,
		Reroll:                            rerollService,
		Lineage:                           lineageService,
		Generation:                        module,
		ModelDelivery:                     modelDelivery,
		Processing:                        processingPlans,
		AccountInputAssets:                repository,
		AccountSettings:                   accountSettingsService,
		AccessKeys:                        accessKeysService,
		AdministrationAccessKeys:          accessKeyAdminService,
		ExecutionGrants:                   executionGrants,
		AgentOperations:                   repository,
		PendingApprovals:                  pendingApprovals,
		ThreeViews:                        threeViews,
		Identity:                          authenticationService,
		Authentication:                    authenticationService,
		Administration:                    administrationService,
		AdministrationAudit:               administrationAuditService,
		AdministrationGenerations:         administrationGenerationService,
		AdministrationGenerationDownloads: administrationGenerationDownloadService,
		AdministrationUsage:               administrationUsageService,
		AdministrationBalanceSummary:      administrationBalanceSummaryService,
		AdministrationUserAssets:          administrationUserAssetsModule,
		AdministrationBackfillStatus:      administrationBackfillStatusModule,
		Cache:                             cacheService,
		Canvas:                            canvasService,
		CanvasImages:                      canvasImageService,
		Chat:                              chatService,
		Projects:                          projectService,
		AssetLists:                        assetListService,
		AssetListPresence:                 assetListPresence,
		AssetListCrops:                    repository.InputAssetCropRepository(),
		ChatScheduler:                     chatScheduler,
		ChatContext:                       harnessCompactor(chatScheduler),
		OwnsAgentSession: func(ctx context.Context, ownerID, sessionID string) bool {
			owned, err := chatService.OwnsSession(ctx, ownerID, sessionID)
			return err == nil && owned
		},
		PromptEnhancer:        promptEnhancer,
		ProviderAccounts:      providerAccountService,
		ProviderBalances:      providerBalances,
		AssetProxy:            notFoundAssetProxy{},
		LegacyGeneratedImages: legacyGeneratedImages,
		Application:           applicationSchema,
		SkillCatalog:          skillCatalog,
		SkillRuns:             skillRuntime,
		InputAssets:           compatibleInputs,
		AssetPreviews:         assetPreviews,
		AssetGateway:          inputs,
		TaskAssetLinks:        harnessTaskAssetLinks(mcpParts),
		Logger:                harnessLogger(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	registerHarnessMCP(t, module, api, mcpParts, accessKeysService, repository)
	return module, api.Handler(), inputs, cacheService
}

// harnessProcessingCatalog mirrors the production processingCatalog.
type harnessProcessingCatalog struct {
	*catalog.Catalog
	module *generation.Module
}

func (c harnessProcessingCatalog) OfferingAvailable(id generation.OfferingID) bool {
	return c.module.OfferingAvailable(id)
}

type noOpChatScheduler struct{}

func (noOpChatScheduler) ResumeConsentWait(string, string) bool { return false }

func (noOpChatScheduler) Schedule(
	context.Context,
	chat.ProcessTurn,
) error {
	return nil
}

func (noOpChatScheduler) CancelTurn(
	context.Context,
	string,
	string,
) (bool, error) {
	return false, nil
}

type notFoundAssetProxy struct{}

func (notFoundAssetProxy) Get(
	context.Context,
	assetdelivery.Request,
) (assetdelivery.Result, error) {
	return assetdelivery.Result{}, assetdelivery.ErrNotFound
}

func uploadInputAsset(t *testing.T, baseURL, owner string) string {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "input.png")
	if err != nil {
		t.Fatal(err)
	}
	// #460 B16: Meshy's image-to-3d checks the decoded pixels of its reference
	// pictures (each side >= 32 px), so the fixture is a real PNG.
	_, _ = part.Write(testReferencePNG(t, 32, 32))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	response := doRequest(
		t,
		http.DefaultClient,
		http.MethodPost,
		baseURL+"/api/v1/assets",
		owner,
		writer.FormDataContentType(),
		&body,
	)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("upload status = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	var asset struct {
		ID generation.AssetID `json:"id"`
	}
	decodeResponse(t, response, &asset)
	return string(asset.ID)
}

func doRequest(
	t *testing.T,
	client *http.Client,
	method, target, owner, contentType string,
	body io.Reader,
) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, target, body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-User-ID", owner)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeResponse(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func readBody(t *testing.T, response *http.Response) []byte {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// harnessApprovalPolicy mirrors the production conversion from the Product
// Schema so HTTP tests exercise the same policy as the application.
// harnessAccessKeys builds the personal access key service the way the
// application assembly does: nil for a Catalog without application.access_keys.
func harnessAccessKeys(t *testing.T, repository *sqlite.Repository, application catalog.ApplicationSchema) *accesskeys.Service {
	t.Helper()
	if application.AccessKeys == nil {
		return nil
	}
	service, err := accesskeys.New(repository, accesskeys.ConfigFromSchema(*application.AccessKeys, nil))
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// harnessAccountSettings builds the person's settings service the way the
// application assembly does: nil for a Catalog without a step policy.
// harnessAccessKeyAdmin builds the administrator's access key view the way
// the application assembly does: nil without application.access_keys.
func harnessAccessKeyAdmin(t *testing.T, repository *sqlite.Repository, audit *administration.AuditService, application catalog.ApplicationSchema) *administration.AccessKeyAdminService {
	t.Helper()
	if application.AccessKeys == nil {
		return nil
	}
	view := application.Administration.AccessKeys
	if view.Permission == "" {
		return nil
	}
	service, err := administration.NewAccessKeyAdminService(administration.AccessKeyAdminConfig{
		Repository: repository, Audit: audit, DefaultEnabled: application.AccessKeys.Enabled,
		DefaultPageSize: view.Limits.DefaultPageSize, MaxPageSize: view.Limits.MaxPageSize,
		AuditActions: administration.AccessKeyAdminAuditActions{
			Read: view.Audit.ReadAction, Revoke: view.Audit.RevokeAction,
			Enable: view.Audit.EnableAction, Disable: view.Audit.DisableAction, Type: view.Audit.ResourceType,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// harnessAssetListSteps mirrors the application's extra-step port (#330 step
// 5C): the list layer gets the grant service's whole-target write and the same
// Schema bounds, with the grant package's errors mapped to the list's.
func harnessAssetListSteps(grants *executiongrant.Service, declared *catalog.StepPolicySchema) *assetlist.StepsConfig {
	if grants == nil || declared == nil {
		return nil
	}
	return &assetlist.StepsConfig{
		Maximum:      declared.ExtraSteps.Maximum,
		TotalMaximum: declared.ExtraSteps.TotalMaximum,
		Set: func(ctx context.Context, ownerID, sessionID string, target int) (assetlist.StepChange, error) {
			change, err := grants.SetExtraSteps(ctx, ownerID, sessionID, target)
			if err != nil {
				switch {
				case errors.Is(err, executiongrant.ErrExtraStepsLimit):
					return assetlist.StepChange{}, &assetlist.StepExtraLimitError{
						Maximum: declared.ExtraSteps.TotalMaximum, Requested: target,
					}
				case errors.Is(err, executiongrant.ErrExtraStepsSingleLimit):
					return assetlist.StepChange{}, &assetlist.StepExtraSingleLimitError{
						Maximum: declared.ExtraSteps.Maximum,
					}
				case errors.Is(err, executiongrant.ErrExtraStepsUnavailable):
					return assetlist.StepChange{}, assetlist.ErrStepUnavailable
				case errors.Is(err, executiongrant.ErrConflict):
					return assetlist.StepChange{}, assetlist.ErrConflict
				case errors.Is(err, executiongrant.ErrInvalid):
					return assetlist.StepChange{}, assetlist.ErrInvalid
				}
				return assetlist.StepChange{}, err
			}
			state := func(summary executiongrant.StepSummary) assetlist.StepState {
				result := assetlist.StepState{Mode: summary.Mode, Used: summary.Used}
				if summary.Mode == approvalpolicy.StepModeCap {
					result.Cap, result.Extra = summary.Cap, summary.Extra
				}
				return result
			}
			return assetlist.StepChange{Previous: state(change.Previous), Current: state(change.Current)}, nil
		},
	}
}

func harnessAccountSettings(t *testing.T, repository *sqlite.Repository, application catalog.ApplicationSchema) *accountsettings.Service {
	t.Helper()
	declared := application.StepPolicy
	if declared == nil {
		return nil
	}
	modes := make([]string, 0, len(declared.Modes))
	for _, mode := range declared.Modes {
		modes = append(modes, mode.Value)
	}
	service, err := accountsettings.New(repository, accountsettings.Config{
		Modes: modes,
		Steps: approvalpolicy.StepsPolicy{
			DefaultList: approvalpolicy.StepSetting{Mode: declared.DefaultMode.List, Cap: declared.Cap.Default},
			DefaultMCP:  approvalpolicy.StepSetting{Mode: declared.DefaultMode.MCP, Cap: declared.Cap.Default},
		},
		CapMinimum: declared.Cap.Minimum, CapMaximum: declared.Cap.Maximum,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func harnessApprovalPolicy(application catalog.ApplicationSchema) approvalpolicy.Policy {
	declared := application.Chat.ApprovalPolicy
	manual := make([]string, 0, len(declared.ManualApprovalCapabilities))
	for _, capability := range declared.ManualApprovalCapabilities {
		manual = append(manual, string(capability))
	}
	known := make([]string, 0, len(application.Capabilities))
	for _, capability := range application.Capabilities {
		known = append(known, string(capability.ID))
	}
	policy := approvalpolicy.New(declared.DefaultMode, declared.Modes, manual, known)
	if application.StepPolicy != nil {
		// As the application assembles it: the internal steps mode exists when the
		// Catalog declares the 3D step policy (#330), and the access-key policy of
		// the external MCP service decides under it.
		counted := make([]string, 0, len(application.StepPolicy.CountedCapabilities))
		for _, capability := range application.StepPolicy.CountedCapabilities {
			counted = append(counted, string(capability))
		}
		policy = policy.WithSteps(approvalpolicy.StepsPolicy{
			Counted:           counted,
			DefaultList:       approvalpolicy.StepSetting{Mode: application.StepPolicy.DefaultMode.List, Cap: application.StepPolicy.Cap.Default},
			DefaultMCP:        approvalpolicy.StepSetting{Mode: application.StepPolicy.DefaultMode.MCP, Cap: application.StepPolicy.Cap.Default},
			ExtraMaximum:      application.StepPolicy.ExtraSteps.Maximum,
			ExtraTotalMaximum: application.StepPolicy.ExtraSteps.TotalMaximum,
		})
	}
	return policy
}

// harnessAttachmentVerifier accepts claims about "own-" prefixed objects only,
// standing in for the production owner and session checks. Only the Tasks
// whose ID starts with "own-model" are 3D models: a model claim about any other
// Task is refused, like a Task without a saved model file.
type harnessAttachmentVerifier struct{}

func (harnessAttachmentVerifier) VerifyAttachment(_ context.Context, _, _ string, ref chat.AttachmentRef) (chat.Attachment, error) {
	for _, id := range []string{ref.TaskID, ref.AssetID, ref.CaptureID} {
		if id != "" && !strings.HasPrefix(id, "own-") {
			return chat.Attachment{}, chat.ErrAttachmentRejected
		}
	}
	if ref.Kind == chat.AttachmentKindModel && !strings.HasPrefix(ref.TaskID, "own-model") {
		return chat.Attachment{}, chat.ErrAttachmentRejected
	}
	attachment := chat.Attachment{Kind: ref.Kind, Role: chat.AttachmentRoleGenerated, AssetID: ref.AssetID, TaskID: ref.TaskID,
		CaptureID: ref.CaptureID, ImageSlot: ref.ImageSlot, URL: ref.URL}
	if ref.CaptureID != "" {
		attachment.Role = chat.AttachmentRoleCapture
	}
	if strings.HasPrefix(ref.AssetID, harnessCanvasPrefix) && ref.TaskID == "" && ref.CaptureID == "" {
		// An artboard snapshot is an owned upload.
		attachment.Role = chat.AttachmentRoleUpload
	}
	return attachment, nil
}

// harnessCanvasPrefix names the harness's artboard snapshots: 64x48 pictures
// whose format follows the prefix ("own-canvas-jpeg-1" is a JPEG). Every other
// asset has no readable size or format, so canvas turns stay out of the other
// tests. harnessMaskPrefix names its 64x48 masks that select a region (#583).
const (
	harnessCanvasPrefix = "own-canvas-"
	harnessMaskPrefix   = "own-mask-"
)

func (harnessAttachmentVerifier) ImageDimensions(_ context.Context, _, assetID string) (int, int, error) {
	if !strings.HasPrefix(assetID, harnessCanvasPrefix) {
		return 0, 0, errors.New("harness: no picture size")
	}
	return 64, 48, nil
}

func (harnessAttachmentVerifier) PictureFormat(_ context.Context, _, assetID string) (string, error) {
	format, _, found := strings.Cut(strings.TrimPrefix(assetID, harnessCanvasPrefix), "-")
	if !strings.HasPrefix(assetID, harnessCanvasPrefix) || !found {
		return "", errors.New("harness: no picture format")
	}
	return format, nil
}

func (harnessAttachmentVerifier) MeasureMask(_ context.Context, _, assetID string) (chat.SelectionMask, error) {
	if !strings.HasPrefix(assetID, harnessMaskPrefix) {
		return chat.SelectionMask{}, errors.New("harness: no mask")
	}
	return chat.SelectionMask{Width: 64, Height: 48, BBox: [4]int{8, 8, 23, 23}, Coverage: 0.08}, nil
}

// harnessCompactor is the scheduler's context compactor when it has one
// (#322); the default no-op scheduler has none.
func harnessCompactor(scheduler httpapi.ChatTurnScheduler) httpapi.ChatContextCompactor {
	if compactor, ok := scheduler.(httpapi.ChatContextCompactor); ok {
		return compactor
	}
	return nil
}

// assetListReferenceAssets is the harness copy of app's #331 reference port:
// the harness composes services directly instead of calling app.New.
type assetListReferenceAssets struct {
	inputs *inputassets.Service
	crops  *sqlite.InputAssetCropRepository
}

func (port assetListReferenceAssets) ResolveInputAsset(ctx context.Context,
	owner generation.OwnerID, inputAssetID generation.AssetID,
) (assetlist.InputAssetReference, bool, error) {
	asset, err := port.inputs.OwnedInputAsset(ctx, owner, inputAssetID)
	if err != nil {
		var domain *generation.Error
		if errors.As(err, &domain) &&
			(domain.Code == generation.ErrNotFound || domain.Code == generation.ErrForbidden) {
			return assetlist.InputAssetReference{}, false, nil
		}
		return assetlist.InputAssetReference{}, false, err
	}
	reference := assetlist.InputAssetReference{MediaType: asset.MediaType}
	crop, found, err := port.crops.Get(ctx, owner, inputAssetID)
	if err != nil {
		return assetlist.InputAssetReference{}, false, err
	}
	switch {
	case found:
		reference.IsCrop = true
		reference.CropSourceAssetID = crop.SourceAssetID
	case asset.AssetType == listcrop.CropAssetType:
		reference.IsCrop = true
	}
	return reference, true, nil
}

// testReferencePNG is a real PNG of the given size, used where a Provider
// decodes the pixels of an uploaded reference picture.
func testReferencePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			picture.Set(x, y, color.RGBA{R: 0x20, G: 0x40, B: 0x60, A: 0xff})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, picture); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// legacyHistoricalImage is the archived picture of the legacy fixtures: a
// valid 1x1 lossless WebP, because a delivered WebP is proved by its RIFF walk
// and a decode (#576).
const legacyHistoricalImage = "RIFF\x1c\x00\x00\x00WEBPVP8L\x0f\x00\x00\x00\x2f\x00\x00\x00\x00\x07\x10\xfd\x8f\xfe\x07\x22\xa2\xff\x01\x00"
