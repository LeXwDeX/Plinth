package provideraccounts

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// Reading is one account's balance as the Cache last saw it (#332). A balance
// that could not be read is Known == false: it is never a zero, and the caller
// must say "unknown" instead of inventing an amount.
type Reading struct {
	AccountID string
	Known     bool
	// Native is the exact amount in the provider's own unit, when the account's
	// reader offers one. Value is the plain balance of a reader that does not.
	Native *schema.NativeAmount
	Value  float64
	// ReadAt is when the provider was asked; for an unknown reading it is the
	// time of the failed attempt.
	ReadAt time.Time
}

// BalanceSource is what the Cache reads: the Service of the accounts.
type BalanceSource interface {
	NativeBalance(ctx context.Context, accountID string) (*schema.NativeAmount, error)
	Balance(ctx context.Context, accountID string) (float64, error)
}

var _ BalanceSource = (*Service)(nil)

// CacheConfig tunes a Cache. TTL and Timeout come from the Product Schema
// (application.step_policy.balance_cache_seconds and
// balance_read_timeout_seconds).
type CacheConfig struct {
	// TTL is how long a reading, including a failed one, is served before the
	// provider is asked again.
	TTL time.Duration
	// Timeout bounds one read of one account.
	Timeout time.Duration
	// Clock is the time source; nil means time.Now.
	Clock func() time.Time
	// Logger receives one warning per failed read; nil discards them.
	Logger *slog.Logger
}

// Cache is the short-lived, shared reader of provider balances. Many callers
// (the external MCP service, later the platform Agent) read the same accounts;
// the providers are asked at most once per TTL per account, however many ask.
// Reading never fails: an account that cannot be read answers Known == false
// and is not asked again until its TTL passes, so a provider that is down does
// not slow every call.
type Cache struct {
	source  BalanceSource
	ttl     time.Duration
	timeout time.Duration
	clock   func() time.Time
	logger  *slog.Logger

	mu       sync.Mutex
	entries  map[string]cacheEntry
	inflight map[string]*flight
}

type cacheEntry struct {
	reading Reading
	expires time.Time
}

type flight struct {
	done    chan struct{}
	reading Reading
}

// NewCache builds the reader; a source, a positive TTL and a positive timeout
// are required.
func NewCache(source BalanceSource, config CacheConfig) (*Cache, error) {
	if source == nil || config.TTL <= 0 || config.Timeout <= 0 {
		return nil, ErrInvalidCache
	}
	clock := config.Clock
	if clock == nil {
		clock = time.Now
	}
	return &Cache{
		source: source, ttl: config.TTL, timeout: config.Timeout, clock: clock, logger: config.Logger,
		entries: map[string]cacheEntry{}, inflight: map[string]*flight{},
	}, nil
}

// ErrInvalidCache means the cache was configured without a source, a TTL or a
// timeout.
var ErrInvalidCache = errors.New("provider accounts: invalid balance cache configuration")

// Request is one account's read. Plain asks for the plain number only, for a
// caller that shows the balance as a number whatever the reader could say in
// the provider's own unit; otherwise the exact native amount is read when the
// reader offers one (#385).
type Request struct {
	AccountID string
	Plain     bool
}

// Read returns one reading per account, in the order asked. The accounts are
// read concurrently.
func (c *Cache) Read(ctx context.Context, accountIDs []string) []Reading {
	requests := make([]Request, len(accountIDs))
	for index, id := range accountIDs {
		requests[index] = Request{AccountID: id}
	}
	return c.ReadRequests(ctx, requests)
}

// ReadRequests is Read for callers that need the plain number of some accounts.
// The two kinds of one account are cached apart: they are different reads of
// the provider.
func (c *Cache) ReadRequests(ctx context.Context, requests []Request) []Reading {
	readings := make([]Reading, len(requests))
	var group sync.WaitGroup
	for index, request := range requests {
		group.Add(1)
		go func() {
			defer group.Done()
			readings[index] = c.one(ctx, request)
		}()
	}
	group.Wait()
	return readings
}

func (c *Cache) one(ctx context.Context, request Request) Reading {
	id := request.AccountID
	key := id
	if request.Plain {
		key = id + "\x00plain"
	}
	c.mu.Lock()
	if entry, found := c.entries[key]; found && c.clock().Before(entry.expires) {
		c.mu.Unlock()
		return entry.reading
	}
	if running, found := c.inflight[key]; found {
		c.mu.Unlock()
		select {
		case <-running.done:
			return running.reading
		case <-ctx.Done():
			return Reading{AccountID: id, ReadAt: c.clock()}
		}
	}
	running := &flight{done: make(chan struct{})}
	c.inflight[key] = running
	c.mu.Unlock()

	// However the read ends, even in a panic of the reader, the flight is
	// completed so nobody waits on it for ever; a read that did not finish is
	// an unknown balance.
	reading := Reading{AccountID: id, ReadAt: c.clock()}
	defer func() {
		c.mu.Lock()
		c.entries[key] = cacheEntry{reading: reading, expires: reading.ReadAt.Add(c.ttl)}
		delete(c.inflight, key)
		c.mu.Unlock()
		running.reading = reading
		close(running.done)
	}()
	// The read is shared with whoever asks while it runs, so it must not be
	// cut short by the first caller's own deadline.
	reading = c.fetch(request)
	return reading
}

func (c *Cache) fetch(request Request) (reading Reading) {
	id := request.AccountID
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	reading = Reading{AccountID: id, ReadAt: c.clock()}
	// Each account is read on its own goroutine, where a panic of a provider's
	// reader would end the process; it is an unknown balance instead.
	defer func() {
		if recovered := recover(); recovered != nil {
			reading = Reading{AccountID: id, ReadAt: reading.ReadAt}
			if c.logger != nil {
				c.logger.Error("Provider 余额读取异常", "account_id", id, "panic", recovered)
			}
		}
	}()
	var native *schema.NativeAmount
	var err error
	if !request.Plain {
		native, err = c.source.NativeBalance(ctx, id)
	}
	if err == nil && native != nil {
		reading.Known, reading.Native = true, native
		return reading
	}
	if err == nil {
		var value float64
		if value, err = c.source.Balance(ctx, id); err == nil {
			reading.Known, reading.Value = true, value
			return reading
		}
	}
	if c.logger != nil {
		c.logger.Warn("Provider 余额读取失败", "account_id", id, "error", err)
	}
	return reading
}
