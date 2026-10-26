package chat

import "context"

type streamOperation struct {
	token chan struct{}
	users int
}

// Session operations serialize the durable replay/register/publication boundary.
// The registry mutex only protects maps; database calls and waiting happen
// outside it so another session or a disconnected subscriber can make progress.
func (service *Service) lockStreamSession(ctx context.Context, sessionID string) (func(), error) {
	service.streamMu.Lock()
	if service.streamOperations == nil {
		service.streamOperations = map[string]*streamOperation{}
	}
	operation := service.streamOperations[sessionID]
	if operation == nil {
		operation = &streamOperation{token: make(chan struct{}, 1)}
		operation.token <- struct{}{}
		service.streamOperations[sessionID] = operation
	}
	operation.users++
	service.streamMu.Unlock()
	releaseReference := func() {
		service.streamMu.Lock()
		defer service.streamMu.Unlock()
		operation.users--
		if operation.users == 0 {
			delete(service.streamOperations, sessionID)
		}
	}
	select {
	case <-ctx.Done():
		releaseReference()
		return nil, ctx.Err()
	case <-operation.token:
		return func() { operation.token <- struct{}{}; releaseReference() }, nil
	}
}
