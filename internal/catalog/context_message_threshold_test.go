package catalog

import "testing"

// #322 note 98983: with compaction on, the unsummarized history is bounded by
// compaction only; auto_message_threshold is the fallback trigger while the
// tokens are not measured, and it must leave rounds to summarize.
func TestContextCompactionDeclaresAMessageThreshold(t *testing.T) {
	loaded, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	compaction := loaded.Application().Chat.ContextCompaction
	if compaction.AutoMessageThreshold != 200 {
		t.Fatalf("auto_message_threshold = %d", compaction.AutoMessageThreshold)
	}
	for _, threshold := range []int{0, -1, 2 * compaction.KeepRecentRounds} {
		changed := compaction
		changed.AutoMessageThreshold = threshold
		if validateContextCompaction(changed) == nil {
			t.Fatalf("threshold %d accepted", threshold)
		}
	}
}
