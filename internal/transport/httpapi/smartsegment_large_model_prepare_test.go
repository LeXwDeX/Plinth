package httpapi_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
)

// lazyInputs lets the real Tripo adapter read the harness's own input assets,
// which exist only after the harness is built.
type lazyInputs struct{ target generation.InputAssetResolver }

func (l *lazyInputs) Resolve(ctx context.Context, owner generation.OwnerID, offering generation.Offering, input json.RawMessage) (map[string][]generation.InputAsset, error) {
	return l.target.Resolve(ctx, owner, offering, input)
}

func (l *lazyInputs) Open(ctx context.Context, asset generation.InputAsset) (io.ReadCloser, error) {
	return l.target.Open(ctx, asset)
}

// #321 A at the HTTP boundary: a Rodin result's GLB over 64 MB (dev-test saw
// 107 and 110 MB) is listed for Tripo Smart Segmentation (up to 150 MB), so
// POST /tasks/prepare must accept it even when the process temp directory has
// no room (the container's /tmp is a 64 MB tmpfs). Before the fix the Tripo
// check copied the model there and the answer was 400 invalid_input.
func TestPrepareSmartSegmentationOfALargeStoredModel(t *testing.T) {
	const offeringID = "tripo/geometry/smartsegment-model"
	source, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := source.Get(offeringID)
	if !ok || !offering.Enabled {
		t.Fatal("Smart Segmentation Offering is missing or disabled")
	}
	inputs := &lazyInputs{}
	adapter, err := tripo.New(tripo.Config{BaseURL: "http://127.0.0.1:9/v3", APIKey: "synthetic-tripo-key",
		InputAssets: inputs, OfferingRoutes: source.List("")})
	if err != nil {
		t.Fatal(err)
	}
	directory := &authDirectory{byUsername: map[string]authentication.UserRecord{}, byID: map[string]authentication.UserRecord{}}
	record := authentication.UserRecord{ID: "owner-a", Username: "owner-a", Nickname: "owner-a", Email: "owner-a@example.test", Active: true,
		PasswordHash: "$2b$04$UVs2NmHqVLnDFtkcPXeOhuCLZNX27fLFYc/TbHf7w5wkWsWZXCP2."}
	directory.byUsername["owner-a"], directory.byID["owner-a"] = record, record
	var databasePath string
	_, handler, service, _ := newHTTPHarnessWithCatalogAndProviders(t, []string{"user"}, nil,
		func(config *authentication.Config) { config.Directory = directory }, nil, nil, source,
		map[string]generation.ProviderPort{offering.AdapterKey: adapter},
		func(_ *testing.T, path string) { databasePath = path })
	inputs.target = service

	// A synthetic GLB just over the tmpfs size, stored as a succeeded Task's model.
	doc, bin := modelinputtest.Document(modelinputtest.Options{Indexed: true})
	bin = append(bin, make([]byte, 70<<20)...)
	doc["buffers"].([]any)[0].(map[string]any)["byteLength"] = len(bin)
	raw := modelinputtest.GLB(doc, bin)
	store, err := local.New(filepath.Join(filepath.Dir(databasePath), "storage"))
	if err != nil {
		t.Fatal(err)
	}
	blob, err := store.PutBlob(context.Background(), bytes.NewReader(raw), "model/gltf-binary", offering.Parameters[0].MaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`INSERT INTO generation_tasks (id,owner_id,capability,offering_json,input_json,request_hash,state,version,created_at,updated_at) VALUES ('rodin-task','owner-a','text-to-3d','{}','{}','hash','SUCCEEDED',1,'2026-10-22T00:00:00Z','2026-10-22T00:00:00Z')`,
		`INSERT INTO generation_attempts (id,task_id,ordinal,offering_json,state,submission_key,started_at,updated_at) VALUES ('rodin-attempt','rodin-task',1,'{}','SUCCEEDED','rodin-key','2026-10-22T00:00:00Z','2026-10-22T00:00:00Z')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO generation_assets (id,task_id,attempt_id,role,required,ordinal,storage_key,media_type,size_bytes,sha256,created_at,variant) VALUES ('rodin-glb','rodin-task','rodin-attempt','model',1,0,?,'model/gltf-binary',?,?,'2026-10-22T00:00:00Z','glb')`,
		blob.StorageKey, blob.Size, blob.SHA256); err != nil {
		t.Fatal(err)
	}

	login := grantTestRequest(t, handler, nil, http.MethodPost, "/api/auth/login", `{"username":"owner-a","password":"correct-horse"}`, "")
	if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 {
		t.Fatalf("login=%d %s", login.Code, login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	created := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/chat/sessions", `{"title":"large model"}`, "")
	var session struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil || session.ID == "" {
		t.Fatalf("session=%d %s", created.Code, created.Body.String())
	}

	// The container's /tmp: nothing fits there any more.
	full := filepath.Join(t.TempDir(), "full-tmpfs")
	if err := os.WriteFile(full, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", full)

	body, _ := json.Marshal(map[string]any{"session_id": session.ID, "capability": "mesh-segmentation", "offering_id": offeringID,
		"input": map[string]any{"source_asset": "rodin-glb", "authorize_extra_credits": true}})
	prepared := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks/prepare", string(body), "")
	if prepared.Code != http.StatusCreated {
		t.Fatalf("prepare of a %d-byte stored GLB = %d %s", len(raw), prepared.Code, prepared.Body.String())
	}
}
