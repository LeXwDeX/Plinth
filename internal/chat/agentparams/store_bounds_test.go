package agentparams_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
)

// store 限额防御无界内存增长：超量键/超大值被忽略，scope 总量封顶。
func TestStoreBoundsRejectUnboundedGrowth(t *testing.T) {
	store := agentparams.NewStore()
	scope := "owner\x00session"
	params := map[string]json.RawMessage{}
	for i := 0; i < 200; i++ {
		params["key_"+strings.Repeat("x", i%8)+string(rune('a'+i%26))+string(rune(i))] = json.RawMessage(`"v"`)
	}
	store.Set(scope, params)
	if got := len(store.Get(scope)); got > 64 {
		t.Fatalf("keys = %d, want <= 64", got)
	}
	store.Set(scope, map[string]json.RawMessage{
		"huge": json.RawMessage(`"` + strings.Repeat("a", 8<<10) + `"`),
	})
	for _, entry := range store.Get(scope) {
		if len(entry) > 4<<10 {
			t.Fatalf("oversized value accepted: %d bytes", len(entry))
		}
	}
}
