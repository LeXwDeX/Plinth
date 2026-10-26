package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func recoveryFixture(t *testing.T) (generation.RecoverySource, generation.Offering, packageManifest, []byte) {
	t.Helper()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, _ := c.Get("local/existing-assets/recover")
	offering.Outputs = offering.Outputs[:2]
	old := generation.Offering{ID: "tripo/frozen/text-to-3d", Revision: "original-r1", Outputs: append([]generation.AssetRequirement(nil), offering.Outputs...)}
	old.Outputs[0].Variant = ""
	old.Outputs[1].Variant = "converted.gltf"
	offering.ProviderOptions, _ = json.Marshal(schema.RecoveryOptions{ReceiptParameter: "receipt_id", DigestParameter: "receipt_digest", Sources: []schema.RecoverySourceContract{{OfferingID: old.ID, OfferingRevision: old.Revision, Slots: []schema.RecoverySlotMapping{{SourceRole: "model", Role: "model", Variant: "glb"}, {SourceRole: "converted_model", SourceVariant: "converted.gltf", Role: "converted_model", Variant: "glb"}}}}})
	source := generation.RecoverySource{Binding: generation.RecoverySourceBinding{OwnerID: "owner", SessionID: "session", TaskID: "source", AttemptID: "attempt", OfferingID: old.ID, OfferingRevision: old.Revision, OfferingSHA256: strings.Repeat("a", 64), SnapshotSHA256: strings.Repeat("b", 64)}, Offering: old}
	raw := []byte(`{"asset":{"version":"2.0"},"scene":0,"scenes":[{"nodes":[0]}],"nodes":[{}]}`)
	for len(raw)%4 != 0 {
		raw = append(raw, ' ')
	}
	glb := make([]byte, 20+len(raw))
	copy(glb, "glTF")
	binary.LittleEndian.PutUint32(glb[4:], 2)
	binary.LittleEndian.PutUint32(glb[8:], uint32(len(glb)))
	binary.LittleEndian.PutUint32(glb[12:], uint32(len(raw)))
	copy(glb[16:], "JSON")
	copy(glb[20:], raw)
	sum := sha256.Sum256(glb)
	count := 0
	manifest := packageManifest{OriginalTaskID: "source", OriginalTaskState: "FAILED", OriginalRegisteredAssetCount: &count, FrozenOfferingRevision: old.Revision, Files: []packageFile{{File: "assets/model.glb", Roles: []packageRole{{Role: "model"}, {Role: "converted_model", Variant: "converted.gltf"}}, Size: int64(len(glb)), SHA256: hex.EncodeToString(sum[:]), StoredMediaType: "model/gltf-binary"}}}
	return source, offering, manifest, glb
}
func writePackage(t *testing.T, manifest packageManifest, model []byte, extras []string) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "package.zip")
	f, e := os.Create(name)
	if e != nil {
		t.Fatal(e)
	}
	w := zip.NewWriter(f)
	raw, _ := json.Marshal(manifest)
	for _, entry := range []struct {
		name string
		body []byte
	}{{"manifest.json", raw}, {"assets/model.glb", model}} {
		b, e := w.Create(entry.name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = b.Write(entry.body); e != nil {
			t.Fatal(e)
		}
	}
	for _, name := range extras {
		b, e := w.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		b.Write([]byte("untrusted"))
	}
	if e = w.Close(); e != nil {
		t.Fatal(e)
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
	return name
}
func TestCollectPackagePreservesDistinctSlotsSharingExactBytes(t *testing.T) {
	source, offering, manifest, body := recoveryFixture(t)
	name := writePackage(t, manifest, body, nil)
	store, e := local.New(filepath.Join(t.TempDir(), "assets"))
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	receipt, e := collectPackage(context.Background(), name, strings.Repeat("c", 64), source, offering, store)
	if e != nil {
		t.Fatal(e)
	}
	if len(receipt.Slots) != 2 || receipt.Slots[0].StorageKey != receipt.Slots[1].StorageKey || receipt.Slots[0].Role == receipt.Slots[1].Role || receipt.Slots[1].SourceVariant != "converted.gltf" || receipt.Slots[1].Variant != "glb" {
		t.Fatalf("lost canonical aliases: %+v", receipt.Slots)
	}
}
func TestCollectPackageRejectsChangedEvidenceAndUnsafeMembers(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*packageManifest, *[]byte)
		extra  []string
	}{
		{"wrong digest", func(m *packageManifest, _ *[]byte) { m.Files[0].SHA256 = strings.Repeat("0", 64) }, nil},
		{"wrong source", func(m *packageManifest, _ *[]byte) { m.OriginalTaskID = "other" }, nil},
		{"wrong revision", func(m *packageManifest, _ *[]byte) { m.FrozenOfferingRevision = "other" }, nil},
		{"unknown role", func(m *packageManifest, _ *[]byte) { m.Files[0].Roles[1].Role = "unknown" }, nil},
		{"missing alias", func(m *packageManifest, _ *[]byte) { m.Files[0].Roles = m.Files[0].Roles[:1] }, nil},
		{"duplicate alias", func(m *packageManifest, _ *[]byte) { m.Files[0].Roles[1] = m.Files[0].Roles[0] }, nil},
		{"invalid actual bytes", func(m *packageManifest, b *[]byte) {
			(*b)[0] = 'X'
			sum := sha256.Sum256(*b)
			m.Files[0].SHA256 = hex.EncodeToString(sum[:])
		}, nil},
		{"unlisted member", nil, []string{"assets/unknown.bin"}},
		{"traversal", nil, []string{"../outside"}},
		{"duplicate member", nil, []string{"assets/model.glb"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			source, offering, manifest, body := recoveryFixture(t)
			if tt.mutate != nil {
				tt.mutate(&manifest, &body)
			}
			name := writePackage(t, manifest, body, tt.extra)
			store, e := local.New(filepath.Join(t.TempDir(), "assets"))
			if e != nil {
				t.Fatal(e)
			}
			defer store.Close()
			if _, e = collectPackage(context.Background(), name, strings.Repeat("c", 64), source, offering, store); e == nil {
				t.Fatal("untrusted package accepted")
			}
		})
	}
}
func TestPinnedArchiveRejectsDigestAndSymlink(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	body := []byte("original pinned bytes")
	if e := os.WriteFile(source, body, 0600); e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	target := filepath.Join(dir, "copy")
	if e := copyPinnedArchive(context.Background(), source, target, digest); e != nil {
		t.Fatal(e)
	}
	got, _ := os.ReadFile(target)
	if !bytes.Equal(got, body) {
		t.Fatal("pinned bytes differ")
	}
	if e := copyPinnedArchive(context.Background(), source, filepath.Join(dir, "bad-copy"), strings.Repeat("0", 64)); e == nil {
		t.Fatal("wrong digest accepted")
	}
	link := filepath.Join(dir, "link")
	if e := os.Symlink(source, link); e != nil {
		t.Fatal(e)
	}
	if e := copyPinnedArchive(context.Background(), link, filepath.Join(dir, "link-copy"), digest); e == nil {
		t.Fatal("symlink accepted")
	}
}

