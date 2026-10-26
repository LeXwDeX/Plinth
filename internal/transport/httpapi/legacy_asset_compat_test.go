package httpapi_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestLegacyAssetRoutePreviewsHistoricalUploadFromConfiguredStorage(
	t *testing.T,
) {
	t.Parallel()
	const (
		storageKey = "image/ab/cd/historical.png"
		content    = "historical upload bytes"
	)
	_, handler, _, _ := newHTTPHarnessWithServices(
		t,
		nil,
		func(application *catalog.ApplicationSchema) {
			application.Runtime.HTTP.LegacyRoutes.AssetItem =
				"/configured-assets/{legacyAsset}"
		},
		nil,
		nil,
		nil,
		func(t *testing.T, databasePath string) {
			t.Helper()
			seedLegacyInputAsset(
				t,
				databasePath,
				"historical-asset",
				"owner-a",
				storageKey,
			)
			absolutePath := filepath.Join(
				filepath.Dir(databasePath),
				"legacy-assets",
				filepath.FromSlash(storageKey),
			)
			if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(absolutePath, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	)
	response := serveLegacyRequest(
		handler,
		http.MethodGet,
		"/configured-assets/historical-asset",
		nil,
	)
	if response.Code != http.StatusOK ||
		response.Header().Get("Content-Type") != "image/png" ||
		response.Header().Get("Content-Length") != "23" ||
		response.Body.String() != content {
		t.Fatalf(
			"status=%d headers=%v body=%q",
			response.Code,
			response.Header(),
			response.Body.String(),
		)
	}
}

func TestLegacyAssetUploadJSONAndBinaryContracts(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	payload := []byte("invalid PNG payload is intentionally preserved")
	request := legacyAssetUploadRequest(t, payload, "reference.png", "image/png")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("upload status = %d, body = %s", response.Code, response.Body.String())
	}
	expectedHash := sha256.Sum256(payload)
	var uploaded struct {
		AssetID   generation.AssetID `json:"asset_id"`
		AssetType string             `json:"asset_type"`
		StoredSHA string             `json:"stored_sha256"`
		FileSize  int64              `json:"file_size"`
	}
	if err := json.NewDecoder(response.Body).Decode(&uploaded); err != nil {
		t.Fatal(err)
	}
	if uploaded.AssetID == "" ||
		uploaded.AssetType != "image" ||
		uploaded.StoredSHA != hex.EncodeToString(expectedHash[:]) ||
		uploaded.FileSize != int64(len(payload)) {
		t.Fatalf("uploaded = %#v", uploaded)
	}

	jsonResponse := serveLegacyRequest(
		handler,
		http.MethodGet,
		"/api/assets/"+string(uploaded.AssetID)+"?format=json",
		nil,
	)
	if jsonResponse.Code != http.StatusOK {
		t.Fatalf("json status = %d, body = %s", jsonResponse.Code, jsonResponse.Body.String())
	}
	var metadata struct {
		ID               generation.AssetID `json:"id"`
		AssetType        string             `json:"asset_type"`
		OriginalFilename string             `json:"original_filename"`
		MediaType        string             `json:"mime_type"`
		FileSize         int64              `json:"file_size"`
		DataURI          string             `json:"data_uri"`
		CreatedAt        string             `json:"created_at"`
	}
	if err := json.NewDecoder(jsonResponse.Body).Decode(&metadata); err != nil {
		t.Fatal(err)
	}
	expectedDataURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(payload)
	if metadata.ID != uploaded.AssetID ||
		metadata.AssetType != "image" ||
		metadata.OriginalFilename != "reference.png" ||
		metadata.MediaType != "image/png" ||
		metadata.FileSize != int64(len(payload)) ||
		metadata.DataURI != expectedDataURI ||
		metadata.CreatedAt == "" {
		t.Fatalf("metadata = %#v", metadata)
	}

	binaryResponse := serveLegacyRequest(
		handler,
		http.MethodGet,
		"/api/assets/"+string(uploaded.AssetID),
		nil,
	)
	if binaryResponse.Code != http.StatusOK {
		t.Fatalf("binary status = %d, body = %s", binaryResponse.Code, binaryResponse.Body.String())
	}
	if binaryResponse.Header().Get("Content-Type") != "image/png" ||
		binaryResponse.Header().Get("Cache-Control") !=
			"private, no-store" ||
		!bytes.Equal(binaryResponse.Body.Bytes(), payload) {
		t.Fatalf(
			"binary headers = %#v, body = %q",
			binaryResponse.Header(),
			binaryResponse.Body.Bytes(),
		)
	}
}

func TestLegacyAssetWireFieldsComeFromProductSchema(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithApplication(
		t,
		nil,
		func(application *catalog.ApplicationSchema) {
			fields := &application.Runtime.InputAssets.API.Fields
			fields.AssetID = "configuredAssetId"
			fields.AssetType = "configuredAssetType"
			fields.StoredSHA256 = "configuredStoredHash"
			fields.FileSize = "configuredFileSize"
			fields.ID = "configuredId"
			fields.OriginalFilename = "configuredFilename"
			fields.MediaType = "configuredMediaType"
			fields.DataURI = "configuredDataUri"
			fields.CreatedAt = "configuredCreatedAt"
		},
	)
	request := legacyAssetUploadRequest(
		t,
		[]byte("schema-driven asset wire"),
		"configured.png",
		"image/png",
	)
	uploadedResponse := httptest.NewRecorder()
	handler.ServeHTTP(uploadedResponse, request)
	if uploadedResponse.Code != http.StatusOK {
		t.Fatalf(
			"upload status=%d body=%s",
			uploadedResponse.Code,
			uploadedResponse.Body,
		)
	}
	var uploaded map[string]any
	if err := json.Unmarshal(uploadedResponse.Body.Bytes(), &uploaded); err != nil {
		t.Fatal(err)
	}
	assetID, _ := uploaded["configuredAssetId"].(string)
	if assetID == "" ||
		uploaded["configuredAssetType"] != "image" ||
		uploaded["configuredStoredHash"] == "" ||
		uploaded["configuredFileSize"] == nil ||
		uploaded["asset_id"] != nil {
		t.Fatalf("configured upload wire=%#v", uploaded)
	}

	metadataResponse := serveLegacyRequest(
		handler,
		http.MethodGet,
		"/api/assets/"+assetID+"?format=json",
		nil,
	)
	if metadataResponse.Code != http.StatusOK {
		t.Fatalf(
			"metadata status=%d body=%s",
			metadataResponse.Code,
			metadataResponse.Body,
		)
	}
	var metadata map[string]any
	if err := json.Unmarshal(metadataResponse.Body.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["configuredId"] != assetID ||
		metadata["configuredAssetType"] != "image" ||
		metadata["configuredFilename"] != "configured.png" ||
		metadata["configuredMediaType"] != "image/png" ||
		metadata["configuredFileSize"] == nil ||
		metadata["configuredDataUri"] == "" ||
		metadata["configuredCreatedAt"] == "" ||
		metadata["id"] != nil {
		t.Fatalf("configured metadata wire=%#v", metadata)
	}
}

func seedLegacyInputAsset(
	t *testing.T,
	databasePath string,
	id string,
	owner string,
	storageKey string,
) {
	t.Helper()
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE asset_registry (
			id TEXT PRIMARY KEY,
			assetType TEXT NOT NULL,
			originalFilename TEXT NOT NULL,
			storedFilename TEXT NOT NULL,
			mimeType TEXT NOT NULL,
			fileSize INTEGER NOT NULL,
			sourceSha256 TEXT NOT NULL,
			storedSha256 TEXT NOT NULL,
			storagePath TEXT NOT NULL,
			uploaderUserId TEXT NOT NULL,
			uploaderEmail TEXT NOT NULL DEFAULT '',
			createdAt TEXT NOT NULL
		)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO asset_registry (
			id, assetType, originalFilename, storedFilename, mimeType,
			fileSize, sourceSha256, storedSha256, storagePath,
			uploaderUserId, uploaderEmail, createdAt
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id,
		"image",
		"historical.png",
		filepath.Base(storageKey),
		"image/png",
		23,
		"source-sha",
		"stored-sha",
		storageKey,
		owner,
		"owner@example.test",
		"2026-07-26T08:30:00Z",
	); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyAssetAuditUsesProductSchemaAndPersists(t *testing.T) {
	t.Parallel()
	_, handler, _, _ := newHTTPHarnessWithServices(
		t,
		[]string{"admin"},
		func(application *catalog.ApplicationSchema) {
			audit := &application.Runtime.InputAssets.Audit
			audit.UploadAction = "login"
			audit.AccessAction = "logout"
			audit.ResourceType = "configured_asset"
			audit.DetailFields.Filename = "configured_filename"
			audit.DetailFields.MediaType = "configured_media"
			audit.DetailFields.FileSize = "configured_size"
		},
		nil,
		nil,
		nil,
	)
	payload := []byte("schema-driven-asset")
	upload := httptest.NewRecorder()
	handler.ServeHTTP(
		upload,
		legacyAssetUploadRequest(
			t,
			payload,
			"audit.png",
			"image/png",
		),
	)
	if upload.Code != http.StatusOK {
		t.Fatalf("upload status=%d body=%s", upload.Code, upload.Body)
	}
	var uploaded struct {
		AssetID generation.AssetID `json:"asset_id"`
	}
	if err := json.NewDecoder(upload.Body).Decode(&uploaded); err != nil {
		t.Fatal(err)
	}
	assertPersistedInputAssetAudit(
		t,
		handler,
		"login",
		"configured_asset",
		string(uploaded.AssetID),
		map[string]any{
			"configured_filename": "audit.png",
			"configured_media":    "image/png",
			"configured_size":     float64(len(payload)),
		},
	)

	access := serveLegacyRequest(
		handler,
		http.MethodGet,
		"/api/assets/"+string(uploaded.AssetID),
		nil,
	)
	if access.Code != http.StatusOK {
		t.Fatalf("access status=%d body=%s", access.Code, access.Body)
	}
	assertPersistedInputAssetAudit(
		t,
		handler,
		"logout",
		"configured_asset",
		string(uploaded.AssetID),
		map[string]any{},
	)
}

func TestV1AssetAuditUsesTheSameProductSchemaContract(t *testing.T) {
	t.Parallel()
	_, handler, _, _ := newHTTPHarnessWithServices(
		t,
		[]string{"admin"},
		func(application *catalog.ApplicationSchema) {
			audit := &application.Runtime.InputAssets.Audit
			audit.UploadAction = "login"
			audit.AccessAction = "logout"
			audit.ResourceType = "configured_v1_asset"
			audit.DetailFields.Filename = "configured_filename"
			audit.DetailFields.MediaType = "configured_media"
			audit.DetailFields.FileSize = "configured_size"
		},
		nil,
		nil,
		nil,
	)
	payload := []byte("v1-schema-driven-asset")
	request := legacyAssetUploadRequest(
		t,
		payload,
		"v1-audit.png",
		"image/png",
	)
	request.URL.Path = "/api/v1/assets"
	request.RequestURI = "/api/v1/assets"
	upload := httptest.NewRecorder()
	handler.ServeHTTP(upload, request)
	if upload.Code != http.StatusCreated {
		t.Fatalf("upload status=%d body=%s", upload.Code, upload.Body)
	}
	var uploaded struct {
		ID generation.AssetID `json:"id"`
	}
	if err := json.NewDecoder(upload.Body).Decode(&uploaded); err != nil {
		t.Fatal(err)
	}
	assertPersistedInputAssetAudit(
		t,
		handler,
		"login",
		"configured_v1_asset",
		string(uploaded.ID),
		map[string]any{
			"configured_filename": "v1-audit.png",
			"configured_media":    "image/png",
			"configured_size":     float64(len(payload)),
		},
	)

	accessRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/assets/"+string(uploaded.ID),
		nil,
	)
	accessRequest.Header.Set("X-User-ID", "owner-a")
	access := httptest.NewRecorder()
	handler.ServeHTTP(access, accessRequest)
	if access.Code != http.StatusOK {
		t.Fatalf("access status=%d body=%s", access.Code, access.Body)
	}
	assertPersistedInputAssetAudit(
		t,
		handler,
		"logout",
		"configured_v1_asset",
		string(uploaded.ID),
		map[string]any{},
	)
}

func assertPersistedInputAssetAudit(
	t *testing.T,
	handler http.Handler,
	action string,
	resourceType string,
	resourceID string,
	detail map[string]any,
) {
	t.Helper()
	response := administrationRequest(
		handler,
		http.MethodGet,
		"/api/admin/audit-logs?action="+action,
		"",
		nil,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("audit status=%d body=%s", response.Code, response.Body)
	}
	body := decodeAdministrationBody(t, response)
	if body["total"] != float64(1) {
		t.Fatalf("%s audit body=%#v", action, body)
	}
	event := body["data"].([]any)[0].(map[string]any)
	if event["action"] != action ||
		event["userId"] != "owner-a" ||
		event["resourceType"] != resourceType ||
		event["resourceId"] != resourceID {
		t.Fatalf("%s audit event=%#v", action, event)
	}
	var gotDetail map[string]any
	if err := json.Unmarshal([]byte(event["detail"].(string)), &gotDetail); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotDetail, detail) {
		t.Fatalf("%s audit detail=%#v", action, gotDetail)
	}
}

func TestLegacyAssetAccessScopePreservesAdminBehavior(t *testing.T) {
	t.Parallel()
	t.Run("non-admin cannot read another owner's asset", func(t *testing.T) {
		_, handler, inputs := newHTTPHarnessWithRoles(t, nil)
		asset, err := inputs.UploadAsset(
			context.Background(),
			"another-owner",
			generation.InputAssetUpload{
				Body:             strings.NewReader("private"),
				OriginalFilename: "private.png",
				MediaType:        "image/png",
				AssetType:        "image",
				MaxBytes:         1024,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		response := serveLegacyRequest(
			handler,
			http.MethodGet,
			"/api/assets/"+string(asset.ID),
			nil,
		)
		if response.Code != http.StatusForbidden ||
			!strings.Contains(response.Body.String(), "无权限访问") {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
	})

	t.Run("admin can read another owner's asset", func(t *testing.T) {
		payload := modelinputtest.Triangle(modelinputtest.Options{})
		_, handler, inputs := newHTTPHarnessWithRoles(t, []string{"admin"})
		asset, err := inputs.UploadAsset(
			context.Background(),
			"another-owner",
			generation.InputAssetUpload{
				Body:             bytes.NewReader(payload),
				OriginalFilename: "shared.glb",
				MediaType:        "application/octet-stream",
				AssetType:        "model",
				MaxBytes:         1024,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		response := serveLegacyRequest(
			handler,
			http.MethodGet,
			"/api/assets/"+string(asset.ID),
			nil,
		)
		if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), payload) {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
	})
}

func TestLegacyAssetRulesAndErrorsComeFromProductSchema(t *testing.T) {
	t.Parallel()
	t.Run("configured upload field and missing-file text", func(t *testing.T) {
		_, handler, _ := newHTTPHarnessWithApplication(
			t,
			nil,
			func(application *catalog.ApplicationSchema) {
				application.Runtime.InputAssets.UploadFieldName = "asset"
				application.Content.Messages["error.legacy_asset_missing_upload"] = "Schema 要求 asset 文件"
			},
		)
		response := httptest.NewRecorder()
		handler.ServeHTTP(
			response,
			legacyAssetUploadRequest(
				t,
				[]byte("payload"),
				"reference.png",
				"image/png",
			),
		)
		if response.Code != http.StatusBadRequest ||
			!isErrorBody(response, "Schema 要求 asset 文件") {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
	})

	t.Run("configured file limit and formatted text", func(t *testing.T) {
		_, handler, _ := newHTTPHarnessWithApplication(
			t,
			nil,
			func(application *catalog.ApplicationSchema) {
				application.Runtime.InputAssets.UploadMaxBytes = 1 << 20
				application.Runtime.InputAssets.ModelProfile.Limits.MaxBytes = 1 << 20
				application.Runtime.InputAssets.ModelProfile.Limits.MaxJSONBytes = 1 << 20
				application.Content.Messages["error.legacy_asset_upload_too_large"] = "Schema 限制 {limit}"
			},
		)
		response := httptest.NewRecorder()
		handler.ServeHTTP(
			response,
			legacyAssetUploadRequest(
				t,
				bytes.Repeat([]byte("x"), (1<<20)+1),
				"large.png",
				"image/png",
			),
		)
		if response.Code != http.StatusBadRequest ||
			!isErrorBody(response, "Schema 限制 1.04 MB") { // decimal MB with its unit (#327)
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
	})

	t.Run("configured extension classification", func(t *testing.T) {
		_, handler, _ := newHTTPHarnessWithApplication(
			t,
			nil,
			func(application *catalog.ApplicationSchema) {
				// A test-only extension proves classification is declared, while
				// the model validator still checks actual static GLB bytes.
				input := &application.Runtime.InputAssets
				input.ModelProfile.Extensions = append(input.ModelProfile.Extensions, ".surface")
				for index := range input.TypeRules {
					if input.TypeRules[index].AssetType == input.ModelProfile.AssetType {
						input.TypeRules[index].Extensions = append(input.TypeRules[index].Extensions, ".surface")
					}
				}
			},
		)
		request := legacyAssetUploadRequest(
			t,
			modelinputtest.Triangle(modelinputtest.Options{}),
			"scene.surface",
			"application/octet-stream",
		)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		var body struct {
			AssetType string `json:"asset_type"`
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusOK || body.AssetType != "model" {
			t.Fatalf("status = %d, body = %#v", response.Code, body)
		}
	})
}

func legacyAssetUploadRequest(
	t *testing.T,
	payload []byte,
	filename string,
	mediaType string,
) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set(
		"Content-Disposition",
		`form-data; name="file"; filename="`+filename+`"`,
	)
	header.Set("Content-Type", mediaType)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(part, bytes.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/assets/upload",
		bytes.NewReader(body.Bytes()),
	)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-User-ID", "owner-a")
	return request
}
