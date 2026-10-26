package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
)

func TestApplicationRetiresSavedBuiltInSkillsBeforeServingNewTurns(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "art-ai.db")
	repo, err := sqlite.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("../agentconfiguration/testdata/legacy_builtin_skill.json")
	if err != nil {
		t.Fatal(err)
	}
	var old catalog.SkillSchema
	if err := json.Unmarshal(fixture, &old); err != nil {
		t.Fatal(err)
	}
	legacyCatalog := catalog.SkillCatalogSchema{Revision: "legacy", Skills: []catalog.SkillSchema{old}}
	service, err := agentconfiguration.New(repo, product.Application(), legacyCatalog, product.List(""))
	if err != nil {
		t.Fatal(err)
	}
	base, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	oldVersion, err := service.Publish(context.Background(), base.Revision, "admin", "publish", base.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	application, err := New(Config{
		RuntimeSettings: map[string]string{"llm_model": "test-model"},
		Environment:     "development", Address: "127.0.0.1:0",
		DatabasePath: databasePath, StorageRoot: filepath.Join(root, "storage"),
		LegacyGeneratedImagesRoot: filepath.Join(root, "generated"),
		ProviderMode:              "mock", AgentMode: "mock", AuthMode: "development",
		DevelopmentUser: "owner-a",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.generation.Close() })
	repo, err = sqlite.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	active, err := repo.LatestAgentConfiguration(context.Background())
	if err != nil || active.Revision == oldVersion.Revision || len(active.Configuration.Skills) != 0 {
		t.Fatalf("startup did not publish retired configuration: %#v %v", active, err)
	}
	historical, err := repo.AgentConfigurationVersion(context.Background(), oldVersion.Revision)
	if err != nil || len(historical.Configuration.Skills) != 1 {
		t.Fatalf("startup rewrote historical version: %#v %v", historical, err)
	}
}

