// Package upstreampool is the Provider-layer upstream concurrency pool: a
// process-wide, in-memory gate a Provider Adapter registers its own upstream
// pools with and asks for a slot before a paid dispatch (#468).
//
// The module is deliberately outside the generation kernel and does not change
// the public Task state machine. It only answers one question: may this holder
// send one more upstream job right now? A holder that may not gets a "not yet"
// answer and a wait hint; it does not submit, does not take a private dispatch
// permit and does not spend retry or rate-limit budget. The Adapter turns that
// answer into an ordinary running observation that carries the upstream queue
// clock, so a full pool reads as "queued", never as a failure.
//
// # Scope and semantics
//
//   - One Manager serves one process. The state is in memory only: a restart
//     begins from zero and the upstream's own 429 (Tripo code 2000) answers the
//     short overshoot until the first polls re-register what is still running
//     (Readopt).
//   - A pool is keyed by (connection, pool). Registering a pool enables it; a
//     holder whose (connection, pool) was never registered is never queued
//     (Acquire succeeds with an inert Ticket), so Providers that do not
//     register keep their exact previous behavior.
//   - A holder is keyed by attempt id. Repeated Acquire calls for one attempt
//     are idempotent: the children of one attempt share one slot, so a parent
//     job and its private children cannot wait on each other (handoff).
//   - Slots are released by Ticket. A ticket carries the pool's grant token, so
//     a holder's late Release cannot free a slot a different holder now owns.
//   - Idle holders are reclaimed after an idle TTL (and a hard maximum
//     lifetime) so a path that never reports a terminal state cannot leak a
//     slot forever. A holder that keeps polling refreshes its last-seen time and
//     is never reclaimed by the idle TTL.
//   - Waiting is FIFO by the sequence number assigned at the first Acquire. When
//     slots free, the smallest still-live sequence numbers are admitted; a
//     waiter whose idle TTL ran out is skipped.
package upstreampool

import (
	"context"
	"sync"
	"time"
)

// Holder names one attempt's place in one upstream pool. Connection is the
// Provider connection the pool belongs to (the Adapter key, e.g. "tripo.v3");
// Pool is the pool name declared by that connection; Attempt is the public
// attempt id that owns the slot.
type Holder struct {
	Connection string
	Pool       string
	Attempt    string
}

// Ticket is the proof of one held slot. It is valid only for the pool that
// granted it and only until Release; a stale ticket is ignored.
type Ticket struct {
	manager *Manager
	pool    *pool
	attempt string
	seq     uint64
	token   uint64
}

// Active reports whether the ticket holds a real slot. A disabled pool grants
// an inert ticket that is always active but never counted.
func (t Ticket) Active() bool { return t.manager != nil }

// Release frees the slot this ticket proves. It is safe to call on an inert
// ticket (a disabled pool) and on a stale one (the release is ignored).
func (t Ticket) Release() {
	if t.manager != nil {
		t.manager.Release(t)
	}
}

// PoolConfig is one registered pool's declaration.
type PoolConfig struct {
	// Limit is how many holders may hold a slot at once. Zero or less disables
	// the pool (every Acquire succeeds without queueing).
	Limit int
	// MaxLifetime is the hard upper bound on one entry's age, independent of
	// last-seen. Zero means no hard cap (the idle TTL still applies).
	MaxLifetime time.Duration
	// IdleTTL overrides the Manager's default idle TTL for this pool. Zero
	// keeps the Manager default.
	IdleTTL time.Duration
	// WaitHint overrides the Manager's default wait hint for this pool. Zero
	// keeps the Manager default.
	WaitHint time.Duration
	// QueuePoll is how long a queued Adapter should wait before polling again.
	// Zero means the Adapter's own fallback.
	QueuePoll time.Duration
}

// Options tunes the Manager's clock and default bounds.
type Options struct {
	// Now is the clock; tests inject one. Default time.Now.
	Now func() time.Time
	// IdleTTL is how long an untouched entry may hold a slot before it is
	// reclaimed. Default 10 minutes (the floor the design requires).
	IdleTTL time.Duration
	// WaitHint is the wait Acquire suggests when the pool is full. Default
	// 2 seconds; an Adapter may use its own shorter poll.
	WaitHint time.Duration
	// QueuePoll is the default wait a queued Adapter uses before polling again
	// when its pool does not declare one. Default 2 seconds.
	QueuePoll time.Duration
}

