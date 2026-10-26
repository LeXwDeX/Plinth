package etl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/etl/etltest"
)

// All source/snapshot evidence is synthetic and local. No collector invokes
// Docker, a network transport, migration or any target/CAS storage port.
type proofFixture struct {
	legacy                                                                         *etltest.Fixture
	source, snapshot, evidencePath, bindingsPath, manifestPath, inventoryPath, out string
	evidence, bindings                                                             map[string]any
}

func newProofFixture(t *testing.T) *proofFixture {
	t.Helper()
	f := &proofFixture{legacy: etltest.NewFixture(t), source: t.TempDir(), out: t.TempDir()}
	f.snapshot = filepath.Join(f.legacy.Root, "storage")
	diagnosticWrite(t, filepath.Join(f.snapshot, "assets/PRIVATE_FILENAME.png"), diagnosticPNG(t))
	if err := filepath.WalkDir(f.snapshot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(f.snapshot, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(f.source, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0700)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, body, 0600)
	}); err != nil {
		t.Fatal(err)
	}
	input := t.TempDir()
	f.evidencePath = filepath.Join(input, "projection.json")
	f.bindingsPath = filepath.Join(input, "bindings.json")
	f.manifestPath = filepath.Join(input, "manifest.txt")
	f.inventoryPath = filepath.Join(input, "inventory.txt")
	var manifest, inventory strings.Builder
	fmt.Fprintln(&manifest, "format=art-ai-go-cutover-backup-v1")
	fmt.Fprintln(&inventory, "format=art-ai-formal-production-inventory-v1")
	fmt.Fprintln(&inventory, "collected_at_utc=2026-10-15T00:00:00Z")
	fmt.Fprintln(&inventory, "hostname=PRIVATE_HOST")
	fmt.Fprintln(&inventory, "legacy_container_image_id=sha256:"+strings.Repeat("d", 64))
	fmt.Fprintln(&inventory, "legacy_compose_project=PRIVATE_PROJECT")
	fmt.Fprintln(&inventory, "legacy_compose_service=PRIVATE_SERVICE")
	fmt.Fprintln(&inventory, "legacy_container_running=false")
	fmt.Fprintln(&inventory, "database_snapshot_sha256="+proofFileHash(t, f.legacy.DatabasePath()))
	fmt.Fprintf(&manifest, "backup_database_sha256=%s\n", proofFileHash(t, f.legacy.DatabasePath()))
	for _, dir := range []string{"assets", "generated", "cache", "audit", "thumbnails", "task-inputs"} {
		count := 0
		var size int64
		if err := filepath.WalkDir(filepath.Join(f.snapshot, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type().IsRegular() {
				info, err := d.Info()
				if err != nil {
					return err
				}
				count++
				size += info.Size()
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&manifest, "storage.%s.files=%d\n", dir, count)
		fmt.Fprintf(&inventory, "storage_%s_files=%d\nstorage_%s_bytes=%d\n", dir, count, dir, size)
	}
	fmt.Fprintln(&inventory, "legacy_mount=bind|/PRIVATE_ORIGIN/storage|/app/storage|true")
	diagnosticWrite(t, f.manifestPath, []byte(manifest.String()))
	diagnosticWrite(t, f.inventoryPath, []byte(inventory.String()))
	identity := map[string]any{"host": "PRIVATE_HOST", "daemon": "PRIVATE_DAEMON", "container_id": strings.Repeat("c", 64), "compose_project": "PRIVATE_PROJECT", "compose_service": "PRIVATE_SERVICE", "image_id": "sha256:" + strings.Repeat("d", 64)}
	f.evidence = map[string]any{"format": "art-ai-snapshot-evidence-v1", "capture_epoch": "PRIVATE_EPOCH", "captured_at_utc": "2026-10-15T00:00:00Z", "consistency": map[string]any{"kind": "stopped_writer", "reference": "PRIVATE_FREEZE"}, "identity": identity, "state": "stopped", "storage_prefix": "/app/storage", "mounts": []any{map[string]any{"type": "bind", "source": "/PRIVATE_ORIGIN/storage", "destination": "/app/storage"}}, "binds": []any{map[string]any{"source": "/PRIVATE_ORIGIN/storage", "destination": "/app/storage"}}, "volumes": []any{"/app/storage"}, "upper_dir": "/PRIVATE_UPPER", "export_paths": []any{"/app/storage"}, "diff": []any{}, "inventory_sha256": proofFileHash(t, f.inventoryPath), "manifest_sha256": proofFileHash(t, f.manifestPath), "database_sha256": proofFileHash(t, f.legacy.DatabasePath())}
	f.bindings = map[string]any{"format": "art-ai-snapshot-bindings-v1", "expected_identity": identity, "scopes": []any{map[string]any{"id": "PRIVATE_SCOPE", "kind": "mount", "container_path": "/app/storage", "origin": "/PRIVATE_ORIGIN/storage", "source_root": f.source, "snapshot_path": "."}}}
	f.save(t)
	return f
}
func proofFileHash(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return diagnosticHash(b)
}
func (f *proofFixture) save(t *testing.T) {
	t.Helper()
	for path, v := range map[string]any{f.evidencePath: f.evidence, f.bindingsPath: f.bindings} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		diagnosticWrite(t, path, b)
	}
}
func (f *proofFixture) args(report string) []string {
	return []string{"verify-snapshot", "--evidence", f.evidencePath, "--scope-bindings", f.bindingsPath, "--legacy-database", f.legacy.DatabasePath(), "--snapshot-storage", f.snapshot, "--snapshot-manifest", f.manifestPath, "--inventory", f.inventoryPath, "--report", report, "--generated-at-utc", "2026-10-15T01:00:00Z"}
}
func TestVerifySnapshotCompleteReadOnlyRedactedAndDeterministic(t *testing.T) {
	f := newProofFixture(t)
	beforeSource := diagnosticTree(t, f.source)
	beforeSnapshot := diagnosticTree(t, f.snapshot)
	var first []byte
	for _, name := range []string{"first.json", "second.json"} {
		path := filepath.Join(f.out, name)
		var stdout, stderr bytes.Buffer
		if code := Main(f.args(path), &stdout, &stderr); code != 0 {
			t.Fatalf("proof failed %d: %s %s", code, stdout.String(), stderr.String())
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var report map[string]any
		if err = json.Unmarshal(body, &report); err != nil {
			t.Fatal(err)
		}
		if report["decision"] != "supported" || report["source_loss_approval"] != false || report["live_source_authenticated"] != false || report["snapshot_identity"] == "" {
			t.Fatalf("wrong proof: %s", body)
		}
		criteria, ok := report["criteria"].([]any)
		if !ok || len(criteria) != 6 {
			t.Fatal("missing C1-C6")
		}
		for _, raw := range criteria {
			if raw.(map[string]any)["status"] != "supported" {
				t.Fatalf("criterion unclosed: %s", body)
			}
		}
		for _, secret := range []string{"PRIVATE_", f.source, f.snapshot} {
			if bytes.Contains(append(append(body, stdout.Bytes()...), stderr.Bytes()...), []byte(secret)) {
				t.Fatalf("proof exposed private evidence: %q", secret)
			}
		}
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0600 {
			t.Fatal("unsafe report mode")
		}
		if first != nil && !bytes.Equal(first, body) {
			t.Fatal("nondeterministic proof")
		}
		first = body
	}
	if !reflect.DeepEqual(beforeSource, diagnosticTree(t, f.source)) || !reflect.DeepEqual(beforeSnapshot, diagnosticTree(t, f.snapshot)) {
		t.Fatal("proof mutated input trees")
	}
}
func TestVerifySnapshotStopsOnMissingOrContradictoryEvidence(t *testing.T) {
	for _, name := range []string{"epoch", "freeze", "diff_missing", "mount_missing", "volume_missing", "identity", "scope_missing", "scope_ambiguous", "scope_escape", "writable_uncovered", "dangling", "count_same_bytes_changed", "unlisted_file", "directory_missing", "manifest_digest", "source_wal", "unresolved_reference"} {
		t.Run(name, func(t *testing.T) {
			f := newProofFixture(t)
			switch name {
			case "epoch":
				delete(f.evidence, "capture_epoch")
			case "freeze":
				delete(f.evidence, "consistency")
			case "diff_missing":
				delete(f.evidence, "diff")
			case "mount_missing":
				f.evidence["mounts"] = append(f.evidence["mounts"].([]any), map[string]any{"type": "volume", "source": "/PRIVATE_SECOND", "destination": "/app/storage/extra"})
			case "volume_missing":
				f.evidence["volumes"] = append(f.evidence["volumes"].([]any), "/app/storage/anonymous")
			case "identity":
				f.bindings["expected_identity"].(map[string]any)["container_id"] = "PRIVATE_REPLACED"
				f.evidence["identity"] = map[string]any{"host": "PRIVATE_HOST", "daemon": "PRIVATE_DAEMON", "container_id": strings.Repeat("c", 64), "compose_project": "PRIVATE_PROJECT", "compose_service": "PRIVATE_SERVICE", "image_id": "sha256:" + strings.Repeat("d", 64)}
			case "scope_missing":
				f.bindings["scopes"] = []any{}
			case "scope_ambiguous":
				first := f.bindings["scopes"].([]any)[0].(map[string]any)
				duplicate := make(map[string]any, len(first))
				for key, value := range first {
					duplicate[key] = value
				}
				duplicate["id"] = "PRIVATE_AMBIGUOUS"
				f.bindings["scopes"] = append(f.bindings["scopes"].([]any), duplicate)
			case "scope_escape":
				f.bindings["scopes"].([]any)[0].(map[string]any)["snapshot_path"] = "../PRIVATE_ESCAPE"
			case "writable_uncovered":
				f.evidence["diff"] = []any{map[string]any{"change": "A", "path": "/app/storage/PRIVATE_WRITABLE"}}
			case "dangling":
				if err := os.Symlink("PRIVATE_MISSING", filepath.Join(f.source, "assets/PRIVATE_LINK")); err != nil {
					t.Fatal(err)
				}
			case "count_same_bytes_changed":
				diagnosticWrite(t, filepath.Join(f.source, "assets/PRIVATE_FILENAME.png"), []byte("same one file different bytes"))
			case "unlisted_file":
				diagnosticWrite(t, filepath.Join(f.snapshot, "cache/PRIVATE_EXTRA"), []byte("extra"))
			case "directory_missing":
				if err := os.Remove(filepath.Join(f.source, "thumbnails")); err != nil {
					t.Fatal(err)
				}
			case "manifest_digest":
				diagnosticWrite(t, f.manifestPath, []byte("PRIVATE_TAMPERED"))
			case "source_wal":
				diagnosticWrite(t, f.legacy.DatabasePath()+"-wal", []byte("PRIVATE_WAL"))
			case "unresolved_reference":
				diagnosticTask(t, f.legacy, "PRIVATE_TASK", "PRIVATE_NOT_A_ROUTE")
				f.evidence["database_sha256"] = proofFileHash(t, f.legacy.DatabasePath())
			}
			f.save(t)
			var out, stderr bytes.Buffer
			path := filepath.Join(f.out, "stop.json")
			if Main(f.args(path), &out, &stderr) == 0 {
				t.Fatalf("unclosed evidence accepted: %s", out.String())
			}
			body, _ := os.ReadFile(path)
			var doc map[string]any
			if len(body) > 0 {
				if json.Unmarshal(body, &doc) != nil || doc["decision"] != "STOP" {
					t.Fatalf("unsafe failure proof: %s", body)
				}
			}
			if bytes.Contains(append(append(body, out.Bytes()...), stderr.Bytes()...), []byte("PRIVATE_")) {
				t.Fatal("failure exposed sensitive evidence")
			}
		})
	}
}

func (f *proofFixture) refreshDatabase(t *testing.T) {
	t.Helper()
	raw, err := os.ReadFile(f.legacy.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	diagnosticWrite(t, filepath.Join(f.source, "data/prod.db"), raw)
	manifest, err := os.ReadFile(f.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(manifest), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "backup_database_sha256=") {
			lines[i] = "backup_database_sha256=" + diagnosticHash(raw)
		}
	}
	diagnosticWrite(t, f.manifestPath, []byte(strings.Join(lines, "\n")))
	inventory, err := os.ReadFile(f.inventoryPath)
	if err != nil {
		t.Fatal(err)
	}
	inventoryLines := strings.Split(string(inventory), "\n")
	for i, line := range inventoryLines {
		if strings.HasPrefix(line, "database_snapshot_sha256=") {
			inventoryLines[i] = "database_snapshot_sha256=" + diagnosticHash(raw)
		}
	}
	diagnosticWrite(t, f.inventoryPath, []byte(strings.Join(inventoryLines, "\n")))
	f.evidence["inventory_sha256"] = proofFileHash(t, f.inventoryPath)
	f.evidence["database_sha256"] = diagnosticHash(raw)
	f.evidence["manifest_sha256"] = proofFileHash(t, f.manifestPath)
	f.save(t)
}
func proofRunDocument(t *testing.T, f *proofFixture) (int, map[string]any, []byte) {
	t.Helper()
	var out, stderr bytes.Buffer
	path := filepath.Join(f.out, "result.json")
	code := Main(f.args(path), &out, &stderr)
	raw, _ := os.ReadFile(path)
	var report map[string]any
	if len(raw) > 0 && json.Unmarshal(raw, &report) != nil {
		t.Fatal("malformed proof")
	}
	combined := append(append(append([]byte{}, raw...), out.Bytes()...), stderr.Bytes()...)
	if bytes.Contains(combined, []byte("PRIVATE_")) {
		t.Fatal("private sentinel escaped")
	}
	return code, report, combined
}
func TestVerifySnapshotRemoteOfflineAndAllLedgerReferences(t *testing.T) {
	for _, status := range []string{"SUCCEEDED", "FAILED"} {
		t.Run(status, func(t *testing.T) {
			f := newProofFixture(t)
			diagnosticTask(t, f.legacy, "PRIVATE_REMOTE", "https://PRIVATE_HOST.invalid/PRIVATE_TOKEN.png?key=PRIVATE_SECRET")
			f.legacy.Exec(t, `UPDATE usage_ledger SET taskStatus=?`, status)
			f.refreshDatabase(t)
			code, doc, raw := proofRunDocument(t, f)
			if code != 0 {
				t.Fatalf("known remote bucket rejected: %s", raw)
			}
			found := false
			for _, raw := range doc["reference_buckets"].([]any) {
				b := raw.(map[string]any)
				if b["kind"] == "remote_offline" && b["count"] == float64(1) {
					found = true
				}
			}
			if !found || doc["source_loss_approval"] != false {
				t.Fatal("remote reference omitted or treated as approved loss")
			}
		})
	}
	f := newProofFixture(t)
	diagnosticTask(t, f.legacy, "PRIVATE_TASK", "PRIVATE_UNRECOGNIZED")
	f.refreshDatabase(t)
	code, doc, _ := proofRunDocument(t, f)
	if code == 0 || doc["decision"] != "STOP" {
		t.Fatal("unexplained URL was accepted")
	}
}
func TestVerifySnapshotAcceptsExplicitWritableCaptureAndRunningFilesystemSnapshot(t *testing.T) {
	for _, state := range []string{"stopped_writable", "running_fs_snapshot"} {
		t.Run(state, func(t *testing.T) {
			f := newProofFixture(t)
			if state == "running_fs_snapshot" {
				f.evidence["state"] = "running"
				f.evidence["consistency"].(map[string]any)["kind"] = "filesystem_snapshot"
				f.evidence["mountinfo"] = []any{"/", "/app/storage"}
				inventory, _ := os.ReadFile(f.inventoryPath)
				inventory = bytes.Replace(inventory, []byte("legacy_container_running=false"), []byte("legacy_container_running=true"), 1)
				diagnosticWrite(t, f.inventoryPath, inventory)
				f.evidence["inventory_sha256"] = proofFileHash(t, f.inventoryPath)
				delete(f.evidence, "export_paths")
			} else {
				f.evidence["diff"] = []any{map[string]any{"change": "M", "path": "/app/storage/assets/PRIVATE_FILENAME.png"}}
				f.evidence["export_paths"] = []any{"/app/storage", "/app/storage/assets/PRIVATE_FILENAME.png"}
				f.bindings["scopes"] = append(f.bindings["scopes"].([]any), map[string]any{"id": "PRIVATE_WRITABLE_SCOPE", "kind": "writable_layer", "container_path": "/app/storage", "origin": "/PRIVATE_UPPER", "source_root": f.source, "snapshot_path": "."})
			}
			f.save(t)
			code, _, raw := proofRunDocument(t, f)
			if code != 0 {
				t.Fatalf("explicit covered capture rejected: %s", raw)
			}
		})
	}
}
func TestVerifySnapshotRejectsUntrustedJSONAndUnsafeOutputs(t *testing.T) {
	for _, name := range []string{"unknown", "duplicate", "trailing", "permissions", "self_tree", "protected_output", "source_output", "credential_in_tree", "mountinfo_missing", "writable_export_missing"} {
		t.Run(name, func(t *testing.T) {
			f := newProofFixture(t)
			report := filepath.Join(f.out, "result.json")
			protected := t.TempDir()
			diagnosticWrite(t, filepath.Join(protected, "PRIVATE_target.db"), []byte("PRIVATE_TARGET"))
			before := diagnosticTree(t, protected)
			raw, _ := os.ReadFile(f.evidencePath)
			switch name {
			case "unknown":
				raw = bytes.Replace(raw, []byte(`"state":"stopped"`), []byte(`"state":"stopped","Env":["PRIVATE_CREDENTIAL"]`), 1)
				diagnosticWrite(t, f.evidencePath, raw)
			case "duplicate":
				raw = bytes.Replace(raw, []byte(`"state":"stopped"`), []byte(`"state":"stopped","state":"running"`), 1)
				diagnosticWrite(t, f.evidencePath, raw)
			case "trailing":
				diagnosticWrite(t, f.evidencePath, append(raw, []byte(` {"PRIVATE_TRAILING":true}`)...))
			case "permissions":
				if err := os.Chmod(f.evidencePath, 0644); err != nil {
					t.Fatal(err)
				}
			case "self_tree":
				f.bindings["scopes"].([]any)[0].(map[string]any)["source_root"] = f.snapshot
				f.save(t)
			case "protected_output":
				report = filepath.Join(protected, "report.json")
			case "source_output":
				report = filepath.Join(f.source, "report.json")
			case "credential_in_tree":
				diagnosticWrite(t, filepath.Join(f.source, "assets/runtime.env"), []byte("PRIVATE_CREDENTIAL"))
			case "mountinfo_missing":
				f.evidence["state"] = "running"
				f.evidence["consistency"].(map[string]any)["kind"] = "filesystem_snapshot"
				f.save(t)
			case "writable_export_missing":
				f.evidence["diff"] = []any{map[string]any{"change": "M", "path": "/app/storage/assets/PRIVATE_FILENAME.png"}}
				f.bindings["scopes"] = append(f.bindings["scopes"].([]any), map[string]any{"id": "PRIVATE_LAYER", "kind": "writable_layer", "container_path": "/app/storage", "origin": "/PRIVATE_UPPER", "source_root": f.source, "snapshot_path": "."})
				f.save(t)
			}
			var out, stderr bytes.Buffer
			args := append(f.args(report), "--protected-root", protected)
			if Main(args, &out, &stderr) == 0 {
				t.Fatal("unsafe input or output accepted")
			}
			body, _ := os.ReadFile(report)
			if bytes.Contains(append(append(body, out.Bytes()...), stderr.Bytes()...), []byte("PRIVATE_")) {
				t.Fatal("failure leaked")
			}
			if !reflect.DeepEqual(before, diagnosticTree(t, protected)) {
				t.Fatal("proof modified protected target")
			}
		})
	}
}
func TestVerifySnapshotWritableBytesMustComeFromTheWritableScope(t *testing.T) {
	for _, capture := range []string{"empty", "nested_valid"} {
		t.Run(capture, func(t *testing.T) {
			f := newProofFixture(t)
			const changed = "/app/storage/assets/PRIVATE_FILENAME.png"
			f.evidence["diff"] = []any{map[string]any{"change": "M", "path": changed}}
			f.evidence["export_paths"] = []any{"/app/storage", changed}
			writableSource := t.TempDir()
			containerPath, snapshotPath := "/app/storage", "."
			if capture == "nested_valid" {
				containerPath, snapshotPath = "/app/storage/assets", "assets"
				body, err := os.ReadFile(filepath.Join(f.source, "assets/PRIVATE_FILENAME.png"))
				if err != nil {
					t.Fatal(err)
				}
				diagnosticWrite(t, filepath.Join(writableSource, "PRIVATE_FILENAME.png"), body)
			}
			f.bindings["scopes"] = append(f.bindings["scopes"].([]any), map[string]any{"id": "PRIVATE_WRITABLE_SCOPE", "kind": "writable_layer", "container_path": containerPath, "origin": "/PRIVATE_UPPER", "source_root": writableSource, "snapshot_path": snapshotPath})
			f.save(t)
			code, report, output := proofRunDocument(t, f)
			if capture == "nested_valid" {
				if code != 0 {
					t.Fatalf("real nested writable bytes rejected: %s", output)
				}
				return
			}
			if code == 0 {
				t.Fatalf("mount bytes impersonated an empty writable capture: %s", output)
			}
			if report["decision"] != "STOP" {
				t.Fatalf("missing STOP: %s", output)
			}
			for _, collection := range report["collection_evidence"].([]any) {
				item := collection.(map[string]any)
				if item["id"] == "D3" && item["status"] == "unknown" && item["reason"] == "writable_bytes_not_captured" {
					return
				}
			}
			t.Fatalf("wrong writable capture refusal: %s", output)
		})
	}
}

func TestVerifySnapshotRejectsEvidencePermissionsChangingAfterRead(t *testing.T) {
	for _, name := range []string{"evidence", "bindings", "inventory", "manifest"} {
		t.Run(name, func(t *testing.T) {
			f := newProofFixture(t)
			path := map[string]string{"evidence": f.evidencePath, "bindings": f.bindingsPath, "inventory": f.inventoryPath, "manifest": f.manifestPath}[name]
			_, observed, err := readProofFile(path, true)
			if err != nil {
				t.Fatal(err)
			}
			check := proofCheck{files: map[string]proofStableFile{path: observed}}
			if !check.unchanged() {
				t.Fatal("unmodified private evidence rejected")
			}
			if err := os.Chmod(path, 0644); err != nil {
				t.Fatal(err)
			}
			if check.unchanged() {
				t.Fatal("evidence made public after initial read was accepted as unchanged")
			}
		})
	}
}

func TestVerifySnapshotStabilityChecksObjectIdentityAndJournals(t *testing.T) {
	for _, change := range []string{"same_bytes_replacement", "same_size_rewrite", "wal", "new_file"} {
		t.Run(change, func(t *testing.T) {
			f := newProofFixture(t)
			observed := map[string]os.FileInfo{}
			tree, err := scanProofObservedTree(f.source, observed)
			if err != nil {
				t.Fatal(err)
			}
			info, digest, err := diagnosticSnapshotIdentity(f.legacy.DatabasePath())
			if err != nil {
				t.Fatal(err)
			}
			check := proofCheck{config: proofConfig{database: f.legacy.DatabasePath()}, files: map[string]proofStableFile{f.legacy.DatabasePath(): {info: info, digest: digest}}, sourceTrees: map[string]proofTree{f.source: tree}, treeIdentities: map[string]map[string]os.FileInfo{f.source: observed}}
			switch change {
			case "same_bytes_replacement":
				path := filepath.Join(f.source, "assets/PRIVATE_FILENAME.png")
				body, _ := os.ReadFile(path)
				replacement := filepath.Join(f.source, "replacement")
				diagnosticWrite(t, replacement, body)
				if err := os.Rename(replacement, path); err != nil {
					t.Fatal(err)
				}
			case "same_size_rewrite":
				path := filepath.Join(f.source, "assets/PRIVATE_FILENAME.png")
				body, _ := os.ReadFile(path)
				body[0] ^= 1
				diagnosticWrite(t, path, body)
			case "wal":
				diagnosticWrite(t, f.legacy.DatabasePath()+"-wal", []byte("PRIVATE_WAL"))
			case "new_file":
				diagnosticWrite(t, filepath.Join(f.source, "cache/PRIVATE_LATE"), []byte("late"))
			}
			if check.unchanged() {
				t.Fatal("changed input accepted as stable proof")
			}
		})
	}
}

func TestVerifySnapshotRejectsHardlinkedSourceAndSnapshot(t *testing.T) {
	f := newProofFixture(t)
	target := filepath.Join(f.snapshot, "assets/PRIVATE_FILENAME.png")
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(f.source, "assets/PRIVATE_FILENAME.png"), target); err != nil {
		t.Fatal(err)
	}
	code, _, _ := proofRunDocument(t, f)
	if code == 0 {
		t.Fatal("same physical source file masqueraded as an independent snapshot")
	}
}

