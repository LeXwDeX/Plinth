package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestPreparedInputPersistsAcrossSQLiteRestartAndCannotBeUpdated(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "prepared.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	prepared := generation.PreparedInput{ID: "prepared-a", OwnerID: "owner-a", SessionID: "session-a",
		Capability: generation.TextToImage, OfferingID: "offering-a", OfferingRevision: "v1",
		SchemaVersion: "schema-v1", NormalizedInput: []byte(`{"prompt":"paint"}`),
		Assets: []generation.PreparedInputAsset{}, Dependencies: []generation.PreparedInputDependency{},
		InputHash: "frozen-hash", CreatedAt: time.Now().UTC()}
	if err := repository.CreatePreparedInput(ctx, prepared); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetPreparedInput(ctx, "owner-b", prepared.ID); !errors.Is(err, generation.RepositoryNotFound) {
		t.Fatalf("foreign owner read = %v", err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	repository, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	recovered, err := repository.GetPreparedInput(ctx, "owner-a", prepared.ID)
	if err != nil || recovered.InputHash != prepared.InputHash || string(recovered.NormalizedInput) != string(prepared.NormalizedInput) {
		t.Fatalf("reopened snapshot = %#v, %v", recovered, err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `UPDATE generation_prepared_inputs SET input_hash = 'changed' WHERE id = ?`, prepared.ID); err == nil {
		t.Fatal("immutable prepared input was updated")
	}
}
