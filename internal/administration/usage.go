package administration

import (
	"context"
	"errors"
	"fmt"
	"github.com/LeXwDeX/Plinth/internal/schema"
	"regexp"
	"sort"
	"strings"
	"time"
)

var (
	ErrUsageInvalidRange = errors.New(
		"administration: usage range is invalid",
	)
	ErrUsageFailed = errors.New(
		"administration: usage query failed",
	)
)

type UsageQuery struct {
	UserID string
	Since  time.Time
	Until  *time.Time
}

type UsageBucket struct {
	NativeQuote      *schema.NativeQuote
	UserID           string
	UserName         string
	UserEmail        string
	TaskType         string
	TaskStatus       string
	TaskCount        int
	EstimatedCredits int
	LastActiveAt     time.Time
	// Uncharged marks Tasks that failed or were canceled before their
	// upstream finished: they count as tasks but add no credits (#327), the
	// rule the session total follows.
	Uncharged bool
}

type UsageRepository interface {
	QueryAdministrationUsage(
		context.Context,
		UsageQuery,
	) ([]UsageBucket, error)
}

type UsageRequest struct {
	UserID    string
	Days      int
	StartDate string
	EndDate   string
}

type UsageSummary struct {
	PriceTotals      []schema.NativeAmount
	UserID           string
	UserName         string
	UserEmail        string
	TotalTasks       int
	SucceededTasks   int
	FailedTasks      int
	EstimatedCredits int
	LastActiveAt     time.Time
	ByType           map[string]int
}

// UsageTotal is the spent-credit estimate of every listed user together
// (#327): the plain credit estimate, or, once any Task carries a
// Provider-native quote, the amounts per unit the way each user's row states
// them (legacy credits under "legacy.credits").
type UsageTotal struct {
	EstimatedCredits int
	PriceTotals      []schema.NativeAmount
}

// UsageOverview is the per-user statistics and their total.
type UsageOverview struct {
	Users []UsageSummary
	Total UsageTotal
}

type UsageServiceConfig struct {
	Repository      UsageRepository
	Now             func() time.Time
	DefaultDays     int
	DatePattern     string
	DateLayout      string
	Timezone        string
	SucceededStates []string
	FailedStates    []string
}

type UsageService struct {
	repository      UsageRepository
	now             func() time.Time
	defaultDays     int
	datePattern     *regexp.Regexp
	dateLayout      string
	location        *time.Location
	succeededStates map[string]struct{}
	failedStates    map[string]struct{}
}