// Manager is the process-wide registry of upstream pools. It is safe for
// concurrent use.
type Manager struct {
	now       func() time.Time
	idleTTL   time.Duration
	waitHint  time.Duration
	queuePoll time.Duration

	mu    sync.Mutex
	pools map[poolKey]*pool
}

type poolKey struct {
	connection string
	pool       string
}

type entry struct {
	attempt string
	seq     uint64
	held    bool
	created time.Time
	seen    time.Time
	token   uint64
}

// retiredEntry remembers when a reclaimed entry first held a slot. A public job
// is re-adopted on every Observe (Adapter.Observe), so without this the
// re-registration after a reclaim would reset created and the hard maximum
// lifetime could never expire for a holder that keeps polling.
type retiredEntry struct {
	created time.Time
	seen    time.Time
}

type pool struct {
	limit       int
	maxLifetime time.Duration
	idleTTL     time.Duration
	waitHint    time.Duration
	queuePoll   time.Duration
	entries     map[string]*entry
	retired     map[string]retiredEntry
	nextSeq     uint64
	nextToken   uint64
}

// New builds an empty Manager. No pool is registered, so every Acquire succeeds
// until a Provider registers one.
func New(options Options) *Manager {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.IdleTTL <= 0 {
		options.IdleTTL = 10 * time.Minute
	}
	if options.WaitHint <= 0 {
		options.WaitHint = 2 * time.Second
	}
	if options.QueuePoll <= 0 {
		options.QueuePoll = 2 * time.Second
	}
	return &Manager{
		now:       options.Now,
		idleTTL:   options.IdleTTL,
		waitHint:  options.WaitHint,
		queuePoll: options.QueuePoll,
		pools:     map[poolKey]*pool{},
	}
}

// Register enables a pool. Calling it again for the same key updates the limit
// and maximum lifetime without dropping the live entries.
func (m *Manager) Register(connection, name string, config PoolConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := poolKey{connection: connection, pool: name}
	if existing, ok := m.pools[key]; ok {
		existing.limit = config.Limit
		existing.maxLifetime = config.MaxLifetime
		existing.idleTTL = config.IdleTTL
		existing.waitHint = config.WaitHint
		existing.queuePoll = config.QueuePoll
		return
	}
	m.pools[key] = &pool{
		limit:       config.Limit,
		maxLifetime: config.MaxLifetime,
		idleTTL:     config.IdleTTL,
		waitHint:    config.WaitHint,
		queuePoll:   config.QueuePoll,
		entries:     map[string]*entry{},
		retired:     map[string]retiredEntry{},
	}
}

// Enabled reports whether the holder's pool was registered with a positive
// limit.
func (m *Manager) Enabled(holder Holder) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pools[poolKey{connection: holder.Connection, pool: holder.Pool}]
	return ok && p.limit > 0
}

