package httpapi_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

const assetListArchiveRoute = "/api/v1/projects/{projectID}/asset-lists/{listID}/archive"

// newAssetListArchiveHarness is newAssetListHTTPHarnessWith with the extra
// database setups the archive test needs to seed a stored Task.
func newAssetListArchiveHarness(
	t *testing.T,
	customize func(*catalog.ApplicationSchema),
	setups ...func(*testing.T, string),
) *assetListHTTPHarness {
	t.Helper()
	module, handler, cookie, _ := grantTestBrowserWithCatalog(t, customize, []string{"user"}, nil, setups...)
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture repository")
	}
	state := value.(*fixtureExecutionState)
	created := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/projects",
		`{"name":"Asset Archive"}`, "")
	var createdProject struct {
		ID string `json:"id"`
	}
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &createdProject) != nil ||
		createdProject.ID == "" {
		t.Fatalf("create project = %d %s", created.Code, created.Body.String())
	}
	session := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/chat/sessions",
		`{"title":"asset archive chat","project_id":"`+createdProject.ID+`"}`, "")
	var createdSession struct {
		ID string `json:"id"`
	}
	if session.Code != http.StatusCreated || json.Unmarshal(session.Body.Bytes(), &createdSession) != nil ||
		createdSession.ID == "" {
		t.Fatalf("create session = %d %s", session.Code, session.Body.String())
	}
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return &assetListHTTPHarness{
		handler: handler, cookie: cookie, repository: state.repository, databasePath: state.databasePath,
		seed: assetListSeedService(t, state.repository, nil), product: product.Application(),
		projectID: createdProject.ID, sessionID: createdSession.ID,
	}
}

// seedArchiveTask stores one succeeded Task with one stored GLB through the
// harness's own blob store, so the generation module really reads it back.
func seedArchiveTask(t *testing.T, databasePath string) (assetID string, data []byte) {
	t.Helper()
	doc, bin := modelinputtest.Document(modelinputtest.Options{Indexed: true})
	data = modelinputtest.GLB(doc, bin)
	store, err := local.New(filepath.Join(filepath.Dir(databasePath), "storage"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	blob, err := store.PutBlob(context.Background(), bytes.NewReader(data), "model/gltf-binary", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`INSERT INTO generation_tasks (id,owner_id,capability,offering_json,input_json,request_hash,state,version,created_at,updated_at) VALUES ('task_archive','owner-a','text-to-3d','{}','{}','hash','SUCCEEDED',1,'2026-10-22T00:00:00Z','2026-10-22T00:00:00Z')`,
		`INSERT INTO generation_attempts (id,task_id,ordinal,offering_json,state,submission_key,started_at,updated_at) VALUES ('attempt_archive','task_archive',1,'{}','SUCCEEDED','archive-key','2026-10-22T00:00:00Z','2026-10-22T00:00:00Z')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	assetID = "asset_archive_glb"
	if _, err := db.Exec(`INSERT INTO generation_assets (id,task_id,attempt_id,role,required,ordinal,storage_key,media_type,size_bytes,sha256,created_at,variant) VALUES (?, 'task_archive','attempt_archive','model',1,0,?,'model/gltf-binary',?,?,'2026-10-22T00:00:00Z','glb')`,
		assetID, blob.StorageKey, blob.Size, blob.SHA256); err != nil {
		t.Fatal(err)
	}
	return assetID, data
}

// acceptArchiveItem moves one list item to accepted with a review snapshot of
// the seeded Task and asset.
func acceptArchiveItem(t *testing.T, databasePath, itemID, assetID string, size int64, sha string) {
	t.Helper()
	review, err := json.Marshal(assetlist.ReviewSnapshot{
		TaskID: "task_archive",
		Artifacts: []assetlist.ReviewArtifact{{
			AssetID: assetID, Role: "model", MediaType: "model/gltf-binary", Size: size, SHA256: sha,
		}},
		Model3D: &assetlist.ReviewModel3D{
			AssetID: assetID,
			Checks:  []assetlist.ReviewCheck{{Check: "triangles", Status: "pass", Actual: float64(9000)}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE asset_list_items SET state = 'accepted', round = 1, review_json = ? WHERE id = ?`,
		string(review), itemID); err != nil {
		t.Fatal(err)
	}
}

func TestProjectAssetListArchiveRequiresARealBrowserSession(t *testing.T) {
	t.Parallel()
	handler, _, _, _ := bearerKeyHarness(t)
	path := assetListRoute(assetListArchiveRoute, "prj_any", "alist_any")

	anonymous := httptest.NewRecorder()
	handler.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, path, nil))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous archive = %d %s", anonymous.Code, anonymous.Body.String())
	}

	bearer := httptest.NewRequest(http.MethodGet, path, nil)
	bearer.Header.Set("Authorization", "Bearer "+deployToken)
	bearerResponse := httptest.NewRecorder()
	handler.ServeHTTP(bearerResponse, bearer)
	if bearerResponse.Code != http.StatusForbidden {
		t.Fatalf("bearer archive = %d %s", bearerResponse.Code, bearerResponse.Body.String())
	}
}

