package etl

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/etl/etltest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
)

func TestCompletenessReadGapCannotBeHidden(t *testing.T) {
	for _, test := range []struct {
		name                         string
		source, loaded, skipped, gap int
	}{
		{"under_read_with_processed_skip", 2, 1, 1, 1},
		{"over_read", 1, 2, 0, -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifest := buildCompletenessManifest("", taskDomainReport{}, chatMigrationReport{messageLoaded: test.loaded, messageSkipped: make([]string, test.skipped)}, canvasMigrationReport{}, balanceMigrationReport{}, auditMigrationReport{}, inputAssetMigrationReport{}, taskOutputMigrationReport{}, sqlite.ETLReconciliation{}, casVerificationResult{}, legacyBaseline{counts: map[string]int{"chat_messages": test.source}}, nil, 0, 0)
			for _, domain := range manifest.Domains {
				if domain.Domain != "chat_messages" {
					continue
				}
				if domain.ReadGap != test.gap || domain.UnexplainedGap != test.gap || domain.Status != "fail" {
					t.Fatalf("read discrepancy must remain signed and fail regardless of processed skips: %+v", domain)
				}
			}
			if completenessManifestFailures(manifest) == 0 {
				t.Fatal("machine gate accepted inconsistent source/loaded rows")
			}
		})
	}
}

// queryMutation models a defective loader query without altering the independent
// COUNT/SUM queries or adding test hooks to the migration runtime.
type queryMutation struct {
	legacyQuery
	mutate func(string) string
}

func (q queryMutation) Query(query string, args ...any) (*sql.Rows, error) {
	return q.legacyQuery.Query(q.mutate(query), args...)
}
func (q queryMutation) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return q.legacyQuery.QueryContext(ctx, q.mutate(query), args...)
}