func TestVerifySnapshotCrossPathHardlinksCannotMasqueradeAsIndependentTrees(t *testing.T) {
	for _, crossed := range []bool{false, true} {
		t.Run(fmt.Sprintf("crossed=%t", crossed), func(t *testing.T) {
			f := newProofFixture(t)
			names := []string{"data/PRIVATE_A.bin", "data/PRIVATE_B.bin"}
			for _, name := range names {
				diagnosticWrite(t, filepath.Join(f.source, name), []byte("PRIVATE_IDENTICAL_BYTES"))
				diagnosticWrite(t, filepath.Join(f.snapshot, name), []byte("PRIVATE_IDENTICAL_BYTES"))
			}
			if crossed {
				for index, name := range names {
					target := filepath.Join(f.snapshot, name)
					if err := os.Remove(target); err != nil {
						t.Fatal(err)
					}
					if err := os.Link(filepath.Join(f.source, names[1-index]), target); err != nil {
						t.Fatal(err)
					}
					sourceInfo, err := os.Stat(filepath.Join(f.source, name))
					if err != nil {
						t.Fatal(err)
					}
					snapshotInfo, err := os.Stat(target)
					if err != nil {
						t.Fatal(err)
					}
					if os.SameFile(sourceInfo, snapshotInfo) {
						t.Fatal("fixture must bypass only same-path inode checks")
					}
				}
			}
			code, report, output := proofRunDocument(t, f)
			if !crossed {
				if code != 0 {
					t.Fatalf("independent same-byte files rejected: %s", output)
				}
				return
			}
			if code == 0 {
				t.Fatalf("cross-path source hardlinks accepted: %s", output)
			}
			for _, criterion := range report["criteria"].([]any) {
				item := criterion.(map[string]any)
				if item["id"] == "C2" && item["status"] == "refuted" && item["reason"] == "source_snapshot_not_independent" {
					return
				}
			}
			t.Fatalf("wrong independence refusal: %s", output)
		})
	}
}

