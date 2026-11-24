package upstreampool

import (
	"context"
	"testing"
	"time"
)

// A waiter that stopped polling (its attempt was canceled or vanished) must not
// be handed a freed slot: the slot would sit with nobody to use it until the
// idle TTL ran out, and the live waiters behind it would stay blocked (#510).
func TestReleaseSkipsWaiterThatStoppedPolling(t *testing.T) {
	m, clock := newTestManager(1, time.Hour)
	ctx := context.Background()
	a, _, _ := m.Acquire(ctx, holder("a"))
	if _, ok, _ := m.Acquire(ctx, holder("gone")); ok {
		t.Fatal("gone should queue")
	}
	clock.advance(5 * time.Minute)
	m.Touch(holder("a"))
	if _, ok, _ := m.Acquire(ctx, holder("live")); ok {
		t.Fatal("live should queue behind a")
	}
	m.Release(a)
	if _, ok, _ := m.Acquire(ctx, holder("live")); !ok {
		t.Fatal("the freed slot went to a waiter that stopped polling five minutes ago")
	}
}

// A waiter admitted on release that never comes back to claim its slot gives
// it up after the claim window, so the next live waiter gets it (#510).
func TestUnclaimedAdmissionLapsesToNextWaiter(t *testing.T) {
	m, clock := newTestManager(1, time.Hour)
	ctx := context.Background()
	a, _, _ := m.Acquire(ctx, holder("a"))
	if _, ok, _ := m.Acquire(ctx, holder("vanishes")); ok {
		t.Fatal("vanishes should queue")
	}
	if _, ok, _ := m.Acquire(ctx, holder("live")); ok {
		t.Fatal("live should queue")
	}
	m.Release(a) // "vanishes" is admitted on its behalf, then never polls again.
	if _, ok, _ := m.Acquire(ctx, holder("live")); ok {
		t.Fatal("live jumped the queue while the earlier waiter may still claim")
	}
	for range 20 {
		clock.advance(2 * time.Second)
		if _, ok, _ := m.Acquire(ctx, holder("live")); ok {
			return
		}
	}
	t.Fatalf("an admitted waiter that never claimed kept the slot: %+v", m.Stats())
}

// An admitted waiter that does come back keeps the slot it was given.
func TestClaimedAdmissionIsKept(t *testing.T) {
	m, clock := newTestManager(1, time.Hour)
	ctx := context.Background()
	a, _, _ := m.Acquire(ctx, holder("a"))
	_, _, _ = m.Acquire(ctx, holder("first"))
	_, _, _ = m.Acquire(ctx, holder("second"))
	m.Release(a)
	clock.advance(2 * time.Second)
	if _, ok, _ := m.Acquire(ctx, holder("first")); !ok {
		t.Fatal("first waiter was not admitted")
	}
	clock.advance(10 * time.Minute)
	m.Touch(holder("first"))
	if _, ok, _ := m.Acquire(ctx, holder("second")); ok {
		t.Fatal("a claimed slot was taken from a holder that keeps polling")
	}
}

// Terminal cleanup of a holder that is still queued takes it out of the queue,
// so a released slot is never admitted to an attempt that already ended.
func TestReleaseHolderRemovesWaiter(t *testing.T) {
	m, _ := newTestManager(1, time.Hour)
	ctx := context.Background()
	a, _, _ := m.Acquire(ctx, holder("a"))
	_, _, _ = m.Acquire(ctx, holder("canceled"))
	_, _, _ = m.Acquire(ctx, holder("live"))
	m.ReleaseHolder(holder("canceled"))
	if stats := m.Stats(); stats.TotalWaiting != 1 {
		t.Fatalf("stats = %+v, want the canceled waiter gone", stats)
	}
	m.Release(a)
	if _, ok, _ := m.Acquire(ctx, holder("live")); !ok {
		t.Fatal("the freed slot did not reach the live waiter")
	}
}
