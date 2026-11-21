package sqlite

// migration_v180_upgrade_test.go（#435）：用 v1.8.0 存量数据库演练当前迁移。
//
// formal-test 保留数据并运行 v1.8.0（7a510796），main 在它之后新增了 0094 起的
// 迁移（最高版本见 MigrationHead）；本测试让这批迁移第一次在有存量数据时执行，
// 而不是只面对空库。
//
// 建库方式：v1.8.0 的迁移台账（版本、文件名、校验和）固化在
// testdata/v1.8.0_schema_migrations.tsv，来源与生成命令见该目录的 README。
// 测试只在当前二进制内嵌的 0001–0093 迁移与台账逐行一致时才用它建库，
// 因此任何对历史迁移的改写都会在这里直接失败，而不是静默换成新内容。
// 建库后写入覆盖各主要业务表的合成数据（不含真实内容），再用当前代码打开
// 同一个数据库升级到 MigrationHead()，并断言：
//   - 台账推进到 MigrationHead()，0001–0093 的行（名称与校验和）没有被改写；
//   - 升级前快照的每一列、每一行在升级后原样保留，只多出新增列；
//   - foreign_key_check 与 integrity_check 通过；
//   - 用户、会话与消息（含 tool_calls）、回合与检查点（done/error/waiting_task/
//     running）、3D 处理计划（processing_plans）、对话里的制作预算计划
//     （production_budgets/budget_reservations，plan_json 钉住模型名）、
//     SkillRun 与步骤、生成任务/Attempt/资产、执行授权与 Agent 配置版本都能
//     经仓库层读回。

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

const (
	// v180HeadVersion is the last migration of v1.8.0 (7a510796): 0093.
	// formal-test still holds a database written by that release.
	v180HeadVersion = 93
	// v180LedgerSnapshot is the v1.8.0 go_schema_migrations content: one
	// "<version>\t<file name>\t<sha256>" line per migration.
	v180LedgerSnapshot = "testdata/v1.8.0_schema_migrations.tsv"
	// v180Stamp is the fractional-second form every v1.8.0 table stores; task
	// schedule columns use the fixed nine-digit form of 0061.
	v180Stamp       = "2024-05-02T10:00:00Z"
	v180TaskStamp   = "2024-05-02T10:00:00.000000000Z"
	v180Digest      = "0000000000000000000000000000000000000000000000000000000000000000"
	v180ShaModel    = "1111111111111111111111111111111111111111111111111111111111111111"
	v180ShaThumb    = "2222222222222222222222222222222222222222222222222222222222222222"
	v180ShaConvert  = "3333333333333333333333333333333333333333333333333333333333333333"
	v180ShaUpload   = "4444444444444444444444444444444444444444444444444444444444444444"
	v180ShaCrop     = "5555555555555555555555555555555555555555555555555555555555555555"
	v180SessionID   = "sess-a"
	v180OwnerID     = "owner-a"
	v180ChatModel   = "chat-model-a"
	v180OfferingID  = "model-3d-a"
	v180OfferingRev = "rev-3d-a"
)

// v180LedgerRow is one frozen go_schema_migrations line of v1.8.0.
type v180LedgerRow struct {
	version  int
	name     string
	checksum string
}

// v180TableSnapshot keeps every column of a table as its rows in text form,
// row order normalized by sorting the whole row, so one snapshot can be
// compared with the same table after the upgrade.
type v180TableSnapshot map[string][]string

func readV180Ledger(t *testing.T) []v180LedgerRow {
	t.Helper()
	raw, err := os.ReadFile(v180LedgerSnapshot)
	if err != nil {
		t.Fatalf("读取 v1.8.0 迁移台账快照: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	rows := make([]v180LedgerRow, 0, len(lines))
	for index, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			t.Fatalf("%s 第 %d 行不是三段: %q", v180LedgerSnapshot, index+1, line)
		}
		version, err := strconv.Atoi(fields[0])
		if err != nil {
			t.Fatalf("%s 第 %d 行版本号: %v", v180LedgerSnapshot, index+1, err)
		}
		rows = append(rows, v180LedgerRow{version: version, name: fields[1], checksum: fields[2]})
	}
	if len(rows) != v180HeadVersion {
		t.Fatalf("v1.8.0 迁移台账 = %d 行; want %d", len(rows), v180HeadVersion)
	}
	for index, row := range rows {
		if row.version != index+1 {
			t.Fatalf("v1.8.0 台账第 %d 行版本 %d，不连续", index+1, row.version)
		}
	}
	return rows
}

// v180Migrations gates the historical fixture on the current binary still
// carrying the exact v1.8.0 SQL: the frozen checksums must match, otherwise a
// database built here would not be the one formal-test holds.
func v180Migrations(t *testing.T) []migration {
	t.Helper()
	ledger := readV180Ledger(t)
	var historical []migration
	for _, migration := range migrations() {
		if migration.version > v180HeadVersion {
			break
		}
		historical = append(historical, migration)
	}
	if len(historical) != len(ledger) {
		t.Fatalf("当前内嵌的 0001–%04d 迁移 = %d 条; v1.8.0 台账 %d 条",
			v180HeadVersion, len(historical), len(ledger))
	}
	for index, want := range ledger {
		got := historical[index]
		name := fmt.Sprintf("%04d_%s.sql", got.version, got.name)
		if got.version != want.version || name != want.name {
			t.Fatalf("migration #%d = %04d_%s (%s); v1.8.0 台账为 %d %s",
				index, got.version, got.name, name, want.version, want.name)
		}
		if checksum := migrationChecksum(got.sql); checksum != want.checksum {
			t.Fatalf("migration %s 内容与 v1.8.0 不一致: checksum %s, want %s；"+
				"历史迁移不能改写，否则真实存量库会校验和不匹配",
				name, checksum, want.checksum)
		}
	}
	return historical
}

