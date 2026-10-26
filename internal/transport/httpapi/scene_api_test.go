package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/rendermanifest"
	"github.com/LeXwDeX/Plinth/internal/scene"
)

type sceneAPIFakeRepo struct {
	created              scene.Create
	snapshot             scene.Snapshot
	lastMutation         scene.Mutation
	lastRepresentationID string
}

func (f *sceneAPIFakeRepo) Create(_ context.Context, c scene.Create) (scene.Object, error) {
	f.created = c
	return c.Object, nil
}
func (f *sceneAPIFakeRepo) RegisterCandidate(context.Context, scene.Candidate) (scene.Representation, error) {
	return scene.Representation{}, scene.ErrInvalid
}
func (f *sceneAPIFakeRepo) ApplyCandidate(_ context.Context, m scene.Mutation, id string) (scene.Object, error) {
	f.lastMutation, f.lastRepresentationID = m, id
	return scene.Object{ID: m.ObjectID, Revision: m.ExpectedRevision + 1, ActiveRepresentationID: id}, nil
}
func (f *sceneAPIFakeRepo) Move(_ context.Context, m scene.Mutation, rect scene.Rect, anchor scene.Anchor) (scene.Object, error) {
	f.lastMutation = m
	return scene.Object{ID: m.ObjectID, Revision: m.ExpectedRevision + 1, Rect: rect, Anchor: anchor}, nil
}
func (f *sceneAPIFakeRepo) Delete(_ context.Context, m scene.Mutation) (scene.Object, error) {
	f.lastMutation = m
	return scene.Object{ID: m.ObjectID, Revision: m.ExpectedRevision + 1}, nil
}
func (f *sceneAPIFakeRepo) Get(context.Context, string, string, string) (scene.Object, error) {
	return scene.Object{}, scene.ErrNotFound
}
func (f *sceneAPIFakeRepo) GetRepresentation(context.Context, string, string, string, string) (scene.Representation, error) {
	return scene.Representation{}, scene.ErrNotFound
}
func (f *sceneAPIFakeRepo) ListRepresentations(_ context.Context, owner, session, id string) ([]scene.Representation, error) {
	if owner != "owner-a" || session != "session-a" || id != "object-a" {
		return nil, scene.ErrNotFound
	}
	return []scene.Representation{{ID: "representation-a", ObjectID: id, Kind: scene.Bitmap, AssetID: "source-asset"}}, nil
}
func (f *sceneAPIFakeRepo) Snapshot(_ context.Context, owner, session string) (scene.Snapshot, error) {
	v := f.snapshot
	v.OwnerID = owner
	v.SessionID = session
	return v, nil
}
func (f *sceneAPIFakeRepo) ReconcileTaskCandidates(context.Context, int) (int, error) { return 0, nil }

type renderAPIFakeAssets struct {
	uploads int
	input   []byte
}

func (a *renderAPIFakeAssets) OpenOwned(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(a.input)), nil
}
func (a *renderAPIFakeAssets) UploadPNG(_ context.Context, _ string, pixels []byte) (string, error) {
	a.uploads++
	if len(pixels) == 0 {
		return "", rendermanifest.ErrInvalid
	}
	return "server-asset", nil
}

type renderAPIFakeRepo struct{ manifest rendermanifest.Manifest }