// Acquire atomically checks the holder's pool and takes a slot when one is
// free. It never blocks: when the pool is full it returns an inert ticket,
// false and a wait hint, and the holder must try again later. Repeated calls
// for the same attempt are idempotent and return the same live ticket.
func (m *Manager) Acquire(ctx context.Context, holder Holder, weight int) (Ticket, bool, time.Duration) {
	_ = weight // MVP grants one slot per holder; weight is accepted for callers.
	if err := ctx.Err(); err != nil {
		return Ticket{}, false, m.waitHint
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pools[poolKey{connection: holder.Connection, pool: holder.Pool}]
	if !ok || p.limit <= 0 {
		// Not registered: never queue. The inert ticket keeps the Adapter's
		// call site uniform.
		return Ticket{manager: m}, true, 0
	}
	now := m.now()
	m.reclaimLocked(p, now)
	if existing, exists := p.entries[holder.Attempt]; exists {
		if existing.held {
			existing.seen = now
			return m.ticket(p, existing), true, 0
		}
		m.admitLocked(p, now)
		if existing.held {
			return m.ticket(p, existing), true, 0
		}
		existing.seen = now
		return Ticket{}, false, m.poolWaitHint(p)
	}
	m.admitLocked(p, now)
	if p.heldCountLocked() < p.limit {
		created := m.grantLocked(p, holder.Attempt, now)
		return m.ticket(p, created), true, 0
	}
	created := now
	if retired, ok := p.retired[holder.Attempt]; ok {
		created = retired.created
	}
	// A new queue entry starts its own life; an older tombstone is stale.
	delete(p.retired, holder.Attempt)
	p.entries[holder.Attempt] = &entry{
		attempt: holder.Attempt,
		seq:     p.nextSeq,
		created: created,
		seen:    now,
	}
	p.nextSeq++
	return Ticket{}, false, m.poolWaitHint(p)
}

// Release frees the slot the ticket proves. A ticket that no longer matches the
// live grant (the slot was released and taken again) is ignored, so a holder's
// late Release cannot free another holder's slot.
func (m *Manager) Release(ticket Ticket) {
	if ticket.manager == nil {
		return
	}
	owner := ticket.manager
	owner.mu.Lock()
	defer owner.mu.Unlock()
	p := ticket.pool
	if p == nil {
		return
	}
	current, ok := p.entries[ticket.attempt]
	if !ok || !current.held || current.token != ticket.token {
		return
	}
	delete(p.entries, ticket.attempt)
	owner.admitLocked(p, owner.now())
}

// Touch refreshes the holder's last-seen time. A Provider Adapter calls it on
// every invocation so a holder that keeps polling is never reclaimed.
func (m *Manager) Touch(holder Holder) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pools[poolKey{connection: holder.Connection, pool: holder.Pool}]
	if !ok {
		return
	}
	if existing, exists := p.entries[holder.Attempt]; exists {
		existing.seen = m.now()
	}
}

// ReleaseHolder releases whatever slot the holder currently owns. It is for
// terminal cleanup, where the caller holds no live ticket. Unlike Release it
// does not prove a specific grant; the caller must only use it once the holder
// is known to be finished.
func (m *Manager) ReleaseHolder(holder Holder) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pools[poolKey{connection: holder.Connection, pool: holder.Pool}]
	if !ok {
		return
	}
	current, exists := p.entries[holder.Attempt]
	if !exists || !current.held {
		return
	}
	delete(p.entries, holder.Attempt)
	m.admitLocked(p, m.now())
}

// Readopt registers holders whose private children were submitted before a
// restart and are still running upstream. It deliberately does not check the
// limit: the count must reflect what is really in flight, and the upstream's
// own 429 answers a short overshoot until one polling cycle converges.
func (m *Manager) Readopt(holders ...Holder) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	for _, holder := range holders {
		p, ok := m.pools[poolKey{connection: holder.Connection, pool: holder.Pool}]
		if !ok {
			continue
		}
		if existing, exists := p.entries[holder.Attempt]; exists {
			existing.held = true
			existing.seen = now
			continue
		}
		// grantLocked keeps the original created time of a reclaimed holder.
		m.grantLocked(p, holder.Attempt, now)
	}
}

// Handoff keeps one attempt's slot for its children: a parent job that finished
// its own upstream work hands the same slot to the private children that run in
// the same attempt, so they cannot wait on each other.
func (m *Manager) Handoff(holder Holder) {
	m.Readopt(holder)
}

// PoolStats is one pool's live counts.
type PoolStats struct {
	Connection string
	Pool       string
	Limit      int
	Held       int
	Waiting    int
}

// Stats is the Manager's live snapshot for diagnostics and tests.
type Stats struct {
	Pools        []PoolStats
	TotalHeld    int
	TotalWaiting int
}

// Stats reports every registered pool's counts in a stable order.
func (m *Manager) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	stats := Stats{}
	for key, p := range m.pools {
		held, waiting := 0, 0
		for _, e := range p.entries {
			if e.held {
				held++
			} else {
				waiting++
			}
		}
		stats.Pools = append(stats.Pools, PoolStats{
			Connection: key.connection,
			Pool:       key.pool,
			Limit:      p.limit,
			Held:       held,
			Waiting:    waiting,
		})
		stats.TotalHeld += held
		stats.TotalWaiting += waiting
	}
	return stats
}