func TestCollectorDryRunLeavesDatabaseUntouchedAndApplyOnlyAddsReceipt(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dbPath := filepath.Join(root, "tasks.db")
	repo, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.Close(); err != nil {
		t.Fatal(err)
	}
	source, offering, manifest, body := recoveryFixture(t)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	frozen, _ := json.Marshal(source.Offering)
	stamp := "2026-10-16T00:00:00Z"
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at) VALUES ('session','owner','Owner','History',?,?)`, []any{stamp, stamp}},
		{`INSERT INTO generation_tasks(id,owner_id,session_id,capability,offering_json,input_json,request_hash,state,current_attempt_id,version,created_at,updated_at,finished_at) VALUES ('source','owner','session','text-to-3d',?,'{}','old','FAILED','attempt',1,?,?,?)`, []any{string(frozen), stamp, stamp, stamp}},
		{`INSERT INTO generation_attempts(id,task_id,ordinal,offering_json,state,submission_key,upstream_provider_id,upstream_job_id,opaque_json,started_at,updated_at,finished_at) VALUES ('attempt','source',1,?,'FAILED','old','tripo','private-id','{}',?,?,?)`, []any{string(frozen), stamp, stamp, stamp}},
	} {
		if _, err = db.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	before, err := sqlite.ReadRecoverySourceFile(ctx, dbPath, "owner", "source")
	if err != nil {
		t.Fatal(err)
	}
	catalogRaw, err := os.ReadFile("../../internal/catalog/default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	json.Unmarshal(catalogRaw, &doc)
	var entries []map[string]json.RawMessage
	json.Unmarshal(doc["offerings"], &entries)
	for _, entry := range entries {
		if string(entry["id"]) == `"local/existing-assets/recover"` {
			var outputs []json.RawMessage
			json.Unmarshal(entry["outputs"], &outputs)
			entry["outputs"], _ = json.Marshal(outputs[:2])
			entry["provider_options"] = offering.ProviderOptions
		}
	}
	doc["offerings"], _ = json.Marshal(entries)
	catalogRaw, _ = json.Marshal(doc)
	catalogPath := filepath.Join(root, "catalog.json")
	if err = os.WriteFile(catalogPath, catalogRaw, 0600); err != nil {
		t.Fatal(err)
	}
	name := writePackage(t, manifest, body, nil)
	archive, _ := os.ReadFile(name)
	sum := sha256.Sum256(archive)
	args := []string{"--database", dbPath, "--owner", "owner", "--source-task", "source", "--source-attempt", "attempt", "--package", name, "--expected-package-sha256", hex.EncodeToString(sum[:]), "--offering", string(offering.ID), "--catalog", catalogPath}
	var output, diagnostics bytes.Buffer
	if err = run(ctx, args, &output, &diagnostics); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(dbPath)
	if !bytes.Equal(original, after) {
		t.Fatal("dry-run changed database bytes")
	}
	if !strings.Contains(output.String(), `"dry_run":true`) {
		t.Fatal("preview not declared")
	}
	store := filepath.Join(root, "live-cas")
	if err = os.Mkdir(store, 0700); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err = run(ctx, append(args, "--apply", "--storage-root", store), &output, &diagnostics); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"task_submitted":false`) || !strings.Contains(output.String(), `"provider_requests":0`) {
		t.Fatal("apply scope missing")
	}
	afterSource, err := sqlite.ReadRecoverySourceFile(ctx, dbPath, "owner", "source")
	if err != nil || before.Binding != afterSource.Binding {
		t.Fatalf("source changed: %v", err)
	}
	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var tasks, receipts, assets int
	if err = db.QueryRow(`SELECT (SELECT count(*) FROM generation_tasks),(SELECT count(*) FROM generation_recovery_receipts),(SELECT count(*) FROM generation_assets)`).Scan(&tasks, &receipts, &assets); err != nil || tasks != 1 || receipts != 1 || assets != 0 {
		t.Fatalf("apply changed task/asset state: %d %d %d %v", tasks, receipts, assets, err)
	}
}