// openV180Database builds a database exactly where v1.8.0 left it: the same
// ledger table, the same connection pragmas, only the migrations of that
// release. The current binary then upgrades this file in the test.
func openV180Database(t *testing.T, path string, historical []migration) *Repository {
	t.Helper()
	dsn, _, err := normalizeDSN(path)
	if err != nil {
		t.Fatalf("解析 SQLite 路径: %v", err)
	}
	db, err := sql.Open("sqlite", withConnectionPragmas(dsn))
	if err != nil {
		t.Fatalf("打开 SQLite: %v", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	repository := &Repository{db: db, clock: func() time.Time { return time.Now().UTC() }}
	if _, err := db.Exec("PRAGMA journal_mode = WAL"); err != nil {
		_ = db.Close()
		t.Fatalf("设置 journal_mode: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS go_schema_migrations (
			version     INTEGER PRIMARY KEY,
			name        TEXT NOT NULL,
			checksum    TEXT NOT NULL,
			applied_at  TEXT NOT NULL
		)`); err != nil {
		_ = db.Close()
		t.Fatalf("创建 v1.8.0 migration ledger: %v", err)
	}
	if err := repository.runMigrations(context.Background(), historical); err != nil {
		_ = db.Close()
		t.Fatalf("按 v1.8.0 迁移建库: %v", err)
	}
	return repository
}

func readV180LedgerHead(t *testing.T, db *sql.DB) int {
	t.Helper()
	var head sql.NullInt64
	if err := db.QueryRow(`SELECT MAX(version) FROM go_schema_migrations`).Scan(&head); err != nil {
		t.Fatalf("读取 migration 版本: %v", err)
	}
	if !head.Valid {
		return 0
	}
	return int(head.Int64)
}

func execV180Statements(t *testing.T, db *sql.DB, statements []string) {
	t.Helper()
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("写入 v1.8.0 存量数据失败: %v\n%s", err, statement)
		}
	}
}

// seedV180Data writes one representative row set per business table in the
// v1.8.0 shape (columns and JSON encodings of that release). Every value is
// synthetic; no production content is copied.
func seedV180Data(t *testing.T, db *sql.DB) {
	t.Helper()
	execV180Statements(t, db, []string{
		// Users: one active creator, one soft-deleted account.
		`INSERT INTO authentication_users
		 (id, username, password_hash, nickname, name, email, roles_json, is_admin, is_active,
		  created_at, updated_at, deleted_at, last_login, open_id, bootstrap_managed)
		 VALUES
		 ('owner-a', 'alice', '$2a$10$synthetic-alice', 'Alice', 'Alice Chen', 'alice@example.test',
		  '["creator","reviewer"]', 0, 1, '2024-04-01T08:00:00Z', '2024-05-02T10:00:00Z', NULL,
		  '2024-05-02T09:00:00Z', '', 0),
		 ('owner-b', 'bob', '$2a$10$synthetic-bob', 'Bob', 'Bob Li', 'bob@example.test',
		  '["creator"]', 0, 1, '2024-04-02T08:00:00Z', '2024-04-03T08:00:00Z',
		  '2024-04-03T08:00:00Z', NULL, 'openid-bob', 0)`,
		// Chat sessions: a working budget-mode session, the sessions of the
		// pending/closed/denied and exhausted production plans, and one
		// owner-b session.
		`INSERT INTO go_chat_sessions
		 (id, owner_id, owner_name, title, created_at, updated_at, approval_mode, notes, picked,
		  agent_scope, title_source, reasoning_effort, context_prompt_tokens, context_completion_tokens,
		  context_model, context_updated_at, chat_model, brief, explicit_model_3d, parent_session_id,
		  parent_message_id, parent_title, parent_deleted, style_lock_enabled, style_lock_text,
		  style_lock_references, context_vision_tokens, explicit_model_image, visual_anchors,
		  project_id, style_lock_source)
		 VALUES
		 ('sess-a', 'owner-a', 'Alice', '合成概念车', '2024-05-02T10:00:00Z', '2024-05-02T10:30:00Z',
		  'budget', '合成备注', '', 'auto', 'user', 'high', 1280, 320, 'chat-model-a',
		  '2024-05-02T10:20:00Z', 'chat-model-a', '{"subject":"一辆合成概念车","style":"简洁"}',
		  'model-3d-a', '', '', '', 0, 0, '', '[]', 96, '', '[]', '', 'project'),
		 ('sess-b', 'owner-b', 'Bob', 'Bob 的会话', '2024-05-03T10:00:00Z', '2024-05-03T10:05:00Z',
		  'auto', '', '', '2d', 'auto', '', NULL, NULL, '', '', '', '', '', '', '', '', 0, 0, '',
		  '[]', NULL, '', '[]', '', 'project')`,
		`INSERT INTO go_chat_sessions
		 (id, owner_id, owner_name, title, created_at, updated_at, approval_mode, agent_scope, title_source)
		 VALUES
		 ('sess-budget', 'owner-a', 'Alice', '预算计划会话', '2024-05-04T10:00:00Z',
		  '2024-05-04T10:30:00Z', 'budget', 'auto', 'user'),
		 ('sess-budget-exhausted', 'owner-a', 'Alice', '预算用尽会话', '2024-05-05T10:00:00Z',
		  '2024-05-05T11:00:00Z', 'budget', '3d', 'user')`,
		// Messages: one user message and one assistant message per turn state.
		`INSERT INTO go_chat_messages
		 (id, session_id, role, content, image_urls, tool_calls, attachments, multiview, created_at)
		 VALUES
		 ('msg-u1', 'sess-a', 'user', '做一辆合成概念车', '["/uploads/synthetic-car.png"]', '[]', '[]', '', '2024-05-02T10:01:00Z'),
		 ('msg-a1', 'sess-a', 'assistant', '模型已生成', '[]',
		  '[{"id":"call-1","name":"generate_3d","arguments":"{\"prompt\":\"合成概念车\"}","task_id":"task-done","idempotency_key":"idem-call-1","estimated_credits":20,"approval_source":"policy"}]',
		  '[]', '', '2024-05-02T10:05:00Z'),
		 ('msg-a2', 'sess-a', 'assistant', '正在细化', '[]',
		  '[{"id":"call-2","name":"generate_3d","arguments":"{\"prompt\":\"合成概念车细化\"}","task_id":"task-running","idempotency_key":"idem-call-2","estimated_credits":20,"approval_source":"human"}]',
		  '[]', '', '2024-05-02T10:10:00Z'),
		 ('msg-a3', 'sess-a', 'assistant', '上游失败', '[]', '[]', '[]', '', '2024-05-02T10:15:00Z'),
		 ('msg-a4', 'sess-a', 'assistant', '', '[]', '[]', '[]', '', '2024-05-02T10:20:00Z'),
		 ('msg-b1', 'sess-b', 'user', '合成图片请求', '[]', '[]', '[]', '', '2024-05-03T10:01:00Z')`,
		// Turns: done / error / waiting_task / running with their checkpoints.
		`INSERT INTO go_chat_turns
		 (assistant_message_id, session_id, owner_id, options_json, state, created_at, updated_at,
		  stop_requested, checkpoint_json, checkpoint_version)
		 VALUES
		 ('msg-a1', 'sess-a', 'owner-a', '{"AIModel":"chat-model-a","AspectRatio":"1:1","SkillID":"three-view"}', 'done',
		  '2024-05-02T10:05:00Z', '2024-05-02T10:06:00Z', 0,
		  '{"round":2,"assistant":{"ID":"msg-a1","SessionID":"sess-a","Role":"assistant","Content":"模型已生成","ImageURLs":[],"ToolCalls":[],"Attachments":[],"CreatedAt":"2024-05-02T10:05:00Z"},"completion":{"Content":"","ToolCalls":[]},"completed":[{"Role":"tool","ToolCallID":"call-1","Content":"{\"state\":\"SUCCEEDED\"}"}],"waiting_task_id":""}',
		  3),
		 ('msg-a2', 'sess-a', 'owner-a', '{"AIModel":"chat-model-a","AspectRatio":"1:1"}', 'waiting_task',
		  '2024-05-02T10:10:00Z', '2024-05-02T10:11:00Z', 0,
		  '{"round":2,"waiting_task_id":"task-running","completed":[{"Role":"tool","ToolCallID":"call-2","Content":"{\"state\":\"RUNNING\"}"}]}',
		  2),
		 ('msg-a3', 'sess-a', 'owner-a', '{"AIModel":"chat-model-a"}', 'error',
		  '2024-05-02T10:15:00Z', '2024-05-02T10:16:00Z', 1,
		  '{"round":1,"completed":[]}', 1),
		 ('msg-a4', 'sess-a', 'owner-a', '{"AIModel":"chat-model-a"}', 'running',
		  '2024-05-02T10:20:00Z', '2024-05-02T10:21:00Z', 0, '{"round":1}', 0)`,
		// Processing plan: steps_json in the v1.8.0 shape, offering_id is the model name.
		`INSERT INTO processing_plans
		 (id, owner_id, session_id, source_task_id, source_asset_id, state, approved_credits,
		  request_hash, steps_json, failure, created_at, updated_at, version, harness_revision,
		  harness_digest, origin)
		 VALUES
		 ('plan-1', 'owner-a', 'sess-a', 'task-done', 'asset-model-1', 'RUNNING', 0, 'hash-plan-1',
		  '[{"capability":"remesh","offering_id":"model-3d-a","input":{"source":"asset-model-1"},"estimated_credits":5,"revision":"rev-remesh","state":"SUCCEEDED","task_id":"task-conv","retry":0},{"capability":"retexture","offering_id":"model-3d-a","input":{"source":"asset-conv-1"},"estimated_credits":8,"revision":"rev-retexture","state":"QUEUED","retry":1,"resumable":true}]',
		  '', '2024-05-02T10:25:00Z', '2024-05-02T10:26:00Z', 4, 0, '', 'browser')`,
		// Generation tasks of every interesting state.
		`INSERT INTO generation_tasks
		 (id, owner_id, capability, offering_json, input_json, request_hash, idempotency_key, state,
		  progress, failure_json, current_attempt_id, materialize_failures, next_action_at, version,
		  lease_owner, lease_token, lease_until, created_at, updated_at, finished_at,
		  resource_state_json, session_id, native_quote_json, native_quote_required)
		 VALUES
		 ('task-done', 'owner-a', 'image-to-3d',
		  '{"id":"` + v180OfferingID + `","revision":"` + v180OfferingRev + `","provider_id":"provider-a","provider_name":"Provider A","model_id":"` + v180OfferingID + `","model_name":"Model 3D A","capability":"image-to-3d","parameters":[]}',
		  '{"prompt":"合成概念车","source_asset":"input-1"}', 'hash-task-done', 'idem-task-done',
		  'SUCCEEDED', 100, NULL, 'att-1', 0, NULL, 7, NULL, NULL, NULL,
		  '` + v180TaskStamp + `', '2024-05-02T10:20:00.000000000Z', '2024-05-02T10:20:00.000000000Z',
		  NULL, 'sess-a', NULL, 0),
		 ('task-conv', 'owner-a', 'format-conversion',
		  '{"id":"format-gltf","revision":"rev-gltf","provider_id":"provider-a","capability":"format-conversion","parameters":[]}',
		  '{"source_asset":"asset-model-1"}', 'hash-task-conv', 'idem-task-conv',
		  'SUCCEEDED', 100, NULL, 'att-2', 0, NULL, 3, NULL, NULL, NULL,
		  '2024-05-02T10:22:00.000000000Z', '2024-05-02T10:23:00.000000000Z',
		  '2024-05-02T10:23:00.000000000Z', NULL, 'sess-a', NULL, 0),
		 ('task-running', 'owner-a', 'image-to-3d',
		  '{"id":"` + v180OfferingID + `","revision":"` + v180OfferingRev + `","provider_id":"provider-a","capability":"image-to-3d","parameters":[]}',
		  '{"prompt":"合成概念车细化"}', 'hash-task-running', 'idem-task-running',
		  'RUNNING', 40, NULL, 'att-3', 0, '2024-05-02T10:31:00.000000000Z', 2,
		  'worker-1', 'lease-token-1', '2024-05-02T10:32:00.000000000Z',
		  '2024-05-02T10:24:00.000000000Z', '2024-05-02T10:30:00.000000000Z', NULL,
		  NULL, 'sess-a', NULL, 0),
		 ('task-failed', 'owner-a', 'image-to-3d',
		  '{"id":"` + v180OfferingID + `","revision":"` + v180OfferingRev + `","provider_id":"provider-a","capability":"image-to-3d","parameters":[]}',
		  '{"prompt":"合成概念车失败分支"}', 'hash-task-failed', 'idem-task-failed',
		  'FAILED', 10, '{"class":"transport","retryable":true,"detail":"合成上游超时"}', 'att-4', 1,
		  NULL, 4, NULL, NULL, NULL,
		  '2024-05-02T10:18:00.000000000Z', '2024-05-02T10:19:00.000000000Z',
		  '2024-05-02T10:19:00.000000000Z', NULL, 'sess-a', NULL, 0),
		 ('task-exhausted', 'owner-a', 'image-to-3d',
		  '{"id":"` + v180OfferingID + `","revision":"` + v180OfferingRev + `","provider_id":"provider-a","capability":"image-to-3d","parameters":[]}',
		  '{"prompt":"合成概念车（预算用尽）"}', 'hash-task-exhausted', 'idem-task-exhausted',
		  'SUCCEEDED', 100, NULL, NULL, 0, NULL, 2, NULL, NULL, NULL,
		  '2024-05-05T10:05:00.000000000Z', '2024-05-05T10:40:00.000000000Z',
		  '2024-05-05T10:40:00.000000000Z', NULL, 'sess-budget-exhausted', NULL, 0)`,
		`INSERT INTO generation_attempts
		 (id, task_id, ordinal, offering_json, state, submission_key, upstream_provider_id,
		  upstream_job_id, failure_json, opaque_json, started_at, updated_at, finished_at)
		 VALUES
		 ('att-1', 'task-done', 1, '{"id":"` + v180OfferingID + `"}', 'UPSTREAM_SUCCEEDED',
		  'submit-att-1', 'provider-a', 'job-1', NULL, '[{"identifier":"job-1"}]',
		  '2024-05-02T10:05:00.000000000Z', '2024-05-02T10:10:00.000000000Z',
		  '2024-05-02T10:10:00.000000000Z'),
		 ('att-2', 'task-conv', 1, '{"id":"format-gltf"}', 'UPSTREAM_SUCCEEDED',
		  'submit-att-2', 'provider-a', 'job-2', NULL, '[{"identifier":"job-2"}]',
		  '2024-05-02T10:22:00.000000000Z', '2024-05-02T10:23:00.000000000Z',
		  '2024-05-02T10:23:00.000000000Z'),
		 ('att-3', 'task-running', 1, '{"id":"` + v180OfferingID + `"}', 'RUNNING',
		  'submit-att-3', 'provider-a', 'job-3', NULL, '[{"identifier":"job-3"}]',
		  '2024-05-02T10:24:00.000000000Z', '2024-05-02T10:30:00.000000000Z', NULL),
		 ('att-4', 'task-failed', 1, '{"id":"` + v180OfferingID + `"}', 'FAILED',
		  'submit-att-4', 'provider-a', 'job-4',
		  '{"class":"transport","retryable":true,"detail":"合成上游超时"}', NULL,
		  '2024-05-02T10:18:00.000000000Z', '2024-05-02T10:19:00.000000000Z',
		  '2024-05-02T10:19:00.000000000Z')`,
		// Delivered assets of the finished task and the conversion task.
		`INSERT INTO generation_assets
		 (id, task_id, attempt_id, role, required, ordinal, storage_key, media_type, size_bytes,
		  sha256, created_at, variant, delivery_profile)
		 VALUES
		 ('asset-model-1', 'task-done', 'att-1', 'model', 1, 0, 'cas/aa/asset-model-1.glb',
		  'model/gltf-binary', 1024, '` + v180ShaModel + `', '2024-05-02T10:20:00Z', 'glb', ''),
		 ('asset-thumb-1', 'task-done', 'att-1', 'thumbnail', 0, 0, 'cas/bb/asset-thumb-1.png',
		  'image/png', 512, '` + v180ShaThumb + `', '2024-05-02T10:20:00Z', '', 'inline'),
		 ('asset-conv-1', 'task-conv', 'att-2', 'model', 1, 0, 'cas/cc/asset-conv-1.gltf',
		  'model/gltf+json', 2048, '` + v180ShaConvert + `', '2024-05-02T10:23:00Z', 'gltf', '')`,
		// Input assets: one upload and one crop derived from it.
		`INSERT INTO generation_input_assets
		 (id, owner_id, storage_key, media_type, size_bytes, sha256, created_at,
		  original_filename, asset_type, derived_from_asset_id, derivation_method, source_size_bytes)
		 VALUES
		 ('input-1', 'owner-a', 'uploads/input-1.png', 'image/png', 4096, '` + v180ShaUpload + `',
		  '2024-05-02T09:50:00Z', 'synthetic-car.png', 'image', NULL, NULL, NULL),
		 ('input-crop-1', 'owner-a', 'uploads/input-crop-1.png', 'image/png', 1024, '` + v180ShaCrop + `',
		  '2024-05-02T09:55:00Z', 'synthetic-car-crop.png', 'list_crop', 'input-1', 'crop', 4096)`,
		`INSERT INTO generation_prepared_inputs
		 (id, owner_id, session_id, input_hash, payload_json, created_at, native_quote_required)
		 VALUES
		 ('prep-1', 'owner-a', 'sess-a', 'hash-prep-1',
		  '{"prompt":"合成概念车","source_asset":"input-1"}', '2024-05-02T10:04:00Z', 0),
		 ('prep-2', 'owner-a', 'sess-a', 'hash-prep-2',
		  '{"prompt":"合成概念车细化"}', '2024-05-02T10:09:00Z', 0)`,
		// Execution grants: every status, both historical sources, budget eligible or not.
		`INSERT INTO execution_grants
		 (request_id, owner_id, session_id, offering_id, offering_revision, prepared_input_id,
		  prepared_input_hash, idempotency_key, operation_id, plan_step_id, status, task_id, source,
		  request_version, decided_by, decided_at, created_at, updated_at, trigger_turn_id,
		  harness_revision, harness_digest, budget_eligible)
		 VALUES
		 ('eg-pending', 'owner-a', 'sess-a', '` + v180OfferingID + `', '` + v180OfferingRev + `',
		  'prep-2', 'hash-prep-2', 'idem-eg-pending', 'op-eg-pending', '', 'pending', NULL,
		  'human_browser', 1, NULL, NULL, '2024-05-02T10:09:00Z', '2024-05-02T10:09:00Z', '', 0, '', 0),
		 ('eg-approved', 'owner-a', 'sess-a', '` + v180OfferingID + `', '` + v180OfferingRev + `',
		  'prep-1', 'hash-prep-1', 'idem-eg-approved', 'op-eg-approved', 'front-1', 'approved', NULL,
		  'agent_human_decision', 3, 'policy:auto', '2024-05-02T10:10:00Z',
		  '2024-05-02T10:04:00Z', '2024-05-02T10:10:00Z', 'msg-a2', 2, '` + v180Digest + `', 1),
		 ('eg-claimed', 'owner-a', 'sess-a', '` + v180OfferingID + `', '` + v180OfferingRev + `',
		  'prep-1', 'hash-prep-1', 'idem-eg-claimed', 'op-eg-claimed', '', 'claimed', NULL,
		  'human_browser', 2, 'owner-a', '2024-05-02T10:20:00Z', '2024-05-02T10:04:00Z',
		  '2024-05-02T10:20:00Z', '', 0, '', 0),
		 ('eg-bound', 'owner-a', 'sess-a', 'format-gltf', 'rev-gltf',
		  'prep-1', 'hash-prep-1', 'idem-eg-bound', 'op-eg-bound', 'front-1', 'bound', 'task-conv',
		  'agent_human_decision', 1, 'policy:auto', '2024-05-02T10:22:00Z',
		  '2024-05-02T10:21:00Z', '2024-05-02T10:22:00Z', '', 2, '` + v180Digest + `', 1),
		 ('eg-denied', 'owner-a', 'sess-a', '` + v180OfferingID + `', '` + v180OfferingRev + `',
		  'prep-2', 'hash-prep-2', 'idem-eg-denied', 'op-eg-denied', 'side-1', 'denied', NULL,
		  'agent_human_decision', 3, 'owner-a', '2024-05-02T10:11:00Z', '2024-05-02T10:09:00Z',
		  '2024-05-02T10:11:00Z', 'msg-a2', 0, '', 0),
		 ('eg-budget-released', 'owner-a', 'sess-budget', '` + v180OfferingID + `',
		  '` + v180OfferingRev + `', 'prep-2', 'hash-prep-2', 'idem-eg-budget-released',
		  'op-eg-budget-released', '', 'bound', 'task-failed', 'agent_human_decision', 1,
		  'policy:auto', '2024-05-02T10:18:00Z', '2024-05-02T10:18:00Z',
		  '2024-05-02T10:19:00Z', '', 1, '` + v180Digest + `', 1),
		 ('eg-budget-exhausted', 'owner-a', 'sess-budget-exhausted', '` + v180OfferingID + `',
		  '` + v180OfferingRev + `', 'prep-2', 'hash-prep-2', 'idem-eg-budget-exhausted',
		  'op-eg-budget-exhausted', '', 'bound', 'task-exhausted', 'agent_human_decision', 1,
		  'owner-a', '2024-05-05T10:10:00Z', '2024-05-05T10:05:00Z',
		  '2024-05-05T10:10:00Z', '', 0, '', 1)`,
		// Production budgets of every state (one active/exhausted per session,
		// as the partial unique index requires) and their unit reservations.
		// plan_json keeps the v1.8.0 shape: a step may pin the model name in
		// offering_id or pin a real Offering ID, quotes are per unit, and a
		// closed plan still carries the pre-unit integer estimated_credits.
		`INSERT INTO production_budgets
		 (id, owner_id, session_id, plan_json, limits_json, state, created_at, decided_at,
		  decided_by, message_id, call_id, notice_sent_at)
		 VALUES
		 ('budget-pending', 'owner-a', 'sess-budget',
		  '{"title":"合成概念车制作计划（待批准）","steps":[{"capability":"text-to-3d","offering_id":"model-3d-a","purpose":"主体模型","estimate":{"unit":"legacy.credits","unit_label":"legacy.credits","amount":"20"},"check":"self_check"},{"capability":"format-conversion","offering_id":"local/generated-glb/to-gltf","purpose":"导出 glTF","estimate":{"unit":"legacy.credits","unit_label":"legacy.credits","amount":"3"}}],"correction_capabilities":["retexture","remesh"],"acceptance":[{"id":"look","statement":"造型符合描述","check":"visual_quality"}],"checkpoints":[{"after_capability":"text-to-3d","checks":[{"check":"watertight","required":true}]}],"contingencies":[{"id":"fix-uv","trigger":["self_check"],"step":{"capability":"remesh","offering_id":"model-3d-a","estimate":{"unit":"legacy.credits","unit_label":"legacy.credits","amount":"6"}}}]}',
		  '[{"unit":"legacy.credits","unit_label":"legacy.credits","amount":"29"}]',
		  'pending', '2024-05-04T10:05:00Z', NULL, '', 'msg-a2', 'call-2', ''),
		 ('budget-closed', 'owner-a', 'sess-budget',
		  '{"title":"旧版制作计划","steps":[{"capability":"text-to-3d","offering_id":"model-3d-a","purpose":"主体模型","estimated_credits":12}],"correction_capabilities":["remesh"]}',
		  '[{"unit":"legacy.credits","unit_label":"legacy.credits","amount":"30"}]',
		  'closed', '2024-05-04T10:00:00Z', '2024-05-04T10:20:00Z', 'system', 'msg-a2', 'call-2', ''),
		 ('budget-denied', 'owner-a', 'sess-budget',
		  '{"title":"被拒的制作计划","steps":[{"capability":"text-to-3d","offering_id":"model-3d-a","purpose":"主体模型","estimate":{"unit":"meshy.credit","unit_label":"Meshy credits","amount":"4"}}]}',
		  '[{"unit":"meshy.credit","unit_label":"Meshy credits","amount":"9"}]',
		  'denied', '2024-05-04T10:02:00Z', '2024-05-04T10:10:00Z', 'owner-a', 'msg-a2', 'call-2', ''),
		 ('budget-active', 'owner-a', 'sess-a',
		  '{"title":"合成概念车制作计划","steps":[{"capability":"text-to-3d","offering_id":"model-3d-a","purpose":"主体模型","estimate":{"unit":"meshy.credit","unit_label":"Meshy credits","amount":"5.5"}},{"capability":"format-conversion","offering_id":"local/generated-glb/to-gltf","purpose":"导出 glTF","estimate":{"unit":"legacy.credits","unit_label":"legacy.credits","amount":"3"}}],"correction_capabilities":["remesh"],"contingencies":[{"id":"fix-holes","trigger":["self_check"],"step":{"capability":"remesh","offering_id":"tripo/geometry/decimate-quad","estimate":{"unit":"meshy.credit","unit_label":"Meshy credits","amount":"1.25"}}}]}',
		  '[{"unit":"legacy.credits","unit_label":"legacy.credits","amount":"10"},{"unit":"meshy.credit","unit_label":"Meshy credits","amount":"8"}]',
		  'active', '2024-05-02T10:06:00Z', '2024-05-02T10:07:00Z', 'owner-a', 'msg-a2', 'call-2', ''),
		 ('budget-exhausted', 'owner-a', 'sess-budget-exhausted',
		  '{"title":"用尽的制作计划","steps":[{"capability":"text-to-3d","offering_id":"model-3d-a","estimate":{"unit":"legacy.credits","unit_label":"legacy.credits","amount":"20"}}]}',
		  '[{"unit":"legacy.credits","unit_label":"legacy.credits","amount":"20"}]',
		  'exhausted', '2024-05-05T10:02:00Z', '2024-05-05T10:03:00Z', 'owner-a', 'msg-a2',
		  'call-2', '2024-05-05T11:00:00Z')`,
		`INSERT INTO budget_reservations
		 (grant_request_id, budget_id, task_id, unit, amount_micros, state, created_at, settled_at)
		 VALUES
		 ('eg-approved', 'budget-active', '', 'meshy.credit', 1500000, 'reserved',
		  '2024-05-02T10:10:00Z', NULL),
		 ('eg-bound', 'budget-active', 'task-conv', 'legacy.credits', 3000000, 'charged',
		  '2024-05-02T10:21:00Z', '2024-05-02T10:23:00Z'),
		 ('eg-budget-released', 'budget-closed', 'task-failed', 'legacy.credits', 4000000,
		  'released', '2024-05-02T10:18:00Z', '2024-05-02T10:19:00Z'),
		 ('eg-budget-exhausted', 'budget-exhausted', 'task-exhausted', 'legacy.credits', 20000000,
		  'charged', '2024-05-05T10:10:00Z', '2024-05-05T10:40:00Z')`,
		// SkillRun with a finished (output_ready) step and a planned one; its
		// step references the delivered conversion task and asset.
		`INSERT INTO skill_runs
		 (id, owner_id, session_id, recipe_id, recipe_revision, recipe_hash, selected_offering_id,
		  source_asset_id, source_task_id, source_prepared_input_id, idempotency_key, request_hash,
		  created_at, updated_at, harness_revision, harness_digest)
		 VALUES
		 ('run-1', 'owner-a', 'sess-a', 'three-view', '1', 'recipe-hash-1', 'model-3d-a',
		  'asset-model-1', 'task-done', NULL, 'idem-run-1', 'hash-run-1',
		  '2024-05-02T10:27:00Z', '2024-05-02T10:28:00Z', 2, '` + v180Digest + `')`,
		`INSERT INTO skill_run_steps
		 (run_id, ordinal, step_key, spec_json, phase, operation_id, idempotency_key, plan_step_id,
		  prepared_input_id, prepared_input_hash, grant_request_id, task_id, output_asset_id,
		  review, reviewed_at, attempt, replacement_asset_id)
		 VALUES
		 ('run-1', 0, 'front',
		  '{"key":"front","label":"正面","capability":"image-to-multiview","offering_id":"model-3d-a","offering_revision":"` + v180OfferingRev + `","prompt_template":"正面视图","output_role":"view"}',
		  'output_ready', 'op-front', 'idem-front-1', 'front-1',
		  'prep-1', 'hash-prep-1', 'eg-bound', 'task-conv', 'asset-conv-1',
		  'accepted', '2024-05-02T10:29:00Z', 0, NULL),
		 ('run-1', 1, 'side',
		  '{"key":"side","label":"侧面","capability":"image-to-multiview","offering_id":"model-3d-a","offering_revision":"` + v180OfferingRev + `","prompt_template":"侧面视图","output_role":"view","source_step_key":"front"}',
		  'planned', 'op-side', 'idem-side-1', 'side-1',
		  NULL, NULL, NULL, NULL, NULL, 'unreviewed', NULL, 1, 'input-crop-1')`,
		// Published Agent configuration versions.
		`INSERT INTO agent_configuration_versions
		 (revision, configuration_json, actor_id, source, created_at)
		 VALUES
		 (1, '{"system_prompt":"合成系统提示词 v1","domain_context":{},"skills":[],"enabled_tool_names":["generate_3d"],"enabled_mcp_preset_ids":[]}',
		  'owner-admin', 'publish', '2024-04-20T08:00:00Z'),
		 (2, '{"system_prompt":"合成系统提示词 v2","domain_context":{"models":"synthetic"},"skills":[],"enabled_tool_names":["generate_3d","inspect_asset"],"enabled_mcp_preset_ids":[]}',
		  'owner-admin', 'publish', '2024-05-01T12:00:00Z')`,
	})
}

// v180SnapshotTables covers every table the fixture writes: the upgrade must
// keep each row and each pre-existing column of them.
var v180SnapshotTables = []string{
	"authentication_users",
	"go_chat_sessions",
	"go_chat_messages",
	"go_chat_turns",
	"processing_plans",
	"skill_runs",
	"skill_run_steps",
	"generation_tasks",
	"generation_task_summaries",
	"generation_attempts",
	"generation_assets",
	"generation_input_assets",
	"generation_prepared_inputs",
	"production_budgets",
	"budget_reservations",
	"execution_grants",
	"agent_configuration_versions",
}

// snapshotV180Tables reads SELECT * of each table into column → rows text.
// Rows are sorted by their whole value tuple so the snapshot does not depend
// on storage order (0097 rebuilds execution_grants in one transaction).
func snapshotV180Tables(t *testing.T, db *sql.DB) map[string]v180TableSnapshot {
	t.Helper()
	snapshots := make(map[string]v180TableSnapshot, len(v180SnapshotTables))
	for _, table := range v180SnapshotTables {
		snapshots[table] = snapshotV180Table(t, db, table)
	}
	return snapshots
}

func snapshotV180Table(t *testing.T, db *sql.DB, table string) v180TableSnapshot {
	t.Helper()
	rows, err := db.Query(`SELECT * FROM ` + table)
	if err != nil {
		t.Fatalf("读取 %s 快照: %v", table, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("读取 %s 列名: %v", table, err)
	}
	var tuples [][]string
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatalf("扫描 %s: %v", table, err)
		}
		tuple := make([]string, len(columns))
		for index, value := range values {
			tuple[index] = fmt.Sprintf("%T|%v", value, value)
		}
		tuples = append(tuples, tuple)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("读取 %s 行: %v", table, err)
	}
	sort.Slice(tuples, func(left, right int) bool {
		return strings.Join(tuples[left], "\x00") < strings.Join(tuples[right], "\x00")
	})
	snapshot := make(v180TableSnapshot, len(columns))
	for index, column := range columns {
		values := make([]string, 0, len(tuples))
		for _, tuple := range tuples {
			values = append(values, tuple[index])
		}
		snapshot[column] = values
	}
	if len(tuples) == 0 {
		// Every fixture table must hold rows: an empty snapshot would make the
		// "kept after upgrade" assertion vacuous.
		t.Fatalf("%s 在 v1.8.0 存量数据里没有行", table)
	}
	return snapshot
}

func assertV180DatabaseIntegrity(t *testing.T, db *sql.DB, stage string) {
	t.Helper()
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("%s: foreign_key_check: %v", stage, err)
	}
	defer rows.Close()
	if rows.Next() {
		var table, parent string
		var rowID, fkID sql.NullInt64
		if err := rows.Scan(&table, &rowID, &parent, &fkID); err != nil {
			t.Fatalf("%s: 读取 foreign_key_check: %v", stage, err)
		}
		t.Fatalf("%s: foreign_key_check 发现悬空引用: 表 %s 父表 %s", stage, table, parent)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("%s: foreign_key_check 行: %v", stage, err)
	}
	var result string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&result); err != nil {
		t.Fatalf("%s: integrity_check: %v", stage, err)
	}
	if result != "ok" {
		t.Fatalf("%s: integrity_check = %q; want ok", stage, result)
	}
}

func assertV180LedgerContiguous(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`SELECT version FROM go_schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("读取 migration 台账: %v", err)
	}
	defer rows.Close()
	var versions []int
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			t.Fatalf("扫描 migration 版本: %v", err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("migration 台账行: %v", err)
	}
	if want := MigrationVersions(); !reflect.DeepEqual(versions, want) {
		t.Fatalf("升级后 migration 台账 = %v; want %v", versions, want)
	}
}

func assertV180LedgerPreserved(t *testing.T, db *sql.DB, historical []v180LedgerRow) {
	t.Helper()
	for _, want := range historical {
		// The frozen name is "<file name>.sql"; the ledger stores the bare name.
		stem := strings.TrimSuffix(want.name, ".sql")
		name := strings.TrimPrefix(stem, fmt.Sprintf("%04d_", want.version))
		var gotName, gotChecksum string
		if err := db.QueryRow(
			`SELECT name, checksum FROM go_schema_migrations WHERE version = ?`,
			want.version,
		).Scan(&gotName, &gotChecksum); err != nil {
			t.Fatalf("读取 migration %d 账行: %v", want.version, err)
		}
		if gotName != name || gotChecksum != want.checksum {
			t.Fatalf("migration %d 的账行被改写: name=%s checksum=%s; want %s %s",
				want.version, gotName, gotChecksum, name, want.checksum)
		}
	}
}

func assertV180SnapshotsKept(t *testing.T, before, after map[string]v180TableSnapshot) {
	t.Helper()
	for _, table := range v180SnapshotTables {
		original := before[table]
		upgraded, ok := after[table]
		if !ok {
			t.Fatalf("%s 升级后消失了", table)
		}
		for column, want := range original {
			got, ok := upgraded[column]
			if !ok {
				t.Fatalf("%s.%s 升级后消失了", table, column)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s.%s 的数据在升级后变化:\nv1.8.0 %v\n升级后 %v", table, column, want, got)
			}
		}
	}
}

// TestUpgradeFromV180KeepsStoredData is the rehearsal the Issue asks for: build
// the database v1.8.0 leaves behind, fill it with representative rows, upgrade
// it with the current binary, and read every business fact back.
func TestUpgradeFromV180KeepsStoredData(t *testing.T) {
	historical := v180Migrations(t)
	path := filepath.Join(t.TempDir(), "v180.db")

	v180 := openV180Database(t, path, historical)
	t.Cleanup(func() { _ = v180.Close() })
	if head := readV180LedgerHead(t, v180.db); head != v180HeadVersion {
		t.Fatalf("v1.8.0 建库后 migration 版本 = %d; want %d", head, v180HeadVersion)
	}
	seedV180Data(t, v180.db)
	before := snapshotV180Tables(t, v180.db)
	assertV180DatabaseIntegrity(t, v180.db, "v1.8.0 存量库")
	if err := v180.Close(); err != nil {
		t.Fatalf("关闭 v1.8.0 数据库: %v", err)
	}

	// The same file is now opened by the current binary: every migration after
	// v1.8.0 runs against the rows seeded above.
	upgraded, err := openTestRepository(path)
	if err != nil {
		t.Fatalf("用当前代码升级 v1.8.0 数据库: %v", err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	if head := readV180LedgerHead(t, upgraded.db); head != MigrationHead() {
		t.Fatalf("升级后 migration 版本 = %d; want %d", head, MigrationHead())
	}
	assertV180LedgerContiguous(t, upgraded.db)
	assertV180LedgerPreserved(t, upgraded.db, readV180Ledger(t))
	assertV180SnapshotsKept(t, before, snapshotV180Tables(t, upgraded.db))
	assertV180DatabaseIntegrity(t, upgraded.db, "升级后")

	t.Run("存量行可经仓库层读回", func(t *testing.T) {
		assertV180Reads(t, upgraded)
	})
	t.Run("升级后的表继续执行外键与约束", func(t *testing.T) {
		assertV180Constraints(t, upgraded)
	})
	t.Run("新增表与新增列就位", func(t *testing.T) {
		assertV180HeadSchema(t, upgraded)
	})
}

func assertV180Reads(t *testing.T, r *Repository) {
	t.Helper()
	ctx := context.Background()

	t.Run("用户", func(t *testing.T) {
		alice, found, err := r.FindByUsername(ctx, "alice")
		if err != nil || !found {
			t.Fatalf("读取 alice: found=%v err=%v", found, err)
		}
		if alice.ID != v180OwnerID || alice.Name != "Alice Chen" || alice.Email != "alice@example.test" ||
			alice.Admin || !alice.Active || alice.Deleted {
			t.Fatalf("alice = %+v", alice)
		}
		if !reflect.DeepEqual(alice.Roles, []string{"creator", "reviewer"}) {
			t.Fatalf("alice 角色 = %v", alice.Roles)
		}
		bob, found, err := r.FindByUsername(ctx, "bob")
		if err != nil || !found {
			t.Fatalf("读取 bob: found=%v err=%v", found, err)
		}
		if !bob.Deleted || bob.OpenID != "openid-bob" {
			t.Fatalf("bob = %+v", bob)
		}
	})

	t.Run("会话与消息", func(t *testing.T) {
		sessions, err := r.ListSessions(ctx, v180OwnerID)
		if err != nil {
			t.Fatalf("ListSessions: %v", err)
		}
		if len(sessions) != 3 {
			t.Fatalf("owner-a 会话 = %d; want 3（工作会话与两个预算计划会话）", len(sessions))
		}
		session := sessions[0]
		for _, candidate := range sessions {
			if candidate.ID == v180SessionID {
				session = candidate
			}
		}
		if session.ID != v180SessionID {
			t.Fatalf("sess-a 不在会话列表: %+v", sessions)
		}
		if session.Title != "合成概念车" ||
			session.ApprovalMode != "budget" || session.AgentScope != "auto" ||
			session.TitleSource != "user" ||
			session.ExplicitModel3D != v180OfferingID || session.Messages != 5 {
			t.Fatalf("会话 = %+v", session)
		}
		if string(session.Brief["subject"]) != `"一辆合成概念车"` {
			t.Fatalf("会话 brief = %s", session.Brief)
		}
		messages, err := r.ListMessages(ctx, v180OwnerID, v180SessionID)
		if err != nil {
			t.Fatalf("ListMessages: %v", err)
		}
		if len(messages) != 5 {
			t.Fatalf("sess-a 消息 = %d; want 5", len(messages))
		}
		assistant := messages[1]
		if assistant.ID != "msg-a1" || assistant.Role != "assistant" || assistant.Content != "模型已生成" {
			t.Fatalf("msg-a1 = %+v", assistant)
		}
		if len(assistant.ToolCalls) != 1 {
			t.Fatalf("msg-a1 tool_calls = %d; want 1", len(assistant.ToolCalls))
		}
		call := assistant.ToolCalls[0]
		if call.ID != "call-1" || call.Name != "generate_3d" ||
			string(call.Arguments) != `{"prompt":"合成概念车"}` || call.TaskID != "task-done" ||
			call.IdempotencyKey != "idem-call-1" || call.EstimatedCredits != 20 ||
			call.ApprovalSource != "policy" {
			t.Fatalf("msg-a1 tool_call = %+v", call)
		}
	})

	t.Run("回合与检查点", func(t *testing.T) {
		turns, err := r.ListSessionTurns(ctx, v180OwnerID, v180SessionID,
			[]string{"queued", "running", "waiting_task", "done", "error"})
		if err != nil {
			t.Fatalf("ListSessionTurns: %v", err)
		}
		if len(turns) != 4 {
			t.Fatalf("sess-a 回合 = %d; want 4", len(turns))
		}
		byID := make(map[string]string, len(turns))
		for _, turn := range turns {
			byID[turn.AssistantMessageID] = turn.State
		}
		for assistant, want := range map[string]string{
			"msg-a1": "done", "msg-a2": "waiting_task", "msg-a3": "error", "msg-a4": "running",
		} {
			if byID[assistant] != want {
				t.Fatalf("回合 %s 状态 = %q; want %q", assistant, byID[assistant], want)
			}
		}
		done, err := r.GetTurn(ctx, v180OwnerID, v180SessionID, "msg-a1")
		if err != nil {
			t.Fatalf("GetTurn(msg-a1): %v", err)
		}
		if done.State != "done" || done.CheckpointVersion != 3 || done.StopRequested ||
			done.Checkpoint.Round != 2 || len(done.Checkpoint.Completed) != 1 ||
			done.Checkpoint.Completed[0].ToolCallID != "call-1" || done.Checkpoint.WaitingTaskID != "" {
			t.Fatalf("msg-a1 回合 = %+v", done)
		}
		// 0094 gives historical turns the empty end reason, not a guessed value.
		if done.EndReason != "" {
			t.Fatalf("msg-a1 end_reason = %q; want 空串", done.EndReason)
		}
		if done.Options.AIModel != v180ChatModel || done.Options.AspectRatio != "1:1" ||
			done.Options.SkillID != "three-view" {
			t.Fatalf("msg-a1 options = %+v", done.Options)
		}
		waiting, err := r.GetTurn(ctx, v180OwnerID, v180SessionID, "msg-a2")
		if err != nil {
			t.Fatalf("GetTurn(msg-a2): %v", err)
		}
		if waiting.State != "waiting_task" || waiting.Checkpoint.WaitingTaskID != "task-running" ||
			waiting.Checkpoint.Round != 2 || len(waiting.Checkpoint.Completed) != 1 {
			t.Fatalf("msg-a2 回合 = %+v", waiting)
		}
		stopped, err := r.GetTurn(ctx, v180OwnerID, v180SessionID, "msg-a3")
		if err != nil {
			t.Fatalf("GetTurn(msg-a3): %v", err)
		}
		if !stopped.StopRequested || stopped.State != "error" {
			t.Fatalf("msg-a3 回合 = %+v", stopped)
		}
		running, err := r.GetTurn(ctx, v180OwnerID, v180SessionID, "msg-a4")
		if err != nil {
			t.Fatalf("GetTurn(msg-a4): %v", err)
		}
		if running.State != "running" || running.CheckpointVersion != 0 {
			t.Fatalf("msg-a4 回合 = %+v", running)
		}
	})

	t.Run("3D 处理计划", func(t *testing.T) {
		plan, err := r.GetProcessingPlan(ctx, generation.OwnerID(v180OwnerID), "plan-1")
		if err != nil {
			t.Fatalf("GetProcessingPlan: %v", err)
		}
		if plan.ID != "plan-1" || plan.State != "RUNNING" || plan.Version != 4 ||
			plan.SourceTaskID != "task-done" || plan.SourceAssetID != "asset-model-1" ||
			plan.Origin != "browser" {
			t.Fatalf("plan = %+v", plan)
		}
		if len(plan.Steps) != 2 {
			t.Fatalf("plan steps = %d; want 2", len(plan.Steps))
		}
		// The v1.8.0 plan stores the model name in offering_id; keep it.
		if plan.Steps[0].OfferingID != v180OfferingID || plan.Steps[0].Capability != generation.Remesh ||
			plan.Steps[0].EstimatedCredits != 5 || plan.Steps[0].TaskID != "task-conv" {
			t.Fatalf("plan step[0] = %+v", plan.Steps[0])
		}
		if plan.Steps[1].State != "QUEUED" || plan.Steps[1].Retry != 1 || !plan.Steps[1].Resumable {
			t.Fatalf("plan step[1] = %+v", plan.Steps[1])
		}
	})

	t.Run("制作预算计划", func(t *testing.T) {
		const micros = int64(1000000)
		pending, err := r.GetProductionBudget(ctx, v180OwnerID, "budget-pending")
		if err != nil {
			t.Fatalf("GetProductionBudget(budget-pending): %v", err)
		}
		if pending.State != budget.StatePending || pending.SessionID != "sess-budget" ||
			pending.DecidedAt != nil || pending.DecidedBy != "" ||
			pending.MessageID != "msg-a2" || pending.CallID != "call-2" {
			t.Fatalf("budget-pending = %+v", pending)
		}
		if len(pending.Limits) != 1 || pending.Limits[0].Unit != budget.LegacyUnit ||
			pending.Limits[0].UnitLabel != budget.LegacyUnit ||
			pending.Limits[0].Micros != 29*micros {
			t.Fatalf("budget-pending limits = %+v", pending.Limits)
		}
		listed, err := r.PendingProductionBudgets(ctx, v180OwnerID, "sess-budget")
		if err != nil {
			t.Fatalf("PendingProductionBudgets: %v", err)
		}
		if len(listed) != 1 || listed[0].ID != "budget-pending" {
			t.Fatalf("sess-budget 待批准计划 = %+v; want 只有 budget-pending", listed)
		}
		plan := pending.Plan
		if plan.Title != "合成概念车制作计划（待批准）" || len(plan.Steps) != 2 ||
			len(plan.Contingencies) != 1 || len(plan.Checkpoints) != 1 || len(plan.Acceptance) != 1 {
			t.Fatalf("budget-pending plan = %+v", plan)
		}
		// #434: a v1.8.0 plan pins the model name in offering_id, so decoding
		// keeps that string; whether it still matches an Offering is that
		// Issue's decision, not this storage check's.
		if plan.Steps[0].OfferingID != v180OfferingID || plan.Steps[0].Capability != string(generation.TextTo3D) ||
			plan.Steps[0].Estimate.Unit != budget.LegacyUnit ||
			plan.Steps[0].Estimate.Micros != 20*micros || plan.Steps[0].Check != "self_check" {
			t.Fatalf("budget-pending step[0] = %+v", plan.Steps[0])
		}
		// The second step pins a real Offering ID; v1.8.0 already used the
		// provider/model/capability form for these.
		if plan.Steps[1].OfferingID != "local/generated-glb/to-gltf" ||
			plan.Steps[1].Estimate.Micros != 3*micros {
			t.Fatalf("budget-pending step[1] = %+v", plan.Steps[1])
		}
		if !reflect.DeepEqual(plan.CorrectionCapabilities, []string{"retexture", "remesh"}) {
			t.Fatalf("budget-pending correction capabilities = %v", plan.CorrectionCapabilities)
		}
		if plan.Contingencies[0].ID != "fix-uv" ||
			plan.Contingencies[0].Step.OfferingID != v180OfferingID ||
			plan.Contingencies[0].Step.Estimate.Micros != 6*micros {
			t.Fatalf("budget-pending contingency = %+v", plan.Contingencies[0])
		}

		closed, err := r.GetProductionBudget(ctx, v180OwnerID, "budget-closed")
		if err != nil {
			t.Fatalf("GetProductionBudget(budget-closed): %v", err)
		}
		if closed.State != budget.StateClosed || closed.DecidedBy != budget.SystemActor ||
			closed.DecidedAt == nil {
			t.Fatalf("budget-closed = %+v", closed)
		}
		// The pre-unit integer quote of an old plan is still read as legacy
		// credits, unchanged (#120).
		if got := closed.Plan.Steps[0].Estimate; got.Unit != budget.LegacyUnit || got.Micros != 12*micros {
			t.Fatalf("budget-closed step estimate = %+v; want 12 legacy credits", got)
		}

		denied, err := r.GetProductionBudget(ctx, v180OwnerID, "budget-denied")
		if err != nil {
			t.Fatalf("GetProductionBudget(budget-denied): %v", err)
		}
		if denied.State != budget.StateDenied || denied.DecidedBy != v180OwnerID || denied.DecidedAt == nil {
			t.Fatalf("budget-denied = %+v", denied)
		}
		if got := denied.Plan.Steps[0].Estimate; got.Unit != "meshy.credit" ||
			got.UnitLabel != "Meshy credits" || got.Micros != 4*micros {
			t.Fatalf("budget-denied step estimate = %+v", got)
		}

		// The active plan's summary totals its reservations per unit and never
		// adds different units together.
		summary, found, err := r.ProductionBudgetSummary(ctx, v180OwnerID, v180SessionID)
		if err != nil || !found {
			t.Fatalf("ProductionBudgetSummary(sess-a): found=%v err=%v", found, err)
		}
		if summary.ID != "budget-active" || summary.State != budget.StateActive ||
			len(summary.Units) != 2 || summary.DecidedAt == nil {
			t.Fatalf("budget-active summary = %+v", summary)
		}
		legacy, meshy := summary.Units[0], summary.Units[1]
		if legacy.Unit != budget.LegacyUnit || legacy.UnitLabel != budget.LegacyUnit ||
			legacy.Limit != 10*micros || legacy.Spent != 3*micros ||
			legacy.Reserved != 0 || legacy.Remaining != 7*micros {
			t.Fatalf("budget-active legacy unit = %+v", legacy)
		}
		if meshy.Unit != "meshy.credit" || meshy.UnitLabel != "Meshy credits" ||
			meshy.Limit != 8*micros || meshy.Spent != 0 ||
			meshy.Reserved != 1500000 || meshy.Remaining != 6500000 {
			t.Fatalf("budget-active meshy unit = %+v", meshy)
		}
		if summary.Plan.Steps[0].OfferingID != v180OfferingID ||
			summary.Plan.Steps[0].Estimate.Unit != "meshy.credit" ||
			summary.Plan.Steps[0].Estimate.Micros != 5500000 ||
			summary.Plan.Contingencies[0].Step.OfferingID != "tripo/geometry/decimate-quad" {
			t.Fatalf("budget-active summary plan = %+v", summary.Plan)
		}

		exhausted, found, err := r.ProductionBudgetSummary(ctx, v180OwnerID, "sess-budget-exhausted")
		if err != nil || !found {
			t.Fatalf("ProductionBudgetSummary(sess-budget-exhausted): found=%v err=%v", found, err)
		}
		if exhausted.State != budget.StateExhausted || len(exhausted.Units) != 1 ||
			exhausted.Units[0].Unit != budget.LegacyUnit ||
			exhausted.Units[0].Limit != 20*micros || exhausted.Units[0].Spent != 20*micros ||
			exhausted.Units[0].Reserved != 0 || exhausted.Units[0].Remaining != 0 {
			t.Fatalf("budget-exhausted summary = %+v", exhausted)
		}
	})

	t.Run("SkillRun 与步骤", func(t *testing.T) {
		run, err := r.GetSkillRun(ctx, generation.OwnerID(v180OwnerID), "run-1")
		if err != nil {
			t.Fatalf("GetSkillRun: %v", err)
		}
		if run.ID != "run-1" || run.RecipeID != "three-view" || run.SelectedOfferingID != v180OfferingID ||
			run.SourceAssetID != "asset-model-1" || run.SourceTaskID != "task-done" ||
			run.Harness.Revision != 2 || run.Harness.Digest != v180Digest {
			t.Fatalf("run = %+v", run)
		}
		if len(run.Steps) != 2 {
			t.Fatalf("run steps = %d; want 2", len(run.Steps))
		}
		front := run.Steps[0]
		if front.Spec.Key != "front" || front.Spec.OfferingID != v180OfferingID ||
			front.Phase != "output_ready" || front.GrantRequestID != "eg-bound" ||
			front.TaskID != "task-conv" || front.OutputAssetID != "asset-conv-1" ||
			front.Review != "accepted" || front.ReviewedAt == nil {
			t.Fatalf("run step[0] = %+v", front)
		}
		side := run.Steps[1]
		if side.Spec.Key != "side" || side.Phase != "planned" || side.Attempt != 1 ||
			side.Review != "unreviewed" || side.ReplacementAssetID != "input-crop-1" {
			t.Fatalf("run step[1] = %+v", side)
		}
	})

	t.Run("生成任务与资产", func(t *testing.T) {
		scope := generation.OwnTasks(generation.OwnerID(v180OwnerID))
		// Load reads the stored Task row as it is: the frozen Offering snapshot
		// must keep every field the release wrote, not only the summary
		// projection GetTaskSummary returns.
		loaded, err := r.Load(ctx, "task-done")
		if err != nil {
			t.Fatalf("Load(task-done): %v", err)
		}
		full := loaded.Task
		if full.Offering.Revision != v180OfferingRev || full.Offering.ProviderName != "Provider A" ||
			full.Offering.ModelName != "Model 3D A" || full.Offering.Capability != generation.ImageTo3D {
			t.Fatalf("task-done offering snapshot = %+v", full.Offering)
		}
		if string(full.Input) != `{"prompt":"合成概念车","source_asset":"input-1"}` {
			t.Fatalf("task-done input = %s", full.Input)
		}
		if loaded.CurrentAttempt == nil || loaded.CurrentAttempt.ID != "att-1" ||
			loaded.CurrentAttempt.State != generation.AttemptUpstreamSucceeded ||
			loaded.CurrentAttempt.UpstreamJob == nil ||
			loaded.CurrentAttempt.UpstreamJob.ProviderID != "provider-a" ||
			loaded.CurrentAttempt.UpstreamJob.RemoteID != "job-1" {
			t.Fatalf("task-done current attempt = %+v", loaded.CurrentAttempt)
		}
		done, err := r.GetTaskSummary(ctx, scope, "task-done")
		if err != nil {
			t.Fatalf("GetTaskSummary(task-done): %v", err)
		}
		if done.State != generation.TaskSucceeded || done.Capability != generation.ImageTo3D ||
			done.Offering.ID != v180OfferingID || done.Offering.ModelID != v180OfferingID ||
			done.Offering.ProviderID != "provider-a" ||
			done.SessionID != v180SessionID || done.Progress != 100 || done.Version != 7 ||
			done.FinishedAt == nil {
			t.Fatalf("task-done = %+v", done)
		}
		if string(done.Input) != `{}` {
			// The summary projection keeps only the declared prompt parameters;
			// the raw input column itself is covered by the row snapshot.
			t.Fatalf("task-done input projection = %s", done.Input)
		}
		if len(done.Assets) != 2 {
			t.Fatalf("task-done assets = %d; want 2", len(done.Assets))
		}
		model := done.Assets[0]
		if model.ID != "asset-model-1" || model.AttemptID != "att-1" || model.Role != "model" ||
			!model.Required || model.Variant != "glb" || model.MediaType != "model/gltf-binary" ||
			model.Size != 1024 || model.SHA256 != v180ShaModel ||
			model.StorageKey != "cas/aa/asset-model-1.glb" {
			t.Fatalf("task-done asset[0] = %+v", model)
		}
		if thumbnail := done.Assets[1]; thumbnail.Role != "thumbnail" || thumbnail.Required ||
			thumbnail.DeliveryProfile != "inline" {
			t.Fatalf("task-done asset[1] = %+v", thumbnail)
		}
		running, err := r.GetTaskSummary(ctx, scope, "task-running")
		if err != nil {
			t.Fatalf("GetTaskSummary(task-running): %v", err)
		}
		if running.State != generation.TaskRunning || running.CurrentAttemptID != "att-3" ||
			running.NextActionAt.IsZero() {
			t.Fatalf("task-running = %+v", running)
		}
		failed, err := r.GetTaskSummary(ctx, scope, "task-failed")
		if err != nil {
			t.Fatalf("GetTaskSummary(task-failed): %v", err)
		}
		if failed.State != generation.TaskFailed || failed.Failure == nil ||
			failed.Failure.Class != generation.FailureTransport || !failed.Failure.Retryable ||
			failed.Failure.SafeDetail != "合成上游超时" || failed.MaterializeFailures != 1 {
			t.Fatalf("task-failed = %+v", failed)
		}
		conversion, err := r.GetTaskSummary(ctx, scope, "task-conv")
		if err != nil {
			t.Fatalf("GetTaskSummary(task-conv): %v", err)
		}
		if conversion.Capability != generation.FormatConversion || len(conversion.Assets) != 1 ||
			conversion.Assets[0].ID != "asset-conv-1" {
			t.Fatalf("task-conv = %+v", conversion)
		}
	})

	t.Run("执行授权", func(t *testing.T) {
		for requestID, want := range map[string]struct {
			status  executiongrant.Status
			source  string
			task    string
			version int64
		}{
			"eg-pending":  {executiongrant.Pending, "human_browser", "", 1},
			"eg-approved": {executiongrant.Approved, "agent_human_decision", "", 3},
			"eg-claimed":  {executiongrant.Claimed, "human_browser", "", 2},
			"eg-bound":    {executiongrant.Bound, "agent_human_decision", "task-conv", 1},
			"eg-denied":   {executiongrant.Denied, "agent_human_decision", "", 3},
		} {
			grant, err := r.GetExecutionGrant(ctx, v180OwnerID, requestID)
			if err != nil {
				t.Fatalf("GetExecutionGrant(%s): %v", requestID, err)
			}
			if grant.Status != want.status || grant.Source != want.source ||
				grant.TaskID != want.task || grant.RequestVersion != want.version ||
				grant.AccessKeyID != "" || grant.SessionID != v180SessionID {
				t.Fatalf("%s = %+v", requestID, grant)
			}
		}
		approved, err := r.GetExecutionGrant(ctx, v180OwnerID, "eg-approved")
		if err != nil {
			t.Fatalf("GetExecutionGrant(eg-approved): %v", err)
		}
		if !approved.BudgetEligible || approved.DecidedBy != "policy:auto" ||
			approved.DecidedAt == nil || approved.TriggerTurnID != "msg-a2" ||
			approved.Harness.Revision != 2 || approved.Harness.Digest != v180Digest ||
			approved.PreparedInputID != "prep-1" || approved.PreparedInputHash != "hash-prep-1" {
			t.Fatalf("eg-approved = %+v", approved)
		}
	})

	t.Run("Agent 配置版本", func(t *testing.T) {
		latest, err := r.LatestAgentConfiguration(ctx)
		if err != nil {
			t.Fatalf("LatestAgentConfiguration: %v", err)
		}
		if latest.Revision != "2" || latest.ActorID != "owner-admin" || latest.Source != "publish" ||
			latest.Configuration.SystemPrompt != "合成系统提示词 v2" ||
			latest.Configuration.DomainContext["models"] != "synthetic" {
			t.Fatalf("latest agent configuration = %+v", latest)
		}
		if !reflect.DeepEqual(latest.Configuration.EnabledToolNames, []string{"generate_3d", "inspect_asset"}) {
			t.Fatalf("enabled tools = %v", latest.Configuration.EnabledToolNames)
		}
		history, err := r.AgentConfigurationHistory(ctx, 10)
		if err != nil {
			t.Fatalf("AgentConfigurationHistory: %v", err)
		}
		if len(history) != 2 || history[0].Revision != "2" || history[1].Revision != "1" {
			t.Fatalf("agent configuration history = %+v", history)
		}
	})
}

func assertV180Constraints(t *testing.T, r *Repository) {
	t.Helper()
	for _, probe := range []struct {
		name      string
		statement string
		want      string
	}{
		{"消息没有对应会话", `INSERT INTO go_chat_messages
		 (id, session_id, role, content, image_urls, tool_calls, created_at)
		 VALUES ('probe-msg', 'missing-session', 'user', 'x', '[]', '[]', '` + v180Stamp + `')`,
			"FOREIGN KEY"},
		{"回合没有对应助手消息", `INSERT INTO go_chat_turns
		 (assistant_message_id, session_id, owner_id, options_json, state, created_at, updated_at)
		 VALUES ('missing-message', 'sess-a', 'owner-a', '{}', 'done', '` + v180Stamp + `', '` + v180Stamp + `')`,
			"FOREIGN KEY"},
		{"Attempt 没有对应任务", `INSERT INTO generation_attempts
		 (id, task_id, ordinal, offering_json, state, submission_key, started_at, updated_at)
		 VALUES ('probe-att', 'missing-task', 9, '{}', 'RUNNING', 'probe', '` + v180Stamp + `', '` + v180Stamp + `')`,
			"FOREIGN KEY"},
		{"SkillRun 步骤引用不存在的任务", `INSERT INTO skill_run_steps
		 (run_id, ordinal, step_key, spec_json, phase, operation_id, idempotency_key, plan_step_id,
		  prepared_input_id, prepared_input_hash, grant_request_id, task_id, review)
		 VALUES ('run-1', 9, 'probe', '{}', 'task_bound', 'op-probe', 'idem-probe', 'probe',
		  'prep-1', 'hash-prep-1', 'probe-grant', 'missing-task', 'unreviewed')`,
			"FOREIGN KEY"},
		{"授权使用未知来源", `INSERT INTO execution_grants
		 (request_id, owner_id, session_id, offering_id, offering_revision, prepared_input_id,
		  prepared_input_hash, idempotency_key, operation_id, status, source, request_version,
		  created_at, updated_at)
		 VALUES ('probe-eg-source', 'owner-a', 'sess-a', 'o', 'rev', 'prep-1', 'hash-prep-1',
		  'idem-probe-source', 'op-probe-source', 'pending', 'robot', 1, '` + v180Stamp + `', '` + v180Stamp + `')`,
			"CHECK"},
		{"access_key 授权没有 Key", `INSERT INTO execution_grants
		 (request_id, owner_id, session_id, offering_id, offering_revision, prepared_input_id,
		  prepared_input_hash, idempotency_key, operation_id, status, source, request_version,
		  created_at, updated_at)
		 VALUES ('probe-eg-keyless', 'owner-a', 'sess-a', 'o', 'rev', 'prep-1', 'hash-prep-1',
		  'idem-probe-keyless', 'op-probe-keyless', 'pending', 'access_key', 1, '` + v180Stamp + `', '` + v180Stamp + `')`,
			"CHECK"},
		{"重复的会话消息 id", `INSERT INTO go_chat_messages
		 (id, session_id, role, content, image_urls, tool_calls, created_at)
		 VALUES ('msg-u1', 'sess-a', 'user', 'x', '[]', '[]', '` + v180Stamp + `')`,
			"UNIQUE"},
	} {
		_, err := r.db.Exec(probe.statement)
		if err == nil {
			t.Errorf("%s: 升级后的库接受了非法行", probe.name)
			continue
		}
		if !strings.Contains(strings.ToUpper(err.Error()), strings.ToUpper(probe.want)) {
			t.Errorf("%s: 被拒绝的原因不是 %s: %v", probe.name, probe.want, err)
		}
	}
	// 0097 的规则同样允许新的来源，只要行本身自洽。
	if _, err := r.db.Exec(`INSERT INTO execution_grants
		(request_id, owner_id, session_id, offering_id, offering_revision, prepared_input_id,
		 prepared_input_hash, idempotency_key, operation_id, status, source, request_version,
		 created_at, updated_at, access_key_id)
		VALUES ('probe-eg-keyed', 'owner-a', 'sess-a', 'o', 'rev', 'prep-1', 'hash-prep-1',
		 'idem-probe-keyed', 'op-probe-keyed', 'pending', 'access_key', 1, ?, ?, 'key-1')`,
		v180Stamp, v180Stamp); err != nil {
		t.Fatalf("access_key 来源的合法授权被拒绝: %v", err)
	}
	assertV180DatabaseIntegrity(t, r.db, "约束探针之后")
}

func assertV180HeadSchema(t *testing.T, r *Repository) {
	t.Helper()
	for _, table := range []string{
		"account_settings", "access_keys", "access_key_settings", "mcp_assets",
		"asset_lists", "asset_list_items", "asset_list_runs", "asset_list_presence",
		"asset_list_item_reviews", "input_asset_crops",
	} {
		var count int
		if err := r.db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
		).Scan(&count); err != nil || count != 1 {
			t.Fatalf("新增表 %s 缺失: count=%d err=%v", table, count, err)
		}
	}
	for _, probe := range []struct {
		table, column, defaultValue string
	}{
		{"go_chat_turns", "end_reason", "''"},
		{"execution_grants", "access_key_id", "''"},
		{"access_keys", "request_count", "0"},
		{"asset_list_items", "accepted_at", "''"},
		{"asset_list_items", "work_round", "0"},
		{"asset_list_items", "step_cap_turn_message_id", "''"},
		{"go_chat_sessions", "extra_steps", "0"},
		{"asset_list_item_reviews", "reasons_json", "'[]'"},
	} {
		var (
			notNull int
			dflt    sql.NullString
		)
		err := r.db.QueryRow(
			`SELECT "notnull", dflt_value FROM pragma_table_info(?) WHERE name = ?`,
			probe.table, probe.column,
		).Scan(&notNull, &dflt)
		if err != nil {
			t.Fatalf("%s.%s 缺失: %v", probe.table, probe.column, err)
		}
		if notNull != 1 || !dflt.Valid || dflt.String != probe.defaultValue {
			t.Fatalf("%s.%s = notnull %d default %v; want notnull 1 default %s",
				probe.table, probe.column, notNull, dflt, probe.defaultValue)
		}
	}
	// 旧行在新增列上取默认值，而不是空值或猜测值。只统计 v1.8.0 写入的行。
	var stale int
	if err := r.db.QueryRow(
		`SELECT (SELECT COUNT(*) FROM go_chat_turns WHERE end_reason <> '') +
		        (SELECT COUNT(*) FROM execution_grants
		         WHERE access_key_id <> ''
		           AND request_id IN ('eg-pending','eg-approved','eg-claimed','eg-bound','eg-denied')) +
		        (SELECT COUNT(*) FROM go_chat_sessions
		         WHERE extra_steps <> 0
		           AND id IN ('sess-a','sess-b','sess-budget','sess-budget-exhausted')) +
		        (SELECT COUNT(*) FROM asset_list_items
		         WHERE step_cap_turn_message_id <> '')`,
	).Scan(&stale); err != nil || stale != 0 {
		t.Fatalf("历史行在新增列上有非默认值: %d err=%v", stale, err)
	}
}