func NewUsageService(
	config UsageServiceConfig,
) (*UsageService, error) {
	if config.Repository == nil {
		return nil, errors.New(
			"administration: usage repository is missing",
		)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.DefaultDays <= 0 ||
		strings.TrimSpace(config.DatePattern) == "" ||
		strings.TrimSpace(config.DateLayout) == "" ||
		strings.TrimSpace(config.Timezone) == "" ||
		len(config.SucceededStates) == 0 ||
		len(config.FailedStates) == 0 {
		return nil, errors.New(
			"administration: usage configuration is invalid",
		)
	}
	datePattern, err := regexp.Compile(config.DatePattern)
	if err != nil {
		return nil, fmt.Errorf(
			"administration: compile usage date pattern: %w",
			err,
		)
	}
	location, err := time.LoadLocation(config.Timezone)
	if err != nil {
		return nil, fmt.Errorf(
			"administration: load usage timezone: %w",
			err,
		)
	}
	succeededStates, err := usageStateSet(config.SucceededStates)
	if err != nil {
		return nil, err
	}
	failedStates, err := usageStateSet(config.FailedStates)
	if err != nil {
		return nil, err
	}
	return &UsageService{
		repository:      config.Repository,
		now:             config.Now,
		defaultDays:     config.DefaultDays,
		datePattern:     datePattern,
		dateLayout:      config.DateLayout,
		location:        location,
		succeededStates: succeededStates,
		failedStates:    failedStates,
	}, nil
}

func (service *UsageService) Summarize(
	ctx context.Context,
	request UsageRequest,
) ([]UsageSummary, error) {
	overview, err := service.Overview(ctx, request)
	if err != nil {
		return nil, err
	}
	return overview.Users, nil
}

// Overview is Summarize plus the total of all listed users.
func (service *UsageService) Overview(
	ctx context.Context,
	request UsageRequest,
) (UsageOverview, error) {
	query, err := service.query(request)
	if err != nil {
		return UsageOverview{}, err
	}
	buckets, err := service.repository.QueryAdministrationUsage(
		ctx,
		query,
	)
	if err != nil {
		return UsageOverview{}, fmt.Errorf("%w: %v", ErrUsageFailed, err)
	}
	byUser := make(map[string]*UsageSummary)
	amounts := map[string]*schema.PriceAccumulator{}
	all := &schema.PriceAccumulator{}
	for _, bucket := range buckets {
		entry := byUser[bucket.UserID]
		if entry == nil {
			entry = &UsageSummary{
				UserID:       bucket.UserID,
				UserName:     bucket.UserName,
				UserEmail:    bucket.UserEmail,
				LastActiveAt: bucket.LastActiveAt,
				ByType:       map[string]int{},
			}
			byUser[bucket.UserID] = entry
		}
		if bucket.LastActiveAt.After(entry.LastActiveAt) {
			entry.UserName = bucket.UserName
			entry.UserEmail = bucket.UserEmail
			entry.LastActiveAt = bucket.LastActiveAt
		}
		entry.TotalTasks += bucket.TaskCount
		a := amounts[bucket.UserID]
		if a == nil {
			a = &schema.PriceAccumulator{}
			amounts[bucket.UserID] = a
		}
		if !bucket.Uncharged {
			if e := a.Add(bucket.EstimatedCredits, bucket.NativeQuote); e != nil {
				return UsageOverview{}, fmt.Errorf("%w: price", ErrUsageFailed)
			}
			if e := all.Add(bucket.EstimatedCredits, bucket.NativeQuote); e != nil {
				return UsageOverview{}, fmt.Errorf("%w: price", ErrUsageFailed)
			}
			if bucket.NativeQuote == nil {
				entry.ByType[bucket.TaskType] += bucket.EstimatedCredits
			}
		}
		entry.EstimatedCredits = a.Legacy
		if _, exists := service.succeededStates[bucket.TaskStatus]; exists {
			entry.SucceededTasks += bucket.TaskCount
		}
		if _, exists := service.failedStates[bucket.TaskStatus]; exists {
			entry.FailedTasks += bucket.TaskCount
		}
	}
	hasNative := false
	result := make([]UsageSummary, 0, len(byUser))
	for _, entry := range byUser {
		totals, e := amounts[entry.UserID].Totals()
		if e != nil {
			return UsageOverview{}, fmt.Errorf("%w: price", ErrUsageFailed)
		}
		entry.PriceTotals = totals
		hasNative = hasNative || len(totals) > 0
		result = append(result, *entry)
	}
	sort.Slice(result, func(left int, right int) bool {
		if !hasNative && result[left].EstimatedCredits != result[right].EstimatedCredits {
			return result[left].EstimatedCredits >
				result[right].EstimatedCredits
		}
		return result[left].UserID < result[right].UserID
	})
	totals, err := all.Totals()
	if err != nil {
		return UsageOverview{}, fmt.Errorf("%w: price", ErrUsageFailed)
	}
	return UsageOverview{Users: result, Total: UsageTotal{EstimatedCredits: all.Legacy, PriceTotals: totals}}, nil
}

func (service *UsageService) query(
	request UsageRequest,
) (UsageQuery, error) {
	startDate := strings.TrimSpace(request.StartDate)
	endDate := strings.TrimSpace(request.EndDate)
	if startDate != "" &&
		endDate != "" &&
		service.datePattern.MatchString(startDate) &&
		service.datePattern.MatchString(endDate) {
		since, startErr := time.ParseInLocation(
			service.dateLayout,
			startDate,
			service.location,
		)
		untilDay, endErr := time.ParseInLocation(
			service.dateLayout,
			endDate,
			service.location,
		)
		if startErr != nil || endErr != nil || untilDay.Before(since) {
			return UsageQuery{}, ErrUsageInvalidRange
		}
		until := untilDay.AddDate(0, 0, 1).Add(-time.Nanosecond)
		return UsageQuery{UserID: strings.TrimSpace(request.UserID), Since: since, Until: &until}, nil
	}
	days := request.Days
	if days == 0 {
		days = service.defaultDays
	}
	return UsageQuery{
		UserID: strings.TrimSpace(request.UserID),
		Since:  service.now().In(service.location).AddDate(0, 0, -days),
	}, nil
}

func usageStateSet(values []string) (map[string]struct{}, error) {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, errors.New(
				"administration: usage state is empty",
			)
		}
		result[value] = struct{}{}
	}
	return result, nil
}