func TestVerifySnapshotCrossScopeHardlinksCannotMasqueradeAsIndependentTrees(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprintf("shared=%t", shared), func(t *testing.T) {
			f := newProofFixture(t)
			body, err := os.ReadFile(filepath.Join(f.source, "assets/PRIVATE_FILENAME.png"))
			if err != nil {
				t.Fatal(err)
			}
			// Scope A's data file and scope B's asset have equal bytes but distinct names.
			dataName := "data/PRIVATE_SCOPE_A.bin"
			diagnosticWrite(t, filepath.Join(f.source, dataName), body)
			diagnosticWrite(t, filepath.Join(f.snapshot, dataName), body)
			secondSource := t.TempDir()
			diagnosticWrite(t, filepath.Join(secondSource, "PRIVATE_FILENAME.png"), body)
			f.bindings["scopes"] = append(f.bindings["scopes"].([]any), map[string]any{"id": "PRIVATE_SCOPE_B", "kind": "mount", "container_path": "/app/storage/assets", "origin": "/PRIVATE_ASSET_ORIGIN", "source_root": secondSource, "snapshot_path": "assets"})
			f.evidence["mounts"] = append(f.evidence["mounts"].([]any), map[string]any{"type": "bind", "source": "/PRIVATE_ASSET_ORIGIN", "destination": "/app/storage/assets"})
			f.evidence["binds"] = append(f.evidence["binds"].([]any), map[string]any{"source": "/PRIVATE_ASSET_ORIGIN", "destination": "/app/storage/assets"})
			f.evidence["volumes"] = append(f.evidence["volumes"].([]any), "/app/storage/assets")
			inventory, err := os.ReadFile(f.inventoryPath)
			if err != nil {
				t.Fatal(err)
			}
			diagnosticWrite(t, f.inventoryPath, append(inventory, []byte("legacy_mount=bind|/PRIVATE_ASSET_ORIGIN|/app/storage/assets|true\n")...))
			f.evidence["inventory_sha256"] = proofFileHash(t, f.inventoryPath)
			f.save(t)
			if shared {
				target := filepath.Join(f.snapshot, "assets/PRIVATE_FILENAME.png")
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(filepath.Join(f.source, dataName), target); err != nil {
					t.Fatal(err)
				}
				// Neither source scope's corresponding asset shares this inode.
				snapshotInfo, err := os.Stat(target)
				if err != nil {
					t.Fatal(err)
				}
				for _, matching := range []string{filepath.Join(f.source, "assets/PRIVATE_FILENAME.png"), filepath.Join(secondSource, "PRIVATE_FILENAME.png")} {
					info, err := os.Stat(matching)
					if err != nil {
						t.Fatal(err)
					}
					if os.SameFile(info, snapshotInfo) {
						t.Fatal("fixture must bypass all same-namespace scope inode checks")
					}
				}
			}
			code, report, output := proofRunDocument(t, f)
			if !shared {
				if code != 0 {
					t.Fatalf("independent nested source scope rejected: %s", output)
				}
				return
			}
			if code == 0 {
				t.Fatalf("cross-scope source hardlink accepted: %s", output)
			}
			for _, criterion := range report["criteria"].([]any) {
				item := criterion.(map[string]any)
				if item["id"] == "C2" && item["status"] == "refuted" && item["reason"] == "source_snapshot_not_independent" {
					return
				}
			}
			t.Fatalf("wrong cross-scope independence refusal: %s", output)
		})
	}
}

