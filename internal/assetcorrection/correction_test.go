package assetcorrection_test

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assetcorrection"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for name, content := range files {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(entry, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// tripoOffering is a frozen Offering whose native export ledger declares the
// OBJ child, as Tripo Texture and the #185 geometry Offerings did before the
// correction (still model/obj in the frozen snapshot).
func tripoOffering(id string) generation.Offering {
	options, _ := json.Marshal(map[string]any{"native_conversion": map[string]any{"formats": []map[string]string{
		{"format": "FBX", "role": "converted_model", "variant": "fbx", "media_type": "model/fbx"},
		{"format": "OBJ", "role": "converted_model", "variant": "obj", "media_type": "model/obj"},
	}}})
	return generation.Offering{ID: generation.OfferingID(id), ProviderID: "tripo", ModelID: "m", AdapterKey: "tripo.v3",
		Capability: generation.Retexture, Domain: generation.DomainModel3D, ProviderOptions: options}
}

type fixture struct {
	t          *testing.T
	dbPath     string
	repository *sqlite.Repository
	store      *local.Store
	scratch    *local.Store
	now        time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	f := &fixture{t: t, dbPath: filepath.Join(dir, "art-ai.db"), now: time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)}
	var err error
	if f.repository, err = sqlite.Open(f.dbPath); err != nil {
		t.Fatal(err)
	}
	if f.store, err = local.New(filepath.Join(dir, "storage")); err != nil {
		t.Fatal(err)
	}
	if f.scratch, err = local.New(filepath.Join(dir, "elsewhere")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.repository.Close(); _ = f.store.Close(); _ = f.scratch.Close() })
	return f
}

// add stores body (in the scratch store only when missing is true, so the
// asset record points at a file this store does not have) and imports a
// succeeded task with one converted_model/obj asset.
func (f *fixture) add(taskID string, offering generation.Offering, mediaType string, body []byte, missing bool) generation.Asset {
	f.t.Helper()
	target := f.store
	if missing {
		target = f.scratch
	}
	blob, err := target.PutBlob(context.Background(), bytes.NewReader(body), "application/octet-stream", 1<<20)
	if err != nil {
		f.t.Fatal(err)
	}
	task := generation.Task{ID: generation.TaskID(taskID), OwnerID: "owner", Capability: offering.Capability, Offering: offering,
		Input: json.RawMessage(`{}`), State: generation.TaskSucceeded, Version: 1, CreatedAt: f.now, UpdatedAt: f.now}
	attempt := sqlite.GenerationAttemptImport{ID: generation.AttemptID(taskID + "-attempt"), Ordinal: 1, Offering: offering,
		State: generation.AttemptUpstreamSucceeded, SubmissionKey: taskID, StartedAt: f.now, UpdatedAt: f.now}
	asset := generation.Asset{ID: generation.AssetID(taskID + "-obj"), TaskID: task.ID, AttemptID: attempt.ID, Role: "converted_model",
		Variant: "obj", Required: true, Ordinal: 1, StorageKey: blob.StorageKey, MediaType: mediaType, Size: blob.Size, SHA256: blob.SHA256, CreatedAt: f.now}
	if _, err := f.repository.ImportGenerationTasksAndAssets(context.Background(),
		[]sqlite.GenerationTaskImport{{Task: task, Attempts: []sqlite.GenerationAttemptImport{attempt}}}, []generation.Asset{asset}); err != nil {
		f.t.Fatal(err)
	}
	return asset
}

func (f *fixture) run() assetcorrection.Report {
	f.t.Helper()
	report, err := assetcorrection.CorrectTripoNativeOBJ(context.Background(), f.repository, f.store, local.ErrMediaTypeRejected,
		func() time.Time { return f.now }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		f.t.Fatal(err)
	}
	return report
}

func (f *fixture) mediaType(taskID string) string {
	f.t.Helper()
	task, err := f.repository.Get(context.Background(), "owner", generation.TaskID(taskID))
	if err != nil || len(task.Assets) != 1 {
		f.t.Fatalf("task %s: %v %+v", taskID, err, task.Assets)
	}
	return task.Assets[0].MediaType
}

