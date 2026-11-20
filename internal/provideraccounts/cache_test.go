package provideraccounts_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/provideraccounts"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type fakeSource struct {
	calls   atomic.Int32
	native  map[string]*schema.NativeAmount
	plain   map[string]float64
	failing atomic.Bool
	block   chan struct{}
}

func (s *fakeSource) NativeBalance(ctx context.Context, id string) (*schema.NativeAmount, error) {
	s.calls.Add(1)
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if s.failing.Load() {
		return nil, errors.New("upstream down")
	}
	if _, found := s.native[id]; !found {
		if _, plain := s.plain[id]; !plain {
			return nil, provideraccounts.ErrAccountNotFound
		}
	}
	return s.native[id], nil
}

func (s *fakeSource) Balance(_ context.Context, id string) (float64, error) {
	value, found := s.plain[id]
	if !found {
		return 0, provideraccounts.ErrAccountNotFound
	}
	return value, nil
}

func newCache(t *testing.T, source *fakeSource, clock *fakeClock) *provideraccounts.Cache {
	t.Helper()
	cache, err := provideraccounts.NewCache(source, provideraccounts.CacheConfig{
		TTL: 30 * time.Second, Timeout: time.Second, Clock: clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return cache
}

func TestCacheServesAReadingWithinTheTTLAndAsksAgainAfterIt(t *testing.T) {
	t.Parallel()
	clock := &fakeClock{now: time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)}
	source := &fakeSource{native: map[string]*schema.NativeAmount{"meshy": {Unit: "meshy.credit", UnitLabel: "Meshy credits", Amount: "1000"}}}
	cache := newCache(t, source, clock)

	first := cache.Read(context.Background(), []string{"meshy"})
	if len(first) != 1 || !first[0].Known || first[0].Native == nil || first[0].Native.Amount != "1000" || !first[0].ReadAt.Equal(clock.Now()) {
		t.Fatalf("first = %+v", first)
	}
	clock.Advance(29 * time.Second)
	source.native["meshy"] = &schema.NativeAmount{Unit: "meshy.credit", UnitLabel: "Meshy credits", Amount: "999"}
	again := cache.Read(context.Background(), []string{"meshy"})
	if source.calls.Load() != 1 || again[0].Native.Amount != "1000" {
		t.Fatalf("within the TTL: calls=%d reading=%+v; the cached value must be served", source.calls.Load(), again[0])
	}
	clock.Advance(2 * time.Second)
	after := cache.Read(context.Background(), []string{"meshy"})
	if source.calls.Load() != 2 || after[0].Native.Amount != "999" {
		t.Fatalf("after the TTL: calls=%d reading=%+v; the provider must be asked again", source.calls.Load(), after[0])
	}
}

func TestCacheFailureIsUnknownNeverZeroAndIsKeptForTheTTL(t *testing.T) {
	t.Parallel()
	clock := &fakeClock{now: time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)}
	source := &fakeSource{native: map[string]*schema.NativeAmount{"tripo": {Unit: "tripo.credit", UnitLabel: "Tripo credits", Amount: "50"}}}
	source.failing.Store(true)
	cache := newCache(t, source, clock)

	reading := cache.Read(context.Background(), []string{"tripo"})[0]
	if reading.Known || reading.Native != nil || reading.Value != 0 {
		t.Fatalf("a failed read = %+v; want unknown with no amount", reading)
	}
	cache.Read(context.Background(), []string{"tripo"})
	if source.calls.Load() != 1 {
		t.Fatalf("a failing provider was asked %d times inside the TTL", source.calls.Load())
	}
	source.failing.Store(false)
	clock.Advance(31 * time.Second)
	if recovered := cache.Read(context.Background(), []string{"tripo"})[0]; !recovered.Known || recovered.Native.Amount != "50" {
		t.Fatalf("recovered = %+v", recovered)
	}
}

func TestCacheReadsPlainBalancesAndUnknownAccounts(t *testing.T) {
	t.Parallel()
	clock := &fakeClock{now: time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)}
	source := &fakeSource{plain: map[string]float64{"hyper3d": 12.5}}
	cache := newCache(t, source, clock)
	readings := cache.Read(context.Background(), []string{"hyper3d", "nobody"})
	if !readings[0].Known || readings[0].Native != nil || readings[0].Value != 12.5 || readings[0].AccountID != "hyper3d" {
		t.Fatalf("plain = %+v", readings[0])
	}
	if readings[1].Known || readings[1].AccountID != "nobody" {
		t.Fatalf("unknown account = %+v", readings[1])
	}
}