func (r *renderAPIFakeRepo) Create(_ context.Context, m rendermanifest.Manifest) (rendermanifest.Manifest, error) {
	r.manifest = m
	return m, nil
}
func (r *renderAPIFakeRepo) Get(_ context.Context, owner, session, id string) (rendermanifest.Manifest, error) {
	if r.manifest.OwnerID != owner || r.manifest.SessionID != session || r.manifest.ID != id {
		return rendermanifest.Manifest{}, rendermanifest.ErrNotFound
	}
	return r.manifest, nil
}
func sceneAPITestServer(t *testing.T) (http.Handler, *sceneAPIFakeRepo, *renderAPIFakeAssets) {
	t.Helper()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	policy, err := access.NewPolicy(access.PolicyConfig{DefaultRole: "user", AdminRole: "admin", Roles: []access.RoleDefinition{{Key: "user"}, {Key: "admin"}}})
	if err != nil {
		t.Fatal(err)
	}
	scenes := &sceneAPIFakeRepo{snapshot: scene.Snapshot{Revision: 1, Items: []scene.SnapshotItem{{
		Object:         scene.Object{ID: "object-a", Revision: 1, Rect: scene.Rect{Width: 1, Height: 1}},
		Representation: scene.Representation{ID: "representation-a", Kind: scene.Bitmap, AssetID: "source-asset"},
	}}}}
	svc, err := scene.New(scenes)
	if err != nil {
		t.Fatal(err)
	}
	input := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	input.Set(0, 0, color.NRGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, input); err != nil {
		t.Fatal(err)
	}
	assets := &renderAPIFakeAssets{input: encoded.Bytes()}
	manifests := &renderAPIFakeRepo{}
	render, err := rendermanifest.New(svc, assets, manifests, 100, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	application := c.Application()
	application.Runtime.HTTP.CSRFOriginEnforced = true
	s := &Server{identity: DevelopmentIdentity{DefaultClaims: access.Claims{Roles: []string{"user"}}, Policy: policy}, application: application,
		scene: svc, renderManifests: render, ownsAgentSession: func(_ context.Context, owner, session string) bool {
			return owner == "owner-a" && session == "session-a"
		}, mux: http.NewServeMux()}
	routes := application.Runtime.HTTP.Routes
	s.mux.HandleFunc("POST "+routes.SceneObjects, s.withIdentity(s.createSceneObject))
	s.mux.HandleFunc("GET "+routes.SceneSnapshot, s.withIdentity(s.getSceneSnapshot))
	s.mux.HandleFunc("PATCH "+routes.SceneObjectItem, s.withIdentity(s.moveSceneObject))
	s.mux.HandleFunc("DELETE "+routes.SceneObjectItem, s.withIdentity(s.deleteSceneObject))
	s.mux.HandleFunc("POST "+routes.SceneObjectApply, s.withIdentity(s.applySceneCandidate))
	s.mux.HandleFunc("GET "+routes.SceneObjectRepresentations, s.withIdentity(s.listSceneRepresentations))
	s.mux.HandleFunc("POST "+routes.RenderManifests, s.withIdentity(s.createRenderManifest))
	s.mux.HandleFunc("GET "+routes.RenderManifestItem, s.withIdentity(s.getRenderManifest))
	return s.securityHeadersMiddleware(s.sameOriginCSRFMiddleware(s.mux)), scenes, assets
}
func sceneAPIRequest(handler http.Handler, method, path, body, owner string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Origin", "http://example.com")
	if owner != "" {
		r.Header.Set("X-User-ID", owner)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}
func TestSceneAPIRequiresOwnedSessionAndServerBoundIdentity(t *testing.T) {
	t.Parallel()
	handler, repo, _ := sceneAPITestServer(t)
	path := "/api/v1/scenes/session-a/objects"
	body := `{"rect":{"x":0,"y":0,"width":2,"height":2},"anchor":{"x":0,"y":0},"initial":{"kind":"bitmap","asset_id":"asset-a","metadata_schema_revision":"v1"}}`
	if r := sceneAPIRequest(handler, "POST", path, body, ""); r.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous=%d %s", r.Code, r.Body.String())
	}
	if r := sceneAPIRequest(handler, "POST", path, body, "owner-b"); r.Code != http.StatusNotFound {
		t.Fatalf("cross-owner=%d %s", r.Code, r.Body.String())
	}
	if repo.created.Object.ID != "" {
		t.Fatal("unauthorized mutation reached scene service")
	}
	r := sceneAPIRequest(handler, "POST", path, body, "owner-a")
	if r.Code != http.StatusCreated {
		t.Fatalf("created=%d %s", r.Code, r.Body.String())
	}
	if repo.created.Object.OwnerID != "owner-a" || repo.created.Object.SessionID != "session-a" || repo.created.Initial.ID == "" {
		t.Fatalf("server identity missing: %+v", repo.created)
	}
	var response map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if _, ok := response["owner_id"]; ok {
		t.Fatal("response leaked owner ID")
	}
}
func TestRenderManifestAPIRejectsClientProofAndRendersOnServer(t *testing.T) {
	t.Parallel()
	handler, _, assets := sceneAPITestServer(t)
	path := "/api/v1/scenes/session-a/render-manifests"
	forged := `{"frame":{"x":0,"y":0,"width":1,"height":1,"pixel_width":1,"pixel_height":1},"output":{"asset_id":"client-pixel"}}`
	if r := sceneAPIRequest(handler, "POST", path, forged, "owner-a"); r.Code != http.StatusBadRequest {
		t.Fatalf("forged=%d %s", r.Code, r.Body.String())
	}
	if assets.uploads != 0 {
		t.Fatal("forged proof reached renderer")
	}
	valid := `{"frame":{"x":0,"y":0,"width":1,"height":1,"pixel_width":1,"pixel_height":1}}`
	r := sceneAPIRequest(handler, "POST", path, valid, "owner-a")
	if r.Code != http.StatusCreated {
		t.Fatalf("created=%d %s", r.Code, r.Body.String())
	}
	if assets.uploads != 1 {
		t.Fatalf("server uploads=%d", assets.uploads)
	}
	var response struct {
		ID     string `json:"id"`
		Output struct {
			AssetID string `json:"asset_id"`
			SHA256  string `json:"sha256"`
		} `json:"output"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ID == "" || response.Output.AssetID != "server-asset" || response.Output.SHA256 == "" {
		t.Fatalf("manifest=%+v", response)
	}
	if got := sceneAPIRequest(handler, "GET", path+"/"+response.ID, "", "owner-a"); got.Code != http.StatusOK {
		t.Fatalf("get=%d %s", got.Code, got.Body.String())
	}
	if got := sceneAPIRequest(handler, "GET", path+"/"+response.ID, "", "owner-b"); got.Code != http.StatusNotFound {
		t.Fatalf("cross-owner get=%d %s", got.Code, got.Body.String())
	}
}

func TestSceneAPIMutationsBindOwnerAndUseRevision(t *testing.T) {
	t.Parallel()
	handler, repo, _ := sceneAPITestServer(t)
	item := "/api/v1/scenes/session-a/objects/object-a"
	cases := []struct{ method, path, body string }{
		{"PATCH", item, `{"expected_revision":3,"rect":{"x":1,"y":2,"width":4,"height":5},"anchor":{"x":0,"y":0}}`},
		{"POST", item + "/apply", `{"expected_revision":4,"representation_id":"candidate-a"}`},
		{"DELETE", item, `{"expected_revision":5}`},
	}
	for _, tc := range cases {
		if r := sceneAPIRequest(handler, tc.method, tc.path, tc.body, "owner-b"); r.Code != http.StatusNotFound {
			t.Fatalf("cross-owner %s=%d %s", tc.method, r.Code, r.Body.String())
		}
		if repo.lastMutation.OwnerID != "" {
			t.Fatal("cross-owner mutation reached repository")
		}
		r := sceneAPIRequest(handler, tc.method, tc.path, tc.body, "owner-a")
		if r.Code != http.StatusOK {
			t.Fatalf("%s=%d %s", tc.method, r.Code, r.Body.String())
		}
		if repo.lastMutation.OwnerID != "owner-a" || repo.lastMutation.SessionID != "session-a" || repo.lastMutation.ObjectID != "object-a" {
			t.Fatalf("unscoped mutation %+v", repo.lastMutation)
		}
		repo.lastMutation = scene.Mutation{}
	}
	if r := sceneAPIRequest(handler, "GET", item+"/representations", "", "owner-a"); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "representation-a") {
		t.Fatalf("representations=%d %s", r.Code, r.Body.String())
	}
	if r := sceneAPIRequest(handler, "GET", "/api/v1/scenes/session-a/snapshot", "", "owner-a"); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "representation-a") {
		t.Fatalf("snapshot=%d %s", r.Code, r.Body.String())
	}
}