func TestDisabledLegacyAssetProxyDoesNotFetchOrExposeURL(
	t *testing.T,
) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := taskCatalog.Application().Runtime.AssetDelivery.Proxy
	root := t.TempDir()
	cacheRoot := filepath.Join(root, "legacy-cache")
	thumbnailRoot := filepath.Join(root, "legacy-thumbnails")
	targetURL :=
		"https://cdn.meshy.ai/models/a.glb?Signature=cached"
	sum := sha256.Sum256([]byte(targetURL))
	hash := hex.EncodeToString(sum[:])
	cacheDirectory := filepath.Join(
		cacheRoot,
		hash[:schema.Cache.ShardCharacters],
	)
	if err := os.MkdirAll(cacheDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(cacheDirectory, hash+".glb"),
		[]byte("cached-model"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	metadata, err := json.Marshal(map[string]any{
		schema.Cache.MetadataFields.URL:         targetURL,
		schema.Cache.MetadataFields.ContentType: "model/gltf-binary",
		schema.Cache.MetadataFields.Size:        len("cached-model"),
		schema.Cache.MetadataFields.CachedAt:    "2026-07-27T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(
			cacheDirectory,
			hash+schema.Cache.MetadataSuffix,
		),
		metadata,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	application, err := New(Config{
		RuntimeSettings: map[string]string{
			schema.Cache.RootSetting:     cacheRoot,
			schema.Thumbnail.RootSetting: thumbnailRoot,
			"llm_model":                  "test-model",
		},
		Environment:               "development",
		Address:                   "127.0.0.1:0",
		DatabasePath:              filepath.Join(root, "art-ai.db"),
		StorageRoot:               filepath.Join(root, "storage"),
		LegacyGeneratedImagesRoot: filepath.Join(root, "generated"),
		ProviderMode:              "mock",
		AgentMode:                 "mock",
		AuthMode:                  "development",
		DevelopmentUser:           "owner-a",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.generation.Close() })

	query := url.Values{schema.API.Query.URL: []string{targetURL}}
	response := httptest.NewRecorder()
	application.server.Handler.ServeHTTP(
		response,
		httptest.NewRequest(
			http.MethodGet,
			taskCatalog.Application().Runtime.HTTP.LegacyRoutes.
				MeshyProxyAsset+"?"+query.Encode(),
			nil,
		),
	)
	if response.Code != http.StatusNotFound ||
		strings.Contains(response.Body.String(), targetURL) ||
		strings.Contains(response.Header().Get("Location"), targetURL) {
		t.Fatalf(
			"disabled legacy proxy response = status %d headers %#v body %q",
			response.Code,
			response.Header(),
			response.Body.String(),
		)
	}
}

// #229: /api/version reports the commit the binary was linked from (the same
// value as the image's OCI revision), never a separately written file.
func TestRuntimeRevisionReportsLinkedCommitOrUnknown(t *testing.T) {
	original := sourceRevision
	t.Cleanup(func() { sourceRevision = original })
	for _, testCase := range []struct{ linked, want string }{
		{"9517d748e7d0736e3d7cc23c79c37873861d9ce6", "9517d748e7d0736e3d7cc23c79c37873861d9ce6"},
		{"", "unknown"},
		{"unknown", "unknown"},
		{"local", "unknown"},
		{"9517d74", "unknown"},
		{"9517D748E7D0736E3D7CC23C79C37873861D9CE6", "unknown"},
	} {
		sourceRevision = testCase.linked
		if got := runtimeRevision(); got != testCase.want {
			t.Errorf("linked %q: runtimeRevision() = %q, want %q", testCase.linked, got, testCase.want)
		}
	}
}

func TestApplicationSessionCookieNameRuntimeOverride(t *testing.T) {
	names := []string{"art_ai_stage_session", "art_ai_formal_session"}
	for _, cookieName := range names {
		t.Run(cookieName, func(t *testing.T) {
			root := t.TempDir()
			application, err := New(Config{
				RuntimeSettings: map[string]string{
					"llm_model":           "test-model",
					"session_cookie_name": cookieName,
				},
				Environment:               "development",
				Address:                   "127.0.0.1:0",
				DatabasePath:              filepath.Join(root, "art-ai.db"),
				StorageRoot:               filepath.Join(root, "storage"),
				LegacyGeneratedImagesRoot: filepath.Join(root, "generated"),
				ProviderMode:              "mock",
				AgentMode:                 "mock",
				AuthMode:                  "session",
				SessionSecret:             strings.Repeat("s", 32),
				AdminUsername:             "schema-admin",
				AdminPassword:             "correct-horse",
				AdminName:                 "Schema 管理员",
				AdminEmail:                "admin@example.test",
			}, discardLogger())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = application.generation.Close() })

			handler := application.server.Handler
			login := httptest.NewRecorder()
			loginRequest := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(
				`{"username":"schema-admin","password":"correct-horse"}`,
			))
			loginRequest.Header.Set("Content-Type", "application/json")
			handler.ServeHTTP(login, loginRequest)
			if login.Code != http.StatusOK {
				t.Fatalf("login status=%d body=%s", login.Code, login.Body)
			}
			cookies := login.Result().Cookies()
			if len(cookies) != 1 || cookies[0].Name != cookieName {
				t.Fatalf("login cookies=%#v, want name %q", cookies, cookieName)
			}

			session := httptest.NewRecorder()
			sessionRequest := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
			sessionRequest.AddCookie(cookies[0])
			handler.ServeHTTP(session, sessionRequest)
			if session.Code != http.StatusOK || !strings.Contains(session.Body.String(), "Schema 管理员") {
				t.Fatalf("session status=%d body=%s", session.Code, session.Body)
			}

			chatSession := httptest.NewRecorder()
			chatSessionRequest := httptest.NewRequest(http.MethodPost, "/api/chat/sessions", strings.NewReader(`{"title":"cookie override"}`))
			chatSessionRequest.Header.Set("Content-Type", "application/json")
			chatSessionRequest.Header.Set("Origin", "http://example.com")
			chatSessionRequest.AddCookie(cookies[0])
			handler.ServeHTTP(chatSession, chatSessionRequest)
			if chatSession.Code != http.StatusCreated {
				t.Fatalf("create chat session status=%d body=%s", chatSession.Code, chatSession.Body.String())
			}
			var createdSession struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(chatSession.Body.Bytes(), &createdSession); err != nil || createdSession.ID == "" {
				t.Fatalf("chat session=%s err=%v", chatSession.Body.String(), err)
			}
			prepare := httptest.NewRecorder()
			prepareRequest := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/prepare", strings.NewReader(
				`{"session_id":"`+createdSession.ID+`","capability":"text-to-3d","offering_id":"meshy/meshy-7.1/text-to-3d","input":{"prompt":"a carved oak chair"}}`,
			))
			prepareRequest.Header.Set("Content-Type", "application/json")
			prepareRequest.Header.Set("Origin", "http://example.com")
			prepareRequest.AddCookie(cookies[0])
			handler.ServeHTTP(prepare, prepareRequest)
			if prepare.Code != http.StatusCreated {
				t.Fatalf("prepare task status=%d body=%s", prepare.Code, prepare.Body.String())
			}
			var prepared struct {
				ID string `json:"prepared_input_id"`
			}
			if err := json.Unmarshal(prepare.Body.Bytes(), &prepared); err != nil || prepared.ID == "" {
				t.Fatalf("prepared task=%s err=%v", prepare.Body.String(), err)
			}
			grant := httptest.NewRecorder()
			grantRequest := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/grants", strings.NewReader(
				`{"prepared_input_id":"`+prepared.ID+`"}`,
			))
			grantRequest.Header.Set("Content-Type", "application/json")
			grantRequest.Header.Set("Origin", "http://example.com")
			grantRequest.Header.Set("Idempotency-Key", "cookie-override-grant")
			grantRequest.AddCookie(cookies[0])
			handler.ServeHTTP(grant, grantRequest)
			if grant.Code != http.StatusCreated {
				t.Fatalf("request execution grant status=%d body=%s", grant.Code, grant.Body.String())
			}

			logout := httptest.NewRecorder()
			logoutRequest := httptest.NewRequest(http.MethodDelete, "/api/auth/session", nil)
			logoutRequest.AddCookie(cookies[0])
			logoutRequest.Header.Set("Origin", "http://example.com")
			handler.ServeHTTP(logout, logoutRequest)
			if logout.Code != http.StatusOK {
				t.Fatalf("logout status=%d body=%s", logout.Code, logout.Body)
			}
			cleared := logout.Result().Cookies()
			if len(cleared) != 1 || cleared[0].Name != cookieName || cleared[0].MaxAge >= 0 {
				t.Fatalf("logout cookies=%#v, want cleared name %q", cleared, cookieName)
			}

			publicSchema := httptest.NewRecorder()
			handler.ServeHTTP(publicSchema, httptest.NewRequest(http.MethodGet, "/api/v1/schema", nil))
			if strings.Contains(publicSchema.Body.String(), cookieName) ||
				strings.Contains(publicSchema.Body.String(), "SESSION_COOKIE_NAME") {
				t.Fatalf("public schema leaked private cookie setting: %s", publicSchema.Body)
			}
		})
	}
}

