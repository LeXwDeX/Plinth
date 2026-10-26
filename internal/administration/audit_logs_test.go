package administration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAuditServiceNormalizesQueryAndReturnsPage(t *testing.T) {
	repository := &auditRepositoryStub{
		events: []AuditEvent{{ID: "audit-1"}},
		total:  7,
	}
	service, err := NewAuditService(AuditServiceConfig{
		Repository:           repository,
		DefaultPage:          1,
		DefaultPageSize:      50,
		MaximumPageSize:      100,
		IDPrefix:             "audit_",
		WriteMaximumAttempts: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 7, 20, 8, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	to := from.Add(24 * time.Hour)

	page, err := service.QueryAuditEvents(context.Background(), AuditQuery{
		Page:     -4,
		PageSize: 999,
		UserID:   " user-1 ",
		Action:   " login ",
		From:     &from,
		To:       &to,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Page != 1 || page.PageSize != 100 || page.Total != 7 ||
		len(page.Events) != 1 || page.Events[0].ID != "audit-1" {
		t.Fatalf("page = %+v", page)
	}
	got := repository.query
	if got.Page != 1 || got.PageSize != 100 ||
		got.UserID != "user-1" || got.Action != "login" {
		t.Fatalf("normalized query = %+v", got)
	}
	if got.From == nil || !got.From.Equal(from.UTC()) ||
		got.To == nil || !got.To.Equal(to.UTC()) {
		t.Fatalf("normalized time range = %+v", got)
	}
}

func TestAuditServiceUsesConfiguredPageDefaults(t *testing.T) {
	repository := &auditRepositoryStub{}
	service, err := NewAuditService(AuditServiceConfig{
		Repository:           repository,
		DefaultPage:          2,
		DefaultPageSize:      25,
		MaximumPageSize:      75,
		IDPrefix:             "audit_",
		WriteMaximumAttempts: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	page, err := service.QueryAuditEvents(context.Background(), AuditQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Page != 2 || page.PageSize != 25 {
		t.Fatalf("page defaults = %+v", page)
	}
}

func TestAuditServicePropagatesRepositoryFailure(t *testing.T) {
	repository := &auditRepositoryStub{err: errors.New("storage failed")}
	service, err := NewAuditService(AuditServiceConfig{
		Repository:           repository,
		DefaultPage:          1,
		DefaultPageSize:      50,
		MaximumPageSize:      100,
		IDPrefix:             "audit_",
		WriteMaximumAttempts: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.QueryAuditEvents(context.Background(), AuditQuery{}); err == nil {
		t.Fatal("expected repository failure")
	}
}

func TestAuditServiceAssignsSchemaDrivenIdentityAndTimeWhenRecording(
	t *testing.T,
) {
	repository := &auditRepositoryStub{}
	now := time.Date(2026, 7, 26, 9, 30, 0, 0, time.FixedZone("CST", 8*60*60))
	service, err := NewAuditService(AuditServiceConfig{
		Repository:           repository,
		DefaultPage:          1,
		DefaultPageSize:      50,
		MaximumPageSize:      100,
		IDPrefix:             "event_",
		WriteMaximumAttempts: 1,
		Clock:                func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	err = service.RecordAudit(context.Background(), AuditEvent{
		Action:       "login",
		ActorID:      " user-1 ",
		ActorName:    " Alice ",
		ResourceType: " user ",
		ResourceID:   " user-1 ",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := repository.recorded
	if !strings.HasPrefix(got.ID, "event_") {
		t.Fatalf("audit id = %q", got.ID)
	}
	if !got.CreatedAt.Equal(now.UTC()) {
		t.Fatalf("created at = %v", got.CreatedAt)
	}
	if got.ActorID != "user-1" || got.ActorName != "Alice" ||
		got.ResourceType != "user" || got.ResourceID != "user-1" {
		t.Fatalf("normalized audit = %+v", got)
	}
}

func TestAuditServiceRetriesWritesWithConfiguredPolicy(t *testing.T) {
	repository := &auditRepositoryStub{
		recordErrors: []error{errors.New("temporary storage failure"), nil},
	}
	var slept []time.Duration
	service, err := NewAuditService(AuditServiceConfig{
		Repository:           repository,
		DefaultPage:          1,
		DefaultPageSize:      50,
		MaximumPageSize:      100,
		IDPrefix:             "audit_",
		WriteMaximumAttempts: 2,
		WriteRetryDelay:      250 * time.Millisecond,
		Sleeper: func(_ context.Context, delay time.Duration) error {
			slept = append(slept, delay)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := service.RecordAudit(
		context.Background(),
		AuditEvent{Action: "login"},
	); err != nil {
		t.Fatal(err)
	}
	if repository.recordCalls != 2 {
		t.Fatalf("record calls = %d", repository.recordCalls)
	}
	if len(slept) != 1 || slept[0] != 250*time.Millisecond {
		t.Fatalf("slept = %#v", slept)
	}
}

type auditRepositoryStub struct {
	query        AuditQuery
	events       []AuditEvent
	total        int
	err          error
	recorded     AuditEvent
	recordErrors []error
	recordCalls  int
}

func (repository *auditRepositoryStub) QueryAdministrationAuditEvents(
	_ context.Context,
	query AuditQuery,
) ([]AuditEvent, int, error) {
	repository.query = query
	return repository.events, repository.total, repository.err
}

func (repository *auditRepositoryStub) RecordAudit(
	_ context.Context,
	event AuditEvent,
) error {
	repository.recorded = event
	repository.recordCalls++
	if repository.recordCalls <= len(repository.recordErrors) {
		return repository.recordErrors[repository.recordCalls-1]
	}
	return repository.err
}
