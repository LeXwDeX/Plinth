package upstreampool

import (
	"context"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

func newTestManager(limit int, idleTTL time.Duration) (*Manager, *fakeClock) {
	clock := &fakeClock{at: time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)}
	m := New(Options{Now: clock.now, IdleTTL: idleTTL, WaitHint: 2 * time.Second})
	m.Register("tripo.v3", "model-processing", PoolConfig{Limit: limit})
	return m, clock
}

func holder(attempt string) Holder {
	return Holder{Connection: "tripo.v3", Pool: "model-processing", Attempt: attempt}
}

func TestAcquireFillsToLimitThenQueues(t *testing.T) {
	m, _ := newTestManager(2, time.Hour)
	ctx := context.Background()
	first, ok, _ := m.Acquire(ctx, holder("a"))
	if !ok || !first.Active() {
		t.Fatalf("first acquire = %v %v", ok, first.Active())
	}
	if _, ok, _ := m.Acquire(ctx, holder("b")); !ok {
		t.Fatal("second acquire should fit")
	}
	ticket, ok, hint := m.Acquire(ctx, holder("c"))
	if ok || ticket.Active() || hint <= 0 {
		t.Fatalf("third acquire = %v %v %v, want queued with a wait hint", ok, ticket.Active(), hint)
	}
	stats := m.Stats()
	if stats.TotalHeld != 2 || stats.TotalWaiting != 1 {
		t.Fatalf("stats = %+v, want 2 held 1 waiting", stats)
	}
}

func TestAcquireIsIdempotentPerAttempt(t *testing.T) {
	m, _ := newTestManager(1, time.Hour)
	ctx := context.Background()
	first, ok, _ := m.Acquire(ctx, holder("a"))
	if !ok {
		t.Fatal("first acquire should fit")
	}
	second, ok, _ := m.Acquire(ctx, holder("a"))
	if !ok || second.token != first.token {
		t.Fatalf("repeat acquire = %v token %d, want the same ticket", ok, second.token)
	}
	if stats := m.Stats(); stats.TotalHeld != 1 {
		t.Fatalf("repeat acquire counted twice: %+v", stats)
	}
}

func TestLateReleaseDoesNotFreeAnotherHoldersSlot(t *testing.T) {
	m, _ := newTestManager(1, time.Hour)
	ctx := context.Background()
	a, _, _ := m.Acquire(ctx, holder("a"))
	m.Release(a)
	b, ok, _ := m.Acquire(ctx, holder("b"))
	if !ok {
		t.Fatal("b should fit after a released")
	}
	// A's late release must not free B's slot.
	m.Release(a)
	if _, ok, _ := m.Acquire(ctx, holder("c")); ok {
		t.Fatal("late release freed a slot B owns")
	}
	m.Release(b)
	if _, ok, _ := m.Acquire(ctx, holder("c")); !ok {
		t.Fatal("releasing B should free the slot")
	}
}

func TestReleaseAdmitsFIFO(t *testing.T) {
	m, _ := newTestManager(1, time.Hour)
	ctx := context.Background()
	a, _, _ := m.Acquire(ctx, holder("a"))
	if _, ok, _ := m.Acquire(ctx, holder("first")); ok {
		t.Fatal("first waiter should queue")
	}
	if _, ok, _ := m.Acquire(ctx, holder("second")); ok {
		t.Fatal("second waiter should queue")
	}
	m.Release(a)
	// Releasing a slot admits the earliest waiter even before it calls again.
	stats := m.Stats()
	if stats.TotalHeld != 1 || stats.TotalWaiting != 1 {
		t.Fatalf("after release stats = %+v, want 1 held 1 waiting", stats)
	}
	// The earliest waiter gets the slot when it calls Acquire.
	ticket, ok, _ := m.Acquire(ctx, holder("first"))
	if !ok || !ticket.Active() {
		t.Fatalf("first waiter = %v, want admitted", ok)
	}
	// The later waiter is still queued.
	if _, ok, _ := m.Acquire(ctx, holder("second")); ok {
		t.Fatal("later waiter jumped the queue")
	}
}

