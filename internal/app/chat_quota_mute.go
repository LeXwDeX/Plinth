package app

import (
	"strings"
	"sync"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// chatQuotaMemory is one chat connection's memory of its last used-up quota
// (#640). A gateway that saw the upstream refuse with a used-up quota mutes
// the target and answers later calls itself ("all targets muted until ...");
// within chat.agent.quota_mute's window such an answer is read as the same
// used-up quota, so it is not retried as a transient failure and the person
// reads the quota text. The memory lives in the process only: a restart
// forgets it, and the next refusal that reaches the upstream is read anew.
type chatQuotaMemory struct {
	read func(int, []byte) (string, bool)
	mute *catalog.ChatAgentQuotaMuteSchema
	now  func() time.Time

	mu    sync.Mutex
	at    time.Time
	class string
}

// newChatQuotaMemory wraps a connection's declared exhaustion reader; nil
// when the connection declares none. Without quota_mute it reads as read does.
func newChatQuotaMemory(
	read func(int, []byte) (string, bool),
	mute *catalog.ChatAgentQuotaMuteSchema,
	now func() time.Time,
) *chatQuotaMemory {
	if read == nil {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	return &chatQuotaMemory{read: read, mute: mute, now: now}
}

// exhausted is the Adapter's ExhaustedQuota: the declared reading first, which
// a used-up quota also records; then a declared mute answer within the window
// of the last one, which carries that one's class.
func (memory *chatQuotaMemory) exhausted(status int, body []byte) (string, bool) {
	if class, exhausted := memory.read(status, body); exhausted {
		if memory.mute != nil {
			memory.mu.Lock()
			memory.at, memory.class = memory.now(), class
			memory.mu.Unlock()
		}
		return class, true
	}
	if memory.mute == nil || status != memory.mute.HTTPStatus || !memory.muteAnswer(body) {
		return "", false
	}
	memory.mu.Lock()
	defer memory.mu.Unlock()
	window := time.Duration(memory.mute.WindowSeconds) * time.Second
	if memory.class == "" || memory.now().Sub(memory.at) > window {
		return "", false
	}
	return memory.class, true
}

func (memory *chatQuotaMemory) muteAnswer(body []byte) bool {
	text := strings.ToLower(string(body))
	for _, marker := range memory.mute.ContainsAny {
		if strings.Contains(text, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

// reader is the memory as an Adapter's ExhaustedQuota; nil reads nothing.
func (memory *chatQuotaMemory) reader() func(int, []byte) (string, bool) {
	if memory == nil {
		return nil
	}
	return memory.exhausted
}