func TestProjectAssetListArchiveIsOwnerScopedAndRefusesEmpty(t *testing.T) {
	t.Parallel()
	harness := newAssetListHTTPHarness(t)
	list := harness.propose(t, "owner-a", harness.projectID, harness.sessionID, "Board",
		[]assetlist.ItemInput{{Kind: "character", Name: "Hero"}})
	confirm := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut,
		assetListRoute(assetListDecisionRoute, harness.projectID, list.ID), `{"decision":"confirm"}`, "")
	if confirm.Code != http.StatusOK {
		t.Fatalf("confirm = %d %s", confirm.Code, confirm.Body.String())
	}

	missing := grantTestRequest(t, harness.handler, harness.cookie, http.MethodGet,
		assetListRoute(assetListArchiveRoute, harness.projectID, "alist_missing"), "", "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing list archive = %d %s", missing.Code, missing.Body.String())
	}
	empty := grantTestRequest(t, harness.handler, harness.cookie, http.MethodGet,
		assetListRoute(assetListArchiveRoute, harness.projectID, list.ID), "", "")
	if empty.Code != http.StatusConflict || !strings.Contains(empty.Body.String(), "asset_list_archive_empty") {
		t.Fatalf("empty archive = %d %s", empty.Code, empty.Body.String())
	}
}

func TestProjectAssetListArchiveStreamsTheAcceptedItem(t *testing.T) {
	t.Parallel()
	var assetID, sha string
	var size int64
	var seededData []byte
	setups := []func(*testing.T, string){func(t *testing.T, databasePath string) {
		id, data := seedArchiveTask(t, databasePath)
		assetID = id
		size = int64(len(data))
		seededData = data
		sha = sha256Hex(data)
	}}
	harness := newAssetListArchiveHarness(t, nil, setups...)
	list := harness.propose(t, "owner-a", harness.projectID, harness.sessionID, "Board",
		[]assetlist.ItemInput{{Kind: "character", Name: "Hero"}})
	confirm := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut,
		assetListRoute(assetListDecisionRoute, harness.projectID, list.ID), `{"decision":"confirm"}`, "")
	if confirm.Code != http.StatusOK {
		t.Fatalf("confirm = %d %s", confirm.Code, confirm.Body.String())
	}
	acceptArchiveItem(t, harness.databasePath, list.Items[0].ID, assetID, size, sha)

	response := grantTestRequest(t, harness.handler, harness.cookie, http.MethodGet,
		assetListRoute(assetListArchiveRoute, harness.projectID, list.ID), "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("archive = %d %s", response.Code, response.Body.String())
	}
	for header, wanted := range map[string]string{
		"Content-Type":            "application/zip",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "sandbox",
	} {
		if got := response.Header().Get(header); got != wanted {
			t.Fatalf("%s = %q; want %q", header, got, wanted)
		}
	}
	if disposition := response.Header().Get("Content-Disposition"); !strings.HasPrefix(disposition, `attachment; filename="asset-list-`) {
		t.Fatalf("Content-Disposition = %q", disposition)
	}
	body := response.Body.Bytes()
	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	names := make([]string, 0, len(reader.File))
	for _, file := range reader.File {
		names = append(names, file.Name)
	}
	for _, wanted := range []string{"manifest.json", "README.txt", "01-Hero/model.glb"} {
		if !archiveNameExists(names, wanted) {
			t.Fatalf("%q missing; archive has %v", wanted, names)
		}
	}
	var packed []byte
	for _, file := range reader.File {
		if file.Name != "01-Hero/model.glb" {
			continue
		}
		entry, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		packed, err = io.ReadAll(entry)
		entry.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(packed, seededData) {
		t.Fatalf("packed model = %d bytes; want the %d stored bytes", len(packed), len(seededData))
	}
}

func TestProjectAssetListArchivePrechecksTheSizeLimit(t *testing.T) {
	t.Parallel()
	var assetID, sha string
	var size int64
	setups := []func(*testing.T, string){func(t *testing.T, databasePath string) {
		id, data := seedArchiveTask(t, databasePath)
		assetID = id
		size = int64(len(data))
		sha = sha256Hex(data)
	}}
	harness := newAssetListArchiveHarness(t, func(schema *catalog.ApplicationSchema) {
		schema.AssetList.Review.Archive.MaxEntries = 2
	}, setups...)
	list := harness.propose(t, "owner-a", harness.projectID, harness.sessionID, "Board",
		[]assetlist.ItemInput{{Kind: "character", Name: "Hero"}})
	confirm := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut,
		assetListRoute(assetListDecisionRoute, harness.projectID, list.ID), `{"decision":"confirm"}`, "")
	if confirm.Code != http.StatusOK {
		t.Fatalf("confirm = %d %s", confirm.Code, confirm.Body.String())
	}
	acceptArchiveItem(t, harness.databasePath, list.Items[0].ID, assetID, size, sha)

	response := grantTestRequest(t, harness.handler, harness.cookie, http.MethodGet,
		assetListRoute(assetListArchiveRoute, harness.projectID, list.ID), "", "")
	if response.Code != http.StatusRequestEntityTooLarge ||
		!strings.Contains(response.Body.String(), "asset_list_archive_too_large") {
		t.Fatalf("oversized archive = %d %s", response.Code, response.Body.String())
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func archiveNameExists(names []string, target string) bool {
	for _, name := range names {
		if name == target {
			return true
		}
	}
	return false
}
