package cachemanagement

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

type Stats struct {
	TotalFiles     int64
	TotalSizeBytes int64
}

type StatsSource interface {
	CacheStats(context.Context) (Stats, error)
}

type Operation struct {
	Filename   string
	TaskID     string
	TaskType   string
	ProviderID string
	AssetKind  string
	StartedAt  time.Time
}

type Status struct {
	Active int
	Items  []Operation
	Stats  Stats
}

type Config struct {
	Stats StatsSource
	Clock func() time.Time
}

type Service struct {
	stats  StatsSource
	clock  func() time.Time
	mutex  sync.RWMutex
	nextID uint64
	active map[uint64]Operation
}

func New(config Config) (*Service, error) {
	if config.Stats == nil {
		return nil, errors.New("cachemanagement: 缺少 StatsSource")
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	return &Service{
		stats:  config.Stats,
		clock:  config.Clock,
		active: map[uint64]Operation{},
	}, nil
}

func (s *Service) Begin(operation Operation) func() {
	s.mutex.Lock()
	s.nextID++
	id := s.nextID
	operation.StartedAt = s.clock().UTC()
	s.active[id] = operation
	s.mutex.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			s.mutex.Lock()
			delete(s.active, id)
			s.mutex.Unlock()
		})
	}
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	s.mutex.RLock()
	items := make([]Operation, 0, len(s.active))
	for _, operation := range s.active {
		items = append(items, operation)
	}
	s.mutex.RUnlock()
	sort.Slice(items, func(left int, right int) bool {
		if items[left].StartedAt.Equal(items[right].StartedAt) {
			return items[left].TaskID < items[right].TaskID
		}
		return items[left].StartedAt.Before(items[right].StartedAt)
	})
	stats, err := s.stats.CacheStats(ctx)
	if err != nil {
		return Status{}, err
	}
	return Status{
		Active: len(items),
		Items:  items,
		Stats:  stats,
	}, nil
}
