package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/rendermanifest"
	"github.com/LeXwDeX/Plinth/internal/scene"
)

//go:embed migrations/0034_render_manifests.sql
var renderManifestMigrationForTest string

type fixedManifestBytes struct{ data map[string][]byte }

func (f fixedManifestBytes) OpenOwned(_ context.Context, owner, id string) (io.ReadCloser, error) {
	b, ok := f.data[owner+"/"+id]
	if !ok {
		return nil, errors.New("unowned bytes")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

type fixedRenderVerifier struct{ want []byte }

func (f fixedRenderVerifier) Verify(_ context.Context, m rendermanifest.Manifest, data []byte) error {
	if m.RendererVersion != "renderer-1" || !bytes.Equal(data, f.want) {
		return errors.New("rendered pixels differ")
	}
	return nil
}
func manifestHash(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func manifestFixture(t *testing.T) (*Repository, *RenderManifestRepository, rendermanifest.Manifest) {
	t.Helper()
	ctx := context.Background()
	repo, err := Open(filepath.Join(t.TempDir(), "manifest.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if MigrationHead() < 33 {
		if _, err = repo.db.Exec(sceneMigrationForTest); err != nil {
			t.Fatal(err)
		}
	}
	if MigrationHead() < 34 {
		if _, err = repo.db.Exec(renderManifestMigrationForTest); err != nil {
			t.Fatal(err)
		}
	}
	for _, pair := range [][2]string{{"owner-a", "session-a"}, {"owner-b", "session-b"}} {
		_, err = repo.db.ExecContext(ctx, `INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at) VALUES (?,?,?,?,?,?)`, pair[1], pair[0], "name", "title", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = repo.db.ExecContext(ctx, `INSERT INTO scene_revisions(session_id,owner_id,revision,updated_at) VALUES (?,?,?,?)`, "session-a", "owner-a", 1, "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	inputData, outputData := []byte("input pixels"), []byte("verified rendered frame")
	for _, item := range []struct {
		id   string
		data []byte
	}{{"input-a", inputData}, {"output-a", outputData}} {
		_, err = repo.db.ExecContext(ctx, `INSERT INTO generation_input_assets(id,owner_id,storage_key,media_type,size_bytes,sha256,created_at) VALUES (?,?,?,?,?,?,?)`, item.id, "owner-a", "key", "image/png", len(item.data), manifestHash(item.data), "2026-01-01T00:00:00Z")
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = repo.db.ExecContext(ctx, `INSERT INTO scene_objects(id,owner_id,session_id,revision,active_representation_id,rect_x,rect_y,rect_width,rect_height,anchor_x,anchor_y,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, "object-a", "owner-a", "session-a", 1, "rep-a", 0, 0, 100, 100, 0, 0, "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.db.ExecContext(ctx, `INSERT INTO scene_representations(id,object_id,kind,asset_id,metadata_schema_revision,created_at) VALUES (?,?,?,?,?,?)`, "rep-a", "object-a", "bitmap", "input-a", "v1", "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.RenderManifestRepository(RenderManifestRepositoryConfig{Evidence: SQLiteRenderManifestEvidence{}, MaxOutputBytes: 1024}); !errors.Is(err, rendermanifest.ErrInvalid) {
		t.Fatalf("missing proof accepted: %v", err)
	}
	mr, err := repo.RenderManifestRepository(RenderManifestRepositoryConfig{Bytes: fixedManifestBytes{map[string][]byte{"owner-a/output-a": outputData}}, Verifier: fixedRenderVerifier{outputData}, Evidence: SQLiteRenderManifestEvidence{}, MaxOutputBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	m := rendermanifest.Manifest{ID: "manifest-a", OwnerID: "owner-a", SessionID: "session-a", SceneRevision: 1, Frame: rendermanifest.Frame{X: 10, Y: 20, Width: 100, Height: 50, PixelWidth: 200, PixelHeight: 100, Matrix: [6]float64{2, 0, 0, 2, -20, -40}}, Inputs: []rendermanifest.Input{{ObjectID: "object-a", ObjectRevision: 1, RepresentationID: "rep-a", AssetID: "input-a", AssetSHA256: manifestHash(inputData)}}, RendererVersion: "renderer-1", Output: rendermanifest.Output{AssetID: "output-a", SizeBytes: int64(len(outputData)), SHA256: manifestHash(outputData)}}
	return repo, mr, m
}

func TestRenderManifestAdmissionAndImmutableStorage(t *testing.T) {
	repo, mr, m := manifestFixture(t)
	ctx := context.Background()
	cross := m
	cross.OwnerID = "owner-b"
	cross.SessionID = "session-b"
	if _, err := mr.Create(ctx, cross); !errors.Is(err, rendermanifest.ErrNotFound) {
		t.Fatalf("cross-owner scene accepted: %v", err)
	}
	bad := m
	bad.Inputs = append([]rendermanifest.Input(nil), m.Inputs...)
	bad.Inputs[0].AssetSHA256 = manifestHash([]byte("wrong"))
	if _, err := mr.Create(ctx, bad); !errors.Is(err, rendermanifest.ErrMismatch) {
		t.Fatalf("bad input hash: %v", err)
	}
	bad = m
	bad.Output.SHA256 = manifestHash([]byte("wrong"))
	if _, err := mr.Create(ctx, bad); !errors.Is(err, rendermanifest.ErrMismatch) {
		t.Fatalf("bad output hash: %v", err)
	}
	created, err := mr.Create(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	if created.CreatedAt.IsZero() {
		t.Fatal("missing creation timestamp")
	}
	if _, err = mr.Create(ctx, m); !errors.Is(err, rendermanifest.ErrConflict) {
		t.Fatalf("duplicate: %v", err)
	}
	loaded, err := mr.Get(ctx, "owner-a", "session-a", m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Inputs) != 1 || loaded.Inputs[0].AssetSHA256 != m.Inputs[0].AssetSHA256 || loaded.Frame.Matrix != m.Frame.Matrix {
		t.Fatalf("roundtrip: %+v", loaded)
	}
	if _, err = mr.Get(ctx, "owner-b", "session-b", m.ID); !errors.Is(err, rendermanifest.ErrNotFound) {
		t.Fatalf("cross-owner read: %v", err)
	}
	if _, err = repo.db.ExecContext(ctx, `UPDATE render_manifests SET renderer_version='forged' WHERE id=?`, m.ID); err == nil {
		t.Fatal("immutable row modified")
	}
}

func TestRenderManifestRejectsRevisionDriftAndWrongBytes(t *testing.T) {
	repo, mr, m := manifestFixture(t)
	ctx := context.Background()
	_, err := repo.db.ExecContext(ctx, `UPDATE scene_revisions SET revision=2 WHERE session_id='session-a'`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = mr.Create(ctx, m); !errors.Is(err, rendermanifest.ErrDrift) {
		t.Fatalf("scene drift: %v", err)
	}
	_, err = repo.db.ExecContext(ctx, `UPDATE scene_revisions SET revision=1 WHERE session_id='session-a'`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.db.ExecContext(ctx, `UPDATE scene_objects SET revision=2 WHERE id='object-a'`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = mr.Create(ctx, m); !errors.Is(err, rendermanifest.ErrDrift) {
		t.Fatalf("object drift: %v", err)
	}
	_, err = repo.db.ExecContext(ctx, `UPDATE scene_objects SET revision=1 WHERE id='object-a'`)
	if err != nil {
		t.Fatal(err)
	}
	wrongBytes, err := repo.RenderManifestRepository(RenderManifestRepositoryConfig{Bytes: fixedManifestBytes{map[string][]byte{"owner-a/output-a": []byte("forged bytes")}}, Verifier: fixedRenderVerifier{[]byte("forged bytes")}, Evidence: SQLiteRenderManifestEvidence{}, MaxOutputBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = wrongBytes.Create(ctx, m); !errors.Is(err, rendermanifest.ErrMismatch) {
		t.Fatalf("wrong upload bytes: %v", err)
	}
}

func TestRenderManifestUnlistedObjectChangeAdvancesSceneRevision(t *testing.T) {
	repo, mr, m := manifestFixture(t)
	ctx := context.Background()
	sr, err := repo.SceneRepository(SQLiteSceneEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	extra := scene.Create{Object: scene.Object{ID: "object-b", OwnerID: "owner-a", SessionID: "session-a", Rect: scene.Rect{Width: 10, Height: 10}}, Initial: scene.Representation{ID: "rep-b", ObjectID: "object-b", Kind: scene.Bitmap, AssetID: "input-a", MetadataSchemaRevision: "v1"}}
	if _, err = sr.Create(ctx, extra); err != nil {
		t.Fatal(err)
	}
	if _, err = mr.Create(ctx, m); !errors.Is(err, rendermanifest.ErrDrift) {
		t.Fatalf("unlisted new object did not invalidate frame: %v", err)
	}
	m.SceneRevision = 2
	if _, err = sr.Delete(ctx, scene.Mutation{OwnerID: "owner-a", SessionID: "session-a", ObjectID: "object-b", ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = mr.Create(ctx, m); !errors.Is(err, rendermanifest.ErrDrift) {
		t.Fatalf("unlisted deleted object did not invalidate frame: %v", err)
	}
}
