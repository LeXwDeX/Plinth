package httpapi_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func v1AssetUploadRequest(
	t *testing.T,
	payload []byte,
	filename string,
	mediaType string,
) *http.Request {
	t.Helper()
	request := legacyAssetUploadRequest(t, payload, filename, mediaType)
	request.URL.Path = "/api/v1/assets"
	request.RequestURI = "/api/v1/assets"
	return request
}

func TestUploadRejectsUnsafeMediaTypes(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	for _, mediaType := range []string{
		"text/html",
		"image/svg+xml",
		"application/xhtml+xml",
		"text/plain",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(
			response,
			v1AssetUploadRequest(
				t,
				[]byte("<script>alert(1)</script>"),
				"payload.bin",
				mediaType,
			),
		)
		if response.Code != http.StatusBadRequest ||
			!strings.Contains(response.Body.String(), "不支持的文件类型") {
			t.Fatalf(
				"mediaType=%s status=%d body=%s",
				mediaType,
				response.Code,
				response.Body,
			)
		}
		legacyResponse := httptest.NewRecorder()
		handler.ServeHTTP(
			legacyResponse,
			legacyAssetUploadRequest(
				t,
				[]byte("<script>alert(1)</script>"),
				"payload.bin",
				mediaType,
			),
		)
		if legacyResponse.Code != http.StatusBadRequest ||
			!strings.Contains(legacyResponse.Body.String(), "不支持的文件类型") {
			t.Fatalf(
				"legacy mediaType=%s status=%d body=%s",
				mediaType,
				legacyResponse.Code,
				legacyResponse.Body,
			)
		}
	}
}

func TestV1AssetDeliverySeparatesInlineFromAttachment(t *testing.T) {
	t.Parallel()
	_, handler, _, _ := newHTTPHarnessWithServices(
		t,
		nil,
		nil,
		nil,
		nil,
		nil,
		func(t *testing.T, databasePath string) {
			t.Helper()
			db, err := sql.Open("sqlite", databasePath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(`
				INSERT INTO generation_input_assets (
					id, owner_id, storage_key, original_filename,
					asset_type, media_type, size_bytes, sha256, created_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				"stored-html-asset",
				"owner-a",
				"uploads/stored.html",
				"stored.html",
				"image",
				"text/html",
				25,
				"html-sha",
				"2026-07-26T08:30:00Z",
			); err != nil {
				t.Fatal(err)
			}
			blobPath := filepath.Join(
				filepath.Dir(databasePath),
				"storage",
				"uploads",
				"stored.html",
			)
			if err := os.MkdirAll(filepath.Dir(blobPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(
				blobPath,
				[]byte("<script>alert(1)</script>"),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
		},
	)

	stored := httptest.NewRecorder()
	storedRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/assets/stored-html-asset",
		nil,
	)
	storedRequest.Header.Set("X-User-ID", "owner-a")
	handler.ServeHTTP(stored, storedRequest)
	if stored.Code != http.StatusOK {
		t.Fatalf("stored status=%d body=%s", stored.Code, stored.Body)
	}
	if contentType := stored.Header().Get("Content-Type"); contentType != "application/octet-stream" {
		t.Fatalf("stored content-type=%q", contentType)
	}
	if disposition := stored.Header().Get("Content-Disposition"); disposition != "attachment" {
		t.Fatalf("stored disposition=%q", disposition)
	}
	if policy := stored.Header().Get("Content-Security-Policy"); policy != "sandbox" {
		t.Fatalf("stored CSP=%q", policy)
	}
	if stored.Body.String() != "<script>alert(1)</script>" {
		t.Fatalf("stored body=%q", stored.Body.String())
	}

	upload := httptest.NewRecorder()
	handler.ServeHTTP(
		upload,
		v1AssetUploadRequest(t, []byte("png-bytes"), "reference.png", "image/png"),
	)
	if upload.Code != http.StatusCreated {
		t.Fatalf("upload status=%d body=%s", upload.Code, upload.Body)
	}
	var uploaded struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(upload.Body).Decode(&uploaded); err != nil {
		t.Fatal(err)
	}

	inline := httptest.NewRecorder()
	inlineRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/assets/"+uploaded.ID,
		nil,
	)
	inlineRequest.Header.Set("X-User-ID", "owner-a")
	handler.ServeHTTP(inline, inlineRequest)
	if inline.Code != http.StatusOK {
		t.Fatalf("legacy route status=%d body=%s", inline.Code, inline.Body)
	}
	if contentType := inline.Header().Get("Content-Type"); contentType != "image/png" {
		t.Fatalf("inline content-type=%q", contentType)
	}
	if disposition := inline.Header().Get("Content-Disposition"); disposition != "inline" {
		t.Fatalf("inline disposition=%q", disposition)
	}
	if policy := inline.Header().Get("Content-Security-Policy"); policy != "sandbox" {
		t.Fatalf("inline CSP=%q", policy)
	}
	if options := inline.Header().Get("X-Content-Type-Options"); options != "nosniff" {
		t.Fatalf("inline nosniff=%q", options)
	}
}

func TestSecurityHeadersCSRFAndLoginRateLimit(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Header().Get("Content-Security-Policy") == "" ||
		recorder.Header().Get("X-Frame-Options") == "" ||
		recorder.Header().Get("Referrer-Policy") == "" {
		t.Fatalf("security headers missing: %v", recorder.Header())
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/tasks",
		strings.NewReader(`{}`),
	)
	request.Header.Set("Origin", "http://evil.example")
	request.Header.Set("X-User-ID", "owner-a")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST = %d, want 403", recorder.Code)
	}

	// A cookie-authenticated browser write cannot be classified as a machine
	// call just because both browser-origin signals are absent.
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(`{}`))
	request.AddCookie(&http.Cookie{Name: "meshyai_session", Value: "test-session"})
	request.Header.Set("X-User-ID", "owner-a")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cookie POST without origin metadata = %d, want 403", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(`{}`))
	request.AddCookie(&http.Cookie{Name: "meshyai_session", Value: "test-session"})
	request.Header.Set("Origin", "http://example.com")
	request.Header.Set("X-User-ID", "owner-a")
	handler.ServeHTTP(recorder, request)
	if recorder.Code == http.StatusForbidden {
		t.Fatal("same-origin browser request was blocked by CSRF middleware")
	}

	for attempt := 0; attempt < 10; attempt++ {
		recorder = httptest.NewRecorder()
		request = httptest.NewRequest(
			http.MethodPost,
			"/api/auth/login",
			strings.NewReader(`{"username":"nouser","password":"wrong"}`),
		)
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(recorder, request)
	}
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(
		http.MethodPost,
		"/api/auth/login",
		strings.NewReader(`{"username":"nouser","password":"wrong"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("login flood = %d, want 429", recorder.Code)
	}
}
