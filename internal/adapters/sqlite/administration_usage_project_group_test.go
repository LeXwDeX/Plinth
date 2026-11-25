package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/administration"
)

// #599: usage buckets carry the project group the Task's user is in now, joined
// live from authentication_users for both the native and the ledger-imported
// branch. A soft-deleted user keeps the group; a Task whose user record does not
// exist is ungrouped. Changing a user's group moves the user's whole history.
func TestAdministrationUsageJoinsUsersCurrentProjectGroup599(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "usage-groups.db")
	repository, err := sqlitetest.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	seedAdministrationGenerations(t, path)
	until := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	query := administration.UsageQuery{Since: until.Add(-2 * time.Hour), Until: &until}

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	exec := func(statement string, args ...any) {
		t.Helper()
		if _, err := raw.Exec(statement, args...); err != nil {
			t.Fatal(err)
		}
	}
	ledgerAt := until.Add(-30 * time.Minute)
	exec(`INSERT INTO authentication_users (
			id, username, nickname, name, email, roles_json,
			is_admin, is_active, created_at, updated_at, project_group_key
		) VALUES ('ledger-member', 'carol', 'Carol', '', 'carol@example.test', '["user"]', 0, 1, ?, ?, 'studio-b')`,
		ledgerAt.Format(time.RFC3339Nano), ledgerAt.Format(time.RFC3339Nano))
	exec(`INSERT INTO usage_ledger (
			id, userId, userName, userEmail, taskType, taskId,
			providerId, estimatedCredits, taskStatus, params,
			resultUrls, createdAt, finishedAt
		) VALUES ('member-ledger', 'ledger-member', 'Carol', 'carol@example.test', 'text-to-3d', 'member-task',
			'meshy', 30, 'SUCCEEDED', '{}', '[]', ?, ?)`,
		ledgerAt.UnixMilli(), ledgerAt.UnixMilli())

	baseline, err := repository.QueryAdministrationUsage(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	groupsOf := func(buckets []administration.UsageBucket) map[string]string {
		result := map[string]string{}
		for _, bucket := range buckets {
			result[bucket.UserID] = bucket.ProjectGroupKey
		}
		return result
	}
	if got := groupsOf(baseline); !reflect.DeepEqual(got, map[string]string{
		"native-user": "", "legacy-user": "", "ledger-member": "studio-b",
	}) {
		t.Fatalf("baseline groups = %#v", got)
	}

	exec(`UPDATE authentication_users SET project_group_key = 'studio-a' WHERE id = 'native-user'`)
	if err := repository.SoftDeleteAdministrationUser(ctx, "ledger-member", until, administration.UserGuards{}); err != nil {
		t.Fatal(err)
	}
	grouped, err := repository.QueryAdministrationUsage(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if got := groupsOf(grouped); !reflect.DeepEqual(got, map[string]string{
		"native-user": "studio-a", "legacy-user": "", "ledger-member": "studio-b",
	}) {
		t.Fatalf("native branch, soft-deleted and missing users: groups = %#v", got)
	}

	// The group join adds a column and changes nothing else of the buckets.
	strip := func(buckets []administration.UsageBucket) []administration.UsageBucket {
		result := make([]administration.UsageBucket, len(buckets))
		for index, bucket := range buckets {
			bucket.ProjectGroupKey = ""
			result[index] = bucket
		}
		return result
	}
	if !reflect.DeepEqual(strip(grouped), strip(baseline)) || len(grouped) != 3 {
		t.Fatalf("group join changed usage buckets:\n got %#v\nwant %#v", grouped, baseline)
	}

	exec(`UPDATE authentication_users SET project_group_key = 'studio-c' WHERE id = 'native-user'`)
	moved, err := repository.QueryAdministrationUsage(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if got := groupsOf(moved)["native-user"]; got != "studio-c" {
		t.Fatalf("history must follow the user's current group, got %q", got)
	}
}
