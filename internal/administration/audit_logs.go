package administration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AuditQuery 是管理审计域的稳定查询接口。HTTP 查询字段名由 Product Schema
// 负责翻译，存储适配器只消费这里的领域值。
type AuditQuery struct {
	Page     int
	PageSize int
	UserID   string
	Action   string
	From     *time.Time
	To       *time.Time
}

type AuditPage struct {
	Events   []AuditEvent
	Total    int
	Page     int
	PageSize int
}

type AuditRepository interface {
	QueryAdministrationAuditEvents(
		context.Context,
		AuditQuery,
	) ([]AuditEvent, int, error)
	RecordAudit(context.Context, AuditEvent) error
}

type AuditServiceConfig struct {
	Repository           AuditRepository
	DefaultPage          int
	DefaultPageSize      int
	MaximumPageSize      int
	IDPrefix             string
	WriteMaximumAttempts int
	WriteRetryDelay      time.Duration
	Clock                func() time.Time
	Sleeper              func(context.Context, time.Duration) error
}

type AuditService struct {
	repository           AuditRepository
	defaultPage          int
	defaultPageSize      int
	maximumPageSize      int
	idPrefix             string
	clock                func() time.Time
	writeMaximumAttempts int
	writeRetryDelay      time.Duration
	sleeper              func(context.Context, time.Duration) error
}

func NewAuditService(config AuditServiceConfig) (*AuditService, error) {
	if config.Repository == nil {
		return nil, errors.New("administration: 缺少 AuditRepository")
	}
	if config.DefaultPage <= 0 ||
		config.DefaultPageSize <= 0 ||
		config.MaximumPageSize < config.DefaultPageSize {
		return nil, errors.New("administration: 审计分页配置无效")
	}
	if strings.TrimSpace(config.IDPrefix) == "" {
		return nil, errors.New("administration: 审计 ID 前缀不能为空")
	}
	if config.WriteMaximumAttempts <= 0 || config.WriteRetryDelay < 0 {
		return nil, errors.New("administration: 审计写入重试配置无效")
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	if config.Sleeper == nil {
		config.Sleeper = sleepAuditRetry
	}
	return &AuditService{
		repository:           config.Repository,
		defaultPage:          config.DefaultPage,
		defaultPageSize:      config.DefaultPageSize,
		maximumPageSize:      config.MaximumPageSize,
		idPrefix:             config.IDPrefix,
		clock:                config.Clock,
		writeMaximumAttempts: config.WriteMaximumAttempts,
		writeRetryDelay:      config.WriteRetryDelay,
		sleeper:              config.Sleeper,
	}, nil
}

func (service *AuditService) RecordAudit(
	ctx context.Context,
	event AuditEvent,
) error {
	if strings.TrimSpace(event.ID) == "" {
		id, err := randomUserID(service.idPrefix)
		if err != nil {
			return fmt.Errorf("administration: create audit id: %w", err)
		}
		event.ID = id
	}
	event.Action = strings.TrimSpace(event.Action)
	event.ActorID = strings.TrimSpace(event.ActorID)
	event.ActorName = strings.TrimSpace(event.ActorName)
	event.ResourceType = strings.TrimSpace(event.ResourceType)
	event.ResourceID = strings.TrimSpace(event.ResourceID)
	event.IPAddress = strings.TrimSpace(event.IPAddress)
	event.UserAgent = strings.TrimSpace(event.UserAgent)
	if event.CreatedAt.IsZero() {
		event.CreatedAt = service.clock().UTC()
	} else {
		event.CreatedAt = event.CreatedAt.UTC()
	}
	var err error
	for attempt := 1; attempt <= service.writeMaximumAttempts; attempt++ {
		err = service.repository.RecordAudit(ctx, event)
		if err == nil {
			return nil
		}
		if attempt < service.writeMaximumAttempts {
			if sleepErr := service.sleeper(
				ctx,
				service.writeRetryDelay,
			); sleepErr != nil {
				return fmt.Errorf(
					"administration: wait to retry audit event: %w",
					sleepErr,
				)
			}
		}
	}
	return fmt.Errorf("administration: record audit event: %w", err)
}

func sleepAuditRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (service *AuditService) QueryAuditEvents(
	ctx context.Context,
	query AuditQuery,
) (AuditPage, error) {
	query.UserID = strings.TrimSpace(query.UserID)
	query.Action = strings.TrimSpace(query.Action)
	if query.Page <= 0 {
		query.Page = service.defaultPage
	}
	if query.PageSize <= 0 {
		query.PageSize = service.defaultPageSize
	}
	if query.PageSize > service.maximumPageSize {
		query.PageSize = service.maximumPageSize
	}
	query.From = utcTimePointer(query.From)
	query.To = utcTimePointer(query.To)

	events, total, err := service.repository.
		QueryAdministrationAuditEvents(ctx, query)
	if err != nil {
		return AuditPage{}, fmt.Errorf(
			"administration: query audit events: %w",
			err,
		)
	}
	return AuditPage{
		Events:   events,
		Total:    total,
		Page:     query.Page,
		PageSize: query.PageSize,
	}, nil
}

func utcTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := value.UTC()
	return &normalized
}