func TestReadoptRestoresWithoutLimit(t *testing.T) {
	m, _ := newTestManager(1, time.Hour)
	m.Readopt(holder("a"), holder("b"), holder("c"))
	stats := m.Stats()
	if stats.TotalHeld != 3 {
		t.Fatalf("readopt = %+v, want 3 held beyond the limit", stats)
	}
	// A fresh holder still queues while the readopted entries hold the pool.
	if _, ok, _ := m.Acquire(context.Background(), holder("d")); ok {
		t.Fatal("readopted entries should occupy the pool")
	}
}

func TestTouchKeepsPollingHolderAlive(t *testing.T) {
	m, clock := newTestManager(1, time.Minute)
	ctx := context.Background()
	if _, ok, _ := m.Acquire(ctx, holder("a")); !ok {
		t.Fatal("acquire")
	}
	// Half the idle TTL passes, then the holder polls.
	clock.advance(30 * time.Second)
	m.Touch(holder("a"))
	clock.advance(40 * time.Second)
	// The holder is still live: a fresh acquire for another attempt queues
	// rather than reclaiming a.
	if _, ok, _ := m.Acquire(ctx, holder("b")); ok {
		t.Fatal("holder a was reclaimed while polling")
	}
	// Without a touch, the idle TTL reclaims it.
	clock.advance(2 * time.Minute)
	if _, ok, _ := m.Acquire(ctx, holder("c")); !ok {
		t.Fatal("idle holder was not reclaimed")
	}
}

func TestMaxLifetimeReclaimsEvenWhenTouched(t *testing.T) {
	clock := &fakeClock{at: time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)}
	m := New(Options{Now: clock.now, IdleTTL: time.Hour})
	m.Register("tripo.v3", "model-processing", PoolConfig{Limit: 1, MaxLifetime: 2 * time.Hour})
	ctx := context.Background()
	if _, ok, _ := m.Acquire(ctx, holder("a")); !ok {
		t.Fatal("acquire")
	}
	clock.advance(90 * time.Minute)
	m.Touch(holder("a"))
	clock.advance(40 * time.Minute) // past the hard cap, within the idle TTL
	if _, ok, _ := m.Acquire(ctx, holder("b")); !ok {
		t.Fatal("hard maximum lifetime did not reclaim the holder")
	}
}

// #468: a public job is re-adopted on every Observe. A reclaimed entry must
// keep its original created time, so the hard maximum lifetime still reclaims
// a holder that keeps polling instead of being renewed forever.
func TestReadoptKeepsOriginalCreatedForHardLifetime(t *testing.T) {
	clock := &fakeClock{at: time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)}
	m := New(Options{Now: clock.now, IdleTTL: 10 * time.Hour})
	m.Register("tripo.v3", "model-processing", PoolConfig{Limit: 2, MaxLifetime: time.Hour})
	ctx := context.Background()
	if _, ok, _ := m.Acquire(ctx, holder("a")); !ok {
		t.Fatal("acquire a")
	}
	// Past the hard cap another holder's Acquire reclaims a and takes the slot.
	clock.advance(61 * time.Minute)
	if _, ok, _ := m.Acquire(ctx, holder("b")); !ok {
		t.Fatal("acquire b after the hard cap")
	}
	// The public job is re-adopted on its next Observe, as Adapter.Observe
	// does every round.
	m.Readopt(holder("a"))
	// One minute later a third holder polls: the hard cap must still reclaim
	// a, so c fits. If Readopt had reset created, a would be a minute old and
	// c would queue behind the two live entries.
	clock.advance(time.Minute)
	if _, ok, _ := m.Acquire(ctx, holder("c")); !ok {
		t.Fatalf("hard maximum lifetime was reset by Readopt: %+v", m.Stats())
	}
}

// #468: a dispatching public job re-acquires its slot; a reclaimed attempt
// that asks again must keep its original created time as well, or the hard
// maximum lifetime is renewed on every round.
func TestReacquireKeepsOriginalCreatedForHardLifetime(t *testing.T) {
	clock := &fakeClock{at: time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)}
	m := New(Options{Now: clock.now, IdleTTL: 10 * time.Hour})
	m.Register("tripo.v3", "model-processing", PoolConfig{Limit: 2, MaxLifetime: time.Hour})
	ctx := context.Background()
	if _, ok, _ := m.Acquire(ctx, holder("a")); !ok {
		t.Fatal("acquire a")
	}
	clock.advance(61 * time.Minute)
	if _, ok, _ := m.Acquire(ctx, holder("b")); !ok {
		t.Fatal("acquire b after the hard cap")
	}
	// The same attempt asks for its slot again.
	if _, ok, _ := m.Acquire(ctx, holder("a")); !ok {
		t.Fatal("re-acquire a")
	}
	clock.advance(time.Minute)
	if _, ok, _ := m.Acquire(ctx, holder("c")); !ok {
		t.Fatalf("hard maximum lifetime was reset by Acquire: %+v", m.Stats())
	}
}

