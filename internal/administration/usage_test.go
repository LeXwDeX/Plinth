package administration

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestUsageServiceAggregatesSchemaStatesAndSortsByCredits(
	t *testing.T,
) {
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	repository := &memoryUsageRepository{buckets: []UsageBucket{
		{
			UserID: "user-a", UserName: "Alice",
			UserEmail: "alice@example.test", TaskType: "text-to-image",
			TaskStatus: "DONE", TaskCount: 2, EstimatedCredits: 8,
			LastActiveAt: now.Add(-time.Hour),
		},
		{
			UserID: "user-a", UserName: "Alice",
			UserEmail: "alice@example.test", TaskType: "text-to-3d",
			TaskStatus: "BROKEN", TaskCount: 1, EstimatedCredits: 20,
			LastActiveAt: now.Add(-2 * time.Hour),
		},
		{
			UserID: "user-b", UserName: "Bob",
			UserEmail: "bob@example.test", TaskType: "text-to-image",
			TaskStatus: "RUNNING", TaskCount: 3, EstimatedCredits: 3,
			LastActiveAt: now,
		},
	}}
	service := newTestUsageService(t, repository, now)

	summary, err := service.Summarize(
		context.Background(),
		UsageRequest{Days: 7},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !repository.query.Since.Equal(now.AddDate(0, 0, -7)) ||
		repository.query.Until != nil {
		t.Fatalf("repository query = %+v", repository.query)
	}
	want := []UsageSummary{
		{
			UserID: "user-a", UserName: "Alice",
			UserEmail: "alice@example.test", TotalTasks: 3,
			SucceededTasks: 2, FailedTasks: 1, EstimatedCredits: 28,
			LastActiveAt: now.Add(-time.Hour),
			ByType: map[string]int{
				"text-to-image": 8,
				"text-to-3d":    20,
			},
		},
		{
			UserID: "user-b", UserName: "Bob",
			UserEmail: "bob@example.test", TotalTasks: 3,
			EstimatedCredits: 3, LastActiveAt: now,
			ByType: map[string]int{"text-to-image": 3},
		},
	}
	if !reflect.DeepEqual(summary, want) {
		t.Fatalf("summary = %#v, want %#v", summary, want)
	}
}

func TestUsageServiceBuildsInclusiveConfiguredDateRange(t *testing.T) {
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	repository := &memoryUsageRepository{}
	service := newTestUsageService(t, repository, now)

	_, err := service.Summarize(
		context.Background(),
		UsageRequest{
			StartDate: "2026-07-01",
			EndDate:   "2026-07-02",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	since := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(
		2026,
		time.July,
		2,
		23,
		59,
		59,
		int(time.Second-time.Nanosecond),
		time.UTC,
	)
	if !repository.query.Since.Equal(since) ||
		repository.query.Until == nil ||
		!repository.query.Until.Equal(until) {
		t.Fatalf("repository query = %+v", repository.query)
	}
}

func TestUsageServiceCarriesOwnerFilterIntoRepositoryQuery(t *testing.T) {
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	repository := &memoryUsageRepository{}
	service := newTestUsageService(t, repository, now)
	if _, err := service.Summarize(context.Background(), UsageRequest{UserID: " user-b ", Days: 3}); err != nil {
		t.Fatal(err)
	}
	if repository.query.UserID != "user-b" {
		t.Fatalf("owner filter = %q", repository.query.UserID)
	}
}

func TestUsageServiceRejectsReverseRangeAndWrapsRepositoryErrors(
	t *testing.T,
) {
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	repository := &memoryUsageRepository{}
	service := newTestUsageService(t, repository, now)

	_, err := service.Summarize(
		context.Background(),
		UsageRequest{
			StartDate: "2026-07-03",
			EndDate:   "2026-07-02",
		},
	)
	if !errors.Is(err, ErrUsageInvalidRange) {
		t.Fatalf("reverse range error = %v", err)
	}
	repository.err = errors.New("database unavailable")
	_, err = service.Summarize(
		context.Background(),
		UsageRequest{Days: 7},
	)
	if !errors.Is(err, ErrUsageFailed) {
		t.Fatalf("repository error = %v", err)
	}
}

func newTestUsageService(
	t *testing.T,
	repository UsageRepository,
	now time.Time,
) *UsageService {
	t.Helper()
	service, err := NewUsageService(UsageServiceConfig{
		Repository:      repository,
		Now:             func() time.Time { return now },
		DefaultDays:     7,
		DatePattern:     `^\d{4}-\d{2}-\d{2}$`,
		DateLayout:      "2006-01-02",
		Timezone:        "UTC",
		SucceededStates: []string{"DONE"},
		FailedStates:    []string{"BROKEN"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type memoryUsageRepository struct {
	buckets []UsageBucket
	query   UsageQuery
	err     error
}

func (repository *memoryUsageRepository) QueryAdministrationUsage(
	_ context.Context,
	query UsageQuery,
) ([]UsageBucket, error) {
	repository.query = query
	return append([]UsageBucket(nil), repository.buckets...),
		repository.err
}