func openCompletenessSource(t *testing.T, fixture *etltest.Fixture) *sql.Tx {
	t.Helper()
	db, err := sql.Open("sqlite", readOnlyDSN(fixture.DatabasePath()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tx, err := db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func seedCompletenessRows(t *testing.T, f *etltest.Fixture) {
	t.Helper()
	for _, id := range []string{"a", "b"} {
		f.Exec(t, `INSERT INTO users(id,username,updatedAt) VALUES(?,?,CURRENT_TIMESTAMP)`, id, id)
		f.Exec(t, `INSERT INTO usage_ledger(id,userId,taskType,taskId,providerId,estimatedCredits,taskStatus) VALUES(?, 'owner','text-to-image',?,'gpt-image',7,'FAILED')`, id, "task-"+id)
		f.Exec(t, `INSERT INTO task_cache(taskId,taskType,status,data,updatedAt) VALUES(?,'text-to-image','FAILED','{}',CURRENT_TIMESTAMP)`, "task-"+id)
		f.Exec(t, `INSERT INTO task_input_snapshot(taskId,capability,providerId) VALUES(?,'text-to-image','gpt-image')`, "task-"+id)
		f.Exec(t, `INSERT INTO asset_registry(id,assetType,originalFilename,storedFilename,mimeType,fileSize,sourceSha256,storedSha256,storagePath,uploaderUserId) VALUES(?,'image','missing.png','missing.png','image/png',1,'','','image/missing.png','owner')`, id)
		f.Exec(t, `INSERT INTO chat_sessions(id,userId,updatedAt) VALUES(?,'owner',CURRENT_TIMESTAMP)`, id)
		f.Exec(t, `INSERT INTO chat_messages(id,sessionId,role) VALUES(?,?,'user')`, id, id)
		f.Exec(t, `INSERT INTO canvas_projects(id,userId,updatedAt) VALUES(?,'owner',CURRENT_TIMESTAMP)`, id)
		f.Exec(t, `INSERT INTO canvas_nodes(id,projectId,type,x,y,width,height,zIndex,data,updatedAt) VALUES(?,?,'rectangle',0,0,1,1,0,'{}',CURRENT_TIMESTAMP)`, id, id)
		f.Exec(t, `INSERT INTO balance_snapshots(id,balance) VALUES(?,1)`, id)
		f.Exec(t, `INSERT INTO audit_logs(id,action,userId) VALUES(?,'test','owner')`, id)
	}
}

func TestCompletenessDetectsLoaderReadDiscrepancies(t *testing.T) {
	for _, mode := range []string{"under_read", "over_read"} {
		t.Run(mode, func(t *testing.T) {
			for _, domain := range legacyTables {
				t.Run(domain, func(t *testing.T) {
					fixture := etltest.NewFixture(t)
					seedCompletenessRows(t, fixture)
					source := openCompletenessSource(t, fixture)
					baseline, err := readLegacyBaseline(source)
					if err != nil {
						t.Fatal(err)
					}
					faulty := queryMutation{legacyQuery: source, mutate: func(query string) string {
						if strings.Contains(query, "FROM "+domain) && !strings.Contains(query, "COUNT(") {
							if mode == "under_read" {
								return query + " LIMIT 1"
							}
							return "SELECT * FROM (" + query + ") UNION ALL SELECT * FROM (" + query + ")"
						}
						return query
					}}
					var task taskDomainReport
					var chat chatMigrationReport
					var canvas canvasMigrationReport
					var balance balanceMigrationReport
					var audit auditMigrationReport
					var input inputAssetMigrationReport
					users := 0
					switch domain {
					case "users":
						rows, e := loadLegacyUsers(faulty)
						users = len(rows)
						err = e
					case "usage_ledger", "task_cache", "task_input_snapshot":
						_, _, task, err = loadLegacyTasks(faulty)
					case "chat_sessions", "chat_messages":
						_, _, chat, err = loadLegacyChat(t.Context(), faulty)
					case "canvas_projects", "canvas_nodes":
						_, _, canvas, err = loadLegacyCanvas(t.Context(), faulty)
					case "balance_snapshots":
						_, balance, err = loadLegacyBalanceSnapshots(faulty)
					case "audit_logs":
						_, audit, err = loadLegacyAuditEvents(t.Context(), faulty, filepath.Join(fixture.Root, "storage/audit"), t.TempDir())
					case "asset_registry":
						blobs, e := local.New(t.TempDir())
						if e != nil {
							t.Fatal(e)
						}
						_, input, err = loadLegacyInputAssets(t.Context(), faulty, filepath.Join(fixture.Root, "storage"), blobs)
					default:
						t.Fatalf("uncovered domain %s", domain)
					}
					if err != nil {
						t.Fatal(err)
					}
					manifest := buildCompletenessManifest("", task, chat, canvas, balance, audit, input, taskOutputMigrationReport{}, sqlite.ETLReconciliation{}, casVerificationResult{}, baseline, nil, users, 0)
					found := false
					for _, entry := range manifest.Domains {
						if entry.Domain == domain {
							found = true
							wantLoaded, wantGap := 1, 1
							if mode == "over_read" {
								wantLoaded, wantGap = 4, -2
							}
							if entry.SourceCount != 2 || entry.LoadedCount != wantLoaded || entry.ReadGap != wantGap || entry.Status != "fail" {
								t.Fatalf("loader discrepancy hidden: %+v", entry)
							}
						}
					}
					if !found || completenessManifestFailures(manifest) == 0 {
						t.Fatal("under-read passed machine gate")
					}
				})
			}
		})
	}
}

func TestCompletenessRejectsSelfConsistentTaskAndCreditCorruption(t *testing.T) {
	for _, kind := range []string{"wrong_group", "wrong_credits"} {
		t.Run(kind, func(t *testing.T) {
			f := etltest.NewFixture(t)
			seedCompletenessRows(t, f)
			f.Exec(t, `DELETE FROM task_cache`)
			f.Exec(t, `DELETE FROM task_input_snapshot`)
			source := openCompletenessSource(t, f)
			baseline, err := readLegacyBaseline(source)
			if err != nil {
				t.Fatal(err)
			}
			faulty := queryMutation{legacyQuery: source, mutate: func(query string) string {
				if !strings.Contains(query, "FROM usage_ledger ORDER BY") {
					return query
				}
				if kind == "wrong_group" {
					return strings.Replace(query, "id, taskId,", "id, 'task-a' AS taskId,", 1)
				}
				return strings.Replace(query, "providerId, estimatedCredits,", "providerId, estimatedCredits + 1,", 1)
			}}
			tasks, _, report, err := loadLegacyTasks(faulty)
			if err != nil {
				t.Fatal(err)
			}
			targetPath := filepath.Join(t.TempDir(), "target.db")
			target, err := sqlite.Open(targetPath)
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			if _, err := target.ImportGenerationTasksAndAssets(t.Context(), tasks, nil); err != nil {
				t.Fatal(err)
			}
			actual, err := target.ReconcileETL(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if actual.Attempts != 2 || actual.UsageLedger != 2 || actual.Credits != report.credits {
				t.Fatalf("negative fixture must conserve loaded/target rows and credits: %+v report=%+v", actual, report)
			}
			if kind == "wrong_group" && actual.Tasks != 1 {
				t.Fatalf("wrong merge did not occur: %+v", actual)
			}
			manifest := buildCompletenessManifest("", report, chatMigrationReport{}, canvasMigrationReport{}, balanceMigrationReport{}, auditMigrationReport{}, inputAssetMigrationReport{}, taskOutputMigrationReport{}, actual, casVerificationResult{}, legacyBaseline{counts: map[string]int{"usage_ledger": 2}, tasks: baseline.tasks, credits: baseline.credits}, nil, 0, 0)
			if kind == "wrong_credits" && manifest.Credits.Equal {
				t.Fatalf("loaded/target wrong credit values passed independent SUM: %+v", manifest.Credits)
			}
			if completenessManifestFailures(manifest) == 0 {
				t.Fatal("self-consistent corruption passed machine gate")
			}
		})
	}
}

func TestCompletenessSourceBaselineAndLoadShareSnapshot(t *testing.T) {
	f := etltest.NewFixture(t)
	f.Exec(t, `PRAGMA journal_mode=WAL`)
	f.Exec(t, `INSERT INTO users(id,username,updatedAt) VALUES('before','before',CURRENT_TIMESTAMP)`)
	source := openCompletenessSource(t, f)
	baseline, err := readLegacyBaseline(source)
	if err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `INSERT INTO users(id,username,updatedAt) VALUES('after','after',CURRENT_TIMESTAMP)`)
	users, err := loadLegacyUsers(source)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.counts["users"] != 1 || len(users) != 1 || users[0].Record.ID != "before" {
		t.Fatalf("source COUNT and loader observed different snapshots: baseline=%+v users=%+v", baseline, users)
	}
}

func TestCompletenessAuxiliaryTargetsRequireExactPersistedIdentity(t *testing.T) {
	f := etltest.NewFixture(t)
	seedCompletenessRows(t, f)
	f.Exec(t, `DELETE FROM asset_registry`)
	f.Exec(t, `INSERT INTO task_cache(taskId,taskType,status,data,updatedAt) VALUES('orphan','text-to-image','FAILED','{}',CURRENT_TIMESTAMP)`)
	f.Exec(t, `INSERT INTO task_input_snapshot(taskId,capability,providerId) VALUES('orphan','text-to-image','gpt-image')`)
	targetPath := filepath.Join(t.TempDir(), "target.db")
	storage := t.TempDir()
	for i := 0; i < 2; i++ {
		result, err := migrateWithContext(t.Context(), resolveLegacyLayout(f.Root), targetPath, storage)
		if err != nil {
			t.Fatal(err)
		}
		if result.failureCount != 0 {
			t.Fatalf("merged/orphan auxiliary data and idempotent rerun must pass: %s", result.report)
		}
		for _, domain := range result.manifest.Domains {
			if domain.Domain == "task_cache" || domain.Domain == "task_input_snapshot" {
				if domain.TargetCount != 3 || domain.ImportedCount != 3 || domain.ArchivedCount != 1 || domain.Status != "pass" {
					t.Fatalf("persisted auxiliary coverage inaccurate: %+v", domain)
				}
			}
		}
	}
	source := openCompletenessSource(t, f)
	db, err := sql.Open("sqlite", targetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Keep total archive and marker counts constant: global COUNT alone cannot
	// distinguish a deleted source archive from an unrelated replacement.
	for _, table := range []string{"task_cache", "task_input_snapshot"} {
		if _, err := db.Exec(`UPDATE legacy_migration_records SET source_id='unrelated' WHERE source_table=? AND source_id='orphan'`, table); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE etl_import_items SET source_id=? WHERE domain='legacy_migration_record' AND source_id=?`, table+"/unrelated", table+"/orphan"); err != nil {
			t.Fatal(err)
		}
	}
	counts, err := countTaskAuxiliaryTargets(t.Context(), source, targetPath)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := readLegacyBaseline(source)
	if err != nil {
		t.Fatal(err)
	}
	_, _, task, err := loadLegacyTasks(source)
	if err != nil {
		t.Fatal(err)
	}
	manifest := buildCompletenessManifest("", task, chatMigrationReport{}, canvasMigrationReport{}, balanceMigrationReport{}, auditMigrationReport{}, inputAssetMigrationReport{}, taskOutputMigrationReport{}, sqlite.ETLReconciliation{Tasks: 2, UsageLedger: 2, Credits: 14}, casVerificationResult{}, baseline, counts, 0, 0)
	for _, domain := range manifest.Domains {
		if domain.Domain == "task_cache" || domain.Domain == "task_input_snapshot" {
			if domain.TargetCount != 2 || domain.Status != "fail" {
				t.Fatalf("unrelated archive compensated for missing source identity: %+v", domain)
			}
		}
	}
	if completenessManifestFailures(manifest) == 0 {
		t.Fatal("missing auxiliary archives passed machine gate")
	}
}

func TestCompletenessDistinctTasksPreserveMultipleLedgerRows(t *testing.T) {
	f := etltest.NewFixture(t)
	seedCompletenessRows(t, f)
	f.Exec(t, `DELETE FROM task_cache`)
	f.Exec(t, `DELETE FROM task_input_snapshot`)
	f.Exec(t, `UPDATE usage_ledger SET taskId='same-task'`)
	source := openCompletenessSource(t, f)
	baseline, err := readLegacyBaseline(source)
	if err != nil {
		t.Fatal(err)
	}
	tasks, _, report, err := loadLegacyTasks(source)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.tasks != 1 || len(tasks) != 1 || len(tasks[0].Attempts) != 2 || report.credits != baseline.credits {
		t.Fatalf("legitimate repeated task ledger rejected: baseline=%+v report=%+v", baseline, report)
	}
}

func TestCompletenessRejectsEmptyTaskIdentity(t *testing.T) {
	for _, value := range []any{"", "  ", nil} {
		t.Run(fmt.Sprint(value), func(t *testing.T) {
			f := etltest.NewFixture(t)
			// A nullable legacy table models malformed input independently of the
			// fixture's usual NOT NULL constraint; COUNT DISTINCT would hide NULL.
			f.Exec(t, `ALTER TABLE usage_ledger RENAME TO original_ledger`)
			f.Exec(t, `CREATE TABLE usage_ledger(taskId TEXT, estimatedCredits INTEGER)`)
			f.Exec(t, `INSERT INTO usage_ledger(taskId,estimatedCredits) VALUES(?,7)`, value)
			if _, err := readLegacyBaseline(openCompletenessSource(t, f)); err == nil {
				t.Fatal("empty source identity accepted by distinct task baseline")
			}
		})
	}
}

func TestCompletenessRunnerRejectsInconsistentCounts(t *testing.T) {
	gate := completenessRunnerGate(t)
	for _, test := range []struct {
		name, domain, credits string
		pass                  bool
	}{
		{"valid", `{"status":"pass","skipped_count":0,"read_gap":0,"unexplained_gap":0,"source_count":1,"loaded_count":1,"imported_count":1,"target_count":1}`, `{"source_sum":7,"loaded_sum":7,"target_sum":7,"equal":true}`, true},
		{"hidden_read_gap", `{"status":"pass","skipped_count":0,"read_gap":1,"unexplained_gap":0,"source_count":2,"loaded_count":1,"imported_count":1,"target_count":1}`, `{"source_sum":7,"loaded_sum":7,"target_sum":7,"equal":true}`, false},
		{"over_read_zeroed_gap", `{"status":"pass","skipped_count":0,"read_gap":0,"unexplained_gap":0,"source_count":1,"loaded_count":2,"imported_count":1,"target_count":1}`, `{"source_sum":7,"loaded_sum":7,"target_sum":7,"equal":true}`, false},
		{"missing_target", `{"status":"pass","skipped_count":0,"read_gap":0,"unexplained_gap":0,"source_count":1,"loaded_count":1,"imported_count":1,"target_count":0}`, `{"source_sum":7,"loaded_sum":7,"target_sum":7,"equal":true}`, false},
		{"wrong_credits", `{"status":"pass","skipped_count":0,"read_gap":0,"unexplained_gap":0,"source_count":1,"loaded_count":1,"imported_count":1,"target_count":1}`, `{"source_sum":7,"loaded_sum":8,"target_sum":8,"equal":true}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := completenessRunnerDocument()
			var domain, credits map[string]any
			if err := json.Unmarshal([]byte(test.domain), &domain); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(test.credits), &credits); err != nil {
				t.Fatal(err)
			}
			domain["domain"] = "users"
			document["domains"].([]any)[0] = domain
			document["credits"] = credits
			if pass := gate(document); pass != test.pass {
				t.Fatalf("runner pass=%v want %v", pass, test.pass)
			}
		})
	}
}

func TestCompletenessMissingTaskChainRowsFailMigration(t *testing.T) {
	for _, test := range []struct{ table, id string }{
		{"generation_tasks", "task-a"},
		{"generation_attempts", "task-a-attempt-1"},
		{"generation_usage_ledger", "a"},
	} {
		t.Run(test.table, func(t *testing.T) {
			f := etltest.NewFixture(t)
			seedCompletenessRows(t, f)
			f.Exec(t, `DELETE FROM asset_registry`)
			targetPath := filepath.Join(t.TempDir(), "target.db")
			removed := false
			ctx := sqlite.WithETLBatchObserver(t.Context(), func(domain string, committed int) error {
				if domain != "generation_task" {
					return nil
				}
				db, err := sql.Open("sqlite", targetPath)
				if err != nil {
					return err
				}
				defer db.Close()
				result, err := db.Exec(`DELETE FROM `+test.table+` WHERE id=?`, test.id)
				if err != nil {
					return err
				}
				affected, err := result.RowsAffected()
				if err != nil {
					return err
				}
				removed = affected == 1
				return nil
			})
			result, err := migrateWithContext(ctx, resolveLegacyLayout(f.Root), targetPath, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if !removed || result.failureCount == 0 || result.manifest.Overall.Conclusion != "fail" {
				t.Fatalf("missing persisted task chain accepted: removed=%v result=%+v", removed, result)
			}
		})
	}
}

func TestCompletenessRunnerRejectsMalformedNumericFields(t *testing.T) {
	gate := completenessRunnerGate(t)
	for _, invalid := range []any{-1, 1.5, "1", nil, true} {
		for _, field := range []string{"source_count", "loaded_count", "imported_count", "target_count", "skipped_count", "archived_count", "source_sum", "loaded_sum", "target_sum"} {
			t.Run(fmt.Sprintf("%s/%v", field, invalid), func(t *testing.T) {
				domain := map[string]any{"status": "pass", "read_gap": 0, "unexplained_gap": 0, "source_count": 1, "loaded_count": 1, "imported_count": 1, "target_count": 1, "skipped_count": 0}
				credits := map[string]any{"equal": true, "source_sum": 7, "loaded_sum": 7, "target_sum": 7}
				if strings.HasSuffix(field, "_sum") {
					credits[field] = invalid
					if field == "source_sum" {
						credits["loaded_sum"] = invalid
						credits["target_sum"] = invalid
					}
				} else {
					domain[field] = invalid
					if field == "source_count" {
						domain["loaded_count"] = invalid
					}
					if field == "imported_count" {
						domain["target_count"] = invalid
					}
				}
				document := completenessRunnerDocument()
				domain["domain"] = "users"
				document["domains"].([]any)[0] = domain
				document["credits"] = credits
				if gate(document) {
					t.Fatal("invalid numeric field accepted")
				}

			})
		}
	}
}

func completenessRunnerDocument() map[string]any {
	domains := []any{}
	for _, name := range append(append([]string(nil), legacyTables...), "generation_tasks", "task_outputs") {
		domains = append(domains, map[string]any{"domain": name, "status": "pass", "read_gap": 0, "unexplained_gap": 0, "source_count": 0, "loaded_count": 0, "imported_count": 0, "target_count": 0, "skipped_count": 0})
	}
	return map[string]any{"format": completenessManifestFormat, "overall": map[string]any{"conclusion": "pass", "failure_count": 0}, "domains": domains, "credits": map[string]any{"equal": true, "source_sum": 0, "loaded_sum": 0, "target_sum": 0}, "source_loss": map[string]any{"unacknowledged": 0}, "cas": map[string]any{"sha256_mismatch": 0}}
}

func completenessRunnerGate(t *testing.T) func(any) bool {
	t.Helper()
	jq, err := exec.LookPath("jq")
	if err != nil {
		t.Skip("jq is required for the container runner gate")
	}
	filterBytes, err := os.ReadFile(filepath.Join("..", "..", "scripts", "etl-completeness-filter.jq"))
	if err != nil {
		t.Fatal(err)
	}
	filter := string(filterBytes)
	return func(document any) bool {
		t.Helper()
		body, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(jq, "-e", filter)
		cmd.Stdin = bytes.NewReader(body)
		_, err = cmd.CombinedOutput()
		return err == nil
	}
}

func TestCompletenessRunnerRequiresCompleteDomainDispositions(t *testing.T) {
	gate := completenessRunnerGate(t)
	for _, test := range []struct {
		name   string
		pass   bool
		mutate func(map[string]any)
	}{
		{"valid", true, func(map[string]any) {}},
		{"missing_domain", false, func(doc map[string]any) { doc["domains"] = doc["domains"].([]any)[1:] }},
		{"duplicate_domain", false, func(doc map[string]any) { domains := doc["domains"].([]any); domains[len(domains)-1] = domains[0] }},
		{"unknown_domain", false, func(doc map[string]any) { doc["domains"].([]any)[0].(map[string]any)["domain"] = "unknown" }},
		{"missing_native_destination", false, func(doc map[string]any) {
			entry := doc["domains"].([]any)[0].(map[string]any)
			entry["source_count"] = 2
			entry["loaded_count"] = 2
		}},
		{"business_skip", true, func(doc map[string]any) {
			entry := doc["domains"].([]any)[0].(map[string]any)
			entry["source_count"] = 2
			entry["loaded_count"] = 2
			entry["imported_count"] = 1
			entry["target_count"] = 1
			entry["skipped_count"] = 1
		}},
		{"archive_exceeds_imports", false, func(doc map[string]any) {
			for _, entry := range doc["domains"].([]any) {
				domain := entry.(map[string]any)
				if domain["domain"] == "task_cache" {
					domain["archived_count"] = 99
				}
			}
		}},
		{"missing_auxiliary_destination", false, func(doc map[string]any) {
			for _, entry := range doc["domains"].([]any) {
				domain := entry.(map[string]any)
				if domain["domain"] == "task_input_snapshot" {
					domain["source_count"] = 2
					domain["loaded_count"] = 2
					domain["skipped_count"] = 2
				}
			}
		}},
		{"output_problem_details_are_not_rows", true, func(doc map[string]any) {
			for _, entry := range doc["domains"].([]any) {
				domain := entry.(map[string]any)
				if domain["domain"] == "task_outputs" {
					domain["source_count"] = 1
					domain["loaded_count"] = 1
					domain["skipped_count"] = 3
				}
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := completenessRunnerDocument()
			test.mutate(document)
			if pass := gate(document); pass != test.pass {
				t.Fatalf("runner gate pass=%v want %v", pass, test.pass)
			}
			body, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			var manifest completenessManifest
			if err := json.Unmarshal(body, &manifest); err != nil {
				t.Fatal(err)
			}
			if pass := completenessManifestFailures(manifest) == 0; pass != test.pass {
				t.Fatalf("Go gate pass=%v want %v", pass, test.pass)
			}
		})
	}
}