func TestVerifySnapshotNeverContactsRemoteReferences(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.Write([]byte("PRIVATE_REMOTE")) }))
	defer server.Close()
	f := newProofFixture(t)
	diagnosticTask(t, f.legacy, "PRIVATE_NETWORK_TASK", server.URL+"/PRIVATE_TOKEN.png?key=PRIVATE_SECRET")
	f.refreshDatabase(t)
	if code, _, raw := proofRunDocument(t, f); code != 0 {
		t.Fatalf("remote offline bucket failed: %s", raw)
	}
	if calls.Load() != 0 {
		t.Fatal("snapshot proof contacted remote origin")
	}
}
func TestVerifySnapshotDiagnosticDigestMatchesIndependentV1Report(t *testing.T) {
	f := newProofFixture(t)
	code, doc, raw := proofRunDocument(t, f)
	if code != 0 {
		t.Fatalf("proof failed %s", raw)
	}
	path := filepath.Join(f.out, "diagnostic.json")
	var out, stderr bytes.Buffer
	args := append(diagnosticArgs(f.legacy, path), "--snapshot-manifest", f.manifestPath, "--generated-at-utc", "2026-10-15T01:00:00Z")
	if Main(args, &out, &stderr) != 0 {
		t.Fatalf("existing v1 failed %s", stderr.String())
	}
	expected := "sha256:" + proofFileHash(t, path)
	if doc["evidence"].(map[string]any)["diagnostic"] != expected {
		t.Fatal("proof diagnostic evidence does not bind the exact independently emitted v1 bytes")
	}
}
func TestVerifySnapshotReportsDiagnosticBudgetAsUnknown(t *testing.T) {
	f := newProofFixture(t)
	f.legacy.Exec(t, `INSERT INTO task_cache(taskId,taskType,status,data,updatedAt) VALUES ('PRIVATE_TASK','text-to-image','FAILED',?,CURRENT_TIMESTAMP)`, strings.Repeat(" ", 4<<20)+"{}")
	f.refreshDatabase(t)
	code, doc, raw := proofRunDocument(t, f)
	if code == 0 {
		t.Fatalf("oversized metadata accepted %s", raw)
	}
	found := false
	for _, v := range doc["criteria"].([]any) {
		criterion := v.(map[string]any)
		if criterion["reason"] == "diagnostic_budget_exceeded" && criterion["status"] == "unknown" {
			found = true
		}
	}
	if !found {
		t.Fatalf("budget failure not explicit: %s", raw)
	}
}