func TestCacheBoundsASlowReadAndSharesOneReadBetweenCallers(t *testing.T) {
	t.Parallel()
	clock := &fakeClock{now: time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)}
	source := &fakeSource{native: map[string]*schema.NativeAmount{"meshy": {Unit: "u", UnitLabel: "U", Amount: "1"}}, block: make(chan struct{})}
	cache, err := provideraccounts.NewCache(source, provideraccounts.CacheConfig{TTL: time.Minute, Timeout: 50 * time.Millisecond, Clock: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	results := make([]provideraccounts.Reading, 5)
	for index := range results {
		group.Add(1)
		go func() {
			defer group.Done()
			results[index] = cache.Read(context.Background(), []string{"meshy"})[0]
		}()
	}
	group.Wait()
	for index, reading := range results {
		if reading.Known {
			t.Fatalf("reading %d = %+v; a hung provider must read as unknown", index, reading)
		}
	}
	if calls := source.calls.Load(); calls != 1 {
		t.Fatalf("five concurrent callers made %d provider reads; want one shared read", calls)
	}
}

func TestCacheCallerGivesUpWithoutBlockingOnAnotherCallersRead(t *testing.T) {
	t.Parallel()
	clock := &fakeClock{now: time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)}
	source := &fakeSource{native: map[string]*schema.NativeAmount{"meshy": {Unit: "u", UnitLabel: "U", Amount: "1"}}, block: make(chan struct{})}
	cache, err := provideraccounts.NewCache(source, provideraccounts.CacheConfig{TTL: time.Minute, Timeout: 5 * time.Second, Clock: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	go cache.Read(context.Background(), []string{"meshy"})
	deadline := time.Now().Add(2 * time.Second)
	for source.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if reading := cache.Read(ctx, []string{"meshy"})[0]; reading.Known {
		t.Fatalf("a caller whose context ended read %+v", reading)
	}
	close(source.block)
}

func TestNewCacheRequiresASourceTheTTLAndTheTimeout(t *testing.T) {
	t.Parallel()
	source := &fakeSource{}
	for name, config := range map[string]provideraccounts.CacheConfig{
		"no ttl":     {Timeout: time.Second},
		"no timeout": {TTL: time.Second},
	} {
		if _, err := provideraccounts.NewCache(source, config); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := provideraccounts.NewCache(nil, provideraccounts.CacheConfig{TTL: time.Second, Timeout: time.Second}); err == nil {
		t.Error("a nil source was accepted")
	}
}

type panickingSource struct{}

func (panickingSource) NativeBalance(context.Context, string) (*schema.NativeAmount, error) {
	panic("reader bug")
}

func (panickingSource) Balance(context.Context, string) (float64, error) { return 0, nil }

// A reader that panics is an unknown balance, not the end of the process, and
// leaves nobody waiting on its read.
func TestCacheTurnsAReaderPanicIntoAnUnknownBalance(t *testing.T) {
	t.Parallel()
	cache, err := provideraccounts.NewCache(panickingSource{}, provideraccounts.CacheConfig{TTL: time.Minute, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if reading := cache.Read(context.Background(), []string{"meshy"})[0]; reading.Known || reading.AccountID != "meshy" {
			t.Fatalf("reading = %+v", reading)
		}
	}
}

// #385: the web balance route shows an account that is not priced in its
// provider's own unit as a plain number. That read never asks for the native
// amount, and it is cached apart from the native one: they are different reads.
func TestCachePlainRequestReadsThePlainNumberAndIsCachedApart(t *testing.T) {
	t.Parallel()
	clock := &fakeClock{now: time.Unix(1_000, 0)}
	source := &fakeSource{
		native: map[string]*schema.NativeAmount{"meshy": {Unit: "meshy.credit", UnitLabel: "Meshy credits", Amount: "1000"}},
		plain:  map[string]float64{"meshy": 88.5},
	}
	cache := newCache(t, source, clock)

	plain := cache.ReadRequests(context.Background(), []provideraccounts.Request{{AccountID: "meshy", Plain: true}})[0]
	if !plain.Known || plain.Native != nil || plain.Value != 88.5 {
		t.Fatalf("plain reading = %+v", plain)
	}
	if source.calls.Load() != 0 {
		t.Fatalf("a plain read asked for the native amount (%d calls)", source.calls.Load())
	}
	native := cache.Read(context.Background(), []string{"meshy"})[0]
	if !native.Known || native.Native == nil || native.Native.Amount != "1000" {
		t.Fatalf("native reading = %+v", native)
	}
	// Both are kept: neither asks the provider again inside the TTL.
	cache.ReadRequests(context.Background(), []provideraccounts.Request{{AccountID: "meshy", Plain: true}})
	cache.Read(context.Background(), []string{"meshy"})
	if source.calls.Load() != 1 {
		t.Fatalf("native amount read %d times inside the TTL, want 1", source.calls.Load())
	}
}