func TestApplicationRejectsSkillCatalogCategoryAbsentFromProductSchema(
	t *testing.T,
) {
	root := t.TempDir()
	skillCatalogFile := filepath.Join(root, "invalid-category-skills.json")
	if err := os.WriteFile(skillCatalogFile, []byte(`{
		"revision":"configured-invalid-category",
		"skills":[{
			"id":"configured-skill",
			"name":"Configured skill",
			"category":"category-absent-from-product-schema",
			"recommended_size":"1024x1024",
			"thumbnail_path":"/configured.webp",
			"raw_image_url":"https://configured.invalid/image.png",
			"prompt":"configured prompt"
		}]
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	application, err := New(Config{
		Environment:               "development",
		Address:                   "127.0.0.1:0",
		DatabasePath:              filepath.Join(root, "art-ai.db"),
		StorageRoot:               filepath.Join(root, "storage"),
		LegacyGeneratedImagesRoot: filepath.Join(root, "generated"),
		SkillCatalogFile:          skillCatalogFile,
		ProviderMode:              "mock",
		AgentMode:                 "mock",
		RuntimeSettings:           map[string]string{"llm_model": "configured-test-model"},
		AuthMode:                  "development",
		DevelopmentUser:           "owner-a",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if application != nil {
		_ = application.generation.Close()
	}
	if err == nil ||
		!strings.Contains(err.Error(), "category-absent-from-product-schema") {
		t.Fatalf("startup error = %v", err)
	}
}

func TestApplicationRejectsSkillSizeAbsentFromSkillOfferingSchema(
	t *testing.T,
) {
	root := t.TempDir()
	skillCatalogFile := filepath.Join(root, "invalid-size-skills.json")
	if err := os.WriteFile(skillCatalogFile, []byte(`{
		"revision":"configured-invalid-size",
		"skills":[{
			"id":"configured-skill",
			"name":"Configured skill",
			"category":"anime",
			"recommended_size":"configured-size-absent-from-offerings",
			"thumbnail_path":"/configured.webp",
			"raw_image_url":"https://configured.invalid/image.png",
			"prompt":"configured prompt"
		}]
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	application, err := New(Config{
		Environment:               "development",
		Address:                   "127.0.0.1:0",
		DatabasePath:              filepath.Join(root, "art-ai.db"),
		StorageRoot:               filepath.Join(root, "storage"),
		LegacyGeneratedImagesRoot: filepath.Join(root, "generated"),
		SkillCatalogFile:          skillCatalogFile,
		ProviderMode:              "mock",
		AgentMode:                 "mock",
		RuntimeSettings:           map[string]string{"llm_model": "configured-test-model"},
		AuthMode:                  "development",
		DevelopmentUser:           "owner-a",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if application != nil {
		_ = application.generation.Close()
	}
	if err == nil ||
		!strings.Contains(err.Error(), "configured-size-absent-from-offerings") {
		t.Fatalf("startup error = %v", err)
	}
}

func TestLiveRegistrationAdvertisesOnlyImplementedCapabilities(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	registry := providers.NewRegistry()
	err = registerProviders(Config{
		ProviderMode: "live",
		RuntimeSettings: map[string]string{
			"openai_base_url":  "https://openai.test/v1",
			"openai_api_key":   "openai-key",
			"gemini_base_url":  "https://gemini.test/v1",
			"gemini_api_key":   "gemini-key",
			"meshy_base_url":   "https://meshy.test",
			"meshy_api_key":    "meshy-key",
			"tripo_base_url":   "https://tripo.test/v2/openapi",
			"tripo_api_key":    "tripo-key",
			"matting_base_url": "https://matting.test",
			"matting_api_key":  "matting-key",
		},
	}, taskCatalog, registry, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []generation.OfferingID{
		"openai/gpt-image-2/text-to-image",
		"gemini/gemini-3-pro-image/image-to-image",
		"meshy/meshy-7.1/text-to-3d",
		"matting/default/background-removal",
	} {
		offering, exists := taskCatalog.Get(id)
		if !exists {
			t.Fatalf("missing fixture offering %q", id)
		}
		if _, available := registry.Provider(offering); !available {
			t.Errorf("offering %q should be available", id)
		}
	}
	for _, id := range []generation.OfferingID{
		"meshy/processing/remesh",
		"meshy/processing/retexture",
		"meshy/processing/rigging",
		"meshy/processing/animation",
	} {
		offering, exists := taskCatalog.Get(id)
		if !exists {
			t.Fatalf("missing fixture offering %q", id)
		}
		// 默认 Catalog 已声明 inline_model_input，无公网网关时后处理也必须可用
		if _, available := registry.Provider(offering); !available {
			t.Errorf("offering %q should be available via inline input", id)
		}
	}
}

func TestMeshyProcessingUnavailableLogsGatewayReason(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	registry := providers.NewRegistry()
	var logBuffer strings.Builder
	logger := slog.New(slog.NewTextHandler(&logBuffer, nil))
	err = registerProviders(Config{
		ProviderMode: "live",
		RuntimeSettings: map[string]string{
			"meshy_base_url": "https://meshy.test",
			"meshy_api_key":  "meshy-key",
		},
	}, taskCatalog, registry, nil, nil, logger)
	if err != nil {
		t.Fatal(err)
	}
	logged := logBuffer.String()
	if !strings.Contains(logged, "后处理能力") ||
		!strings.Contains(logged, "PUBLIC_BASE_URL") ||
		!strings.Contains(logged, "ASSET_SIGNING_KEY") {
		t.Fatalf(
			"expected gateway-unavailable warning to name PUBLIC_BASE_URL and ASSET_SIGNING_KEY, got: %s",
			logged,
		)
	}
	// 默认 Catalog 全部声明内联，日志必须说明降级为 data URI 内联而非停用
	if !strings.Contains(logged, "内联") {
		t.Fatalf("expected inline degradation notice, got: %s", logged)
	}
}

func TestMeshyProcessingBecomesAvailableWithSignedAssetPublisher(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	registry := providers.NewRegistry()
	err = registerProviders(Config{
		ProviderMode: "live",
		RuntimeSettings: map[string]string{
			"meshy_base_url":  "https://meshy.test",
			"meshy_api_key":   "meshy-key",
			"public_base_url": "https://art-ai.test",
		},
		AssetSigningKey: strings.Repeat("s", 32),
	}, taskCatalog, registry, publishingInputs{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []generation.OfferingID{
		"meshy/processing/remesh",
		"meshy/processing/retexture",
		"meshy/processing/rigging",
		"meshy/processing/animation",
	} {
		offering, exists := taskCatalog.Get(id)
		if !exists {
			t.Fatalf("missing processing offering %q", id)
		}
		if _, available := registry.Provider(offering); !available {
			t.Fatalf("%q should be available with Provider asset publisher", id)
		}
	}
}

func TestImplicitOfferingSelectsHighestRankedRuntimeAvailableProvider(t *testing.T) {
	root := t.TempDir()
	application, err := New(Config{
		Environment:               "development",
		Address:                   "127.0.0.1:0",
		DatabasePath:              filepath.Join(root, "art-ai.db"),
		StorageRoot:               filepath.Join(root, "storage"),
		LegacyGeneratedImagesRoot: filepath.Join(root, "generated"),
		ProviderMode:              "live",
		AgentMode:                 "mock",
		RuntimeSettings: map[string]string{
			"llm_model":       "test-model",
			"gemini_base_url": "https://gemini.test/v1",
			"gemini_api_key":  "gemini-key",
		},
		AuthMode:        "development",
		DevelopmentUser: "owner-a",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = application.generation.Close()
	})
	prepared, err := application.generation.PrepareInput(
		context.Background(),
		generation.PrepareInputCommand{
			OwnerID:    "owner-a",
			Capability: generation.TextToImage,
			Input:      json.RawMessage(`{"prompt":"runtime selection"}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := application.generation.Offering(prepared.OfferingID)
	if !ok || offering.ProviderID != "gemini" {
		t.Fatalf("selected offering = %q, provider = %q", prepared.OfferingID, offering.ProviderID)
	}
}

func TestProductionPublicAssetGatewayRequiresHTTPSAndLongKey(t *testing.T) {
	base := Config{
		Environment:               "production",
		Address:                   "0.0.0.0:13333",
		DatabasePath:              "/app/data/art-ai.db",
		StorageRoot:               "/app/storage",
		LegacyGeneratedImagesRoot: "/app/storage/generated",
		ProviderMode:              "live",
		AgentMode:                 "mock",
		RuntimeSettings:           map[string]string{"llm_model": "test-model"},
		AuthMode:                  "bearer",
		SessionSecret:             strings.Repeat("s", 32),
	}
	invalidHTTP := base
	invalidHTTP.PublicBaseURL = "http://art-ai.test"
	invalidHTTP.AssetSigningKey = strings.Repeat("k", 32)
	if err := invalidHTTP.Validate(); err == nil {
		t.Fatal("production HTTP public URL should be rejected")
	}
	shortKey := base
	shortKey.PublicBaseURL = "https://art-ai.test"
	shortKey.AssetSigningKey = "short"
	if err := shortKey.Validate(); err == nil {
		t.Fatal("short signing key should be rejected")
	}
	valid := base
	valid.PublicBaseURL = "https://art-ai.test"
	valid.AssetSigningKey = strings.Repeat("k", 32)
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid gateway config: %v", err)
	}
}

func TestApplicationCanShutDownImmediatelyWithoutClosingRepositoryEarly(t *testing.T) {
	root := t.TempDir()
	application, err := New(Config{
		Environment:               "development",
		Address:                   "127.0.0.1:0",
		DatabasePath:              filepath.Join(root, "art-ai.db"),
		StorageRoot:               filepath.Join(root, "storage"),
		LegacyGeneratedImagesRoot: filepath.Join(root, "generated"),
		ProviderMode:              "mock",
		AgentMode:                 "mock",
		AuthMode:                  "development",
		DevelopmentUser:           "owner-a",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() {
		done <- application.Run(ctx)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("application shutdown timed out")
	}
}

type publishingInputs struct{}

func (publishingInputs) Resolve(
	context.Context,
	generation.OwnerID,
	generation.Offering,
	json.RawMessage,
) (map[string][]generation.InputAsset, error) {
	return nil, nil
}

func (publishingInputs) Open(
	context.Context,
	generation.InputAsset,
) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (publishingInputs) ExternalURL(
	context.Context,
	generation.InputAsset,
	time.Duration,
) (string, error) {
	return "https://art-ai.test/provider-asset", nil
}
