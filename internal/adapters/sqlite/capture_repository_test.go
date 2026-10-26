package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/capture"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type captureTestBytes struct{ data map[string][]byte }

// The production asset adapter reads metadata from this same single-connection
// database. A capture must release its replay read before calling OpenOwned.
type captureDBBytes struct {
	repo *Repository
	data []byte
}

func (b captureDBBytes) OpenOwned(ctx context.Context, owner, id string) (io.ReadCloser, error) {
	var present int
	if err := b.repo.db.QueryRowContext(ctx, `SELECT 1 FROM generation_input_assets WHERE id=? AND owner_id=?`, id, owner).Scan(&present); err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(b.data)), nil
}

func (b captureTestBytes) OpenOwned(_ context.Context, owner, id string) (io.ReadCloser, error) {
	data, ok := b.data[owner+":"+id]
	if !ok {
		return nil, capture.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func captureFixture(t *testing.T) (*Repository, *CaptureRepository, capture.Command, []byte) {
	t.Helper()
	repo, err := Open(filepath.Join(t.TempDir(), "capture.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if MigrationHead() < 39 {
		t.Fatalf("capture migration missing: head=%d", MigrationHead())
	}
	imageData := image.NewRGBA(image.Rect(0, 0, 4, 3))
	imageData.Set(1, 1, color.RGBA{R: 230, G: 10, B: 20, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, imageData); err != nil {
		t.Fatal(err)
	}
	pngBytes := encoded.Bytes()
	digest := sha256.Sum256(pngBytes)
	stamp := "2026-01-01T00:00:00Z"
	ctx := context.Background()
	for _, pair := range [][2]string{{"owner-a", "session-a"}, {"owner-b", "session-b"}} {
		_, err = repo.db.ExecContext(ctx, `INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at) VALUES (?,?,?,?,?,?)`, pair[1], pair[0], "name", "title", stamp, stamp)
		if err != nil {
			t.Fatal(err)
		}
	}
	offeringJSON, err := json.Marshal(generation.Offering{ID: "frozen-model", Revision: "original", Outputs: []generation.AssetRequirement{
		{Role: "model", MediaTypes: []string{"model/gltf-binary"}},
		{Role: "converted_model", Variant: "converted.gltf", MediaTypes: []string{"model/gltf-binary", "model/gltf+json"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.db.ExecContext(ctx, `INSERT INTO generation_tasks(id,owner_id,session_id,capability,offering_json,input_json,request_hash,state,version,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, "model-task", "owner-a", "session-a", "image-to-3d", string(offeringJSON), "{}", "hash", "SUCCEEDED", 1, stamp, stamp)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.db.ExecContext(ctx, `INSERT INTO generation_attempts(id,task_id,ordinal,offering_json,state,submission_key,started_at,updated_at) VALUES (?,?,?,?,?,?,?,?)`, "model-attempt", "model-task", 1, "{}", "SUCCEEDED", "key", stamp, stamp)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.db.ExecContext(ctx, `INSERT INTO generation_assets(id,task_id,attempt_id,role,required,ordinal,storage_key,media_type,size_bytes,sha256,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, "model-asset", "model-task", "model-attempt", "model", 1, 0, "model.glb", "model/gltf-binary", 4, "model-digest", stamp)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.db.ExecContext(ctx, `INSERT INTO generation_input_assets(id,owner_id,storage_key,media_type,size_bytes,sha256,created_at) VALUES (?,?,?,?,?,?,?)`, "capture-image", "owner-a", "capture.png", "image/png", len(pngBytes), hex.EncodeToString(digest[:]), stamp)
	if err != nil {
		t.Fatal(err)
	}
	store, err := repo.CaptureRepository(CaptureRepositoryConfig{Bytes: captureTestBytes{data: map[string][]byte{"owner-a:capture-image": pngBytes}}, MaxBytes: 1 << 20, MaxPixels: 1024})
	if err != nil {
		t.Fatal(err)
	}
	cmd := capture.Command{ID: "capture-1", OwnerID: "owner-a", SessionID: "session-a", SourceTaskID: "model-task", ModelAssetID: "model-asset", ScreenshotAssetID: "capture-image", IdempotencyKey: "capture-key", View: capture.View{CameraOrbit: "45deg 55deg 4m", CameraTarget: "0m 0m 0m", FieldOfView: "45deg", Environment: "neutral", RendererVersion: "model-viewer:4", AnimationTimeMS: 0}}
	return repo, store, cmd, pngBytes
}

func TestCaptureRecordsDeclaredConvertedModel(t *testing.T) {
	repo, store, cmd, _ := captureFixture(t)
	if _, err := repo.db.Exec(`UPDATE generation_assets SET role='converted_model',variant='converted.gltf' WHERE id=?`, cmd.ModelAssetID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Record(context.Background(), cmd); err != nil {
		t.Fatalf("declared converted model capture: %v", err)
	}
}

func TestCaptureRejectsSourcesOutsideFrozenOutputAndOwnershipContract(t *testing.T) {
	for _, test := range []struct {
		name   string
		sql    string
		change func(*capture.Command)
	}{
		{name: "undeclared role", sql: `UPDATE generation_assets SET role='undeclared'`},
		{name: "undeclared converted variant", sql: `UPDATE generation_assets SET role='converted_model',variant='converted.unknown'`},
		{name: "nonmodel MIME", sql: `UPDATE generation_assets SET media_type='image/png'`},
		{name: "renderable MIME outside contract", sql: `UPDATE generation_assets SET media_type='application/octet-stream'`},
		{name: "unrenderable declared MIME", sql: `UPDATE generation_assets SET media_type='model/obj'; UPDATE generation_tasks SET offering_json='{"outputs":[{"role":"model","media_types":["model/obj"]}]}'`},
		{name: "no storage", sql: `UPDATE generation_assets SET storage_key=''`},
		{name: "no hash", sql: `UPDATE generation_assets SET sha256=''`},
		{name: "unready task", sql: `UPDATE generation_tasks SET state='MATERIALIZING'`},
		{name: "foreign task owner", sql: `UPDATE generation_tasks SET owner_id='owner-b'`},
		{name: "foreign task session", sql: `UPDATE generation_tasks SET session_id='session-b'`},
		{name: "foreign session owner", change: func(cmd *capture.Command) { cmd.SessionID = "session-b" }},
		{name: "wrong task", change: func(cmd *capture.Command) { cmd.SourceTaskID = "other-task" }},
		{name: "unknown model", change: func(cmd *capture.Command) { cmd.ModelAssetID = "other-model" }},
		{name: "foreign screenshot owner", sql: `UPDATE generation_input_assets SET owner_id='owner-b'`},
		{name: "missing screenshot storage", sql: `UPDATE generation_input_assets SET storage_key=''`},
		{name: "bad frozen contract shape", sql: `UPDATE generation_tasks SET offering_json='{"outputs":"broken"}'`},
		{name: "empty frozen outputs", sql: `UPDATE generation_tasks SET offering_json='{"outputs":[]}'`},
		{name: "missing frozen outputs", sql: `UPDATE generation_tasks SET offering_json='{}'`},
		{name: "null frozen offering", sql: `UPDATE generation_tasks SET offering_json='null'`},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, store, cmd, _ := captureFixture(t)
			if test.sql != "" {
				if _, err := repo.db.Exec(test.sql); err != nil {
					t.Fatal(err)
				}
			}
			if test.change != nil {
				test.change(&cmd)
			}
			if _, err := store.Record(context.Background(), cmd); !errors.Is(err, capture.ErrNotFound) {
				t.Fatalf("source accepted: %v", err)
			}
			var count int
			if err := repo.db.QueryRow(`SELECT count(*) FROM model_captures`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("rejected source wrote capture: %d %v", count, err)
			}
		})
	}
}

func TestCaptureNewRegistrationUsesFrozenRoleVariantContractAfterReopen(t *testing.T) {
	for _, role := range []string{"model", "converted_model", "another-declared-model-role"} {
		t.Run(role, func(t *testing.T) {
			repo, _, cmd, data := captureFixture(t)
			// This represents the Offering at submission. The later revision removes
			// the slot. New historical capture must use the persisted old declaration.
			offering := generation.Offering{ID: "same-offering", Revision: "submitted", Outputs: []generation.AssetRequirement{
				{Role: role, VariantPrefix: "export.", MediaTypes: []string{"model/*"}},
			}}
			frozen, err := json.Marshal(offering)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = repo.db.Exec(`UPDATE generation_tasks SET offering_json=?`, string(frozen)); err != nil {
				t.Fatal(err)
			}
			if _, err = repo.db.Exec(`UPDATE generation_assets SET role=?,variant='export.gltf',media_type='model/gltf+json'`, role); err != nil {
				t.Fatal(err)
			}
			offering.Revision = "later-catalog"
			offering.Outputs = nil
			if generation.AssetMatchesOutput(offering.Outputs, role, "export.gltf", "model/gltf+json") {
				t.Fatal("later Catalog still declares the source output")
			}
			var sequence int
			var schemaName, dbPath string
			if err = repo.db.QueryRow(`PRAGMA database_list`).Scan(&sequence, &schemaName, &dbPath); err != nil {
				t.Fatal(err)
			}
			if err = repo.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			store, err := reopened.CaptureRepository(CaptureRepositoryConfig{Bytes: captureTestBytes{data: map[string][]byte{"owner-a:capture-image": data}}, MaxBytes: 1 << 20, MaxPixels: 1024})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.Record(context.Background(), cmd); err != nil {
				t.Fatalf("new capture of frozen %s output after reopen/catalog drift: %v", role, err)
			}
			var persisted string
			if err = reopened.db.QueryRow(`SELECT offering_json FROM generation_tasks WHERE id=?`, cmd.SourceTaskID).Scan(&persisted); err != nil || persisted != string(frozen) {
				t.Fatalf("capture changed task snapshot: %v", err)
			}
		})
	}
}

func TestCaptureCommittedReplayDoesNotRevalidateChangedSource(t *testing.T) {
	repo, store, cmd, _ := captureFixture(t)
	ctx := context.Background()
	first, err := store.Record(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	// Stronger than a Catalog edit: even loss of the source's frozen contract
	// or available bytes cannot invalidate a committed capture's identical replay.
	if _, err = repo.db.Exec(`UPDATE generation_tasks SET offering_json='{}',state='FAILED'; UPDATE generation_assets SET storage_key='',sha256=''`); err != nil {
		t.Fatal(err)
	}
	store.cfg.Bytes = captureTestBytes{}
	retry := cmd
	retry.ID = "retry-id"
	got, err := store.Record(ctx, retry)
	if err != nil || got.ID != first.ID || got.SHA256 != first.SHA256 {
		t.Fatalf("committed replay=%+v err=%v", got, err)
	}
	retry.ModelAssetID = "changed-model"
	if _, err = store.Record(ctx, retry); !errors.Is(err, capture.ErrConflict) {
		t.Fatalf("changed replay accepted: %v", err)
	}
	var sequence int
	var schemaName, dbPath string
	if err = repo.db.QueryRow(`PRAGMA database_list`).Scan(&sequence, &schemaName, &dbPath); err != nil {
		t.Fatal(err)
	}
	if err = repo.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	replayStore, err := reopened.CaptureRepository(CaptureRepositoryConfig{Bytes: captureTestBytes{}, MaxBytes: 1 << 20, MaxPixels: 1024})
	if err != nil {
		t.Fatal(err)
	}
	got, err = replayStore.Record(ctx, cmd)
	if err != nil || got.ID != first.ID || got.SHA256 != first.SHA256 {
		t.Fatalf("reopened committed replay=%+v err=%v", got, err)
	}
}

func TestCaptureRejectsScreenshotMetadataMismatch(t *testing.T) {
	for _, statement := range []string{
		`UPDATE generation_input_assets SET media_type='image/jpeg'`,
		`UPDATE generation_input_assets SET sha256=''`,
		`UPDATE generation_input_assets SET size_bytes=size_bytes+1`,
	} {
		t.Run(statement, func(t *testing.T) {
			repo, store, cmd, _ := captureFixture(t)
			if _, err := repo.db.Exec(statement); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Record(context.Background(), cmd); !errors.Is(err, capture.ErrMismatch) {
				t.Fatalf("screenshot metadata accepted: %v", err)
			}
		})
	}
}

func TestCaptureRecordsOwnedReadyModelAndRealPNGBytes(t *testing.T) {
	_, store, cmd, pngBytes := captureFixture(t)
	ctx := context.Background()
	recorded, err := store.Record(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if recorded.PixelWidth != 4 || recorded.PixelHeight != 3 || recorded.SizeBytes != int64(len(pngBytes)) || len(recorded.SHA256) != 64 {
		t.Fatalf("capture evidence = %+v", recorded)
	}
	retry := cmd
	retry.ID = "different-id"
	replayed, err := store.Record(ctx, retry)
	if err != nil || replayed.ID != cmd.ID {
		t.Fatalf("idempotent capture = %+v, %v", replayed, err)
	}
	retry.View.CameraOrbit = "different orbit"
	if _, err := store.Record(ctx, retry); !errors.Is(err, capture.ErrConflict) {
		t.Fatalf("changed command reused key: %v", err)
	}
	if _, err := store.Get(ctx, "owner-b", cmd.ID); !errors.Is(err, capture.ErrNotFound) {
		t.Fatalf("cross owner capture read: %v", err)
	}
	foreign := cmd
	foreign.ID, foreign.IdempotencyKey, foreign.OwnerID, foreign.SessionID = "capture-b", "foreign-key", "owner-b", "session-b"
	if _, err := store.Record(ctx, foreign); !errors.Is(err, capture.ErrNotFound) {
		t.Fatalf("foreign model was captured: %v", err)
	}
}

func TestCaptureRejectsUnreadySourceAndCorruptScreenshot(t *testing.T) {
	repo, store, cmd, pngBytes := captureFixture(t)
	ctx := context.Background()
	if _, err := repo.db.ExecContext(ctx, `UPDATE generation_tasks SET state='RUNNING' WHERE id=?`, cmd.SourceTaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Record(ctx, cmd); !errors.Is(err, capture.ErrNotFound) {
		t.Fatalf("running model accepted: %v", err)
	}
	if _, err := repo.db.ExecContext(ctx, `UPDATE generation_tasks SET state='SUCCEEDED' WHERE id=?`, cmd.SourceTaskID); err != nil {
		t.Fatal(err)
	}
	store.cfg.Bytes = captureTestBytes{data: map[string][]byte{"owner-a:capture-image": append([]byte(nil), pngBytes[:len(pngBytes)-4]...)}}
	if _, err := store.Record(ctx, cmd); !errors.Is(err, capture.ErrInvalid) {
		t.Fatalf("truncated PNG accepted: %v", err)
	}
	store.cfg.Bytes = captureTestBytes{data: map[string][]byte{"owner-a:capture-image": append(append([]byte(nil), pngBytes...), 0)}}
	if _, err := store.Record(ctx, cmd); !errors.Is(err, capture.ErrMismatch) {
		t.Fatalf("changed screenshot bytes accepted: %v", err)
	}
}

func TestCaptureReleasesSingleSQLiteConnectionBeforeReadingAsset(t *testing.T) {
	repo, store, cmd, data := captureFixture(t)
	store.cfg.Bytes = captureDBBytes{repo: repo, data: data}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := store.Record(ctx, cmd); err != nil {
		t.Fatalf("capture with same-database asset reader: %v", err)
	}
}

func TestCaptureListSessionIsOwnerAndSessionScoped(t *testing.T) {
	repo, store, cmd, _ := captureFixture(t)
	ctx := context.Background()
	if records, err := store.ListSession(ctx, "owner-a", "session-a"); err != nil || len(records) != 0 {
		t.Fatalf("empty session: %v %v", records, err)
	}
	record, err := store.Record(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	records, err := store.ListSession(ctx, "owner-a", "session-a")
	if err != nil || len(records) != 1 || records[0].ID != record.ID ||
		records[0].SourceTaskID != "model-task" || records[0].ScreenshotAssetID != "capture-image" ||
		records[0].View.RendererVersion != "model-viewer:4" {
		t.Fatalf("records = %+v err = %v", records, err)
	}
	// Another owner and another session never see it.
	for _, pair := range [][2]string{{"owner-b", "session-a"}, {"owner-a", "session-b"}, {"owner-b", "session-b"}} {
		if records, err := store.ListSession(ctx, pair[0], pair[1]); err != nil || len(records) != 0 {
			t.Fatalf("%v saw %+v err=%v", pair, records, err)
		}
	}
	if _, err := store.ListSession(ctx, "", "session-a"); !errors.Is(err, capture.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
	_ = repo
	// Oldest first.
	later := cmd
	later.ID, later.IdempotencyKey = "capture-2", "capture-key-2"
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO model_captures
		(id,owner_id,session_id,source_task_id,model_asset_id,screenshot_asset_id,idempotency_key,command_hash,view_json,pixel_width,pixel_height,size_bytes,sha256,created_at)
		SELECT ?,owner_id,session_id,source_task_id,model_asset_id,screenshot_asset_id,?,'h2',view_json,pixel_width,pixel_height,size_bytes,sha256,'2030-01-01T00:00:00Z' FROM model_captures WHERE id=?`,
		later.ID, later.IdempotencyKey, record.ID); err != nil {
		t.Fatal(err)
	}
	records, err = store.ListSession(ctx, "owner-a", "session-a")
	if err != nil || len(records) != 2 || records[0].ID != record.ID || records[1].ID != "capture-2" {
		t.Fatalf("order = %+v err=%v", records, err)
	}
}
