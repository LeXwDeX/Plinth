package httpapi_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"reflect"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
)

func httpModelInputPolicy(t *testing.T, profile *catalog.ModelInputProfile) modelinput.Policy {
	t.Helper()
	l := profile.Limits
	policy := modelinput.Policy{ProfileID: profile.ID, Revision: profile.Revision, Limits: modelinput.Limits{
		MaxBytes: l.MaxBytes, MaxJSONBytes: l.MaxJSONBytes, MaxDepth: l.MaxDepth, MaxElements: l.MaxElements,
		MaxVertices: l.MaxVertices, MaxTriangles: l.MaxTriangles, MaxNodes: l.MaxNodes, MaxWork: l.MaxWork,
		MaxImagePixels: l.MaxImagePixels, MaxTotalImagePixels: l.MaxTotalImagePixels,
	}}
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	return policy
}
func uploadedModelCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	source, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return source
}
func uploadedModelRepository(t *testing.T, module *generation.Module) *sqlite.Repository {
	t.Helper()
	state, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing HTTP fixture repository")
	}
	return state.(*fixtureExecutionState).repository
}
func uploadModelHTTP(t *testing.T, handler http.Handler, cookie *http.Cookie, owner, path string, application catalog.ApplicationSchema, filename string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="`+application.Runtime.InputAssets.UploadFieldName+`"; filename="`+filename+`"`)
	header.Set("Content-Type", application.Runtime.InputAssets.ModelProfile.UploadMediaType)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Origin", "http://example.com")
	request.Header.Set("X-User-ID", owner)
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
func uploadModelIdentity(t *testing.T, response *httptest.ResponseRecorder, application catalog.ApplicationSchema, legacy bool) generation.AssetID {
	t.Helper()
	var value map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	field := application.Runtime.InputAssets.API.Fields.ID
	if legacy {
		field = application.Runtime.InputAssets.API.Fields.AssetID
	}
	var id generation.AssetID
	if err := json.Unmarshal(value[field], &id); err != nil || id == "" {
		t.Fatalf("upload identity: %s %v", response.Body.String(), err)
	}
	return id
}
func uploadedModelUpdate(t *testing.T, source *catalog.Catalog, revision int64, asset generation.AssetID, kind harness.SourceKind, task string) string {
	t.Helper()
	offering, ok := source.Get("meshy/processing/remesh")
	if !ok {
		t.Fatal("default remesh missing")
	}
	selection := harness.Selection{
		OperationID: "remesh", OfferingID: string(offering.ID), OfferingRevision: offering.Revision,
		EnabledOperations: []string{"remesh"},
		Sources:           []harness.Source{{Kind: kind, Parameter: "source_asset", AssetID: string(asset), TaskID: task}},
		FixedParameters:   map[string]json.RawMessage{}, FillableParameters: []string{},
	}
	encoded, err := json.Marshal(map[string]any{"expected_revision": revision, "selection": selection})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestUploadedModelHTTPNativeAndLegacyValidateBeforeRegistration(t *testing.T) {
	t.Parallel()
	source := uploadedModelCatalog(t)
	for _, legacy := range []bool{false, true} {
		for _, kind := range []string{"valid", "invalid", "unsupported", "limit"} {
			t.Run(map[bool]string{false: "native", true: "legacy"}[legacy]+"/"+kind, func(t *testing.T) {
				module, handler, cookie, session := grantTestBrowserWithApplication(t, func(application *catalog.ApplicationSchema) {
					if kind == "limit" {
						application.Runtime.InputAssets.ModelProfile.Limits.MaxVertices = 2
					}
				}, []string{"user"})
				application := source.Application()
				path := application.Runtime.HTTP.Routes.Assets
				status := http.StatusCreated
				if legacy {
					path = application.Runtime.HTTP.LegacyRoutes.AssetUpload
					status = http.StatusOK
				}
				data := modelinputtest.Triangle(modelinputtest.Options{})
				message := ""
				switch kind {
				case "invalid":
					data = []byte("not a GLB")
					message = "error.invalid_model_input"
				case "unsupported":
					doc, bin := modelinputtest.Document(modelinputtest.Options{})
					doc["extensionsUsed"] = []any{"KHR_draco_mesh_compression"}
					data = modelinputtest.GLB(doc, bin)
					message = "error.unsupported_model_input"
				case "limit":
					message = "error.model_input_limit"
				}
				before := harnessWorkSet(t, handler, cookie, session)
				response := uploadModelHTTP(t, handler, cookie, "owner-a", path, application, "triangle.glb", data)
				_, total, err := uploadedModelRepository(t, module).ListOwnedInputAssets(context.Background(), "owner-a", 1, 20)
				if err != nil {
					t.Fatal(err)
				}
				if kind == "valid" {
					if response.Code != status || total != 1 {
						t.Fatalf("valid upload=%d %s registrations=%d", response.Code, response.Body.String(), total)
					}
					id := uploadModelIdentity(t, response, application, legacy)
					asset, err := uploadedModelRepository(t, module).GetInputAsset(context.Background(), id)
					hash := sha256.Sum256(data)
					if err != nil || asset.SHA256 != hex.EncodeToString(hash[:]) || asset.Size != int64(len(data)) {
						t.Fatalf("upload changed bytes: %+v %v", asset, err)
					}
				} else {
					if response.Code != http.StatusBadRequest || total != 0 || application.Content.Messages[message] == "" || !strings.Contains(response.Body.String(), application.Content.Messages[message]) {
						t.Fatalf("%s upload=%d %s registrations=%d expected message=%q", kind, response.Code, response.Body.String(), total, application.Content.Messages[message])
					}
				}
				if after := harnessWorkSet(t, handler, cookie, session); after != before {
					t.Fatalf("upload created work: before=%+v after=%+v", before, after)
				}
			})
		}
	}
}

func TestUploadedModelHTTPHarnessPersistsOwnedAssetCASAndExactBytes(t *testing.T) {
	t.Parallel()
	source := uploadedModelCatalog(t)
	application := source.Application()
	module, handler, cookie, session := grantTestBrowserWithApplication(t, nil, []string{"user"})
	before := harnessWorkSet(t, handler, cookie, session)
	data := modelinputtest.Triangle(modelinputtest.Options{Indexed: true})
	uploaded := uploadModelHTTP(t, handler, cookie, "owner-a", application.Runtime.HTTP.Routes.Assets, application, "角色.glb", data)
	if uploaded.Code != http.StatusCreated {
		t.Fatal(uploaded.Code, uploaded.Body.String())
	}
	id := uploadModelIdentity(t, uploaded, application, false)
	body := uploadedModelUpdate(t, source, 0, id, harness.SourceInputAsset, "")
	put := grantTestRequest(t, handler, cookie, http.MethodPut, harnessPath(session), body, "")
	if put.Code != http.StatusOK {
		t.Fatal(put.Code, put.Body.String())
	}
	var saved struct {
		Selection   harness.Selection    `json:"selection"`
		SourceViews []resolve.SourceView `json:"source_views"`
		Status      string               `json:"status"`
	}
	if err := json.Unmarshal(put.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Status != "ready" || saved.Selection.Revision != 1 || len(saved.SourceViews) != 1 || saved.SourceViews[0].AssetID != id || saved.SourceViews[0].OriginalFilename != "角色.glb" || saved.SourceViews[0].ProfileID != application.Runtime.InputAssets.ModelProfile.ID || saved.Selection.Sources[0].TaskID != "" {
		t.Fatalf("saved=%s", put.Body.String())
	}
	get := grantTestRequest(t, handler, cookie, http.MethodGet, harnessPath(session), "", "")
	var restored struct {
		Selection   harness.Selection    `json:"selection"`
		SourceViews []resolve.SourceView `json:"source_views"`
	}
	if get.Code != http.StatusOK || json.Unmarshal(get.Body.Bytes(), &restored) != nil || !reflect.DeepEqual(saved.Selection, restored.Selection) || !reflect.DeepEqual(saved.SourceViews, restored.SourceViews) {
		t.Fatalf("refresh=%d %s", get.Code, get.Body.String())
	}
	stale := grantTestRequest(t, handler, cookie, http.MethodPut, harnessPath(session), body, "")
	if stale.Code != http.StatusConflict {
		t.Fatal(stale.Code, stale.Body.String())
	}
	download := grantTestRequest(t, handler, cookie, http.MethodGet, strings.ReplaceAll(application.Runtime.HTTP.Routes.AssetItem, "{assetID}", string(id)), "", "")
	if download.Code != http.StatusOK || !bytes.Equal(download.Body.Bytes(), data) {
		t.Fatalf("delivery changed upload: %d", download.Code)
	}
	asset, err := uploadedModelRepository(t, module).GetInputAsset(context.Background(), id)
	hash := sha256.Sum256(download.Body.Bytes())
	if err != nil || asset.SHA256 != hex.EncodeToString(hash[:]) {
		t.Fatal("persisted upload hash changed", err)
	}
	if after := harnessWorkSet(t, handler, cookie, session); after != before {
		t.Fatalf("source selection created work: before=%+v after=%+v", before, after)
	}
}

func TestUploadedModelHTTPHarnessRejectsForeignMissingAndTaskRelabel(t *testing.T) {
	t.Parallel()
	source := uploadedModelCatalog(t)
	application := source.Application()
	module, handler, cookie, session := grantTestBrowserWithApplication(t, nil, []string{"user"})
	foreign := uploadModelHTTP(t, handler, nil, "owner-b", application.Runtime.HTTP.Routes.Assets, application, "foreign.glb", modelinputtest.Triangle(modelinputtest.Options{}))
	if foreign.Code != http.StatusCreated {
		t.Fatal(foreign.Code, foreign.Body.String())
	}
	foreignID := uploadModelIdentity(t, foreign, application, false)
	task := createSucceededMeshyModelTask(t, module, "owner-a")
	if len(task.Assets) == 0 {
		t.Fatal("fixture Task has no asset")
	}
	before := harnessWorkSet(t, handler, cookie, session)
	var inaccessible string
	for _, test := range []struct {
		name   string
		asset  generation.AssetID
		task   string
		status int
	}{{"foreign", foreignID, "", http.StatusNotFound}, {"missing", "missing-upload", "", http.StatusNotFound}, {"task-output", task.Assets[0].ID, "", http.StatusNotFound}, {"hybrid", foreignID, string(task.ID), http.StatusBadRequest}} {
		t.Run(test.name, func(t *testing.T) {
			put := grantTestRequest(t, handler, cookie, http.MethodPut, harnessPath(session), uploadedModelUpdate(t, source, 0, test.asset, harness.SourceInputAsset, test.task), "")
			if put.Code != test.status {
				t.Fatalf("rejected=%d %s", put.Code, put.Body.String())
			}
			// request_id names this one request (#233); everything else must match.
			body := withoutRequestID(t, put.Body.Bytes())
			if test.name == "foreign" {
				inaccessible = body
			}
			if test.name == "missing" && body != inaccessible {
				t.Fatal("foreign and missing uploads distinguishable")
			}
		})
	}
	selected, err := uploadedModelRepository(t, module).GetHarnessSelection(context.Background(), "owner-a", session)
	if err != nil || selected.Revision != 0 || len(selected.Sources) != 0 {
		t.Fatalf("rejected source persisted: %+v %v", selected, err)
	}
	get := grantTestRequest(t, handler, cookie, http.MethodGet, harnessPath(session), "", "")
	if strings.Contains(get.Body.String(), "foreign.glb") || strings.Contains(get.Body.String(), string(foreignID)) {
		t.Fatal("foreign upload leaked into source view")
	}
	if after := harnessWorkSet(t, handler, cookie, session); after != before {
		t.Fatalf("rejection created work: before=%+v after=%+v", before, after)
	}
}