func TestTripoNativeOBJCorrectionChecksBytesRecordsAndIsIdempotent(t *testing.T) {
	f := newFixture(t)
	archive := zipOf(t, map[string]string{"model.obj": "mtllib model.mtl\nv 0 0 0\n", "model.mtl": "newmtl base\n"})
	corrected := f.add("texture", tripoOffering("tripo/processing/texture"), "model/obj", archive, false)
	f.add("not-obj", tripoOffering("tripo/geometry/segment"), "model/obj", zipOf(t, map[string]string{"readme.txt": "no obj"}), false)
	f.add("plain-text", tripoOffering("tripo/geometry/complete"), "model/obj", []byte("v 0 0 0\nf 1 1 1\n"), false)
	missing := zipOf(t, map[string]string{"part.obj": "v 1 1 1\n"})
	f.add("missing", tripoOffering("tripo/geometry/decimate-triangle"), "model/obj", missing, true)
	other := tripoOffering("meshy/processing/remesh")
	other.AdapterKey = "meshy.v2"
	f.add("other-provider", other, "model/obj", archive, false)
	f.add("already", tripoOffering("tripo/geometry/quick-cap"), "application/zip", archive, false)

	report := f.run()
	if report != (assetcorrection.Report{Candidates: 5, Corrected: 1, Kept: 2, Deferred: 1, Ignored: 1}) {
		t.Fatalf("first run = %+v", report)
	}
	for task, want := range map[string]string{"texture": "application/zip", "not-obj": "model/obj", "plain-text": "model/obj",
		"missing": "model/obj", "other-provider": "model/obj", "already": "application/zip"} {
		if got := f.mediaType(task); got != want {
			t.Fatalf("%s labelled %s, want %s", task, got, want)
		}
	}
	// The corrected asset is delivered under its new label by the normal checks.
	corrected.MediaType = "application/zip"
	reader, err := f.store.Open(context.Background(), corrected)
	if err != nil {
		t.Fatalf("corrected asset not deliverable: %v", err)
	}
	_ = reader.Close()
	records, err := f.repository.MediaTypeCorrections(context.Background(), assetcorrection.TripoNativeOBJArchive)
	if err != nil || len(records) != 3 {
		t.Fatalf("records %+v %v", records, err)
	}
	outcomes := map[string]assetcorrection.Record{}
	for _, record := range records {
		outcomes[string(record.TaskID)] = record
	}
	if r := outcomes["texture"]; r.Outcome != "corrected" || r.PreviousMediaType != "model/obj" || r.MediaType != "application/zip" || r.SHA256 != corrected.SHA256 {
		t.Fatalf("texture record %+v", r)
	}
	for _, task := range []string{"not-obj", "plain-text"} {
		if r := outcomes[task]; r.Outcome != "kept" || r.MediaType != "model/obj" || r.Detail == "" {
			t.Fatalf("%s record %+v", task, r)
		}
	}

	// A second start re-reads nothing that was decided.
	if report := f.run(); report != (assetcorrection.Report{Candidates: 2, Deferred: 1, Ignored: 1}) {
		t.Fatalf("second run = %+v", report)
	}
	// Once the missing file is back, the next start corrects it.
	if _, err := f.store.PutBlob(context.Background(), bytes.NewReader(missing), "application/octet-stream", 1<<20); err != nil {
		t.Fatal(err)
	}
	if report := f.run(); report != (assetcorrection.Report{Candidates: 2, Corrected: 1, Ignored: 1}) {
		t.Fatalf("third run = %+v", report)
	}
	if got := f.mediaType("missing"); got != "application/zip" {
		t.Fatalf("restored file not corrected: %s", got)
	}

	// The documented rollback restores every corrected label from its record.
	if err := f.repository.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(assetcorrection.RollbackSQL, assetcorrection.TripoNativeOBJArchive); err != nil {
		t.Fatal(err)
	}
	var relabelled int
	if err := db.QueryRow(`SELECT COUNT(*) FROM generation_assets WHERE media_type = 'model/obj'`).Scan(&relabelled); err != nil || relabelled != 5 {
		t.Fatalf("rollback left %d model/obj assets: %v", relabelled, err)
	}
	_ = db.Close()
	if f.repository, err = sqlite.Open(f.dbPath); err != nil {
		t.Fatal(err)
	}
}

// A record can only correct an asset that still carries its previous label.
func TestCorrectionRecordRequiresTheExpectedPreviousLabel(t *testing.T) {
	f := newFixture(t)
	asset := f.add("texture", tripoOffering("tripo/processing/texture"), "application/zip", zipOf(t, map[string]string{"m.obj": "v 0 0 0\n"}), false)
	err := f.repository.RecordMediaTypeCorrection(context.Background(), assetcorrection.Record{
		Correction: assetcorrection.TripoNativeOBJArchive, AssetID: asset.ID, TaskID: asset.TaskID, Outcome: assetcorrection.OutcomeCorrected,
		PreviousMediaType: "model/obj", MediaType: "application/zip", SHA256: asset.SHA256, RecordedAt: f.now,
	})
	if err == nil {
		t.Fatal("correction of an asset without the previous label was recorded")
	}
	if records, _ := f.repository.MediaTypeCorrections(context.Background(), assetcorrection.TripoNativeOBJArchive); len(records) != 0 {
		t.Fatalf("partial record written: %+v", records)
	}
}