// QueuePoll is the configured wait a queued Adapter uses before polling again
// for this pool, or the Manager default.
func (m *Manager) QueuePoll(connection, name string) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.pools[poolKey{connection: connection, pool: name}]; ok && p.queuePoll > 0 {
		return p.queuePoll
	}
	return m.queuePoll
}

// IdleTTL is the configured idle TTL for this pool, or the Manager default.
func (m *Manager) IdleTTL(connection, name string) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.pools[poolKey{connection: connection, pool: name}]; ok {
		return m.poolIdleTTL(p)
	}
	return m.idleTTL
}

// WaitHint is the configured wait hint for this pool, or the Manager default.
func (m *Manager) WaitHint(connection, name string) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.pools[poolKey{connection: connection, pool: name}]; ok {
		return m.poolWaitHint(p)
	}
	return m.waitHint
}

func (m *Manager) poolWaitHint(p *pool) time.Duration {
	if p != nil && p.waitHint > 0 {
		return p.waitHint
	}
	return m.waitHint
}

func (m *Manager) poolIdleTTL(p *pool) time.Duration {
	if p != nil && p.idleTTL > 0 {
		return p.idleTTL
	}
	return m.idleTTL
}

func (m *Manager) ticket(p *pool, e *entry) Ticket {
	return Ticket{manager: m, pool: p, attempt: e.attempt, seq: e.seq, token: e.token}
}

func (m *Manager) grantLocked(p *pool, attempt string, now time.Time) *entry {
	// A reclaimed holder keeps its original created time: a public job is
	// re-adopted on every Observe (and a dispatching one re-acquires), so
	// resetting created here would let a leaked slot live past the hard maximum
	// lifetime forever. A fresh attempt has no tombstone and starts at now.
	created := now
	if retired, ok := p.retired[attempt]; ok {
		created = retired.created
	}
	delete(p.retired, attempt)
	return m.grantLockedAt(p, attempt, created, now)
}

func (m *Manager) grantLockedAt(p *pool, attempt string, created, now time.Time) *entry {
	p.nextToken++
	e := &entry{
		attempt: attempt,
		seq:     p.nextSeq,
		held:    true,
		created: created,
		seen:    now,
		token:   p.nextToken,
	}
	p.nextSeq++
	p.entries[attempt] = e
	return e
}

// admitLocked grants free slots to the waiting entries with the smallest
// sequence numbers. A waiter whose idle TTL ran out is skipped (reclaim already
// removed it before admission runs).
func (m *Manager) admitLocked(p *pool, now time.Time) {
	for p.heldCountLocked() < p.limit {
		var next *entry
		for _, e := range p.entries {
			if e.held {
				continue
			}
			if next == nil || e.seq < next.seq {
				next = e
			}
		}
		if next == nil {
			return
		}
		p.nextToken++
		next.held = true
		next.token = p.nextToken
		next.seen = now
	}
}

// reclaimLocked drops entries whose idle TTL or hard maximum lifetime ran out.
// A holder that touched recently is kept; the hard cap is a backstop for a path
// that never reports a terminal state. The dropped entry's created time is
// remembered so a later Readopt cannot reset the hard cap's clock.
func (m *Manager) reclaimLocked(p *pool, now time.Time) {
	for attempt, e := range p.entries {
		if p.maxLifetime > 0 && now.Sub(e.created) > p.maxLifetime {
			p.retired[attempt] = retiredEntry{created: e.created, seen: now}
			delete(p.entries, attempt)
			continue
		}
		if now.Sub(e.seen) > m.poolIdleTTL(p) {
			p.retired[attempt] = retiredEntry{created: e.created, seen: now}
			delete(p.entries, attempt)
		}
	}
	// A tombstone is only useful while its attempt may still be re-adopted.
	for attempt, retired := range p.retired {
		if now.Sub(retired.seen) > m.poolIdleTTL(p) {
			delete(p.retired, attempt)
		}
	}
}

func (p *pool) heldCountLocked() int {
	count := 0
	for _, e := range p.entries {
		if e.held {
			count++
		}
	}
	return count
}
