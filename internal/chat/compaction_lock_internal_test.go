package chat

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func newCompactionLockOrchestrator() *Orchestrator {
	return &Orchestrator{compacting: map[string]*compactionLock{}}
}

// A long-running process compacts many sessions; the per-session locks must
// be released once nobody holds or waits for them, so their number stays
// bounded by the compactions in flight (#334).
func TestCompactionLocksAreReclaimedAfterUse(t *testing.T) {
	orchestrator := newCompactionLockOrchestrator()
	for index := 0; index < 1000; index++ {
		unlock := orchestrator.lockCompaction("owner", fmt.Sprintf("session-%d", index))
		unlock()
	}
	orchestrator.compactMu.Lock()
	left := len(orchestrator.compacting)
	orchestrator.compactMu.Unlock()
	if left != 0 {
		t.Fatalf("compaction locks left after use = %d, want 0", left)
	}
}

// Reclaiming a lock must not let two compactions of one session overlap: a
// waiter that arrived while the lock was held keeps it alive and gets it next.
func TestCompactionLockStillSerializesOneSession(t *testing.T) {
	orchestrator := newCompactionLockOrchestrator()
	var (
		mu      sync.Mutex
		inside  int
		overlap bool
		group   sync.WaitGroup
	)
	for worker := 0; worker < 16; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for round := 0; round < 50; round++ {
				unlock := orchestrator.lockCompaction("owner", "session")
				mu.Lock()
				inside++
				if inside > 1 {
					overlap = true
				}
				mu.Unlock()
				time.Sleep(time.Microsecond)
				mu.Lock()
				inside--
				mu.Unlock()
				unlock()
			}
		}()
	}
	group.Wait()
	if overlap {
		t.Fatal("two compactions of one session ran at the same time")
	}
	orchestrator.compactMu.Lock()
	left := len(orchestrator.compacting)
	orchestrator.compactMu.Unlock()
	if left != 0 {
		t.Fatalf("compaction locks left after use = %d, want 0", left)
	}
}
