package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/administration"
)

func TestAdministrationUsageCombinesNativeAndLegacyWithoutDuplicates(
	t *testing.T,
) {
	path := filepath.Join(t.TempDir(), "usage.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	seedAdministrationGenerations(t, path)

	until := time.Date(
		2026,
		time.July,
		24,
		12,
		0,
		0,
		0,
		time.UTC,
	)
	buckets, err := repository.QueryAdministrationUsage(
		context.Background(),
		administration.UsageQuery{
			Since: until.Add(-2 * time.Hour),
			Until: &until,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 2 {
		t.Fatalf("usage buckets = %#v", buckets)
	}
	byUser := map[string]administration.UsageBucket{}
	for _, bucket := range buckets {
		byUser[bucket.UserID] = bucket
	}
	native := byUser["native-user"]
	if native.UserName != "Native Alice" ||
		native.UserEmail != "alice@example.test" ||
		native.TaskType != "text-to-image" ||
		native.TaskStatus != "SUCCEEDED" ||
		native.TaskCount != 1 ||
		native.EstimatedCredits != 7 ||
		!native.LastActiveAt.Equal(until) {
		t.Fatalf("native usage = %#v", native)
	}
	legacy := byUser["legacy-user"]
	if legacy.UserName != "Legacy Bob" ||
		legacy.TaskType != "image-to-3d" ||
		legacy.TaskCount != 1 ||
		legacy.EstimatedCredits != 20 ||
		!legacy.LastActiveAt.Equal(until.Add(-time.Hour)) {
		t.Fatalf("legacy usage = %#v", legacy)
	}

	since := until.Add(-30 * time.Minute)
	filtered, err := repository.QueryAdministrationUsage(
		context.Background(),
		administration.UsageQuery{Since: since, Until: &until},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].UserID != "native-user" {
		t.Fatalf("filtered usage = %#v", filtered)
	}
	owned, err := repository.QueryAdministrationUsage(
		context.Background(),
		administration.UsageQuery{
			UserID: "legacy-user",
			Since:  until.Add(-2 * time.Hour),
			Until:  &until,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(owned) != 1 || owned[0].UserID != "legacy-user" {
		t.Fatalf("owner-filtered usage = %#v", owned)
	}
}