func TestVerifySnapshotRejectsInventoryForAnotherCapture(t *testing.T) {
	for _, pair := range [][2]string{{"hostname=PRIVATE_HOST", "hostname=PRIVATE_OTHER"}, {"database_snapshot_sha256=", "database_snapshot_sha256=00"}, {"legacy_container_running=false", "legacy_container_running=true"}, {"collected_at_utc=2026-10-15T00:00:00Z", "collected_at_utc=2026-10-16T00:00:00Z"}} {
		t.Run(strings.Split(pair[0], "=")[0], func(t *testing.T) {
			f := newProofFixture(t)
			raw, _ := os.ReadFile(f.inventoryPath)
			raw = bytes.Replace(raw, []byte(pair[0]), []byte(pair[1]), 1)
			diagnosticWrite(t, f.inventoryPath, raw)
			f.evidence["inventory_sha256"] = proofFileHash(t, f.inventoryPath)
			f.save(t)
			if code, _, _ := proofRunDocument(t, f); code == 0 {
				t.Fatal("inventory from contradictory capture accepted despite valid file hash")
			}
		})
	}
}
func TestDiagnosticBuilderPreservesOriginalSnapshotObservation(t *testing.T) {
	f := newProofFixture(t)
	info, digest, err := diagnosticSnapshotIdentity(f.legacy.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	f.legacy.Exec(t, `INSERT INTO audit_logs(id,action,userId) VALUES ('PRIVATE_NEW','read','PRIVATE_OWNER')`)
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	_, err = buildDiagnosticReportAt(f.legacy.DatabasePath(), f.snapshot, f.manifestPath, product, time.Date(2026, 10, 15, 1, 0, 0, 0, time.UTC), diagnosticObserved{info: info, digest: digest})
	if err == nil || err.Error() != "snapshot_changed" {
		t.Fatalf("snapshot observation silently refreshed across Catalog load: %v", err)
	}
}

func TestVerifySnapshotReportsEachCollectionAndStableSnapshotIdentity(t *testing.T) {
	f := newProofFixture(t)
	code, first, raw := proofRunDocument(t, f)
	if code != 0 {
		t.Fatalf("proof failed %s", raw)
	}
	collections, ok := first["collection_evidence"].([]any)
	if !ok || len(collections) != 4 {
		t.Fatal("D1-D4 collection status is not explicit")
	}
	for _, item := range collections {
		if item.(map[string]any)["status"] != "supported" {
			t.Fatal("complete collection marked unknown")
		}
	}
	args := f.args(filepath.Join(f.out, "later.json"))
	args[len(args)-1] = "2026-10-15T02:00:00Z"
	var out, stderr bytes.Buffer
	if Main(args, &out, &stderr) != 0 {
		t.Fatalf("later report failed %s", stderr.String())
	}
	body, _ := os.ReadFile(filepath.Join(f.out, "later.json"))
	var later map[string]any
	if json.Unmarshal(body, &later) != nil {
		t.Fatal("invalid later report")
	}
	if first["snapshot_identity"] != later["snapshot_identity"] {
		t.Fatal("report timestamp changed the identity of unchanged actual snapshot/evidence bytes")
	}
}

func TestVerifySnapshotInputBucketsPreserveTraversalBeforeNormalization(t *testing.T) {
	f := newProofFixture(t)
	body, _ := os.ReadFile(filepath.Join(f.snapshot, "assets/PRIVATE_FILENAME.png"))
	digest := diagnosticHash(body)
	for i := 0; i < 21; i++ {
		path := "PRIVATE_FILENAME.png"
		if i == 20 {
			path = "PRIVATE_PREFIX/../PRIVATE_FILENAME.png"
		}
		f.legacy.Exec(t, `INSERT INTO asset_registry(id,assetType,originalFilename,storedFilename,mimeType,fileSize,sourceSha256,storedSha256,storagePath,uploaderUserId) VALUES (?,'image','PRIVATE_NAME','PRIVATE_NAME','image/png',?,'',?,?,'PRIVATE_OWNER')`, fmt.Sprintf("PRIVATE_ID_%d", i), len(body), digest, path)
	}
	f.refreshDatabase(t)
	code, doc, raw := proofRunDocument(t, f)
	if code != 0 {
		t.Fatalf("existing safe SHA recovery refused %s", raw)
	}
	found := false
	for _, item := range doc["reference_buckets"].([]any) {
		b := item.(map[string]any)
		if b["domain"] == "input_asset" && b["kind"] == "traversal" && b["count"] == float64(1) {
			found = true
		}
	}
	if !found {
		t.Fatal("dot-dot segment disappeared from raw reference classification")
	}
}
