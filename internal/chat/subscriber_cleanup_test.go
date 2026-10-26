package chat

import "testing"

func TestOverflowReleasesLastSessionSubscription(t *testing.T) {
	stalled := make(chan Event, 1)
	stalled <- Event{}
	service := &Service{subscribers: map[string]map[chan Event]struct{}{"session": {stalled: {}}}}
	service.publishLocked(Event{SessionID: "session"})
	if _, exists := service.subscribers["session"]; exists {
		t.Fatal("overflow retained empty session subscriber map")
	}
	<-stalled
	if _, open := <-stalled; open {
		t.Fatal("overflowed subscriber remains open")
	}
}