func TestWaitingHolderTTLIsSkipped(t *testing.T) {
	m, clock := newTestManager(1, time.Hour)
	ctx := context.Background()
	a, _, _ := m.Acquire(ctx, holder("a"))
	if _, ok, _ := m.Acquire(ctx, holder("old")); ok {
		t.Fatal("old waiter should queue")
	}
	clock.advance(30 * time.Minute)
	m.Touch(holder("a"))
	clock.advance(45 * time.Minute) // old waiter past its idle TTL, a still live
	if _, ok, _ := m.Acquire(ctx, holder("new")); ok {
		t.Fatal("new waiter should queue behind a")
	}
	m.Release(a)
	// "old" was reclaimed; "new" is the only waiter and is admitted.
	if _, ok, _ := m.Acquire(ctx, holder("new")); !ok {
		t.Fatal("live waiter was skipped")
	}
}

func TestUnregisteredPoolNeverQueues(t *testing.T) {
	m := New(Options{})
	ticket, ok, hint := m.Acquire(context.Background(), Holder{Connection: "other", Pool: "x", Attempt: "a"})
	if !ok || !ticket.Active() || hint != 0 {
		t.Fatalf("unregistered acquire = %v %v %v", ok, ticket.Active(), hint)
	}
	m.Release(ticket) // no-op
}

func TestConcurrentAcquireRelease(t *testing.T) {
	m, _ := newTestManager(5, time.Hour)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			attempt := string(rune('a' + i%26))
			for j := 0; j < 200; j++ {
				ticket, ok, _ := m.Acquire(ctx, holder(attempt))
				if ok {
					m.Touch(holder(attempt))
					m.Release(ticket)
				}
			}
		}(i)
	}
	wg.Wait()
	stats := m.Stats()
	if stats.TotalHeld > 5 {
		t.Fatalf("held = %d, want at most the limit 5", stats.TotalHeld)
	}
}

// #468: policy values come from the pool declaration when set, else the
// Manager's defaults. Unset keeps the previous behavior (10 min / 2 s / 2 s).
func TestPoolPolicyDefaultsAndOverrides(t *testing.T) {
	defaults := New(Options{})
	defaults.Register("c", "p", PoolConfig{Limit: 1})
	if defaults.QueuePoll("c", "p") != 2*time.Second ||
		defaults.IdleTTL("c", "p") != 10*time.Minute ||
		defaults.WaitHint("c", "p") != 2*time.Second {
		t.Fatalf("defaults = queue %v idle %v hint %v",
			defaults.QueuePoll("c", "p"), defaults.IdleTTL("c", "p"), defaults.WaitHint("c", "p"))
	}
	configured := New(Options{})
	configured.Register("c", "p", PoolConfig{
		Limit:     1,
		QueuePoll: 7 * time.Second,
		IdleTTL:   20 * time.Minute,
		WaitHint:  4 * time.Second,
	})
	if configured.QueuePoll("c", "p") != 7*time.Second ||
		configured.IdleTTL("c", "p") != 20*time.Minute ||
		configured.WaitHint("c", "p") != 4*time.Second {
		t.Fatalf("overrides = queue %v idle %v hint %v",
			configured.QueuePoll("c", "p"), configured.IdleTTL("c", "p"), configured.WaitHint("c", "p"))
	}
	// A full pool returns the configured hint.
	ctx := context.Background()
	local := Holder{Connection: "c", Pool: "p", Attempt: "a"}
	if _, ok, _ := configured.Acquire(ctx, local); !ok {
		t.Fatal("first acquire should fit")
	}
	local.Attempt = "b"
	if _, ok, hint := configured.Acquire(ctx, local); ok || hint != 4*time.Second {
		t.Fatalf("full pool hint = %v ok=%v, want 4s", hint, ok)
	}
}
